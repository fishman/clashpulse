// Package sysproxy applies and resets the desktop's HTTP and HTTPS proxy.
//
// Library review: github.com/Trisia/gosysproxy was considered and rejected
// because it supports Windows only and its package API does not provide the
// cancellable, injectable transaction needed here. A pkg.go.dev search for
// macOS system-proxy libraries did not identify a focused usable alternative;
// Linux uses gsettings and macOS uses networksetup through the stdlib runner,
// both with explicit argv rather than a broad cross-platform dependency.
package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Manager controls this application's system-proxy settings.
type Manager struct {
	mu       sync.Mutex
	runner   commandRunner
	previous map[string]string
	// applied marks settings this manager currently owns. Linux resets them
	// instead of restoring prior values, so an untouched configuration is never
	// mistaken for one this application wrote.
	applied bool
}

// ProxySettings contains the active HTTP and HTTPS proxies; nil entries mean
// that scheme has no configured proxy.
type ProxySettings struct {
	HTTP  *url.URL
	HTTPS *url.URL
}

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// NewManager creates a system-proxy manager for the current platform.
func NewManager() *Manager {
	return &Manager{runner: execRunner{}}
}

func newManager(runner commandRunner) *Manager {
	return &Manager{runner: runner}
}

func makeProxyURL(host string, port int) (*url.URL, error) {
	if host != strings.TrimSpace(host) {
		return nil, errors.New("sysproxy: invalid proxy endpoint")
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
		if net.ParseIP(host) == nil {
			return nil, errors.New("sysproxy: invalid proxy endpoint")
		}
	}
	if host == "" || strings.ContainsAny(host, "/\\?#@[] \t\r\n") || port < 1 || port > 65535 || (strings.Contains(host, ":") && net.ParseIP(host) == nil) {
		return nil, errors.New("sysproxy: invalid proxy endpoint")
	}
	return &url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port))}, nil
}

func splitListenerAddress(address string) (string, int, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("listener address must be a loopback host and port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", 0, errors.New("listener host must be a loopback IP address")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("listener port must be between 1 and 65535")
	}
	return host, port, nil
}

func verifyListenerReady(ctx context.Context, host string, port int) error {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return connection.Close()
}
