package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxRedirects = 5

type Route string

const (
	Direct      Route = "direct"
	SystemProxy Route = "system_proxy"
	MihomoProxy Route = "mihomo_proxy"
)

type Request struct {
	URL          string
	Route        Route
	ETag         string
	LastModified string
	MaxBytes     int64
	AllowHTTP    bool
}

type Response struct {
	StatusCode   int
	Body         []byte
	ETag         string
	LastModified string
}

type Factory func(Route) (http.RoundTripper, error)

type Client struct {
	factory Factory
}

func NewClient(factory Factory) *Client {
	return &Client{factory: factory}
}

func (c *Client) Fetch(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if req.MaxBytes <= 0 {
		return Response{}, fmt.Errorf("download: max bytes must be positive")
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
			etag := response.Header.Get("ETag")
			lastModified := response.Header.Get("Last-Modified")
			response.Body.Close()
			return Response{StatusCode: http.StatusNotModified, ETag: etag, LastModified: lastModified}, nil
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			response.Body.Close()
			return Response{}, fmt.Errorf("download: unexpected status %s", response.Status)
		}

		body, err := readBody(ctx, response.Body, req.MaxBytes)
		response.Body.Close()
		if err != nil {
			return Response{}, err
		}
		return Response{
			StatusCode:   response.StatusCode,
			Body:         body,
			ETag:         response.Header.Get("ETag"),
			LastModified: response.Header.Get("Last-Modified"),
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
		return nil, fmt.Errorf("download: request: %w", err)
	}
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
		return nil, fmt.Errorf("download: request: %w", err)
	}
	return response, nil
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
		return nil, err
	}
	return validateURL(u, allowHTTP)
}

func resolveRedirect(base *url.URL, location string, allowHTTP bool) (*url.URL, error) {
	next, err := url.Parse(location)
	if err != nil {
		return nil, err
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
