package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := Write(path, []byte(contents)); err != nil {
		t.Fatal(err)
	}
}

func startWatcher(t *testing.T, dir string, store *Store) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Watch(ctx, dir, store)
	}()
	return cancel, errCh
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestWatchReloadsCreateUpdateDelete(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "config.toml"), "[mihomo]\nbinary = \"system\"\n")

	store := NewStore(Snapshot{})
	cancel, errCh := startWatcher(t, dir, store)
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}()

	waitFor(t, func() bool { return store.Snapshot().Mihomo.Binary == "system" })

	mustWrite(t, filepath.Join(dir, "subscriptions.toml"), "[[subscription]]\nid = \"primary\"\nname = \"Primary\"\nurl = \"https://provider.example/subscription\"\nenabled = true\nrefresh_interval = \"12h\"\n")
	waitFor(t, func() bool { return len(store.Snapshot().Subscriptions) == 1 })

	mustWrite(t, filepath.Join(dir, "config.toml"), "[mihomo]\nbinary = \"bundled\"\n")
	waitFor(t, func() bool { return store.Snapshot().Mihomo.Binary == "bundled" })

	if err := os.Remove(filepath.Join(dir, "subscriptions.toml")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(store.Snapshot().Subscriptions) == 0 })
}

func TestWatchPreservesLiveSnapshotOnInvalidEdit(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "config.toml"), "[mihomo]\nbinary = \"system\"\n")

	store := NewStore(Snapshot{})
	cancel, errCh := startWatcher(t, dir, store)
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}()

	waitFor(t, func() bool { return store.Snapshot().Mihomo.Binary == "system" })

	mustWrite(t, filepath.Join(dir, "config.toml"), "[mihomo]\nbinary = \"system\"\nbogus = true\n")
	time.Sleep(150 * time.Millisecond)
	if got := store.Snapshot().Mihomo.Binary; got != "system" {
		t.Fatalf("binary = %q", got)
	}
}

func TestWatchNoOpsOnSelfWrite(t *testing.T) {
	dir := t.TempDir()
	contents := "[mihomo]\nbinary = \"system\"\n"
	mustWrite(t, filepath.Join(dir, "config.toml"), contents)

	store := NewStore(Snapshot{})
	cancel, errCh := startWatcher(t, dir, store)
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}()

	waitFor(t, func() bool { return store.Snapshot().Mihomo.Binary == "system" })

	changed := make(chan Change, 1)
	store.Subscribe("mihomo", func(change Change) { changed <- change })
	mustWrite(t, filepath.Join(dir, "config.toml"), contents)

	select {
	case change := <-changed:
		t.Fatalf("unexpected change = %+v", change)
	case <-time.After(250 * time.Millisecond):
	}
}

func TestWatchWithResultsReportsNoopRecoveryAfterInvalidEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	valid := "[mihomo]\nbinary = \"system\"\n"
	mustWrite(t, path, valid)
	results := make(chan error, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- WatchWithResults(ctx, dir, NewStore(Snapshot{}), func(err error) { results <- err }) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}()
	if err := <-results; err != nil {
		t.Fatalf("initial valid config result = %v", err)
	}
	mustWrite(t, path, valid+"bogus = true\n")
	select {
	case err := <-results:
		if err == nil {
			t.Fatal("invalid edit produced a success result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("invalid edit produced no watcher result")
	}
	mustWrite(t, path, valid)
	select {
	case err := <-results:
		if err != nil {
			t.Fatalf("unchanged valid config did not report recovery: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("valid no-op edit produced no recovery result")
	}
}
