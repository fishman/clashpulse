package mihomo

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

// StreamProbeName names both the hidden probe group and its listener.
const StreamProbeName = "clashpulse-stream-probe"

const (
	StreamStallTimeout = 5 * time.Second
	StreamPullTimeout  = 30 * time.Second
)

// StreamListener is the per-start probe listener; its credential lives only
// in memory and is never part of a result or error.
type StreamListener struct {
	Port               int
	Username, Password string
}

type StreamResult struct {
	Bytes               int64
	FirstByte, Duration time.Duration
	Outcome             string
}

// StreamPull points the probe group at proxy and pulls rawURL through the
// pinned listener, so the measurement follows that node only.
func (c *Controller) StreamPull(ctx context.Context, listener StreamListener, proxy, rawURL string) StreamResult {
	if err := c.Select(ctx, StreamProbeName, proxy); err != nil {
		return StreamResult{Outcome: "error"}
	}
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", listener.Port), User: url.UserPassword(listener.Username, listener.Password)}
	transport := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: StreamStallTimeout,
	}
	defer transport.CloseIdleConnections()
	return pullThrough(ctx, transport, rawURL, StreamStallTimeout, StreamPullTimeout)
}

func pullThrough(ctx context.Context, transport http.RoundTripper, rawURL string, stall, total time.Duration) StreamResult {
	ctx, cancel := context.WithTimeout(ctx, total)
	defer cancel()
	started := time.Now()
	result := StreamResult{Outcome: "error"}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return result
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		result.Outcome = classify(ctx, err, false)
		return result
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength <= 0 {
		return result
	}
	// The stall timer cancels the request; the flag tells a stall from the
	// overall deadline, which surfaces as DeadlineExceeded instead.
	var stalled atomic.Bool
	timer := time.AfterFunc(stall, func() { stalled.Store(true); cancel() })
	defer timer.Stop()
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if result.Bytes == 0 {
				result.FirstByte = time.Since(started)
			}
			result.Bytes += int64(n)
			timer.Reset(stall)
		}
		if readErr == nil {
			continue
		}
		result.Duration = time.Since(started)
		switch {
		case errors.Is(readErr, io.EOF) && result.Bytes == response.ContentLength:
			result.Outcome = "success"
		case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
			result.Outcome = "truncated"
		default:
			result.Outcome = classify(ctx, readErr, stalled.Load())
		}
		return result
	}
}

// classify maps a transport error to an outcome. A cancellation by the stall
// timer is a stall; the overall deadline is a timeout.
func classify(ctx context.Context, err error, stalled bool) string {
	var certificate *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	switch {
	case errors.As(err, &certificate) || errors.As(err, &record):
		return "tls"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timeout"
	case stalled:
		return "stall"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "reset"
	}
	return "error"
}
