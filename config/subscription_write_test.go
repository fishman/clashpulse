package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPatchSubscriptionPreservesPrivateSourceAndRejectsInvalidUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subscriptions.toml")
	source := "https://provider.invalid/profile?token=private"
	if err := Write(path, []byte("[[subscription]]\nid = \"daily\"\nurl = \""+source+"\"\nenabled = true\n")); err != nil {
		t.Fatal(err)
	}
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	if err := PatchSubscription(path, current, "daily", SubscriptionEdit{Name: &name}); err != nil {
		t.Fatal(err)
	}
	next, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.Subscriptions[0]; got.Name != name || got.URL != source || !got.Enabled {
		t.Fatalf("update lost private source or intent: %+v", got)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := "http://user:pass@invalid.example/profile"
	if err := PatchSubscription(path, next, "daily", SubscriptionEdit{URL: &bad}); err == nil {
		t.Fatal("unsafe source accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid edit changed user intent")
	}
}
