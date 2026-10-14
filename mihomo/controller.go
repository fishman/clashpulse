package mihomo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const maxControllerResponse = 1 << 20

// maxLogLine bounds one streamed log entry; longer lines end the stream.
const maxLogLine = 64 << 10

type Controller struct {
	BaseURL string
	Secret  string
	Client  *http.Client
}

type Proxy struct {
	Name string
	Type string
	// DelayMillis is the newest delay mihomo's own url-test recorded for this
	// node, or zero when mihomo has no history for it.
	DelayMillis int64
}

type Group struct {
	Name     string
	Type     string
	Proxies  []string
	Selected string
}

type delaySample struct {
	Delay int64 `json:"delay"`
}

func NewController(baseURL, secret string, client *http.Client) (*Controller, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("controller: invalid base URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("controller: invalid base URL scheme")
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("controller: base URL is not loopback")
	}
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Controller{BaseURL: strings.TrimRight(baseURL, "/"), Secret: secret, Client: &copy}, nil
}

// maxControllerSocket is the usable sun_path length on both Linux (107) and
// macOS (103).
const maxControllerSocket = 103

// NewUnixController talks to Mihomo's controller over a Unix socket, so the
// controller listens on no TCP port. Mihomo does not check the secret on the
// socket; the caller must keep it in a directory only its user can enter.
func NewUnixController(socket, secret string, client *http.Client) (*Controller, error) {
	if !filepath.IsAbs(socket) || len(socket) > maxControllerSocket {
		return nil, fmt.Errorf("controller: invalid socket path")
	}
	if client == nil {
		client = http.DefaultClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", socket)
	}
	copy := *client
	copy.Transport = transport
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Controller{BaseURL: "http://mihomo", Secret: secret, Client: &copy}, nil
}

func (c *Controller) Proxies(ctx context.Context) ([]Proxy, []Group, error) {
	body, err := c.do(ctx, http.MethodGet, "/proxies", nil)
	if err != nil {
		return nil, nil, err
	}
	var response struct {
		Proxies map[string]struct {
			Name    string        `json:"name"`
			Type    string        `json:"type"`
			All     []string      `json:"all"`
			Now     string        `json:"now"`
			History []delaySample `json:"history"`
		} `json:"proxies"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, nil, fmt.Errorf("controller proxies: %w", err)
	}
	proxies := make([]Proxy, 0, len(response.Proxies))
	groups := make([]Group, 0, len(response.Proxies))
	for name, proxy := range response.Proxies {
		if proxy.Name == "" {
			proxy.Name = name
		}
		if proxy.All != nil {
			groups = append(groups, Group{Name: proxy.Name, Type: proxy.Type, Proxies: proxy.All, Selected: proxy.Now})
			continue
		}
		proxies = append(proxies, Proxy{Name: proxy.Name, Type: proxy.Type, DelayMillis: newestDelay(proxy.History)})
	}
	return proxies, groups, nil
}

// Mihomo appends a sample per url-test run, newest last, and records a
// non-positive delay when a run fails.
func newestDelay(history []delaySample) int64 {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Delay > 0 {
			return history[i].Delay
		}
	}
	return 0
}

func (c *Controller) Delay(ctx context.Context, proxy, testURL string, timeout time.Duration) (time.Duration, error) {
	path := "/proxies/" + url.PathEscape(proxy) + "/delay?" + url.Values{"url": {testURL}, "timeout": {fmt.Sprint(timeout.Milliseconds())}}.Encode()
	body, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, err
	}
	var response struct {
		Delay *int64 `json:"delay"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("controller delay: %w", err)
	}
	if response.Delay == nil || *response.Delay <= 0 {
		return 0, fmt.Errorf("controller delay: nonpositive delay")
	}
	return time.Duration(*response.Delay) * time.Millisecond, nil
}

func (c *Controller) Select(ctx context.Context, group, proxy string) error {
	body, err := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: proxy})
	if err != nil {
		return fmt.Errorf("controller select: %w", err)
	}
	_, err = c.do(ctx, http.MethodPut, "/proxies/"+url.PathEscape(group), bytes.NewReader(body))
	return err
}

// SetMode switches the running routing mode; open connections survive it.
func (c *Controller) SetMode(ctx context.Context, mode string) error {
	body, err := json.Marshal(map[string]string{"mode": mode})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPatch, "/configs", bytes.NewReader(body))
	return err
}

func (c *Controller) Reload(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPut, "/configs", strings.NewReader("{}"))
	return err
}

// TunEnabled reports whether the TUN inbound is running. Mihomo keeps running
// when it cannot create the device and then reports tun.enable false, so this
// read, not the rendered configuration, confirms TUN.
func (c *Controller) TunEnabled(ctx context.Context) (bool, error) {
	body, err := c.do(ctx, http.MethodGet, "/configs", nil)
	if err != nil {
		return false, err
	}
	var response struct {
		Tun struct {
			Enable *bool `json:"enable"`
		} `json:"tun"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return false, fmt.Errorf("controller configs: %w", err)
	}
	if response.Tun.Enable == nil {
		return false, fmt.Errorf("controller configs: missing tun.enable")
	}
	return *response.Tun.Enable, nil
}

// WatchDialFailures streams Mihomo's warning log and reports every failed dial
// whose rule target is one of groups(), read per failure so group changes apply
// without reconnecting. Log payloads name traffic destinations, so each is
// dropped here and never logged, stored, or returned.
func (c *Controller) WatchDialFailures(ctx context.Context, groups func() []string, report func(group string)) error {
	// The stream is long-lived; ctx, not the request timeout, ends it.
	client := *c.Client
	client.Timeout = 0
	response, err := c.send(ctx, &client, http.MethodGet, "/logs?level=warning", nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("controller logs: %w", err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 4096), maxLogLine)
	for scanner.Scan() {
		var entry struct {
			Payload string `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		if group, ok := dialFailureGroup(entry.Payload, groups); ok {
			report(group)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("controller logs: %w", err)
	}
	return fmt.Errorf("controller logs: stream ended")
}

// dialFailureGroup matches Mihomo's "[TCP] dial <target> ..." warning. Group
// names may contain spaces, so the longest known name followed by a space wins.
func dialFailureGroup(payload string, groups func() []string) (string, bool) {
	rest, ok := strings.CutPrefix(payload, "[TCP] dial ")
	if !ok {
		rest, ok = strings.CutPrefix(payload, "[UDP] dial ")
	}
	if !ok {
		return "", false
	}
	best := ""
	for _, group := range groups() {
		if len(group) > len(best) && strings.HasPrefix(rest, group+" ") {
			best = group
		}
	}
	return best, best != ""
}

func (c *Controller) do(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	response, err := c.send(ctx, c.Client, method, path, body)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxControllerResponse+1))
	if err != nil {
		return nil, fmt.Errorf("controller response: %w", err)
	}
	if len(data) > maxControllerResponse {
		return nil, fmt.Errorf("controller response: too large")
	}
	return data, nil
}

// send issues an authenticated controller request. A non-2xx response is an
// error and its body is already closed.
func (c *Controller) send(ctx context.Context, client *http.Client, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("controller request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("controller request: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		response.Body.Close()
		return nil, fmt.Errorf("controller %s %s: %s", method, path, response.Status)
	}
	return response, nil
}
