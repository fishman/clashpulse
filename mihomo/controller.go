package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxControllerResponse = 1 << 20

type Controller struct {
	BaseURL string
	Secret  string
	Client  *http.Client
}

type Proxy struct {
	Name string
	Type string
}

type Group struct {
	Name     string
	Type     string
	Proxies  []string
	Selected string
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
	return &Controller{BaseURL: strings.TrimRight(baseURL, "/"), Secret: secret, Client: client}, nil
}

func (c *Controller) Proxies(ctx context.Context) ([]Proxy, []Group, error) {
	body, err := c.do(ctx, http.MethodGet, "/proxies", nil)
	if err != nil {
		return nil, nil, err
	}
	var response struct {
		Proxies map[string]struct {
			Name string   `json:"name"`
			Type string   `json:"type"`
			All  []string `json:"all"`
			Now  string   `json:"now"`
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
		proxies = append(proxies, Proxy{Name: proxy.Name, Type: proxy.Type})
	}
	return proxies, groups, nil
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

func (c *Controller) Reload(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPut, "/configs", strings.NewReader("{}"))
	return err
}

func (c *Controller) do(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("controller request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	response, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("controller request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxControllerResponse+1))
	if err != nil {
		return nil, fmt.Errorf("controller response: %w", err)
	}
	if len(data) > maxControllerResponse {
		return nil, fmt.Errorf("controller response: too large")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("controller %s %s: %s", method, path, response.Status)
	}
	return data, nil
}
