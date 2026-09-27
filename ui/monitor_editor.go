package ui

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
	"github.com/fishman/clashpulse/monitor"
)

type monitorPolicyInput struct {
	TestURL, SwitchPolicy, IntervalSeconds, TimeoutMillis, Concurrency string
	ThresholdMillis, AlertThresholdMillis, ConsecutiveBadSamples       string
	MinImprovementMillis, CooldownSeconds, JitterMillis                string
}

// IDs remain opaque; descriptions live in the locale catalogs.
var switchPolicyIDs = []string{monitor.SwitchFailover, monitor.SwitchLowestLatency}

func switchPolicyLabel(value string) string {
	for _, id := range switchPolicyIDs {
		if id == value {
			return localize.T("gui.monitor.switch-policy." + id)
		}
	}
	return localize.T("gui.monitor.switch-policy." + monitor.SwitchFailover)
}

func switchPolicyValue(label string) (string, bool) {
	for _, value := range switchPolicyIDs {
		if switchPolicyLabel(value) == label {
			return value, true
		}
	}
	return "", false
}

func switchPolicySelect(current string) *widget.Select {
	labels := make([]string, 0, len(switchPolicyIDs))
	for _, value := range switchPolicyIDs {
		labels = append(labels, switchPolicyLabel(value))
	}
	selects := widget.NewSelect(labels, nil)
	selects.SetSelected(switchPolicyLabel(current))
	return selects
}

func monitorPolicyPatch(input monitorPolicyInput) (*ipc.ConfigPatch, error) {
	switchPolicy, ok := switchPolicyValue(input.SwitchPolicy)
	if !ok {
		return nil, errors.New(localize.T("select a switch policy"))
	}

	rawURL := strings.TrimSpace(input.TestURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || strings.ContainsAny(input.TestURL, "\x00\r\n#") || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, errors.New(localize.T("test URL must use HTTP or HTTPS without credentials or fragment"))
	}
	parse := func(label, text string, min, max uint64) (*uint32, error) {
		value, err := strconv.ParseUint(strings.TrimSpace(text), 10, 32)
		if err != nil || value < min || value > max {
			return nil, fmt.Errorf(localize.T("%s must be between %d and %d"), localize.T(label), min, max)
		}
		result := uint32(value)
		return &result, nil
	}
	patch := &ipc.ConfigPatch{MonitorTestURL: &rawURL, MonitorSwitchPolicy: &switchPolicy}
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

// urlTestDelayLabel describes the delay settings written into the generated
// config's url-test groups. Nothing set means those groups keep the profile's
// own pacing.
func urlTestDelayLabel(state core.MonitorSnapshot) string {
	parts := make([]string, 0, 2)
	if state.URLTestIntervalSeconds > 0 {
		parts = append(parts, fmt.Sprintf(localize.T("interval %s"), (time.Duration(state.URLTestIntervalSeconds)*time.Second).String()))
	}
	if state.URLTestToleranceMillis > 0 {
		parts = append(parts, fmt.Sprintf(localize.T("tolerance %d ms"), state.URLTestToleranceMillis))
	}
	if len(parts) == 0 {
		return localize.T("Profile default")
	}
	return strings.Join(parts, localize.T(", "))
}

func (p *settingsPage) editURLTestDelay() {
	state := p.monitorState
	entry := func(value int64) *widget.Entry {
		field := widget.NewEntry()
		field.SetPlaceHolder(localize.T("Profile default"))
		if value > 0 {
			field.SetText(strconv.FormatInt(value, 10))
		}
		return field
	}
	interval := entry(state.URLTestIntervalSeconds)
	tolerance := entry(state.URLTestToleranceMillis)
	items := []*widget.FormItem{
		widget.NewFormItem(localize.T("Interval (seconds)"), interval),
		widget.NewFormItem(localize.T("Switch tolerance (ms)"), tolerance),
		widget.NewFormItem(localize.T("Applies to"), widget.NewLabel(localize.T("url-test and fallback groups in the generated config. A blank field keeps what the profile sets."))),
	}
	dialog.NewForm(localize.T("Mihomo url-test delay"), localize.T("Apply"), localize.T("Cancel"), items, func(confirmed bool) {
		if !confirmed {
			return
		}
		patch, err := urlTestDelayPatch(interval.Text, tolerance.Text)
		if err != nil {
			dialog.ShowError(err, p.window)
			return
		}
		p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: patch})
	}, p.window).Show()
}

// A blank field leaves that group setting untouched, so clearing a value back to
// the profile's own pacing is done in config.toml.
func urlTestDelayPatch(interval, tolerance string) (*ipc.ConfigPatch, error) {
	patch := &ipc.ConfigPatch{}
	if text := strings.TrimSpace(interval); text != "" {
		value, err := strconv.ParseUint(text, 10, 32)
		if err != nil || value < 1 || value > 86400 {
			return nil, errors.New(localize.T("url-test interval must be between 1 and 86400 seconds"))
		}
		seconds := uint32(value)
		patch.URLTestIntervalSeconds = &seconds
	}
	if text := strings.TrimSpace(tolerance); text != "" {
		value, err := strconv.ParseUint(text, 10, 32)
		if err != nil || value < 1 || value > 60000 {
			return nil, errors.New(localize.T("url-test switch tolerance must be between 1 and 60000 ms"))
		}
		millis := uint32(value)
		patch.URLTestToleranceMillis = &millis
	}
	if patch.URLTestIntervalSeconds == nil && patch.URLTestToleranceMillis == nil {
		return nil, errors.New(localize.T("enter an interval or a switch tolerance"))
	}
	return patch, nil
}

func (p *settingsPage) editMonitorPolicy() {
	state := p.monitorState
	items := make([]*widget.FormItem, 0, 12)
	add := func(label, value string) *widget.Entry {
		field := widget.NewEntry()
		field.SetText(value)
		items = append(items, widget.NewFormItem(label, field))
		return field
	}
	testURL := add(localize.T("Probe URL"), state.TestURL)
	items = append(items, widget.NewFormItem(localize.T("Warning"), widget.NewLabel(localize.T("Plain HTTP probes can be intercepted; use HTTPS for probe integrity."))))
	policy := switchPolicySelect(state.SwitchPolicy)
	items = append(items, widget.NewFormItem(localize.T("Switch policy"), policy))
	items = append(items, widget.NewFormItem(localize.T("Note"), widget.NewLabel(localize.T("A switch needs at least three probes of each node and the switch cooldown after one. Lowest latency compares median latencies and requires the required improvement; the unhealthy threshold then no longer gates switching."))))
	interval := add(localize.T("Interval (seconds)"), strconv.FormatInt(state.IntervalSeconds, 10))
	timeout := add(localize.T("Timeout (ms)"), strconv.FormatInt(state.TimeoutMillis, 10))
	concurrency := add(localize.T("Concurrent probes"), strconv.Itoa(state.Concurrency))
	threshold := add(localize.T("Unhealthy threshold (ms)"), strconv.FormatInt(state.ThresholdMillis, 10))
	alert := add(localize.T("Alert threshold (ms)"), strconv.FormatInt(state.AlertThresholdMillis, 10))
	consecutive := add(localize.T("Consecutive bad samples"), strconv.Itoa(state.ConsecutiveBadSamples))
	improvement := add(localize.T("Required improvement (ms)"), strconv.FormatInt(state.MinImprovementMillis, 10))
	cooldown := add(localize.T("Switch cooldown (seconds)"), strconv.FormatInt(state.CooldownSeconds, 10))
	jitter := add(localize.T("Probe jitter (ms)"), strconv.FormatInt(state.JitterMillis, 10))
	dialog.NewForm(localize.T("Monitor probe policy"), localize.T("Apply"), localize.T("Cancel"), items, func(confirmed bool) {
		if !confirmed {
			return
		}
		patch, err := monitorPolicyPatch(monitorPolicyInput{
			TestURL: testURL.Text, SwitchPolicy: policy.Selected, IntervalSeconds: interval.Text, TimeoutMillis: timeout.Text,
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
