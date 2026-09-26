package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/subscriptions"
)

func TestLocalProfileFileBoundary(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "my profile.yaml")
	body := []byte("proxies:\n  - name: alpha\n    type: direct\n")
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readLocalProfile(context.Background(), source)
	if err != nil || string(loaded) != string(body) {
		t.Fatalf("read local profile = %q, %v", loaded, err)
	}
	if err := os.WriteFile(source, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if string(loaded) != string(body) {
		t.Fatal("source rewrite changed in-memory profile")
	}

	assertRejected := func(path string) {
		t.Helper()
		_, err := readLocalProfile(context.Background(), path)
		public, ok := core.PublicActivation(err)
		if !ok || public.Stage != core.ActivationFileInput || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "password=private") {
			t.Fatalf("unsafe source error = %v", err)
		}
	}
	link := filepath.Join(dir, "password=private")
	if err := os.Symlink(source, link); err != nil {
		t.Logf("symlink unavailable: %v", err)
	} else {
		assertRejected(link)
	}
	assertRejected(dir)
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	assertRejected(source)
	if err := os.Truncate(source, subscriptions.DefaultMaxProfileBytes+1); err != nil {
		t.Fatal(err)
	}
	assertRejected(source)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	assertCancelled := func() {
		t.Helper()
		_, err := readLocalProfile(cancelled, source)
		public, ok := core.PublicActivation(err)
		if !ok || public.Stage != core.ActivationFileInput {
			t.Fatalf("cancelled file input = %v", err)
		}
	}
	assertCancelled()
}
