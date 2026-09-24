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
