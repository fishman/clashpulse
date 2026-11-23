package mihomo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/monitor"
	"github.com/fishman/clashpulse/services"
)

// ProbeSlots is the number of pinned probe listeners, and so the number of
// nodes that can be measured at once.
const ProbeSlots = 4

const probePrefix = "clashpulse-probe-"

// ProbeName names both the hidden probe group and the listener of a slot.
func ProbeName(slot int) string { return probePrefix + strconv.Itoa(slot) }

func IsProbeName(name string) bool {
	rest, ok := strings.CutPrefix(name, probePrefix)
	if !ok {
		return false
	}
	slot, err := strconv.Atoi(rest)
	return err == nil && slot >= 1 && slot <= ProbeSlots && ProbeName(slot) == name
}

// ProbeListener is one pinned loopback listener; its credential lives only in
// memory and is never part of a result or error.
type ProbeListener struct {
	Port               int
	Username, Password string
}

// NeedsProbes reports whether intent measures through the probe pool.
func NeedsProbes(intent config.Snapshot) bool {
	if intent.Monitor.SwitchPolicy == monitor.SwitchSteadyStream {
		return true
	}
	for _, service := range intent.Services {
		if service.Enabled {
			return true
		}
	}
	return false
}

// ProbePool leases the probe slots. A lease pins its slot's group to one node
// until Release, so concurrent measurements never re-point each other's slot.
type ProbePool struct {
	controller *Controller
	listeners  []ProbeListener
	free       chan int
}

type ProbeLease struct {
	pool *ProbePool
	slot int
}

func NewProbePool(controller *Controller, listeners []ProbeListener) *ProbePool {
	pool := &ProbePool{controller: controller, listeners: append([]ProbeListener(nil), listeners...), free: make(chan int, len(listeners))}
	for slot := 1; slot <= len(listeners); slot++ {
		pool.free <- slot
	}
	return pool
}

func (p *ProbePool) Size() int { return len(p.listeners) }

// Acquire waits for a free slot and selects proxy in its group.
func (p *ProbePool) Acquire(ctx context.Context, proxy string) (*ProbeLease, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case slot := <-p.free:
		if err := p.controller.Select(ctx, ProbeName(slot), proxy); err != nil {
			p.free <- slot
			return nil, fmt.Errorf("mihomo: pin probe slot: %w", err)
		}
		return &ProbeLease{pool: p, slot: slot}, nil
	}
}

func (l *ProbeLease) Release() { l.pool.free <- l.slot }

// Transport sends requests through the leased slot's listener only.
func (l *ProbeLease) Transport() *http.Transport {
	listener := l.pool.listeners[l.slot-1]
	proxy := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", listener.Port), User: url.UserPassword(listener.Username, listener.Password)}
	return services.ProxyTransport(proxy)
}

// StreamPull pulls rawURL through the leased slot, so the measurement follows
// the pinned node only.
func (l *ProbeLease) StreamPull(ctx context.Context, rawURL string) StreamResult {
	transport := l.Transport()
	transport.TLSHandshakeTimeout = StreamStallTimeout
	defer transport.CloseIdleConnections()
	return pullThrough(ctx, transport, rawURL, StreamStallTimeout, StreamPullTimeout)
}
