package ui

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/fishman/clashpulse/ipc"
)

type monitorPolicyInput struct {
	TestURL, IntervalSeconds, TimeoutMillis, Concurrency, ThresholdMillis, AlertThresholdMillis string
	ConsecutiveBadSamples, MinImprovementMillis, CooldownSeconds, JitterMillis                  string
}

func monitorPolicyPatch(input monitorPolicyInput) (*ipc.ConfigPatch, error) {
	rawURL := strings.TrimSpace(input.TestURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || strings.ContainsAny(rawURL, "\x00\r\n") {
		return nil, fmt.Errorf("test URL must be HTTPS without credentials or fragment")
	}
	parse := func(label, text string, min, max uint64) (*uint32, error) {
		value, err := strconv.ParseUint(strings.TrimSpace(text), 10, 32)
		if err != nil || value < min || value > max {
			return nil, fmt.Errorf("%s must be between %d and %d", label, min, max)
		}
		result := uint32(value)
		return &result, nil
	}
	patch := &ipc.ConfigPatch{MonitorTestURL: &rawURL}
	if patch.MonitorIntervalSeconds, err = parse("interval seconds", input.IntervalSeconds, 1, 86400); err != nil {
		return nil, err
	}
	if patch.MonitorTimeoutMillis, err = parse("timeout milliseconds", input.TimeoutMillis, 1, uint64(*patch.MonitorIntervalSeconds)*1000); err != nil {
		return nil, err
	}
	if patch.MonitorConcurrency, err = parse("concurrency", input.Concurrency, 1, 64); err != nil {
		return nil, err
	}
	if patch.MonitorThresholdMillis, err = parse("threshold milliseconds", input.ThresholdMillis, 1, 4294967295); err != nil {
		return nil, err
	}
	if patch.AlertThresholdMillis, err = parse("alert milliseconds", input.AlertThresholdMillis, 1, 60000); err != nil {
		return nil, err
	}
	if patch.MonitorConsecutiveBadSamples, err = parse("consecutive bad samples", input.ConsecutiveBadSamples, 1, 5); err != nil {
		return nil, err
	}
	if patch.MonitorMinImprovementMillis, err = parse("minimum improvement milliseconds", input.MinImprovementMillis, 1, 4294967295); err != nil {
		return nil, err
	}
	if patch.MonitorCooldownSeconds, err = parse("cooldown seconds", input.CooldownSeconds, 0, 4294967295); err != nil {
		return nil, err
	}
	if patch.MonitorJitterMillis, err = parse("jitter milliseconds", input.JitterMillis, 0, uint64(*patch.MonitorIntervalSeconds)*1000-1); err != nil {
		return nil, err
	}
	return patch, nil
}

func (p *settingsPage) editMonitorPolicy() {
	state := p.monitorState
	items := make([]*widget.FormItem, 0, 10)
	add := func(label, value string) *widget.Entry {
		field := widget.NewEntry()
		field.SetText(value)
		items = append(items, widget.NewFormItem(label, field))
		return field
	}
	testURL := add("HTTPS test URL", state.TestURL)
	interval := add("Interval (seconds)", strconv.FormatInt(state.IntervalSeconds, 10))
	timeout := add("Timeout (ms)", strconv.FormatInt(state.TimeoutMillis, 10))
	concurrency := add("Concurrent probes", strconv.Itoa(state.Concurrency))
	threshold := add("Unhealthy threshold (ms)", strconv.FormatInt(state.ThresholdMillis, 10))
	alert := add("Alert threshold (ms)", strconv.FormatInt(state.AlertThresholdMillis, 10))
	consecutive := add("Consecutive bad samples", strconv.Itoa(state.ConsecutiveBadSamples))
	improvement := add("Required improvement (ms)", strconv.FormatInt(state.MinImprovementMillis, 10))
	cooldown := add("Switch cooldown (seconds)", strconv.FormatInt(state.CooldownSeconds, 10))
	jitter := add("Probe jitter (ms)", strconv.FormatInt(state.JitterMillis, 10))
	dialog.NewForm("Monitor probe policy", "Apply", "Cancel", items, func(confirmed bool) {
		if !confirmed {
			return
		}
		patch, err := monitorPolicyPatch(monitorPolicyInput{
			TestURL: testURL.Text, IntervalSeconds: interval.Text, TimeoutMillis: timeout.Text,
			Concurrency: concurrency.Text, ThresholdMillis: threshold.Text, AlertThresholdMillis: alert.Text,
			ConsecutiveBadSamples: consecutive.Text, MinImprovementMillis: improvement.Text,
			CooldownSeconds: cooldown.Text, JitterMillis: jitter.Text,
		})
		if err != nil {
			dialog.ShowError(err, p.window)
			return
		}
		p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: patch})
	}, p.window).Show()
}
