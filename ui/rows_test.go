package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"github.com/fishman/clashpulse/core"
)

func TestLatencyBadge(t *testing.T) {
	configured := core.MonitorSnapshot{AlertThresholdMillis: 250, ThresholdMillis: 800}
	alertUnset := core.MonitorSnapshot{ThresholdMillis: 800}
	for _, tc := range []struct {
		name  string
		proxy core.ProxySnapshot
		mon   core.MonitorSnapshot
		text  string
		color fyne.ThemeColorName
	}{
		{"fast", core.ProxySnapshot{LatencyMillis: 120, Outcome: "success"}, configured, "120 ms", theme.ColorNameSuccess},
		{"at alert", core.ProxySnapshot{LatencyMillis: 250, Outcome: "success"}, configured, "250 ms", theme.ColorNameSuccess},
		{"slow", core.ProxySnapshot{LatencyMillis: 400, Outcome: "success"}, configured, "400 ms", theme.ColorNameWarning},
		{"over threshold", core.ProxySnapshot{LatencyMillis: 900, Outcome: "success"}, configured, "900 ms", theme.ColorNameError},
		{"timeout", core.ProxySnapshot{Outcome: "timeout"}, configured, "timeout", theme.ColorNameError},
		{"error", core.ProxySnapshot{Outcome: "error"}, configured, "error", theme.ColorNameError},
		{"unmeasured", core.ProxySnapshot{}, configured, "-", theme.ColorNameDisabled},
		{"mihomo only", core.ProxySnapshot{MihomoMillis: 75}, configured, "75 ms (mihomo)", theme.ColorNameDisabled},
		{"alert unset under threshold", core.ProxySnapshot{LatencyMillis: 400, Outcome: "success"}, alertUnset, "400 ms", theme.ColorNameSuccess},
		{"alert unset over threshold", core.ProxySnapshot{LatencyMillis: 900, Outcome: "success"}, alertUnset, "900 ms", theme.ColorNameError},
		{"no thresholds", core.ProxySnapshot{LatencyMillis: 900, Outcome: "success"}, core.MonitorSnapshot{}, "900 ms", theme.ColorNameSuccess},
	} {
		text, color := latencyBadge(tc.proxy, tc.mon)
		if text != tc.text || color != tc.color {
			t.Errorf("%s: badge = %q %s, want %q %s", tc.name, text, color, tc.text, tc.color)
		}
	}
}

func TestRowState(t *testing.T) {
	for _, tc := range []struct {
		enabled, validated bool
		failure            string
		want               pillState
	}{
		{true, true, "", pillOn},
		{true, false, "", pillOff},
		{false, true, "", pillOff},
		{true, true, "HTTP 500", pillFailed},
		{false, false, "HTTP 500", pillFailed},
	} {
		if got := rowState(tc.enabled, tc.validated, tc.failure); got != tc.want {
			t.Errorf("rowState(%v, %v, %q) = %v, want %v", tc.enabled, tc.validated, tc.failure, got, tc.want)
		}
	}
}
