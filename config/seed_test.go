package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedEnablesReviewedResourceCatalog(t *testing.T) {
	dir := t.TempDir()
	if err := Seed(dir); err != nil {
		t.Fatal(err)
	}
	assertDefaultResourcesEnabled(t, dir)
}

func TestSeedMigratesPristineDisabledResourceCatalog(t *testing.T) {
	dir := t.TempDir()
	legacy, err := os.ReadFile(filepath.Join("testdata", "resources-disabled-seed.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources.toml"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Seed(dir); err != nil {
		t.Fatal(err)
	}
	assertDefaultResourcesEnabled(t, dir)
}

func TestSeedPreservesCustomizedResourceCatalog(t *testing.T) {
	dir := t.TempDir()
	custom := []byte("[[resource]]\nid = \"custom\"\nkind = \"geoip.dat\"\nformat = \"dat\"\nurl = \"https://custom.invalid/geoip.dat\"\nenabled = false\n")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "resources.toml")
	if err := os.WriteFile(path, custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Seed(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(custom) {
		t.Fatalf("custom resources.toml changed: err=%v", err)
	}
}

func assertDefaultResourcesEnabled(t *testing.T, dir string) {
	t.Helper()
	snapshot, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"geoip": true, "geosite": true, "country": true, "cn": true}
	if len(snapshot.Resources) != len(want) {
		t.Fatalf("default resource count = %d, want %d", len(snapshot.Resources), len(want))
	}
	for _, resource := range snapshot.Resources {
		if !want[resource.ID] || !resource.Enabled {
			t.Errorf("resource %q is not enabled by default", resource.ID)
		}
		delete(want, resource.ID)
	}
	if len(snapshot.DNS.Routes) != 0 || len(snapshot.Filters) != 0 {
		t.Fatal("default resource catalog unexpectedly enabled routing or filters")
	}
}
