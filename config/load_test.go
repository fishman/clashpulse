package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsUnknownKeyWithFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[mihomo]\nbinary = \"system\"\nbogus = true\n")

	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "config.toml") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRejectsDanglingFilterResource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "filters.toml", "[[filter]]\nid = \"ads\"\nresource = \"missing\"\ntarget = \"Proxy\"\n")

	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadAlertThresholdDefaultAndOverride(t *testing.T) {
	dir := t.TempDir()
	defaultConfig, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if defaultConfig.Monitor.AlertThreshold != 250*time.Millisecond {
		t.Fatalf("default alert threshold = %v", defaultConfig.Monitor.AlertThreshold)
	}
	writeFile(t, dir, "config.toml", "[monitor]\nalert_threshold = \"350ms\"\n")
	modified, err := Load(dir)
	if err != nil || modified.Monitor.AlertThreshold != 350*time.Millisecond {
		t.Fatalf("configured threshold = %v, %v", modified.Monitor.AlertThreshold, err)
	}
	writeFile(t, dir, "config.toml", "[monitor]\nalert_threshold = \"-1ms\"\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "alert_threshold") {
		t.Fatalf("invalid threshold accepted: %v", err)
	}
}

func TestLoadMonitorPolicyDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	defaults, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Monitor.TestURL != "https://www.gstatic.com/generate_204" || defaults.Monitor.Interval != 5*time.Minute || defaults.Monitor.Timeout != 5*time.Second || defaults.Monitor.Concurrency != 3 || defaults.Monitor.Threshold != 800*time.Millisecond || defaults.Monitor.AlertThreshold != 250*time.Millisecond || defaults.Monitor.ConsecutiveBadSamples != 3 || defaults.Monitor.MinImprovement != 100*time.Millisecond || defaults.Monitor.Cooldown != 10*time.Minute || defaults.Monitor.Jitter != 15*time.Second {
		t.Fatalf("monitor policy defaults = %+v", defaults.Monitor)
	}
	writeFile(t, dir, "config.toml", "[monitor]\ntimeout = \"2s\"\nconcurrency = 4\nthreshold = \"600ms\"\nconsecutive_bad_samples = 2\nmin_improvement = \"150ms\"\ncooldown = \"0s\"\njitter = \"0s\"\n")
	configured, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if configured.Monitor.Timeout != 2*time.Second || configured.Monitor.Concurrency != 4 || configured.Monitor.Threshold != 600*time.Millisecond || configured.Monitor.ConsecutiveBadSamples != 2 || configured.Monitor.MinImprovement != 150*time.Millisecond || configured.Monitor.Cooldown != 0 || configured.Monitor.Jitter != 0 {
		t.Fatalf("configured monitor policy = %+v", configured.Monitor)
	}
	writeFile(t, dir, "config.toml", "[monitor]\ninterval = \"1s\"\n")
	short, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if short.Monitor.Timeout != time.Second || short.Monitor.Jitter != 100*time.Millisecond {
		t.Fatalf("short-interval defaults = timeout %v, jitter %v", short.Monitor.Timeout, short.Monitor.Jitter)
	}
}

func TestPatchSettingsPersistsMonitorPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, dir, "config.toml", "[monitor]\n")
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	testURL := "https://example.com/204"
	interval, timeout, threshold, alertThreshold := 2*time.Minute, 3*time.Second, 750*time.Millisecond, 350*time.Millisecond
	concurrency, badSamples := 5, 4
	improvement, cooldown, jitter := 125*time.Millisecond, time.Duration(0), time.Duration(0)
	patch := SettingsPatch{
		MonitorTestURL:               &testURL,
		MonitorInterval:              &interval,
		MonitorTimeout:               &timeout,
		MonitorConcurrency:           &concurrency,
		MonitorThreshold:             &threshold,
		AlertThreshold:               &alertThreshold,
		MonitorConsecutiveBadSamples: &badSamples,
		MonitorMinImprovement:        &improvement,
		MonitorCooldown:              &cooldown,
		MonitorJitter:                &jitter,
	}
	if err := PatchSettings(path, current, patch); err != nil {
		t.Fatal(err)
	}
	updated, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Monitor.TestURL != testURL || updated.Monitor.Interval != interval || updated.Monitor.Timeout != timeout || updated.Monitor.Concurrency != concurrency || updated.Monitor.Threshold != threshold || updated.Monitor.AlertThreshold != alertThreshold || updated.Monitor.ConsecutiveBadSamples != badSamples || updated.Monitor.MinImprovement != improvement || updated.Monitor.Cooldown != cooldown || updated.Monitor.Jitter != jitter {
		t.Fatalf("persisted monitor policy = %+v", updated.Monitor)
	}
}

func TestLoadRejectsInvalidMonitorPolicyLimits(t *testing.T) {
	for name, setting := range map[string]string{
		"blank test URL":         "test_url = \"\"",
		"insecure test URL":      "test_url = \"http://example.com\"",
		"fragmented test URL":    "test_url = \"https://example.com/204#fragment\"",
		"zero timeout":           "timeout = \"0s\"",
		"timeout after interval": "timeout = \"6m\"",
		"zero concurrency":       "concurrency = 0",
		"excess concurrency":     "concurrency = 65",
		"zero bad samples":       "consecutive_bad_samples = 0",
		"excess bad samples":     "consecutive_bad_samples = 6",
		"negative cooldown":      "cooldown = \"-1s\"",
		"excess jitter":          "jitter = \"5m\"",
		"interval over one day":  "interval = \"25h\"",
		"zero switch threshold":  "threshold = \"0s\"",
		"zero improvement":       "min_improvement = \"0s\"",
		"negative jitter":        "jitter = \"-1s\"",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "config.toml", "[monitor]\n"+setting+"\n")
			if _, err := Load(dir); err == nil {
				t.Fatalf("invalid monitor setting accepted: %s", setting)
			}
		})
	}
}
