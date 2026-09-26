package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxRedirects = 5

const maxErrorResponseBytes = 4096

type Route string

const (
	Direct      Route = "direct"
	SystemProxy Route = "system_proxy"
	MihomoProxy Route = "mihomo_proxy"
)

type Request struct {
	URL              string
	Route            Route
	ETag             string
	LastModified     string
	MaxBytes         int64
	AllowHTTP        bool
	UserAgent        string
	CaptureErrorBody bool
}

type Response struct {
	StatusCode           int
	Body                 []byte
	DiagnosticBody       string
	ETag                 string
	LastModified         string
	SubscriptionUserInfo string
}

// StatusError contains a numeric status and, only when opted in, a bounded response body.
type StatusError struct {
	Code         int
	ResponseBody string
}

func (e StatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

func (e StatusError) Valid() bool { return e.Code >= 400 && e.Code <= 599 }

// HTTPResponseFrom extracts captured response details from a wrapped error.
func HTTPResponseFrom(err error) (StatusError, bool) {
	var status StatusError
	if errors.As(err, &status) && status.Code >= 100 && status.Code <= 599 {
		return status, true
	}
	var pointer *StatusError
	if errors.As(err, &pointer) && pointer != nil && pointer.Code >= 100 && pointer.Code <= 599 {
		return *pointer, true
	}
	return StatusError{}, false
}

// StatusErrorFrom extracts only 4xx and 5xx statuses.
func StatusErrorFrom(err error) (StatusError, bool) {
	status, ok := HTTPResponseFrom(err)
	return status, ok && status.Valid()
}

// ParseStatus accepts only bounded numeric error statuses, not arbitrary server text.
func ParseStatus(message string) (StatusError, bool) {
	if len(message) != len("HTTP 406") || !strings.HasPrefix(message, "HTTP ") {
		return StatusError{}, false
	}
	code, err := strconv.Atoi(message[len("HTTP "):])
	if err != nil || !(StatusError{Code: code}).Valid() {
		return StatusError{}, false
	}
	return StatusError{Code: code}, true
}

type Factory func(Route) (http.RoundTripper, error)

type Client struct {
	factory          Factory
	captureErrorBody bool
}

func NewClient(factory Factory) *Client {
	return &Client{factory: factory}
}

// SetCaptureErrorBody enables bounded error body capture. Set it before Fetch calls.
func (c *Client) SetCaptureErrorBody(enabled bool) {
	if c != nil {
		c.captureErrorBody = enabled
	}
}

func (c *Client) Fetch(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if req.MaxBytes <= 0 {
		return Response{}, fmt.Errorf("download: max bytes must be positive")
	}
	if len(req.UserAgent) > 256 {
		return Response{}, fmt.Errorf("download: invalid user agent")
	}
	for _, r := range req.UserAgent {
		if r < 0x20 || r > 0x7e {
			return Response{}, fmt.Errorf("download: invalid user agent")
		}
	}

	transport, err := c.transport(req.Route)
	if err != nil {
		return Response{}, err
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	current, err := parseURL(req.URL, req.AllowHTTP)
	if err != nil {
		return Response{}, err
	}
	origin := originOf(current)

	for redirects := 0; ; redirects++ {
		if redirects > maxRedirects {
			return Response{}, fmt.Errorf("download: too many redirects")
		}
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}

		response, err := c.do(ctx, client, current, req, origin)
		if err != nil {
			return Response{}, err
		}
		if isRedirect(response.StatusCode) {
			location := response.Header.Get("Location")
			response.Body.Close()
			if location == "" {
				return Response{}, fmt.Errorf("download: redirect missing location")
			}
			current, err = resolveRedirect(current, location, req.AllowHTTP)
			if err != nil {
				return Response{}, err
			}
			continue
		}
		if response.StatusCode == http.StatusNotModified {
			etag, lastModified := scopedValidators(response.Header, current, origin)
			response.Body.Close()
			return Response{StatusCode: http.StatusNotModified, ETag: etag, LastModified: lastModified, SubscriptionUserInfo: boundedUsageHeader(response.Header.Get("Subscription-Userinfo"))}, nil
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			status := StatusError{Code: response.StatusCode}
			if req.CaptureErrorBody || c.captureErrorBody {
				status.ResponseBody = captureErrorResponse(response.Body)
			}
			response.Body.Close()
			return Response{}, status
		}

		body, err := readBody(ctx, response.Body, req.MaxBytes)
		response.Body.Close()
		if err != nil {
			return Response{}, err
		}
		diagnosticBody := ""
		if req.CaptureErrorBody || c.captureErrorBody {
			diagnosticBody = captureErrorResponse(bytes.NewReader(body))
		}
		etag, lastModified := scopedValidators(response.Header, current, origin)
		return Response{
			StatusCode:           response.StatusCode,
			Body:                 body,
			DiagnosticBody:       diagnosticBody,
			ETag:                 etag,
			LastModified:         lastModified,
			SubscriptionUserInfo: boundedUsageHeader(response.Header.Get("Subscription-Userinfo")),
		}, nil
	}
}

func (c *Client) transport(route Route) (http.RoundTripper, error) {
	if c == nil || c.factory == nil {
		return nil, fmt.Errorf("download: missing transport factory")
	}
	switch route {
	case Direct, SystemProxy, MihomoProxy:
		transport, err := c.factory(route)
		if err != nil {
			return nil, err
		}
		if transport == nil {
			return nil, fmt.Errorf("download: missing transport for %s", route)
		}
		return transport, nil
	default:
		return nil, fmt.Errorf("download: invalid route %q", route)
	}
}

func (c *Client) do(ctx context.Context, client *http.Client, target *url.URL, req Request, origin originKey) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("download: invalid request")
	}
	agent := "clash-pulse"
	if sameOrigin(target, origin) && req.UserAgent != "" {
		agent = req.UserAgent
	}
	httpReq.Header.Set("User-Agent", agent)
	if sameOrigin(target, origin) {
		if req.ETag != "" {
			httpReq.Header.Set("If-None-Match", req.ETag)
		}
		if req.LastModified != "" {
			httpReq.Header.Set("If-Modified-Since", req.LastModified)
		}
	}
	response, err := client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("download: request failed")
	}
	return response, nil
}

func scopedValidators(headers http.Header, final *url.URL, original originKey) (string, string) {
	if !sameOrigin(final, original) {
		return "", ""
	}
	return headers.Get("ETag"), headers.Get("Last-Modified")
}

type originKey struct {
	Scheme string
	Host   string
	Port   string
}

func originOf(u *url.URL) originKey {
	return originKey{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Hostname()), Port: effectivePort(u)}
}

func sameOrigin(u *url.URL, origin originKey) bool {
	return origin == originOf(u)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func parseURL(raw string, allowHTTP bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("download: empty url")
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	} else if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("download: invalid url")
	}
	return validateURL(u, allowHTTP)
}

func resolveRedirect(base *url.URL, location string, allowHTTP bool) (*url.URL, error) {
	next, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("download: invalid redirect url")
	}
	if !next.IsAbs() {
		next = base.ResolveReference(next)
	}
	return validateURL(next, allowHTTP)
}

func validateURL(u *url.URL, allowHTTP bool) (*url.URL, error) {
	if u.User != nil {
		return nil, fmt.Errorf("download: userinfo not allowed")
	}
	if u.Scheme == "https" {
		if u.Host == "" || u.Opaque != "" {
			return nil, fmt.Errorf("download: invalid https url")
		}
		return u, nil
	}
	if allowHTTP && u.Scheme == "http" {
		if u.Host == "" || u.Opaque != "" {
			return nil, fmt.Errorf("download: invalid http url")
		}
		return u, nil
	}
	return nil, fmt.Errorf("download: must use https")
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func readBody(ctx context.Context, body io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("download: max bytes must be positive")
	}
	var buf bytes.Buffer
	chunk := make([]byte, 32*1024)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := body.Read(chunk)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if n > 0 {
			size += int64(n)
			if size > maxBytes {
				return nil, errors.New("download: body too large")
			}
			buf.Write(chunk[:n])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf.Bytes(), nil
			}
			return nil, fmt.Errorf("download: read body: %w", err)
		}
	}
}

func captureErrorResponse(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, maxErrorResponseBytes+1))
	truncated := len(data) > maxErrorResponseBytes
	if truncated {
		data = data[:maxErrorResponseBytes]
	}
	text := strings.ToValidUTF8(string(data), "\uFFFD")
	var output strings.Builder
	for _, char := range text {
		if char == '\n' || char == '\t' || !unicode.IsControl(char) {
			output.WriteRune(char)
		}
	}
	result := output.String()
	const suffix = "\n[response truncated]"
	if len(result) > maxErrorResponseBytes {
		truncated = true
	}
	if truncated {
		result = truncateUTF8(result, maxErrorResponseBytes-len(suffix)) + suffix
	}
	return result
}

func truncateUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func boundedUsageHeader(value string) string {
	if len(value) > 1024 {
		return ""
	}
	return value
}
