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

// newProbeListeners picks distinct free loopback ports and random credentials
// for the probe pool. Every socket stays open until all ports are chosen so no
// two slots share one; ports are re-checked before Mihomo starts.
func newProbeListeners() ([]mihomo.ProbeListener, error) {
	listeners := make([]mihomo.ProbeListener, mihomo.ProbeSlots)
	held := make([]net.Listener, 0, mihomo.ProbeSlots)
	defer func() {
		for _, socket := range held {
			_ = socket.Close()
		}
	}()
	for i := range listeners {
		socket, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		held = append(held, socket)
		secret := make([]byte, 24)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		listeners[i] = mihomo.ProbeListener{Port: socket.Addr().(*net.TCPAddr).Port, Username: hex.EncodeToString(secret[:8]), Password: hex.EncodeToString(secret[8:])}
	}
	return listeners, nil
}

// streamProber measures a steady stream on a leased probe slot.
type streamProber struct{ pool *mihomo.ProbePool }

func (p streamProber) StreamPull(ctx context.Context, proxy, url string) monitor.StreamSample {
	lease, err := p.pool.Acquire(ctx, proxy)
	if err != nil {
		return monitor.StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Outcome: monitor.StreamOutcome("error")}
	}
	defer lease.Release()
	result := lease.StreamPull(ctx, url)
	return monitor.StreamSample{Proxy: proxy, URL: url, FinishedAt: time.Now(), Bytes: result.Bytes,
		FirstByte: result.FirstByte, Duration: result.Duration, Outcome: monitor.StreamOutcome(result.Outcome)}
}
