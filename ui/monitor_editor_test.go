package ui

import "testing"

func TestMonitorPolicyFormKeepsSubsecondTimeoutAndZeroCooldown(t *testing.T) {
	patch, err := monitorPolicyPatch(monitorPolicyInput{
		TestURL: "https://probe.example/check", IntervalSeconds: "300", TimeoutMillis: "750", Concurrency: "3",
		ThresholdMillis: "800", AlertThresholdMillis: "250", ConsecutiveBadSamples: "3",
		MinImprovementMillis: "100", CooldownSeconds: "0", JitterMillis: "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if patch.MonitorTimeoutMillis == nil || *patch.MonitorTimeoutMillis != 750 || patch.MonitorCooldownSeconds == nil || *patch.MonitorCooldownSeconds != 0 || patch.MonitorTestURL == nil || *patch.MonitorTestURL != "https://probe.example/check" {
		t.Fatalf("monitor policy intent lost precision or zero values: %+v", patch)
	}
}

func TestMonitorPolicyFormAcceptsApprovedHTTPProbe(t *testing.T) {
	url := "http://cp.cloudflare.com/generate_204"
	patch, err := monitorPolicyPatch(monitorPolicyInput{
		TestURL: url, IntervalSeconds: "300", TimeoutMillis: "5000", Concurrency: "3",
		ThresholdMillis: "800", AlertThresholdMillis: "250", ConsecutiveBadSamples: "3",
		MinImprovementMillis: "100", CooldownSeconds: "600", JitterMillis: "15000",
	})
	if err != nil || patch.MonitorTestURL == nil || *patch.MonitorTestURL != url {
		t.Fatalf("approved HTTP probe URL unavailable: %+v, %v", patch, err)
	}
}
