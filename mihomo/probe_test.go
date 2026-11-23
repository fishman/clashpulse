package mihomo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
)

func testProbes() []ProbeListener {
	probes := make([]ProbeListener, ProbeSlots)
	for i := range probes {
		probes[i] = ProbeListener{Port: 17891 + i, Username: "u", Password: "p"}
	}
	return probes
}

// selectController answers only PUT /proxies/{group} with status and records
// each selection.
type selectController struct {
	mu      sync.Mutex
	selects []string
}

func newSelectController(t *testing.T, status int) (*Controller, *selectController) {
	t.Helper()
	record := &selectController{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/proxies/") {
			http.NotFound(w, r)
			return
		}
		record.mu.Lock()
		record.selects = append(record.selects, strings.TrimPrefix(r.URL.Path, "/proxies/"))
		record.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	controller, err := NewController(server.URL, "secret", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return controller, record
}

func servicesIntent(enabled bool) config.Snapshot {
	var intent config.Snapshot
	intent.Services = []config.Service{{ID: "claude", Enabled: enabled, CheckInterval: config.DefaultServiceInterval, Failures: 2}}
	return intent
}

func TestRenderProbesForServicesWithoutSteadyStream(t *testing.T) {
	profile := []byte("proxies:\n  - {name: node-a, type: direct}\n")
	out, err := Render(profile, servicesIntent(true), nil, probeListener, Capability{SupportsPinnedListener: true})
	if err != nil {
		t.Fatal(err)
	}
	if listeners, err := ProbeListeners(out); err != nil || len(listeners) != ProbeSlots || listeners[3].Port != 17894 {
		t.Fatalf("listeners = %+v, %v", listeners, err)
	}
	out, err = Render(profile, servicesIntent(false), nil, probeListener, Capability{SupportsPinnedListener: true})
	if err != nil || strings.Contains(string(out), "clashpulse-probe") {
		t.Fatalf("probes rendered for a disabled service: %v", err)
	}
	_, err = Render(profile, servicesIntent(true), nil, probeListener, Capability{})
	var missing *CapabilityError
	if !errors.As(err, &missing) || missing.ResourceID != "services" || missing.Kind != "pinned-listener" {
		t.Fatalf("services without pinned listeners: %v", err)
	}
}

func TestProbePoolLeasesAreExclusive(t *testing.T) {
	controller, record := newSelectController(t, http.StatusNoContent)
	pool := NewProbePool(controller, testProbes())
	ctx := context.Background()
	leases := make([]*ProbeLease, 0, ProbeSlots)
	seen := map[int]bool{}
	for i := 0; i < ProbeSlots; i++ {
		lease, err := pool.Acquire(ctx, "node")
		if err != nil || seen[lease.slot] {
			t.Fatalf("lease %d: %+v, %v", i, lease, err)
		}
		seen[lease.slot] = true
		leases = append(leases, lease)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(waitCtx, "node"); err == nil {
		t.Fatal("a fifth lease was granted while four are held")
	}
	leases[0].Release()
	lease, err := pool.Acquire(ctx, "node")
	if err != nil || lease.slot != leases[0].slot {
		t.Fatalf("released slot not reused: %+v, %v", lease, err)
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if len(record.selects) != ProbeSlots+1 || record.selects[ProbeSlots] != ProbeName(lease.slot) {
		t.Fatalf("selects = %v", record.selects)
	}
}

func TestProbePoolReleasesSlotWhenSelectFails(t *testing.T) {
	controller, _ := newSelectController(t, http.StatusInternalServerError)
	pool := NewProbePool(controller, testProbes()[:1])
	if _, err := pool.Acquire(context.Background(), "node"); err == nil {
		t.Fatal("failed select produced a lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// A freed slot reaches Select again (and fails on 500); a leaked one waits out the deadline.
	if _, err := pool.Acquire(ctx, "node"); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire = %v, want a select error", err)
	}
}

func TestProbeLeaseStreamPullOmitsCredentials(t *testing.T) {
	controller, _ := newSelectController(t, http.StatusNoContent)
	pool := NewProbePool(controller, []ProbeListener{{Port: 1, Username: "probe-user", Password: "probe-password"}})
	lease, err := pool.Acquire(context.Background(), "node-a")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	result := lease.StreamPull(context.Background(), "https://speed.example.com/file")
	if result.Outcome == "success" {
		t.Fatal("pull through a closed port succeeded")
	}
	if strings.Contains(result.Outcome, "probe-password") || strings.Contains(result.Outcome, "probe-user") {
		t.Fatal("result leaks listener credentials")
	}
}
