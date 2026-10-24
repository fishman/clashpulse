package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"time"

	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
)

// newStreamListener picks a free loopback port and a random credential for
// the probe listener. The port is re-checked before Mihomo starts.
func newStreamListener() (mihomo.StreamListener, error) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return mihomo.StreamListener{}, err
	}
	port := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		return mihomo.StreamListener{}, err
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return mihomo.StreamListener{}, err
	}
	return mihomo.StreamListener{Port: port, Username: hex.EncodeToString(secret[:8]), Password: hex.EncodeToString(secret[8:])}, nil
}

type streamProber struct {
	controller *mihomo.Controller
	listener   mihomo.StreamListener
}

func (p streamProber) StreamPull(ctx context.Context, proxy, url string) monitor.StreamSample {
	result := p.controller.StreamPull(ctx, p.listener, proxy, url)
	return monitor.StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Bytes: result.Bytes,
		FirstByte: result.FirstByte, Duration: result.Duration, Outcome: monitor.StreamOutcome(result.Outcome)}
}
