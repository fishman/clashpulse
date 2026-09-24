package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchAutomationPersistsStrictGroupIntent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := Write(path, []byte("[mihomo]\nbinary = \"system\"\n[monitor]\ninterval = \"5m\"\n")); err != nil {
		t.Fatal(err)
	}
	initial, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	if err := PatchAutomation(path, initial, id, true); err != nil {
		t.Fatal(err)
	}
	enabled, err := Load(dir)
	if err != nil || len(enabled.Monitor.AutomatedGroups) != 1 || enabled.Monitor.AutomatedGroups[0] != id || enabled.Mihomo.Binary != "system" {
		t.Fatalf("enabled automation = %+v, %v", enabled.Monitor, err)
	}
	if err := PatchAutomation(path, enabled, id, false); err != nil {
		t.Fatal(err)
	}
	disabled, err := Load(dir)
	if err != nil || len(disabled.Monitor.AutomatedGroups) != 0 {
		t.Fatalf("disabled automation = %+v, %v", disabled.Monitor, err)
	}
	if err := PatchAutomation(path, disabled, "bad/group", true); err == nil {
		t.Fatal("accepted invalid group identity")
	}
}

func TestLoadMonitoringDefaultsOnAndExplicitOptOut(t *testing.T) {
	dir := t.TempDir()
	defaults, err := Load(dir)
	if err != nil || !defaults.Monitor.Enabled {
		t.Fatalf("default monitor = %+v, %v", defaults.Monitor, err)
	}
	if err := Write(filepath.Join(dir, "config.toml"), []byte("[monitor]\nenabled = false\n")); err != nil {
		t.Fatal(err)
	}
	disabled, err := Load(dir)
	if err != nil || disabled.Monitor.Enabled {
		t.Fatalf("explicit monitor opt-out = %+v, %v", disabled.Monitor, err)
	}
}

func TestPatchSettingsValidatesBinarySelectionBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := Write(path, []byte("[mihomo]\nbinary = \"system\"\n")); err != nil {
		t.Fatal(err)
	}
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "mihomo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := PatchSettings(path, current, SettingsPatch{Binary: &binary}); err != nil {
		t.Fatal(err)
	}
	updated, err := Load(dir)
	if err != nil || updated.Mihomo.Binary != binary {
		t.Fatalf("binary selection = %q, %v", updated.Mihomo.Binary, err)
	}
	missing := filepath.Join(dir, "missing")
	if err := PatchSettings(path, updated, SettingsPatch{Binary: &missing}); err == nil {
		t.Fatal("persisted missing executable")
	}
	retained, err := Load(dir)
	if err != nil || retained.Mihomo.Binary != binary {
		t.Fatalf("failed binary edit lost selected executable: %q, %v", retained.Mihomo.Binary, err)
	}
}
