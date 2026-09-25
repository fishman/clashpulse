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

func TestPatchSubscriptionStoresUserAgentWithoutChangingPrivateURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subscriptions.toml")
	if err := Write(path, []byte("[[subscription]]\nid = \"daily\"\nurl = \"https://provider.invalid/profile?token=private\"\n")); err != nil {
		t.Fatal(err)
	}
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	agent := "clash-verge/v2.5.6"
	if err := PatchSubscription(path, current, "daily", SubscriptionEdit{UserAgent: &agent}); err != nil {
		t.Fatal(err)
	}
	updated, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Subscriptions[0]; got.UserAgent != agent || got.URL != current.Subscriptions[0].URL {
		t.Fatal("user agent edit changed the private source or failed to persist")
	}
	bad := "browser\r\nAuthorization: private"
	if err := PatchSubscription(path, updated, "daily", SubscriptionEdit{UserAgent: &bad}); err == nil {
		t.Fatal("HTTP header injection was accepted")
	}
}
