package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/monitor"
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
	if defaults.Monitor.TestURL != "http://cp.cloudflare.com/generate_204" || defaults.Monitor.Interval != time.Minute || defaults.Monitor.Timeout != 5*time.Second || defaults.Monitor.Concurrency != 3 || defaults.Monitor.Threshold != 800*time.Millisecond || defaults.Monitor.AlertThreshold != 250*time.Millisecond || defaults.Monitor.ConsecutiveBadSamples != 3 || defaults.Monitor.WindowSize != 5 || defaults.Monitor.MinCandidateSamples != 3 || defaults.Monitor.MaxFailures != 2 || defaults.Monitor.MinImprovement != 100*time.Millisecond || defaults.Monitor.Cooldown != 5*time.Minute || defaults.Monitor.Jitter != 10*time.Second {
		t.Fatalf("monitor policy defaults = %+v", defaults.Monitor)
	}
	writeFile(t, dir, "config.toml", "[monitor]\ntest_url = \"http://cp.cloudflare.com/generate_204\"\ntimeout = \"2s\"\nconcurrency = 4\nthreshold = \"600ms\"\nconsecutive_bad_samples = 2\nwindow_size = 8\nmin_candidate_samples = 4\nmax_failures = 3\nmin_improvement = \"150ms\"\ncooldown = \"0s\"\njitter = \"0s\"\n")
	configured, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if configured.Monitor.TestURL != "http://cp.cloudflare.com/generate_204" || configured.Monitor.Timeout != 2*time.Second || configured.Monitor.Concurrency != 4 || configured.Monitor.Threshold != 600*time.Millisecond || configured.Monitor.ConsecutiveBadSamples != 2 || configured.Monitor.WindowSize != 8 || configured.Monitor.MinCandidateSamples != 4 || configured.Monitor.MaxFailures != 3 || configured.Monitor.MinImprovement != 150*time.Millisecond || configured.Monitor.Cooldown != 0 || configured.Monitor.Jitter != 0 {
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
	concurrency, badSamples, window, candidates, failures := 5, 4, 10, 6, 4
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
		MonitorWindowSize:            &window,
		MonitorMinCandidateSamples:   &candidates,
		MonitorMaxFailures:           &failures,
	}
	if err := PatchSettings(path, current, patch); err != nil {
		t.Fatal(err)
	}
	updated, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Monitor.TestURL != testURL || updated.Monitor.Interval != interval || updated.Monitor.Timeout != timeout || updated.Monitor.Concurrency != concurrency || updated.Monitor.Threshold != threshold || updated.Monitor.AlertThreshold != alertThreshold || updated.Monitor.ConsecutiveBadSamples != badSamples || updated.Monitor.WindowSize != window || updated.Monitor.MinCandidateSamples != candidates || updated.Monitor.MaxFailures != failures || updated.Monitor.MinImprovement != improvement || updated.Monitor.Cooldown != cooldown || updated.Monitor.Jitter != jitter {
		t.Fatalf("persisted monitor policy = %+v", updated.Monitor)
	}
}

func TestPatchSettingsRejectsSampleCountsBeyondWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, dir, "config.toml", "[monitor]\n")
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	window, candidates := 3, 4
	err = PatchSettings(path, current, SettingsPatch{MonitorWindowSize: &window, MonitorMinCandidateSamples: &candidates})
	if err == nil || !strings.Contains(err.Error(), "config.toml") || !strings.Contains(err.Error(), "monitor.min_candidate_samples") {
		t.Fatalf("candidate samples beyond window = %v", err)
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "[monitor]\n" {
		t.Fatalf("rejected patch rewrote config: %q, %v", data, readErr)
	}
}

func TestLoadStabilityWindowBoundsSampleCounts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[monitor]\nwindow_size = 2\n")
	narrow, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if narrow.Monitor.MinCandidateSamples != 2 || narrow.Monitor.ConsecutiveBadSamples != 2 || narrow.Monitor.MaxFailures != 2 {
		t.Fatalf("defaults exceed a narrow window: %+v", narrow.Monitor)
	}
	writeFile(t, dir, "config.toml", "[monitor]\nwindow_size = 20\nconsecutive_bad_samples = 20\nmin_candidate_samples = 20\nmax_failures = 20\n")
	wide, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if wide.Monitor.WindowSize != 20 || wide.Monitor.ConsecutiveBadSamples != 20 || wide.Monitor.MinCandidateSamples != 20 || wide.Monitor.MaxFailures != 20 {
		t.Fatalf("wide window = %+v", wide.Monitor)
	}
	for _, test := range []struct{ setting, key string }{
		{"window_size = 1", "monitor.window_size"},
		{"window_size = 21", "monitor.window_size"},
		{"min_candidate_samples = 1", "monitor.min_candidate_samples"},
		{"min_candidate_samples = 6", "monitor.min_candidate_samples"},
		{"window_size = 3\nmin_candidate_samples = 4", "monitor.min_candidate_samples"},
		{"max_failures = 0", "monitor.max_failures"},
		{"max_failures = 6", "monitor.max_failures"},
		{"window_size = 3\nmax_failures = 4", "monitor.max_failures"},
		{"consecutive_bad_samples = 0", "monitor.consecutive_bad_samples"},
		{"window_size = 4\nconsecutive_bad_samples = 5", "monitor.consecutive_bad_samples"},
	} {
		writeFile(t, dir, "config.toml", "[monitor]\n"+test.setting+"\n")
		_, err := Load(dir)
		if err == nil || !strings.Contains(err.Error(), "config.toml") || !strings.Contains(err.Error(), test.key) {
			t.Errorf("%q error = %v, want config.toml and %s", test.setting, err, test.key)
		}
	}
}

func TestLoadSwitchPolicyAndURLTestDelays(t *testing.T) {
	dir := t.TempDir()
	defaults, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Monitor.SwitchPolicy != monitor.SwitchFailover {
		t.Fatalf("default switch policy = %q", defaults.Monitor.SwitchPolicy)
	}
	if defaults.Mihomo.URLTestInterval != 0 || defaults.Mihomo.URLTestTolerance != 0 {
		t.Fatalf("unset url-test delays = %v, %v", defaults.Mihomo.URLTestInterval, defaults.Mihomo.URLTestTolerance)
	}
	writeFile(t, dir, "config.toml", "[monitor]\nswitch_policy = \"lowest_latency\"\n[mihomo]\nurl_test_interval = \"60s\"\nurl_test_tolerance = \"50ms\"\n")
	configured, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if configured.Monitor.SwitchPolicy != monitor.SwitchLowestLatency || configured.Mihomo.URLTestInterval != time.Minute || configured.Mihomo.URLTestTolerance != 50*time.Millisecond {
		t.Fatalf("configured switch policy %q with url-test delay %v/%v", configured.Monitor.SwitchPolicy, configured.Mihomo.URLTestInterval, configured.Mihomo.URLTestTolerance)
	}
}

func TestPatchSettingsPersistsSwitchPolicyAndURLTestDelays(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "[monitor]\n")
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	policy := monitor.SwitchLowestLatency
	interval, tolerance := 10*time.Minute, 75*time.Millisecond
	if err := PatchSettings(filepath.Join(dir, "config.toml"), current, SettingsPatch{
		MonitorSwitchPolicy: &policy, MihomoURLTestInterval: &interval, MihomoURLTestTolerance: &tolerance,
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Monitor.SwitchPolicy != policy || updated.Mihomo.URLTestInterval != interval || updated.Mihomo.URLTestTolerance != tolerance {
		t.Fatalf("persisted switch policy %q with url-test delay %v/%v", updated.Monitor.SwitchPolicy, updated.Mihomo.URLTestInterval, updated.Mihomo.URLTestTolerance)
	}
}

func TestLoadRejectsInvalidSwitchPolicyAndURLTestDelays(t *testing.T) {
	for name, document := range map[string]string{
		"unknown switch policy":     "[monitor]\nswitch_policy = \"fastest\"\n",
		"subsecond url-test period": "[mihomo]\nurl_test_interval = \"500ms\"\n",
		"url-test period over day":  "[mihomo]\nurl_test_interval = \"25h\"\n",
		"negative url-test period":  "[mihomo]\nurl_test_interval = \"-1s\"\n",
		"negative tolerance":        "[mihomo]\nurl_test_tolerance = \"-1ms\"\n",
		"tolerance over minute":     "[mihomo]\nurl_test_tolerance = \"61s\"\n",
		"submillisecond tolerance":  "[mihomo]\nurl_test_tolerance = \"500us\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "config.toml", document)
			if _, err := Load(dir); err == nil {
				t.Fatalf("invalid setting accepted: %s", document)
			}
		})
	}
}

func TestLoadRejectsInvalidMonitorPolicyLimits(t *testing.T) {
	for name, setting := range map[string]string{
		"blank test URL":              "test_url = \"\"",
		"unsupported test URL scheme": "test_url = \"ftp://example.com\"",
		"fragmented test URL":         "test_url = \"https://example.com/204#fragment\"",
		"zero timeout":                "timeout = \"0s\"",
		"timeout after interval":      "timeout = \"6m\"",
		"zero concurrency":            "concurrency = 0",
		"excess concurrency":          "concurrency = 65",
		"zero bad samples":            "consecutive_bad_samples = 0",
		"excess bad samples":          "consecutive_bad_samples = 6",
		"negative cooldown":           "cooldown = \"-1s\"",
		"excess jitter":               "jitter = \"5m\"",
		"interval over one day":       "interval = \"25h\"",
		"zero switch threshold":       "threshold = \"0s\"",
		"zero improvement":            "min_improvement = \"0s\"",
		"negative jitter":             "jitter = \"-1s\"",
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

func TestLoadTunDefaultsAndIntent(t *testing.T) {
	dir := t.TempDir()
	defaults, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Tun{Stack: core.TunStackGVisor, DNSMode: core.TunDNSProfile}); defaults.Tun != want {
		t.Fatalf("default tun = %+v, want %+v", defaults.Tun, want)
	}
	writeFile(t, dir, "config.toml", "[tun]\nenabled = true\nstack = \"system\"\ndns_mode = \"fake-ip\"\n")
	configured, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Tun{Enabled: true, Stack: core.TunStackSystem, DNSMode: core.TunDNSFakeIP}); configured.Tun != want {
		t.Fatalf("configured tun = %+v, want %+v", configured.Tun, want)
	}
}

func TestLoadRejectsInvalidTunIntent(t *testing.T) {
	for _, test := range []struct{ setting, key string }{
		{"mtu = 1400", "tun.mtu"},
		{"stack = \"lwip\"", "config.toml: tun.stack"},
		{"stack = \"\"", "config.toml: tun.stack"},
		{"dns_mode = \"normal\"", "config.toml: tun.dns_mode"},
		{"dns_mode = \"\"", "config.toml: tun.dns_mode"},
	} {
		dir := t.TempDir()
		writeFile(t, dir, "config.toml", "[tun]\n"+test.setting+"\n")
		_, err := Load(dir)
		if err == nil || !strings.Contains(err.Error(), "config.toml") || !strings.Contains(err.Error(), test.key) {
			t.Errorf("%q error = %v, want %s", test.setting, err, test.key)
		}
	}
}

func TestPatchSettingsPersistsTunIntent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeFile(t, dir, "config.toml", "[monitor]\n")
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	enabled, stack, mode := true, core.TunStackMixed, core.TunDNSRedirHost
	if err := PatchSettings(path, current, SettingsPatch{TunEnabled: &enabled, TunStack: &stack, TunDNSMode: &mode}); err != nil {
		t.Fatal(err)
	}
	updated, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Tun{Enabled: true, Stack: core.TunStackMixed, DNSMode: core.TunDNSRedirHost}); updated.Tun != want {
		t.Fatalf("persisted tun = %+v, want %+v", updated.Tun, want)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := "lwip"
	if err := PatchSettings(path, updated, SettingsPatch{TunStack: &invalid}); err == nil || !strings.Contains(err.Error(), "config.toml: tun.stack") {
		t.Fatalf("invalid stack patch error = %v", err)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatalf("rejected patch rewrote config.toml: %v\n%s", err, after)
	}
}
