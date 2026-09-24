package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPatchFilterIsTransactionalAndSupportsUpsertDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "filters.toml")
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\n")
	writeFile(t, dir, "resources.toml", "[[resource]]\nid = \"ads\"\nkind = \"rule-set\"\nformat = \"yaml\"\nrule_type = \"domain\"\nurl = \"https://example.com/ads\"\nenabled = true\n\n[[resource]]\nid = \"tracking\"\nkind = \"rule-set\"\nformat = \"yaml\"\nrule_type = \"domain\"\nurl = \"https://example.com/tracking\"\nenabled = true\n")
	writeFile(t, dir, "filters.toml", "[[filter]]\nid = \"ads\"\nresource = \"ads\"\nformat = \"yaml\"\ntarget = \"Proxy\"\nenabled = true\n\n[[filter]]\nid = \"unrelated\"\nresource = \"tracking\"\nformat = \"yaml\"\ntarget = \"DIRECT\"\nenabled = true\n")

	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := "REJECT"
	if err := PatchFilter(path, current, "ads", FilterEdit{Target: &target}); err != nil {
		t.Fatal(err)
	}
	updated, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Filters; len(got) != 2 || got[0].Target != target || got[1].ID != "unrelated" || got[1].Target != "DIRECT" {
		t.Fatalf("patch lost intent or unrelated filter: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("filters file mode = %04o, want 0600", got)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	missing := "missing"
	if err := PatchFilter(path, updated, "ads", FilterEdit{Resource: &missing}); err == nil {
		t.Fatal("unknown resource accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("invalid resource edit changed filters.toml")
	}

	resource, format, target := "tracking", FormatYAML, "DIRECT"
	enabled := true
	if err := PatchFilter(path, updated, "new", FilterEdit{Resource: &resource, Format: &format, Target: &target, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	upserted, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(upserted.Filters) != 3 || upserted.Filters[2].ID != "new" {
		t.Fatalf("new filter not upserted: %+v", upserted.Filters)
	}
	if err := DeleteFilter(path, upserted, "new"); err != nil {
		t.Fatal(err)
	}
	deleted, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Filters) != 2 || deleted.Filters[0].ID != "ads" || deleted.Filters[1].ID != "unrelated" {
		t.Fatalf("delete changed unrelated filters: %+v", deleted.Filters)
	}
}
