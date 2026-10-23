package ui

import (
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/monitor"
)

func TestMonitorPolicyFormKeepsSubsecondTimeoutAndZeroCooldown(t *testing.T) {
	input := stabilityPolicyInput("5", "3", "2", "3")
	input.CooldownSeconds = "0"
	patch, err := monitorPolicyPatch(input)
	if err != nil {
		t.Fatal(err)
	}
	if patch.MonitorTimeoutMillis == nil || *patch.MonitorTimeoutMillis != 750 || patch.MonitorCooldownSeconds == nil || *patch.MonitorCooldownSeconds != 0 || patch.MonitorTestURL == nil || *patch.MonitorTestURL != "https://probe.example/check" {
		t.Fatalf("monitor policy intent lost precision or zero values: %+v", patch)
	}
	if patch.MonitorSwitchPolicy == nil || *patch.MonitorSwitchPolicy != monitor.SwitchFailover {
		t.Fatalf("monitor policy intent lost the switch policy: %+v", patch)
	}
}

func TestMonitorPolicyFormAcceptsApprovedHTTPProbe(t *testing.T) {
	url := "http://cp.cloudflare.com/generate_204"
	input := stabilityPolicyInput("5", "3", "2", "3")
	input.TestURL, input.SwitchPolicy = url, switchPolicyLabel(monitor.SwitchLowestLatency)
	input.TimeoutMillis, input.JitterMillis = "5000", "15000"
	patch, err := monitorPolicyPatch(input)
	if err != nil || patch.MonitorTestURL == nil || *patch.MonitorTestURL != url {
		t.Fatalf("approved HTTP probe URL unavailable: %+v, %v", patch, err)
	}
	if patch.MonitorSwitchPolicy == nil || *patch.MonitorSwitchPolicy != monitor.SwitchLowestLatency {
		t.Fatalf("lowest latency mode was not sent: %+v", patch)
	}
}

func TestMonitorPolicyFormRejectsUnknownSwitchPolicy(t *testing.T) {
	input := stabilityPolicyInput("5", "3", "2", "3")
	input.SwitchPolicy = "fastest"
	if _, err := monitorPolicyPatch(input); err == nil {
		t.Fatal("unlabelled switch policy was accepted")
	}
}

func stabilityPolicyInput(window, candidates, maxFailures, badSamples string) monitorPolicyInput {
	return monitorPolicyInput{
		TestURL: "https://probe.example/check", SwitchPolicy: switchPolicyLabel(monitor.SwitchFailover),
		IntervalSeconds: "300", TimeoutMillis: "750", Concurrency: "3",
		ThresholdMillis: "800", AlertThresholdMillis: "250", ConsecutiveBadSamples: badSamples,
		WindowSize: window, MinCandidateSamples: candidates, MaxFailures: maxFailures,
		MinImprovementMillis: "100", CooldownSeconds: "600", JitterMillis: "0", StreamIntervalSeconds: "1800",
	}
}

func TestMonitorPolicyFormSendsStabilityWindow(t *testing.T) {
	patch, err := monitorPolicyPatch(stabilityPolicyInput("10", "4", "3", "6"))
	if err != nil {
		t.Fatal(err)
	}
	if patch.MonitorWindowSize == nil || *patch.MonitorWindowSize != 10 ||
		patch.MonitorMinCandidateSamples == nil || *patch.MonitorMinCandidateSamples != 4 ||
		patch.MonitorMaxFailures == nil || *patch.MonitorMaxFailures != 3 ||
		patch.MonitorConsecutiveBadSamples == nil || *patch.MonitorConsecutiveBadSamples != 6 {
		t.Fatalf("stability window lost: %+v", patch)
	}
}

func TestMonitorPolicyFormBoundsStabilityByWindow(t *testing.T) {
	for _, input := range [][4]string{
		{"1", "2", "1", "1"}, {"21", "2", "1", "1"}, {"", "3", "2", "3"},
		{"5", "1", "2", "3"}, {"5", "6", "2", "3"},
		{"5", "3", "0", "3"}, {"5", "3", "6", "3"},
		{"5", "3", "2", "0"}, {"5", "3", "2", "6"},
	} {
		if _, err := monitorPolicyPatch(stabilityPolicyInput(input[0], input[1], input[2], input[3])); err == nil {
			t.Fatalf("accepted window %q, candidates %q, max failures %q, bad samples %q", input[0], input[1], input[2], input[3])
		}
	}
	if _, err := monitorPolicyPatch(stabilityPolicyInput("20", "20", "20", "20")); err != nil {
		t.Fatalf("largest window rejected: %v", err)
	}
}

func TestMonitorSwitchPolicySelectRoundTrips(t *testing.T) {
	for _, value := range monitor.SwitchPolicies {
		selects := switchPolicySelect(value)
		if len(selects.Options) != len(monitor.SwitchPolicies) {
			t.Fatalf("switch policy options = %v", selects.Options)
		}
		if got, ok := switchPolicyValue(selects.Selected); !ok || got != value {
			t.Fatalf("switch policy %q came back as %q", value, got)
		}
	}
	if selects := switchPolicySelect(""); selects.Selected != switchPolicyLabel(monitor.DefaultPolicy().SwitchPolicy) || monitor.DefaultPolicy().SwitchPolicy != monitor.SwitchLowestLatency {
		t.Fatalf("unset switch policy showed %q", selects.Selected)
	}
}

func TestMonitorPolicyPatchCarriesStreamSettings(t *testing.T) {
	input := stabilityPolicyInput("5", "3", "2", "3")
	if patch, err := monitorPolicyPatch(input); err != nil || patch.MonitorStreamURL != nil {
		t.Fatalf("blank stream URL did not keep the current one: %+v, %v", patch, err)
	}
	input.SwitchPolicy = switchPolicyLabel(monitor.SwitchSteadyStream)
	input.StreamURL = "https://speed.example.com/file"
	input.StreamIntervalSeconds = "600"
	patch, err := monitorPolicyPatch(input)
	if err != nil {
		t.Fatal(err)
	}
	if *patch.MonitorSwitchPolicy != monitor.SwitchSteadyStream || *patch.MonitorStreamURL != input.StreamURL || *patch.MonitorStreamIntervalSeconds != 600 {
		t.Fatalf("patch = %+v", patch)
	}
	input.StreamURL = "http://speed.example.com/file"
	if _, err := monitorPolicyPatch(input); err == nil {
		t.Fatal("HTTP stream URL accepted")
	}
	input.StreamURL = "https://speed.example.com/file"
	input.StreamIntervalSeconds = "60"
	if _, err := monitorPolicyPatch(input); err == nil {
		t.Fatal("1 minute stream interval accepted")
	}
}

func TestURLTestDelayFormSendsOnlyTypedFields(t *testing.T) {
	patch, err := urlTestDelayPatch("600", "")
	if err != nil {
		t.Fatal(err)
	}
	if patch.URLTestIntervalSeconds == nil || *patch.URLTestIntervalSeconds != 600 || patch.URLTestToleranceMillis != nil {
		t.Fatalf("blank field did not stay unset: %+v", patch)
	}
	both, err := urlTestDelayPatch("600", "50")
	if err != nil || both.URLTestToleranceMillis == nil || *both.URLTestToleranceMillis != 50 {
		t.Fatalf("tolerance dropped: %+v, %v", both, err)
	}
	for _, input := range [][2]string{{"0", "50"}, {"86401", ""}, {"600", "0"}, {"600", "60001"}, {"", ""}, {"abc", ""}} {
		if _, err := urlTestDelayPatch(input[0], input[1]); err == nil {
			t.Fatalf("accepted url-test delay %q/%q", input[0], input[1])
		}
	}
	if got := urlTestDelayLabel(core.MonitorSnapshot{}); got != "Profile default" {
		t.Fatalf("unset url-test delay label = %q", got)
	}
	if got := urlTestDelayLabel(core.MonitorSnapshot{URLTestIntervalSeconds: 600, URLTestToleranceMillis: 50}); got != "interval 10m0s, tolerance 50 ms" {
		t.Fatalf("url-test delay label = %q", got)
	}
}
