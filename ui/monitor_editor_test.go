package ui

import (
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/monitor"
)

func TestMonitorPolicyFormKeepsSubsecondTimeoutAndZeroCooldown(t *testing.T) {
	patch, err := monitorPolicyPatch(monitorPolicyInput{
		TestURL: "https://probe.example/check", SwitchPolicy: switchPolicyLabel(monitor.SwitchFailover),
		IntervalSeconds: "300", TimeoutMillis: "750", Concurrency: "3",
		ThresholdMillis: "800", AlertThresholdMillis: "250", ConsecutiveBadSamples: "3",
		MinImprovementMillis: "100", CooldownSeconds: "0", JitterMillis: "0",
	})
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
	patch, err := monitorPolicyPatch(monitorPolicyInput{
		TestURL: url, SwitchPolicy: switchPolicyLabel(monitor.SwitchLowestLatency),
		IntervalSeconds: "300", TimeoutMillis: "5000", Concurrency: "3",
		ThresholdMillis: "800", AlertThresholdMillis: "250", ConsecutiveBadSamples: "3",
		MinImprovementMillis: "100", CooldownSeconds: "600", JitterMillis: "15000",
	})
	if err != nil || patch.MonitorTestURL == nil || *patch.MonitorTestURL != url {
		t.Fatalf("approved HTTP probe URL unavailable: %+v, %v", patch, err)
	}
	if patch.MonitorSwitchPolicy == nil || *patch.MonitorSwitchPolicy != monitor.SwitchLowestLatency {
		t.Fatalf("lowest latency mode was not sent: %+v", patch)
	}
}

func TestMonitorPolicyFormRejectsUnknownSwitchPolicy(t *testing.T) {
	if _, err := monitorPolicyPatch(monitorPolicyInput{
		TestURL: "https://probe.example/check", SwitchPolicy: "fastest", IntervalSeconds: "300",
		TimeoutMillis: "750", Concurrency: "3", ThresholdMillis: "800", AlertThresholdMillis: "250",
		ConsecutiveBadSamples: "3", MinImprovementMillis: "100", CooldownSeconds: "600", JitterMillis: "0",
	}); err == nil {
		t.Fatal("unlabelled switch policy was accepted")
	}
}

func TestMonitorSwitchPolicySelectRoundTrips(t *testing.T) {
	for _, value := range []string{monitor.SwitchFailover, monitor.SwitchLowestLatency} {
		selects := switchPolicySelect(value)
		if len(selects.Options) != 2 {
			t.Fatalf("switch policy options = %v", selects.Options)
		}
		if got, ok := switchPolicyValue(selects.Selected); !ok || got != value {
			t.Fatalf("switch policy %q came back as %q", value, got)
		}
	}
	if selects := switchPolicySelect(""); selects.Selected != switchPolicyLabel(monitor.SwitchFailover) {
		t.Fatalf("unset switch policy showed %q", selects.Selected)
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
