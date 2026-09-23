package config

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestLoadRejectsWrongTableKinds(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[[mihomo]]\nbinary = \"system\"\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "mihomo") {
		t.Fatalf("config table kind accepted: %v", err)
	}

	dir = t.TempDir()
	writeFile(t, dir, "subscriptions.toml", "[subscription]\nid = \"primary\"\nurl = \"https://provider.example/subscription\"\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "subscription") {
		t.Fatalf("subscription table kind accepted: %v", err)
	}
}

func TestLoadAcceptsMultilineAndLiteralStrings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\n[monitor]\nenabled = true\ntest_url = '''https://example.invalid/generate_204'''\ninterval = \"5m\"\n")
	writeFile(t, dir, "subscriptions.toml", "[[subscription]]\nid = \"primary\"\nname = \"\"\"Primary\nProfile\"\"\"\nurl = \"https://provider.example/subscription\"\nenabled = true\nrefresh_interval = \"12h\"\n")
	if _, err := Load(dir); err != nil {
		t.Fatalf("multiline/literal TOML rejected: %v", err)
	}
}

func TestLoadHonorsAllowHTTPAndRejectsUserinfo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\n")
	writeFile(t, dir, "subscriptions.toml", "[[subscription]]\nid = \"plain\"\nurl = \"http://provider.example/subscription\"\nallow_http = true\nenabled = true\nrefresh_interval = \"12h\"\n")
	if _, err := Load(dir); err != nil {
		t.Fatalf("http subscription with allow_http rejected: %v", err)
	}

	dir = t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\n")
	writeFile(t, dir, "subscriptions.toml", "[[subscription]]\nid = \"auth\"\nurl = \"https://user@example.com/subscription\"\nenabled = true\nrefresh_interval = \"12h\"\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "userinfo") {
		t.Fatalf("userinfo accepted: %v", err)
	}
}

func TestLoadRejectsResolverSetMapAndEndpointIssues(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\n")
	writeFile(t, dir, "resources.toml", strings.Join([]string{
		"[[resource]]",
		"id = \"domestic\"",
		"kind = \"rule-set\"",
		"url = \"https://example.com/rule-set\"",
		"enabled = true",
		"",
		"[[resolver_set]]",
		"id = \"other\"",
		"endpoints = [\"https://resolver.example/dns\"]",
		"",
		"[[dns_route]]",
		"suffix = \"cn\"",
		"resolver_set = \"domestic\"",
	}, "\n"))
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "resolver_set") {
		t.Fatalf("dns_route resolver_set accepted resource id: %v", err)
	}

	dir = t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\n")
	writeFile(t, dir, "resources.toml", "[[resolver_set]]\nid = \"domestic\"\nendpoints = [\"garbage\"]\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "endpoints") {
		t.Fatalf("arbitrary resolver endpoint accepted: %v", err)
	}
}

func TestLoadRejectsSchemesAsPathsAndRequiresFilterFormat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\n")
	writeFile(t, dir, "resources.toml", "[[resource]]\nid = \"geo\"\nkind = \"geosite.dat\"\nurl = \"http:/broken\"\nenabled = true\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("malformed scheme accepted as path: %v", err)
	}

	dir = t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\n")
	writeFile(t, dir, "resources.toml", "[[resource]]\nid = \"ads\"\nkind = \"rule-set\"\nurl = \"https://example.com/ads\"\nenabled = true\n")
	writeFile(t, dir, "filters.toml", "[[filter]]\nid = \"ads\"\nresource = \"ads\"\nenabled = true\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "format") {
		t.Fatalf("missing filter format accepted: %v", err)
	}
}

func TestLoadRejectsCustomBinary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"custom\"\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("custom binary accepted: %v", err)
	}
}

func TestLoadRejectsNonpositiveIntervalsAndTimeouts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", strings.Join([]string{
		"[mihomo]",
		"binary = \"system\"",
		"[monitor]",
		"enabled = true",
		"test_url = \"https://example.invalid/generate_204\"",
		"interval = \"0s\"",
	}, "\n"))
	writeFile(t, dir, "subscriptions.toml", strings.Join([]string{
		"[[subscription]]",
		"id = \"primary\"",
		"url = \"https://provider.example/subscription\"",
		"enabled = true",
		"refresh_interval = \"0s\"",
		"timeout = \"0s\"",
	}, "\n"))
	writeFile(t, dir, "resources.toml", strings.Join([]string{
		"[[resource]]",
		"id = \"ads\"",
		"kind = \"rule-set\"",
		"url = \"https://example.com/ads\"",
		"enabled = true",
		"interval = \"0s\"",
	}, "\n"))
	writeFile(t, dir, "filters.toml", strings.Join([]string{
		"[[filter]]",
		"id = \"ads\"",
		"resource = \"ads\"",
		"format = \"rule-set\"",
		"enabled = true",
		"interval = \"0s\"",
	}, "\n"))
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("nonpositive intervals/timeouts accepted: %v", err)
	}
}

func TestStoreClonesChangeSnapshotsPerCallback(t *testing.T) {
	store := NewStore(Snapshot{Subscriptions: []Subscription{{ID: "one", URL: "https://example.com/sub", Enabled: true}}})
	store.Subscribe("subscriptions", func(change Change) {
		change.After.Subscriptions[0].Name = "mutated"
	})
	next := store.Snapshot()
	next.Subscriptions[0].Name = "original"
	got := store.Replace(next)
	if got.After.Subscriptions[0].Name != "original" {
		t.Fatalf("returned change mutated: %+v", got.After.Subscriptions[0])
	}
	if snap := store.Snapshot(); snap.Subscriptions[0].Name != "original" {
		t.Fatalf("store snapshot mutated: %+v", snap.Subscriptions[0])
	}
}

type fakeWatcher struct {
	added  chan string
	events chan fsnotify.Event
	errs   chan error
	closed chan struct{}
}

func (f *fakeWatcher) Add(name string) error {
	f.added <- name
	return nil
}

func (f *fakeWatcher) Close() error {
	close(f.closed)
	return nil
}

func (f *fakeWatcher) Events() <-chan fsnotify.Event { return f.events }
func (f *fakeWatcher) Errors() <-chan error          { return f.errs }

func TestWatchRegistersBeforeInitialLoadAndStopsOnClosedErrors(t *testing.T) {
	origNewWatcher := newWatcher
	origLoad := loadSnapshotFn
	defer func() {
		newWatcher = origNewWatcher
		loadSnapshotFn = origLoad
	}()

	f := &fakeWatcher{
		added:  make(chan string, 1),
		events: make(chan fsnotify.Event),
		errs:   make(chan error),
		closed: make(chan struct{}),
	}
	newWatcher = func() (dirWatcher, error) { return f, nil }
	loaded := make(chan struct{}, 1)
	loadSnapshotFn = func(dir string) (Snapshot, error) {
		select {
		case <-f.added:
			loaded <- struct{}{}
			return Snapshot{}, nil
		default:
			return Snapshot{}, errors.New("load happened before watch registration")
		}
	}
	close(f.errs)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Watch(ctx, t.TempDir(), NewStore(Snapshot{})); err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	select {
	case <-loaded:
	default:
		t.Fatal("load did not happen after registration")
	}
}

func TestWatchCancelsPendingDebounce(t *testing.T) {
	origNewWatcher := newWatcher
	origLoad := loadSnapshotFn
	defer func() {
		newWatcher = origNewWatcher
		loadSnapshotFn = origLoad
	}()

	f := &fakeWatcher{
		added:  make(chan string, 1),
		events: make(chan fsnotify.Event, 1),
		errs:   make(chan error),
		closed: make(chan struct{}),
	}
	newWatcher = func() (dirWatcher, error) { return f, nil }
	var mu sync.Mutex
	loads := 0
	loadSnapshotFn = func(dir string) (Snapshot, error) {
		mu.Lock()
		loads++
		mu.Unlock()
		return Snapshot{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, t.TempDir(), NewStore(Snapshot{})) }()
	<-f.added
	f.events <- fsnotify.Event{Name: "config.toml"}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not stop on cancel")
	}
	mu.Lock()
	defer mu.Unlock()
	if loads != 1 {
		t.Fatalf("pending debounce reload ran after cancel: %d", loads)
	}
}
