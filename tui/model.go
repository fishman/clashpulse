package tui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/notmutt/lib/tui/form"
)

type Tab string

const (
	TabOverview      Tab = "overview"
	TabProxies       Tab = "proxies"
	TabSubscriptions Tab = "subscriptions"
	TabFilters       Tab = "filters"
	TabResources     Tab = "resources"
	TabSettings      Tab = "settings"
)

var tabs = [...]Tab{TabOverview, TabProxies, TabSubscriptions, TabFilters, TabResources, TabSettings}

type Focus string

const (
	FocusContent Focus = "content"
	FocusModal   Focus = "modal"
)

type ModalKind string

const (
	ModalBinary             ModalKind = "binary"
	ModalDeleteSubscription ModalKind = "delete_subscription"
	ModalSubscription       ModalKind = "subscription"
	ModalResource           ModalKind = "resource"
	ModalFilter             ModalKind = "filter"
	ModalMonitorSetting     ModalKind = "monitor_setting"
	ModalDNSListen          ModalKind = "dns_listen"
	ModalDNSResolver        ModalKind = "dns_resolver"
	ModalDNSRoute           ModalKind = "dns_route"
	ModalDeleteDNS          ModalKind = "delete_dns"
)

type monitorField uint8

const (
	monitorTestURL monitorField = iota + 1
	monitorInterval
	monitorTimeout
	monitorConcurrency
	monitorThreshold
	monitorAlertThreshold
	monitorBadSamples
	monitorImprovement
	monitorCooldown
	monitorJitter
)

type dnsForm struct{ values [3]string }

const (
	dnsRouteMatcher = iota
	dnsRouteValue
	dnsRouteResolver
)

type Modal struct {
	Kind     ModalKind
	Input    string
	TargetID string
	Form     *form.Form
}

// Row is a stable, render-ready item. ID remains unchanged when unrelated
// snapshot data is refreshed or reordered.
type Row struct {
	ID       string
	Title    string
	Detail   string
	Cells    []string
	Selected bool

	kind           rowKind
	groupID        string
	choiceID       string
	subscriptionID string
	filterID       string
	resourceID     string
	dnsID          string
	monitor        monitorField
	action         string
}

type rowKind uint8

const (
	rowNone rowKind = iota
	rowGroup
	rowProxy
	rowSubscription
	rowFilter
	rowResource
	rowSettingBinary
	rowSettingMonitor
	rowSettingDNSListen
	rowDNSResolver
	rowDNSRoute
)

// Model is local view state for the remote IPC client. IPC snapshots are
// immutable by contract and replaced on Apply; no domain control state is kept.
type Model struct {
	Tab        Tab
	Focus      Focus
	Selection  map[Tab]string
	Modal      *Modal
	Pending    int
	Notice     string
	LogOpen    bool
	LogOffset  int
	HelpOpen   bool
	HelpOffset int

	keymap   Keymap
	snapshot ipc.Event
	groupID  string
}

// NewModel creates an Overview model. Supplying a Keymap is useful to callers
// that want to exercise an alternate binding table without changing the model.
func NewModel(keymaps ...Keymap) Model {
	keys, _ := DefaultKeymap()
	if len(keymaps) != 0 {
		keys = keymaps[0]
	}
	return Model{
		Tab:       TabOverview,
		Focus:     FocusContent,
		Selection: make(map[Tab]string),
		keymap:    keys,
	}
}

func (m Model) Apply(event ipc.Event) Model {
	oldSnapshot := m.snapshot
	m.Selection = cloneSelection(m.Selection)
	if m.Modal != nil {
		modal := *m.Modal
		modal.Form = modal.Form.Clone()
		m.Modal = &modal
	}
	m.snapshot = event
	if !hasGroup(m.snapshot, m.groupID) {
		m.groupID = firstGroupID(m.snapshot)
	}

	for _, tab := range tabs {
		selected := m.Selection[tab]
		if (selected == "" && tab != m.Tab) || (selected != "" && !tabRowsChanged(oldSnapshot, event, tab)) {
			continue
		}
		newRows := rowsForSnapshot(event, tab)
		if selected == "" {
			if len(newRows) != 0 {
				m.Selection[tab] = newRows[0].ID
			}
			continue
		}
		if rowIndex(newRows, selected) >= 0 {
			continue
		}
		if len(newRows) == 0 {
			delete(m.Selection, tab)
			continue
		}
		index := rowIndex(rowsForSnapshot(oldSnapshot, tab), selected)
		if index < 0 {
			index = 0
		}
		if index >= len(newRows) {
			index = len(newRows) - 1
		}
		m.Selection[tab] = newRows[index].ID
	}
	if m.Modal == nil && m.Focus == FocusModal {
		m.Focus = FocusContent
	}
	return m
}

func tabRowsChanged(before, after ipc.Event, tab Tab) bool {
	old, next := before.Snapshot, after.Snapshot
	switch tab {
	case TabOverview:
		return !reflect.DeepEqual(old.Binary, next.Binary) || !reflect.DeepEqual(old.Switches, next.Switches) || !reflect.DeepEqual(old.Jobs, next.Jobs) || !reflect.DeepEqual(old.Errors, next.Errors)
	case TabProxies:
		return !reflect.DeepEqual(old.Groups, next.Groups) || !reflect.DeepEqual(old.Proxies, next.Proxies)
	case TabSubscriptions:
		return !reflect.DeepEqual(old.Subscriptions, next.Subscriptions)
	case TabFilters:
		return !reflect.DeepEqual(old.Filters, next.Filters)
	case TabResources:
		return !reflect.DeepEqual(old.Resources, next.Resources)
	case TabSettings:
		return !reflect.DeepEqual(old.Binary, next.Binary) || old.Monitor != next.Monitor || old.SystemProxy != next.SystemProxy || !reflect.DeepEqual(old.DNS, next.DNS)
	default:
		return true
	}
}

func (m Model) Rows() []Row {
	rows := rowsForSnapshot(m.snapshot, m.Tab)
	selected := m.Selection[m.Tab]
	for i := range rows {
		rows[i].Selected = rows[i].ID == selected
		if rows[i].action != "" {
			rows[i].Cells[2] = m.keymap.KeyFor(TabSettings, rows[i].action)
		}
	}
	return rows
}

func (m Model) CurrentGroupID() string { return m.groupID }

func (m Model) bindingContext() Tab {
	if m.LogOpen {
		return Tab("log")
	}
	if m.Modal != nil {
		if m.Modal.Form != nil {
			return Tab("form")
		}
		return Tab("dialog")
	}
	return m.Tab
}

func (m Model) helpEntries() []string { return m.keymap.Help(m.bindingContext()) }

func (m Model) Help() []string {
	if m.HelpOpen {
		return m.keymap.Hints(Tab("help"))
	}
	return m.keymap.Hints(m.bindingContext())
}

// HandleKey applies one normalized key name and optionally returns one IPC
// intent. It performs no I/O.
func (m Model) HandleKey(key string) (Model, *ipc.Command, bool) {
	if m.HelpOpen {
		action, _ := m.keymap.Action(Tab("help"), key)
		maxOffset := max(0, len(m.helpEntries())-1)
		switch action {
		case "help_up":
			m.HelpOffset = max(0, m.HelpOffset-1)
		case "help_down":
			m.HelpOffset = min(maxOffset, m.HelpOffset+1)
		case "help_page_up":
			m.HelpOffset = max(0, m.HelpOffset-10)
		case "help_page_down":
			m.HelpOffset = min(maxOffset, m.HelpOffset+10)
		case "help_home":
			m.HelpOffset = 0
		case "help_end":
			m.HelpOffset = maxOffset
		default:
			m.HelpOpen = false
		}
		return m, nil, false
	}
	context := m.bindingContext()
	if action, _ := m.keymap.Action(context, key); action == "toggle_help" {
		m.HelpOpen, m.HelpOffset = true, 0
		return m, nil, false
	}
	if m.LogOpen {
		action, _ := m.keymap.Action(Tab("log"), key)
		maxOffset := len(m.snapshot.Snapshot.Diagnostics)
		switch action {
		case "log_older":
			if m.LogOffset < maxOffset {
				m.LogOffset++
			}
		case "log_newer":
			if m.LogOffset > 0 {
				m.LogOffset--
			}
		case "log_page_older":
			m.LogOffset += 50
			if m.LogOffset > maxOffset {
				m.LogOffset = maxOffset
			}
		case "log_page_newer":
			m.LogOffset -= 50
			if m.LogOffset < 0 {
				m.LogOffset = 0
			}
		case "log_oldest":
			m.LogOffset = maxOffset
		case "log_newest":
			m.LogOffset = 0
		default:
			m.LogOpen = false
		}
		return m, nil, false
	}
	if m.Modal != nil {
		modal := *m.Modal
		modal.Form = modal.Form.Clone()
		m.Modal = &modal
		return m.handleModalKey(key)
	}
	key = normalizeKey(key)
	action, ok := m.keymap.Action(m.Tab, key)
	if !ok {
		return m, nil, false
	}
	if action == "toggle_log" {
		m.LogOpen = true
		m.LogOffset = 0
		return m, nil, false
	}
	m.Focus = FocusContent
	switch action {
	case "quit":
		return m, nil, true
	case "next_tab":
		return m.selectTab(nextTab(m.Tab, 1)), nil, false
	case "prev_tab":
		return m.selectTab(nextTab(m.Tab, -1)), nil, false
	case "tab_overview":
		return m.selectTab(TabOverview), nil, false
	case "tab_proxies":
		return m.selectTab(TabProxies), nil, false
	case "tab_subscriptions":
		return m.selectTab(TabSubscriptions), nil, false
	case "tab_filters":
		return m.selectTab(TabFilters), nil, false
	case "tab_resources":
		return m.selectTab(TabResources), nil, false
	case "tab_settings":
		return m.selectTab(TabSettings), nil, false
	case "move_up":
		return m.moveSelection(-1), nil, false
	case "move_down":
		return m.moveSelection(1), nil, false
	case "activate":
		return m.activate()
	case "refresh_subscription":
		if row, ok := m.selectedRow(); ok && row.kind == rowSubscription && row.subscriptionID != "" {
			return m, &ipc.Command{Kind: ipc.CommandRefreshSubscription, SubscriptionID: row.subscriptionID}, false
		}
	case "activate_subscription":
		if row, ok := m.selectedRow(); ok && row.kind == rowSubscription && row.subscriptionID != "" {
			return m, &ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: row.subscriptionID}, false
		}
	case "edit_subscription":
		if row, ok := m.selectedRow(); ok && row.kind == rowSubscription && row.subscriptionID != "" {
			return m.openSubscriptionModal(true, row.subscriptionID), nil, false
		}
	case "new_subscription":
		return m.openSubscriptionModal(false, ""), nil, false
	case "delete_subscription":
		if row, ok := m.selectedRow(); ok && row.kind == rowSubscription && row.subscriptionID != "" {
			m.Modal = &Modal{Kind: ModalDeleteSubscription, Input: row.Title, TargetID: row.subscriptionID}
			m.Focus = FocusModal
			m.Notice = "Confirm subscription deletion."
			return m, nil, false
		}
	case "refresh_filter":
		if row, ok := m.selectedRow(); ok && row.kind == rowFilter && row.filterID != "" {
			return m, &ipc.Command{Kind: ipc.CommandRefreshFilter, FilterID: row.filterID}, false
		}
	case "refresh_resource":
		if row, ok := m.selectedRow(); ok && row.kind == rowResource && row.resourceID != "" {
			return m, &ipc.Command{Kind: ipc.CommandRefreshResource, ResourceID: row.resourceID}, false
		}
	case "new_filter":
		return m.openManagedModal(ModalFilter, false, ""), nil, false
	case "edit_filter":
		if row, ok := m.selectedRow(); ok && row.kind == rowFilter && row.filterID != "" {
			return m.openManagedModal(ModalFilter, true, row.filterID), nil, false
		}
	case "new_resource":
		return m.openManagedModal(ModalResource, false, ""), nil, false
	case "edit_resource":
		if row, ok := m.selectedRow(); ok && row.kind == rowResource && row.resourceID != "" {
			return m.openManagedModal(ModalResource, true, row.resourceID), nil, false
		}
	case "cancel_modal":
		return m, nil, false
	case "start":
		return m, command(ipc.CommandStart), false
	case "stop":
		return m, command(ipc.CommandStop), false
	case "reload_configuration":
		return m, command(ipc.CommandReloadConfiguration), false
	case "manual_probe":
		groupID := m.focusedGroupID()
		if groupID == "" {
			m.Notice = "Select a group before requesting a probe."
			return m, nil, false
		}
		if !m.managedSelector(groupID) {
			m.Notice = "Mihomo manages this group; manual probes are unavailable."
			return m, nil, false
		}
		return m, &ipc.Command{Kind: ipc.CommandManualProbe, GroupID: groupID}, false
	case "toggle_automation":
		groupID := m.focusedGroupID()
		if groupID != "" && !m.managedSelector(groupID) {
			m.Notice = "Mihomo manages this group; automation is unavailable."
			return m, nil, false
		}
		enabled, ok := groupAutomation(m.snapshot, groupID)
		if !ok {
			m.Notice = "Select a group before changing automation."
			return m, nil, false
		}
		return m, &ipc.Command{Kind: ipc.CommandSetAutomation, GroupID: groupID, Automation: &ipc.AutomationSetting{Enabled: !enabled}}, false
	case "system_proxy_enable":
		return m, configCommand(&ipc.ConfigPatch{SystemProxyEnabled: new(true)}), false
	case "system_proxy_disable":
		return m, configCommand(&ipc.ConfigPatch{SystemProxyEnabled: new(false)}), false
	case "monitor_enable":
		return m, configCommand(&ipc.ConfigPatch{MonitorEnabled: new(true)}), false
	case "monitor_disable":
		return m, configCommand(&ipc.ConfigPatch{MonitorEnabled: new(false)}), false
	case "edit_monitor_interval":
		return m.openMonitorModal(monitorInterval), nil, false
	case "edit_alert_threshold":
		return m.openMonitorModal(monitorAlertThreshold), nil, false
	case "edit_monitor_url":
		return m.openMonitorModal(monitorTestURL), nil, false
	case "edit_monitor_timeout":
		return m.openMonitorModal(monitorTimeout), nil, false
	case "edit_monitor_concurrency":
		return m.openMonitorModal(monitorConcurrency), nil, false
	case "edit_monitor_threshold":
		return m.openMonitorModal(monitorThreshold), nil, false
	case "edit_monitor_bad_samples":
		return m.openMonitorModal(monitorBadSamples), nil, false
	case "edit_monitor_improvement":
		return m.openMonitorModal(monitorImprovement), nil, false
	case "edit_monitor_cooldown":
		return m.openMonitorModal(monitorCooldown), nil, false
	case "edit_monitor_jitter":
		return m.openMonitorModal(monitorJitter), nil, false
	case "edit_dns_listen":
		return m.openDNSListenModal(), nil, false
	case "new_dns_resolver":
		return m.openDNSResolverModal(false, ""), nil, false
	case "new_dns_route":
		return m.openDNSRouteModal(false, ""), nil, false
	case "edit_dns_entry":
		return m.editSelectedDNS(), nil, false
	case "delete_dns_entry":
		return m.deleteSelectedDNS(), nil, false
	case "edit_binary":
		return m.openModal(ModalBinary), nil, false
	default:
		return m, nil, false
	}
	return m, nil, false
}

func (m Model) CommandQueued() Model {
	m.Pending++
	m.Notice = "Sending intent to the application..."
	return m
}

func (m Model) CommandResult(queued bool, err error, private ...bool) Model {
	if m.Pending > 0 {
		m.Pending--
	}
	if err != nil {
		if len(private) > 0 && private[0] {
			m.Notice = "Managed source command failed."
		} else {
			m.Notice = "Command failed: " + err.Error()
		}
	} else if queued {
		m.Notice = "Intent queued; operation progress appears in Jobs."
	} else {
		m.Notice = "Application did not queue the intent."
	}
	return m
}

func (m Model) QueueFull() Model {
	m.Notice = "Command queue is full; wait for a pending command to finish."
	return m
}

func (m Model) Progress() string {
	jobs := m.snapshot.Snapshot.Jobs
	if len(jobs) == 0 {
		return "Jobs: idle"
	}
	parts := make([]string, 0, len(jobs))
	for _, job := range jobs {
		kind := fallback(job.Kind, "operation")
		state := fallback(job.State, "pending")
		parts = append(parts, kind+": "+state)
	}
	return "Jobs: " + strings.Join(parts, " | ")
}

func (m Model) handleModalKey(key string) (Model, *ipc.Command, bool) {
	if m.Modal.Form != nil {
		return m.handleFormKey(key)
	}
	if key == "esc" {
		m.Modal = nil
		m.Focus = FocusContent
		m.Notice = ""
		return m, nil, false
	}
	if m.Modal.Kind == ModalDNSListen {
		return m.handleDNSListenKey(key)
	}
	if m.Modal.Kind == ModalDeleteSubscription {
		if key != "enter" || m.Modal.TargetID == "" {
			return m, nil, false
		}
		subscriptionID := m.Modal.TargetID
		m.Modal = nil
		m.Focus = FocusContent
		return m, &ipc.Command{Kind: ipc.CommandDeleteSubscription, SubscriptionID: subscriptionID}, false
	}
	if m.Modal.Kind == ModalDeleteDNS {
		if key != "enter" || m.Modal.TargetID == "" {
			return m, nil, false
		}
		targetID := m.Modal.TargetID
		m.Modal = nil
		m.Focus = FocusContent
		return m.removeDNSIntent(targetID)
	}
	if key == "backspace" {
		modal := *m.Modal
		input := []rune(modal.Input)
		if len(input) != 0 {
			modal.Input = string(input[:len(input)-1])
			m.Modal = &modal
		}
		return m, nil, false
	}
	if key == "enter" {
		if m.Modal.Kind == ModalBinary {
			value := m.Modal.Input
			if value != "system" && value != "bundled" && (!filepath.IsAbs(value) || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n")) {
				m.Notice = "Select system, bundled, or an absolute executable path."
				return m, nil, false
			}
			m.Modal = nil
			m.Focus = FocusContent
			return m, configCommand(&ipc.ConfigPatch{Binary: &value}), false
		}
		m.Notice = "Unsupported settings dialog."
		return m, nil, false
	}
	if m.Modal.Kind == ModalBinary {
		if utf8.ValidString(key) && utf8.RuneCountInString(key) == 1 && unicode.IsPrint([]rune(key)[0]) && len(m.Modal.Input)+len(key) <= 4096 {
			m.Modal = &Modal{Kind: ModalBinary, Input: m.Modal.Input + key}
		}
		return m, nil, false
	}
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && utf8.ValidString(key) && len(m.Modal.Input) < 5 {
		m.Modal = &Modal{Kind: m.Modal.Kind, Input: m.Modal.Input + key}
	}
	return m, nil, false
}

func (m Model) openMonitorModal(_ monitorField) Model {
	monitor := m.snapshot.Snapshot.Monitor
	editor, err := form.New([]form.Field{
		{ID: "enabled", Label: "Enabled", Kind: form.Toggle, Value: strconv.FormatBool(monitor.Enabled)},
		{ID: "test_url", Label: "Test URL", Kind: form.Text, Sensitive: true},
		{ID: "interval", Label: "Interval seconds", Kind: form.Text, Value: strconv.FormatInt(monitor.IntervalSeconds, 10)},
		{ID: "timeout", Label: "Timeout milliseconds", Kind: form.Text, Value: strconv.FormatInt(monitor.TimeoutMillis, 10)},
		{ID: "concurrency", Label: "Concurrency", Kind: form.Text, Value: strconv.Itoa(monitor.Concurrency)},
		{ID: "threshold", Label: "Threshold milliseconds", Kind: form.Text, Value: strconv.FormatInt(monitor.ThresholdMillis, 10)},
		{ID: "alert_threshold", Label: "Alert threshold milliseconds", Kind: form.Text, Value: strconv.FormatInt(monitor.AlertThresholdMillis, 10)},
		{ID: "bad_samples", Label: "Consecutive bad samples", Kind: form.Text, Value: strconv.Itoa(monitor.ConsecutiveBadSamples)},
		{ID: "improvement", Label: "Minimum improvement milliseconds", Kind: form.Text, Value: strconv.FormatInt(monitor.MinImprovementMillis, 10)},
		{ID: "cooldown", Label: "Cooldown seconds", Kind: form.Text, Value: strconv.FormatInt(monitor.CooldownSeconds, 10)},
		{ID: "jitter", Label: "Jitter milliseconds", Kind: form.Text, Value: strconv.FormatInt(monitor.JitterMillis, 10)},
	})
	if err != nil {
		m.Notice = "Monitor settings form unavailable."
		return m
	}
	m.Modal = &Modal{Kind: ModalMonitorSetting, Form: editor}
	m.Focus = FocusModal
	m.Notice = "Edit monitor settings; the test URL stays hidden unless changed."
	return m
}

func monitorSettingPatch(field monitorField, input string) (*ipc.ConfigPatch, string) {
	if field == monitorTestURL {
		if input == "" || len(input) > 2048 || strings.ContainsAny(input, "\x00\r\n") {
			return nil, "Monitor test URL must be 1-2048 bytes without line breaks."
		}
		return &ipc.ConfigPatch{MonitorTestURL: &input}, ""
	}
	value, err := strconv.ParseUint(input, 10, 32)
	if err != nil {
		return nil, "Enter a whole number within the setting's range."
	}
	max := uint64(^uint32(0))
	min := uint64(1)
	switch field {
	case monitorInterval:
		max = 86400
	case monitorTimeout:
		max = 86400000
	case monitorConcurrency:
		max = 64
	case monitorAlertThreshold:
		max = 60000
	case monitorBadSamples:
		max = 5
	case monitorCooldown, monitorJitter:
		min = 0
	case monitorThreshold, monitorImprovement:
	default:
		return nil, "Unsupported monitor setting."
	}
	if value < min || value > max {
		return nil, monitorRangeNotice(field)
	}
	parsed := uint32(value)
	patch := &ipc.ConfigPatch{}
	switch field {
	case monitorInterval:
		patch.MonitorIntervalSeconds = &parsed
	case monitorTimeout:
		patch.MonitorTimeoutMillis = &parsed
	case monitorConcurrency:
		patch.MonitorConcurrency = &parsed
	case monitorThreshold:
		patch.MonitorThresholdMillis = &parsed
	case monitorAlertThreshold:
		patch.AlertThresholdMillis = &parsed
	case monitorBadSamples:
		patch.MonitorConsecutiveBadSamples = &parsed
	case monitorImprovement:
		patch.MonitorMinImprovementMillis = &parsed
	case monitorCooldown:
		patch.MonitorCooldownSeconds = &parsed
	case monitorJitter:
		patch.MonitorJitterMillis = &parsed
	}
	return patch, ""
}

func monitorRangeNotice(field monitorField) string {
	switch field {
	case monitorInterval:
		return "Monitor interval must be between 1 and 86400 seconds."
	case monitorTimeout:
		return "Monitor timeout must be between 1 and 86400000 milliseconds."
	case monitorConcurrency:
		return "Monitor concurrency must be between 1 and 64."
	case monitorThreshold:
		return "Monitor threshold must be between 1 and 4294967295 milliseconds."
	case monitorAlertThreshold:
		return "Alert threshold must be between 1 and 60000 milliseconds."
	case monitorBadSamples:
		return "Consecutive bad samples must be between 1 and 5."
	case monitorImprovement:
		return "Minimum improvement must be between 1 and 4294967295 milliseconds."
	case monitorCooldown:
		return "Cooldown must be between 0 and 4294967295 seconds."
	case monitorJitter:
		return "Jitter must be between 0 and 4294967295 milliseconds."
	default:
		return "Monitor setting is out of range."
	}
}
func monitorFormIntent(modal *Modal) (*ipc.Command, string) {
	patch := &ipc.ConfigPatch{}
	for _, change := range modal.Form.Changes() {
		switch change.ID {
		case "enabled":
			if change.Value != "true" && change.Value != "false" {
				return nil, "Monitor enabled must be true or false."
			}
			patch.MonitorEnabled = new(change.Value == "true")
		case "test_url":
			setting, notice := monitorSettingPatch(monitorTestURL, change.Value)
			if notice != "" {
				return nil, notice
			}
			parsed, err := url.Parse(change.Value)
			if err != nil || strings.ContainsAny(change.Value, "\x00\r\n#") || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
				return nil, "Monitor test URL must use HTTP or HTTPS without credentials or fragment."
			}
			patch.MonitorTestURL = setting.MonitorTestURL
		default:
			field := monitorField(0)
			switch change.ID {
			case "interval":
				field = monitorInterval
			case "timeout":
				field = monitorTimeout
			case "concurrency":
				field = monitorConcurrency
			case "threshold":
				field = monitorThreshold
			case "alert_threshold":
				field = monitorAlertThreshold
			case "bad_samples":
				field = monitorBadSamples
			case "improvement":
				field = monitorImprovement
			case "cooldown":
				field = monitorCooldown
			case "jitter":
				field = monitorJitter
			default:
				return nil, "Unsupported monitor setting."
			}
			setting, notice := monitorSettingPatch(field, change.Value)
			if notice != "" {
				return nil, notice
			}
			switch field {
			case monitorInterval:
				patch.MonitorIntervalSeconds = setting.MonitorIntervalSeconds
			case monitorTimeout:
				patch.MonitorTimeoutMillis = setting.MonitorTimeoutMillis
			case monitorConcurrency:
				patch.MonitorConcurrency = setting.MonitorConcurrency
			case monitorThreshold:
				patch.MonitorThresholdMillis = setting.MonitorThresholdMillis
			case monitorAlertThreshold:
				patch.AlertThresholdMillis = setting.AlertThresholdMillis
			case monitorBadSamples:
				patch.MonitorConsecutiveBadSamples = setting.MonitorConsecutiveBadSamples
			case monitorImprovement:
				patch.MonitorMinImprovementMillis = setting.MonitorMinImprovementMillis
			case monitorCooldown:
				patch.MonitorCooldownSeconds = setting.MonitorCooldownSeconds
			case monitorJitter:
				patch.MonitorJitterMillis = setting.MonitorJitterMillis
			}
		}
	}
	if patch.MonitorEnabled == nil && patch.MonitorTestURL == nil && patch.MonitorIntervalSeconds == nil && patch.MonitorTimeoutMillis == nil && patch.MonitorConcurrency == nil && patch.MonitorThresholdMillis == nil && patch.AlertThresholdMillis == nil && patch.MonitorConsecutiveBadSamples == nil && patch.MonitorMinImprovementMillis == nil && patch.MonitorCooldownSeconds == nil && patch.MonitorJitterMillis == nil {
		return nil, ""
	}
	return configCommand(patch), ""
}

func (m Model) openDNSListenModal() Model {
	m.Modal = &Modal{Kind: ModalDNSListen, Input: m.snapshot.Snapshot.DNS.Listen}
	m.Focus = FocusModal
	m.Notice = "Edit the DNS listener; backend validates address and routing conflicts."
	return m
}

func (m Model) handleDNSListenKey(key string) (Model, *ipc.Command, bool) {
	modal := *m.Modal
	if key == "backspace" {
		input := []rune(modal.Input)
		if len(input) > 0 {
			modal.Input = string(input[:len(input)-1])
			m.Modal = &modal
		}
		return m, nil, false
	}
	if key == "enter" {
		if len(modal.Input) == 0 || len(modal.Input) > 128 || strings.ContainsAny(modal.Input, "\x00\r\n") {
			m.Notice = "DNS listener must be 1-128 bytes without line breaks."
			return m, nil, false
		}
		m.Modal = nil
		m.Focus = FocusContent
		return m, configCommand(&ipc.ConfigPatch{DNSListen: &modal.Input}), false
	}
	if utf8.ValidString(key) && utf8.RuneCountInString(key) == 1 && unicode.IsPrint([]rune(key)[0]) && len(modal.Input)+len(key) <= 128 {
		modal.Input += key
		m.Modal = &modal
	}
	return m, nil, false
}

func (m Model) openDNSResolverModal(edit bool, targetID string) Model {
	if !edit && len(m.snapshot.Snapshot.DNS.ResolverSets) >= 256 {
		m.Notice = "DNS policy already has 256 resolver sets."
		return m
	}
	id, dnscrypt := "", false
	if edit {
		found := false
		for _, set := range m.snapshot.Snapshot.DNS.ResolverSets {
			if set.ID == targetID {
				id, dnscrypt, found = set.ID, set.DNSCrypt, true
				break
			}
		}
		if !found {
			m.Notice = "DNS resolver set is no longer available."
			return m
		}
	}
	editor, err := form.New([]form.Field{
		{ID: "id", Label: "Stable ID", Kind: form.Text, Value: id},
		{ID: "endpoints", Label: "Endpoints", Kind: form.Text, Sensitive: true},
		{ID: "dnscrypt", Label: "DNSCrypt", Kind: form.Toggle, Value: strconv.FormatBool(dnscrypt)},
	})
	if err != nil {
		m.Notice = "DNS resolver form unavailable."
		return m
	}
	m.Modal = &Modal{Kind: ModalDNSResolver, TargetID: targetID, Form: editor}
	m.Focus = FocusModal
	m.Notice = "Endpoints accept 1-16 comma-separated values; credentials are not allowed."
	return m
}

func (m Model) openDNSRouteModal(edit bool, targetID string) Model {
	if !edit && len(m.snapshot.Snapshot.DNS.Routes) >= 1024 {
		m.Notice = "DNS policy already has 1024 routes."
		return m
	}
	matcher, value, resolver := "suffix", "", ""
	if edit {
		found := false
		for _, route := range m.snapshot.Snapshot.DNS.Routes {
			if dnsRouteIdentity(route.Suffix, route.GeoSite, route.Resource) == targetID {
				matcher, value = dnsRouteMatcherFields(route.Suffix, route.GeoSite, route.Resource)
				resolver, found = route.ResolverSet, true
				break
			}
		}
		if !found {
			m.Notice = "DNS route is no longer available."
			return m
		}
	}
	editor, err := form.New([]form.Field{
		{ID: "matcher", Label: "Matcher", Kind: form.Choice, Value: matcher, Choices: []string{"suffix", "geosite", "resource"}},
		{ID: "value", Label: "Matcher value", Kind: form.Text, Value: value},
		{ID: "resolver", Label: "Resolver set ID", Kind: form.Text, Value: resolver},
	})
	if err != nil {
		m.Notice = "DNS route form unavailable."
		return m
	}
	m.Modal = &Modal{Kind: ModalDNSRoute, TargetID: targetID, Form: editor}
	m.Focus = FocusModal
	m.Notice = "Choose a suffix, GeoSite, or resource matcher."
	return m
}

func (m Model) dnsFormIntent(modal *Modal) (*ipc.Command, string) {
	changes := modal.Form.Changes()
	editing := modal.TargetID != ""
	if editing && len(changes) == 0 {
		return nil, ""
	}
	if modal.Kind == ModalDNSResolver {
		set := ipc.DNSResolverSet{ID: modal.TargetID}
		index := -1
		if editing {
			for i, existing := range m.dnsResolverSets() {
				if existing.ID == modal.TargetID {
					set, index = existing, i
					break
				}
			}
			if index < 0 {
				return nil, "DNS resolver set is no longer available."
			}
		}
		endpointsChanged := false
		for _, change := range changes {
			switch change.ID {
			case "id":
				set.ID = strings.TrimSpace(change.Value)
			case "endpoints":
				endpoints, err := parseDNSEndpoints(change.Value)
				if err != nil {
					return nil, err.Error()
				}
				set.Endpoints, endpointsChanged = endpoints, true
			case "dnscrypt":
				if change.Value != "true" && change.Value != "false" {
					return nil, "DNSCrypt must be enabled or disabled."
				}
				set.DNSCrypt = change.Value == "true"
			}
		}
		if !editing && !endpointsChanged {
			return nil, "Enter between 1 and 16 DNS endpoints."
		}
		if !validSubscriptionID(set.ID) {
			return nil, "Resolver ID must be a stable ID (max 64 characters)."
		}
		sets, routes := m.dnsResolverSets(), m.dnsRoutes()
		if !editing && len(sets) >= 256 {
			return nil, "DNS policy reached its command limit."
		}
		for i, existing := range sets {
			if existing.ID == set.ID && i != index {
				return nil, "Resolver ID already exists."
			}
		}
		if editing {
			sets[index] = set
		} else {
			sets = append(sets, set)
		}
		if editing && set.ID != modal.TargetID {
			for i := range routes {
				if routes[i].ResolverSet == modal.TargetID {
					routes[i].ResolverSet = set.ID
				}
			}
		}
		return dnsRoutingCommand(sets, routes), ""
	}

	form := dnsForm{}
	form.values[dnsRouteMatcher] = "suffix"
	routes := m.dnsRoutes()
	index := -1
	if editing {
		for i, route := range routes {
			if dnsRouteID(route) == modal.TargetID {
				form.values[dnsRouteMatcher], form.values[dnsRouteValue] = dnsRouteMatcherFields(route.Suffix, route.GeoSite, route.Resource)
				form.values[dnsRouteResolver] = route.ResolverSet
				index = i
				break
			}
		}
		if index < 0 {
			return nil, "DNS route is no longer available."
		}
	}
	for _, change := range changes {
		switch change.ID {
		case "matcher":
			form.values[dnsRouteMatcher] = change.Value
		case "value":
			form.values[dnsRouteValue] = strings.TrimSpace(change.Value)
		case "resolver":
			form.values[dnsRouteResolver] = strings.TrimSpace(change.Value)
		}
	}
	for field := dnsRouteMatcher; field <= dnsRouteResolver; field++ {
		if notice := validateDNSRouteForm(form, field); notice != "" {
			return nil, notice
		}
	}
	if !editing && len(routes) >= 1024 {
		return nil, "DNS policy reached its command limit."
	}
	route := routeFromDNSForm(form)
	for i, existing := range routes {
		if dnsRouteID(existing) == dnsRouteID(route) && (!editing || dnsRouteID(existing) != modal.TargetID) {
			return nil, "DNS route matcher already exists."
		}
		if editing && dnsRouteID(existing) == modal.TargetID {
			index = i
		}
	}
	if editing {
		if index < 0 {
			return nil, "DNS route is no longer available."
		}
		routes[index] = route
	} else {
		routes = append(routes, route)
	}
	return dnsRoutingCommand(m.dnsResolverSets(), routes), ""
}

func (m Model) editSelectedDNS() Model {
	row, ok := m.selectedRow()
	if !ok {
		return m
	}
	switch row.kind {
	case rowDNSResolver:
		return m.openDNSResolverModal(true, row.dnsID)
	case rowDNSRoute:
		return m.openDNSRouteModal(true, row.dnsID)
	default:
		return m
	}
}

func (m Model) deleteSelectedDNS() Model {
	row, ok := m.selectedRow()
	if !ok || row.kind != rowDNSResolver && row.kind != rowDNSRoute {
		return m
	}
	targetID := row.dnsID
	if row.kind == rowDNSResolver {
		targetID = "dns:set:" + targetID
	}
	m.Modal = &Modal{Kind: ModalDeleteDNS, TargetID: targetID, Input: row.Title}
	m.Focus = FocusModal
	m.Notice = "Confirm DNS policy removal."
	return m
}

func validateDNSRouteForm(form dnsForm, field int) string {
	value := form.values[field]
	switch field {
	case dnsRouteMatcher:
		if value != "suffix" && value != "geosite" && value != "resource" {
			return "Matcher must be suffix, geosite, or resource."
		}
	case dnsRouteValue:
		max := 253
		if form.values[dnsRouteMatcher] != "suffix" {
			max = 64
		}
		if value == "" || len(value) > max || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Sprintf("Matcher value must be 1-%d bytes.", max)
		}
		if form.values[dnsRouteMatcher] == "resource" && !validSubscriptionID(value) {
			return "Resource matcher must be a stable resource ID."
		}
	case dnsRouteResolver:
		if !validSubscriptionID(value) {
			return "Resolver set must be a stable ID."
		}
	}
	return ""
}

func parseDNSEndpoints(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	if value == "" || len(parts) > 16 {
		return nil, fmt.Errorf("Enter between 1 and 16 DNS endpoints.")
	}
	endpoints := make([]string, 0, len(parts))
	for _, part := range parts {
		endpoint := strings.TrimSpace(part)
		if endpoint == "" || len(endpoint) > 256 || strings.ContainsAny(endpoint, "\x00\r\n") || strings.ContainsRune(endpoint, '@') {
			return nil, fmt.Errorf("DNS endpoints must be 1-256 bytes and cannot contain credentials.")
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, nil
}

func routeFromDNSForm(form dnsForm) ipc.DNSRoute {
	route := ipc.DNSRoute{ResolverSet: form.values[dnsRouteResolver]}
	switch form.values[dnsRouteMatcher] {
	case "suffix":
		route.Suffix = form.values[dnsRouteValue]
	case "geosite":
		route.GeoSite = form.values[dnsRouteValue]
	case "resource":
		route.Resource = form.values[dnsRouteValue]
	}
	return route
}

func dnsRouteMatcherFields(suffix, geosite, resource string) (string, string) {
	switch {
	case suffix != "":
		return "suffix", suffix
	case geosite != "":
		return "geosite", geosite
	default:
		return "resource", resource
	}
}

func dnsRouteID(route ipc.DNSRoute) string {
	return dnsRouteIdentity(route.Suffix, route.GeoSite, route.Resource)
}

func dnsRouteIdentity(suffix, geosite, resource string) string {
	kind, value := dnsRouteMatcherFields(suffix, geosite, resource)
	return "dns:route:" + kind + ":" + value
}

func (m Model) dnsResolverSets() []ipc.DNSResolverSet {
	source := m.snapshot.Snapshot.DNS.ResolverSets
	out := make([]ipc.DNSResolverSet, len(source))
	for i, set := range source {
		out[i] = ipc.DNSResolverSet{ID: set.ID, Endpoints: append([]string(nil), set.Endpoints...), DNSCrypt: set.DNSCrypt}
	}
	return out
}

func (m Model) dnsRoutes() []ipc.DNSRoute {
	source := m.snapshot.Snapshot.DNS.Routes
	out := make([]ipc.DNSRoute, len(source))
	for i, route := range source {
		out[i] = ipc.DNSRoute{Suffix: route.Suffix, GeoSite: route.GeoSite, Resource: route.Resource, ResolverSet: route.ResolverSet}
	}
	return out
}

func dnsRoutingCommand(sets []ipc.DNSResolverSet, routes []ipc.DNSRoute) *ipc.Command {
	return &ipc.Command{Kind: ipc.CommandSetDNSRouting, DNSRouting: &ipc.DNSRoutingEdit{ResolverSets: sets, Routes: routes}}
}

func (m Model) removeDNSIntent(targetID string) (Model, *ipc.Command, bool) {
	sets := m.dnsResolverSets()
	routes := m.dnsRoutes()
	removed := false
	if strings.HasPrefix(targetID, "dns:set:") {
		id := strings.TrimPrefix(targetID, "dns:set:")
		for i, set := range sets {
			if set.ID == id {
				sets = append(sets[:i], sets[i+1:]...)
				removed = true
				break
			}
		}
	} else {
		for i, route := range routes {
			if dnsRouteID(route) == targetID {
				routes = append(routes[:i], routes[i+1:]...)
				removed = true
				break
			}
		}
	}
	if !removed {
		m.Notice = "DNS policy entry is no longer available."
		return m, nil, false
	}
	return m, dnsRoutingCommand(sets, routes), false
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
func (m Model) openSubscriptionModal(edit bool, targetID string) Model {
	name, route, enabled, refresh, timeout := "", "direct", true, uint32(43200), uint32(30)
	var allowHTTP, allowInvalidTLS bool
	if edit {
		found := false
		for _, item := range m.snapshot.Snapshot.Subscriptions {
			if item.ID == targetID {
				name = item.Name
				route = fallback(item.Route, "direct")
				enabled, allowHTTP, allowInvalidTLS = item.Enabled, item.AllowHTTP, item.AllowInvalidTLS
				if item.RefreshIntervalSeconds > 0 {
					refresh = item.RefreshIntervalSeconds
				}
				if item.TimeoutSeconds > 0 {
					timeout = item.TimeoutSeconds
				}
				found = true
				break
			}
		}
		if !found {
			m.Notice = "Subscription is no longer available."
			return m
		}
	}
	editor, err := form.New([]form.Field{
		{ID: "id", Label: "Stable ID", Kind: form.Text, Value: targetID, ReadOnly: edit},
		{ID: "name", Label: "Display name", Kind: form.Text, Value: name},
		{ID: "url", Label: "Source URL", Kind: form.Text, Sensitive: true},
		{ID: "user_agent", Label: "User-Agent", Kind: form.Text, Sensitive: true},
		{ID: "enabled", Label: "Enabled", Kind: form.Toggle, Value: strconv.FormatBool(enabled)},
		{ID: "refresh_interval", Label: "Refresh seconds", Kind: form.Text, Value: strconv.FormatUint(uint64(refresh), 10)},
		{ID: "timeout", Label: "Timeout seconds", Kind: form.Text, Value: strconv.FormatUint(uint64(timeout), 10)},
		{ID: "route", Label: "Connection route", Kind: form.Choice, Value: route, Choices: []string{"direct", "system_proxy", "mihomo_proxy"}},
		{ID: "allow_http", Label: "Allow HTTP", Kind: form.Toggle, Value: strconv.FormatBool(allowHTTP)},
		{ID: "allow_invalid_tls", Label: "Allow invalid TLS", Kind: form.Toggle, Value: strconv.FormatBool(allowInvalidTLS)},
	})
	if err != nil {
		m.Notice = "Subscription settings unavailable."
		return m
	}
	if edit {
		editor.Move(1)
	}
	m.Modal = &Modal{Kind: ModalSubscription, TargetID: targetID, Form: editor}
	m.Focus = FocusModal
	m.Notice = "Edit fields, then Ctrl+S to save; private fields stay hidden."
	return m
}

func (m Model) handleFormKey(key string) (Model, *ipc.Command, bool) {
	editor := m.Modal.Form
	action, ok := m.keymap.Action(Tab("form"), key)
	if !ok {
		if utf8.ValidString(key) && utf8.RuneCountInString(key) == 1 && unicode.IsPrint([]rune(key)[0]) {
			if !editor.Insert(key) {
				m.Notice = "Selected field does not accept this input."
			}
		}
		return m, nil, false
	}
	switch action {
	case "form_up":
		editor.Move(-1)
	case "form_down":
		editor.Move(1)
	case "form_toggle":
		if !editor.Toggle() {
			editor.Insert(" ")
		}
	case "form_edit":
		if !editor.Toggle() && !editor.Cycle(1) {
			editor.SetText("")
		}
	case "form_left":
		if !editor.Cycle(-1) {
			editor.MoveCursor(-1)
		}
	case "form_right":
		if !editor.Cycle(1) {
			editor.MoveCursor(1)
		}
	case "form_backspace":
		editor.Backspace()
	case "form_cancel":
		editor.Cancel()
		m.Modal = nil
		m.Focus = FocusContent
		m.Notice = ""
		return m, nil, false
	case "form_save":
		var command *ipc.Command
		var notice string
		switch m.Modal.Kind {
		case ModalSubscription:
			command, notice = subscriptionFormIntent(m.Modal)
		case ModalResource, ModalFilter:
			command, notice = managedFormIntent(m.Modal)
		case ModalMonitorSetting:
			command, notice = monitorFormIntent(m.Modal)
		case ModalDNSResolver, ModalDNSRoute:
			command, notice = m.dnsFormIntent(m.Modal)
		default:
			notice = "This configuration form is unavailable."
		}
		if notice != "" {
			m.Notice = notice
			return m, nil, false
		}
		m.Modal = nil
		m.Focus = FocusContent
		m.Notice = ""
		return m, command, false
	default:
		if utf8.ValidString(key) && utf8.RuneCountInString(key) == 1 && unicode.IsPrint([]rune(key)[0]) {
			editor.Insert(key)
		}
	}
	m.Notice = ""
	return m, nil, false
}

func subscriptionFormIntent(modal *Modal) (*ipc.Command, string) {
	patch := &ipc.SubscriptionEdit{}
	id := modal.TargetID
	creating := id == ""
	for _, change := range modal.Form.Changes() {
		value := strings.TrimSpace(change.Value)
		switch change.ID {
		case "id":
			id = value
		case "name":
			if value == "-" && !creating {
				value = ""
			}
			if len(value) > 128 {
				return nil, "Display name must be at most 128 bytes."
			}
			patch.Name = &value
		case "url":
			if value == "" || value == "-" {
				return nil, "A source URL is required."
			}
			patch.URL = &value
		case "user_agent":
			if value == "-" && !creating {
				value = ""
			}
			if len(value) > 256 {
				return nil, "User-Agent must be printable ASCII at most 256 bytes."
			}
			for _, r := range value {
				if r < 0x20 || r > 0x7e {
					return nil, "User-Agent must be printable ASCII at most 256 bytes."
				}
			}
			patch.UserAgent = &value
		case "enabled":
			v := value == "true"
			patch.Enabled = &v
		case "refresh_interval":
			v, err := strconv.ParseUint(value, 10, 32)
			if err != nil || v < 60 || v > 86400*30 {
				return nil, "Refresh interval must be between 60 and 2592000 seconds."
			}
			n := uint32(v)
			patch.RefreshIntervalSeconds = &n
		case "timeout":
			v, err := strconv.ParseUint(value, 10, 32)
			if err != nil || v < 1 || v > 300 {
				return nil, "Timeout must be between 1 and 300 seconds."
			}
			n := uint32(v)
			patch.TimeoutSeconds = &n
		case "route":
			if value != "direct" && value != "system_proxy" && value != "mihomo_proxy" {
				return nil, "Choose direct, system_proxy, or mihomo_proxy."
			}
			patch.Route = &value
		case "allow_http":
			v := value == "true"
			patch.AllowHTTP = &v
		case "allow_invalid_tls":
			v := value == "true"
			patch.AllowInvalidTLS = &v
		}
	}
	if creating {
		if !validSubscriptionID(id) {
			return nil, "A stable subscription ID is required."
		}
		if patch.URL == nil {
			return nil, "A source URL is required."
		}
		if patch.Enabled == nil {
			enabled := true
			patch.Enabled = &enabled
		}
	}
	if patch.Name == nil && patch.URL == nil && patch.UserAgent == nil && patch.Enabled == nil && patch.RefreshIntervalSeconds == nil && patch.TimeoutSeconds == nil && patch.Route == nil && patch.AllowHTTP == nil && patch.AllowInvalidTLS == nil {
		return nil, ""
	}
	return &ipc.Command{Kind: ipc.CommandPutSubscription, SubscriptionID: id, Subscription: patch}, ""
}

func validSubscriptionNumber(value string, min, max uint64) bool {
	parsed, err := strconv.ParseUint(value, 10, 32)
	return err == nil && parsed >= min && parsed <= max
}

func validYesNo(value string) bool {
	switch strings.ToLower(value) {
	case "yes", "y", "true", "no", "n", "false":
		return true
	default:
		return false
	}
}

func validSubscriptionID(value string) bool {
	if len(value) == 0 || len(value) > 64 || !asciiAlphaNumeric(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !asciiAlphaNumeric(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func asciiAlphaNumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func (m Model) openModal(kind ModalKind) Model {
	m.Modal = &Modal{Kind: kind}
	m.Focus = FocusModal
	m.Notice = "Enter " + modalPrompt(kind) + "."
	return m
}

func modalPrompt(kind ModalKind) string {
	switch kind {
	case ModalBinary:
		return "system, bundled, or an absolute executable path"
	case ModalDeleteSubscription:
		return "confirm subscription deletion"
	case ModalDeleteDNS:
		return "confirm DNS policy removal"
	case ModalDNSListen:
		return "DNS listener address"
	default:
		return "Unsupported dialog"
	}
}

func (m Model) selectTab(tab Tab) Model {
	m.Tab = tab
	m.Focus = FocusContent
	m.Selection = cloneSelection(m.Selection)
	if m.Selection[tab] == "" {
		rows := rowsForSnapshot(m.snapshot, tab)
		if len(rows) != 0 {
			m.Selection[tab] = rows[0].ID
		}
	}
	return m
}

func (m Model) moveSelection(delta int) Model {
	rows := m.Rows()
	if len(rows) == 0 {
		return m
	}
	index := rowIndex(rows, m.Selection[m.Tab])
	if index < 0 {
		index = 0
	}
	index += delta
	if index < 0 {
		index = 0
	}
	if index >= len(rows) {
		index = len(rows) - 1
	}
	m.Selection = cloneSelection(m.Selection)
	m.Selection[m.Tab] = rows[index].ID
	m.Focus = FocusContent
	return m
}

func (m Model) activate() (Model, *ipc.Command, bool) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil, false
	}
	switch row.kind {
	case rowGroup:
		m.groupID = row.groupID
		m.Notice = "Focused group " + row.groupID + "."
	case rowProxy:
		if !m.managedSelector(row.groupID) {
			m.Notice = "Mihomo selects members of this group."
			return m, nil, false
		}
		return m, &ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: row.groupID, ChoiceID: row.choiceID}, false
	case rowSettingMonitor:
		return m.openMonitorModal(row.monitor), nil, false
	case rowSettingDNSListen:
		return m.openDNSListenModal(), nil, false
	case rowDNSResolver:
		return m.openDNSResolverModal(true, row.dnsID), nil, false
	case rowDNSRoute:
		return m.openDNSRouteModal(true, row.dnsID), nil, false
	case rowSettingBinary:
		return m.openModal(ModalBinary), nil, false
	}
	return m, nil, false
}

func (m Model) selectedRow() (Row, bool) {
	selected := m.Selection[m.Tab]
	for _, row := range m.Rows() {
		if row.ID == selected {
			return row, true
		}
	}
	return Row{}, false
}

func (m Model) focusedGroupID() string {
	if row, ok := m.selectedRow(); ok && row.groupID != "" {
		return row.groupID
	}
	return m.groupID
}

func (m Model) managedSelector(groupID string) bool {
	for _, group := range m.snapshot.Snapshot.Groups {
		if group.ID == groupID {
			return group.Type == "Selector" || group.Type == "select"
		}
	}
	return false
}

func command(kind ipc.CommandKind) *ipc.Command { return &ipc.Command{Kind: kind} }

func configCommand(patch *ipc.ConfigPatch) *ipc.Command {
	return &ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: patch}
}

func cloneSelection(selection map[Tab]string) map[Tab]string {
	out := make(map[Tab]string, len(selection)+1)
	for tab, id := range selection {
		out[tab] = id
	}
	return out
}

func rowsForSnapshot(event ipc.Event, tab Tab) []Row {
	snapshot := event.Snapshot
	switch tab {
	case TabOverview:
		rows := []Row{{ID: "binary", Title: "Mihomo binary", Detail: binaryDetail(snapshot.Binary.Desired, snapshot.Binary.ObservedVersion, snapshot.Binary.LastCompatibilityFailure, snapshot.Binary.Capabilities)}}
		if len(snapshot.Switches) > 0 {
			switchEvent := snapshot.Switches[len(snapshot.Switches)-1]
			measurements := make([]string, 0, len(switchEvent.Evidence))
			for _, sample := range switchEvent.Evidence {
				measurements = append(measurements, fmt.Sprintf("%s %s %dms", shortProxyID(sample.ProxyID), sample.Outcome, sample.LatencyMillis))
			}
			rows = append(rows, Row{ID: "switch:last", Title: "Last automatic switch", Detail: fmt.Sprintf("%s -> %s: %s [%s]", shortProxyID(switchEvent.OldID), shortProxyID(switchEvent.NewID), switchEvent.Reason, strings.Join(measurements, "; "))})
		}
		for _, job := range snapshot.Jobs {
			id := job.ID
			if id == "" {
				id = job.Kind + ":" + job.State
			}
			rows = append(rows, Row{ID: "job:" + id, Title: fallback(job.Kind, "Operation"), Detail: fallback(job.State, "pending")})
		}
		for _, issue := range snapshot.Errors {
			id := strings.Join([]string{issue.Kind, issue.SourceID, issue.File, issue.Key}, "\x00")
			title := fallback(issue.Key, issue.File)
			if issue.SourceID != "" {
				title += " [" + issue.SourceID + "]"
			}
			rows = append(rows, Row{ID: "error:" + id, Title: title, Detail: issue.Message})
		}
		return rows
	case TabProxies:
		rows := make([]Row, 0)
		proxyInfo := make(map[string]struct{ label, detail, outcome, latency string }, len(snapshot.Proxies))
		for _, proxy := range snapshot.Proxies {
			key := proxy.GroupID + "\x00" + proxy.ID
			info := struct{ label, detail, outcome, latency string }{
				label: proxy.Label, detail: proxyDetail(proxy.LatencyMillis, proxy.Outcome), outcome: proxy.Outcome,
			}
			if proxy.LatencyMillis > 0 && proxy.Outcome == "success" {
				info.latency = fmt.Sprintf("%d ms", proxy.LatencyMillis)
			}
			proxyInfo[key] = info
		}
		for _, group := range snapshot.Groups {
			groupName := fallback(group.Label, group.ID)
			automation := enabledLabel(group.AutomationEnabled)
			detail := group.Type
			if group.Selected != "" {
				selectedLabel := fallback(proxyInfo[group.ID+"\x00"+group.Selected].label, group.Selected)
				detail = appendDetail(detail, "selected "+selectedLabel)
			}
			if group.AutomationEnabled {
				detail = appendDetail(detail, "automation on")
			} else {
				detail = appendDetail(detail, "automation off")
			}
			rows = append(rows, Row{ID: "group:" + group.ID, Title: groupName, Detail: detail,
				Cells: []string{groupName, groupName, fallback(proxyInfo[group.ID+"\x00"+group.Selected].label, group.Selected), "", "", automation},
				kind:  rowGroup, groupID: group.ID})
			for _, proxyID := range group.Proxies {
				proxyKey := group.ID + "\x00" + proxyID
				info := proxyInfo[proxyKey]
				proxyDetailText := info.detail
				if proxyID == group.Selected {
					proxyDetailText = appendDetail("active", proxyDetailText)
				}
				proxyTitle := fallback(info.label, proxyID)
				selected := ""
				if proxyID == group.Selected {
					selected = "yes"
				}
				rows = append(rows, Row{ID: "proxy:" + group.ID + ":" + proxyID, Title: "  " + proxyTitle, Detail: proxyDetailText,
					Cells: []string{groupName, proxyTitle, selected, info.latency, info.outcome, automation},
					kind:  rowProxy, groupID: group.ID, choiceID: proxyID})
			}
		}
		return rows
	case TabSubscriptions:
		rows := make([]Row, 0, len(snapshot.Subscriptions))
		for _, subscription := range snapshot.Subscriptions {
			state := enabledLabel(subscription.Enabled)
			if subscription.Active {
				state = "active"
			}
			checked, next, usage := "", "", ""
			detail := appendDetail(subscription.SourceHost, enabledLabel(subscription.Enabled))
			if subscription.Active {
				detail = appendDetail(detail, "active")
				if subscription.PendingActivation {
					detail = appendDetail(detail, "update ready")
				}
			}
			if subscription.LastCheck > 0 {
				checked = timeLabel(subscription.LastCheck)
				detail = appendDetail(detail, "checked "+checked)
			}
			if subscription.LastSuccess > 0 {
				detail = appendDetail(detail, "success "+timeLabel(subscription.LastSuccess))
			}
			if subscription.NextDue > 0 {
				next = timeLabel(subscription.NextDue)
				detail = appendDetail(detail, "next "+next)
			}
			if subscription.HashPrefix != "" {
				detail = appendDetail(detail, "hash "+subscription.HashPrefix)
			}
			if subscription.AppliedHashPrefix != "" {
				detail = appendDetail(detail, "applied "+subscription.AppliedHashPrefix)
			}
			if subscription.LastFailure != "" {
				detail = appendDetail(detail, "needs attention")
			}
			if subscription.Usage != nil {
				usage = fmt.Sprintf("%d up %d down", subscription.Usage.UploadedBytes, subscription.Usage.DownloadedBytes)
				detail = appendDetail(detail, fmt.Sprintf("usage up %d down %d total %d bytes", subscription.Usage.UploadedBytes, subscription.Usage.DownloadedBytes, subscription.Usage.TotalBytes))
				if subscription.Usage.ExpiresAt > 0 {
					detail = appendDetail(detail, "expires "+timeLabel(subscription.Usage.ExpiresAt))
				}
			}
			rows = append(rows, Row{ID: "subscription:" + subscription.ID, Title: fallback(subscription.Name, subscription.ID), Detail: detail,
				Cells: []string{fallback(subscription.Name, subscription.ID), subscription.SourceHost, state, checked, next, usage},
				kind:  rowSubscription, subscriptionID: subscription.ID})
		}
		return rows
	case TabFilters:
		rows := make([]Row, 0, len(snapshot.Filters))
		for _, filter := range snapshot.Filters {
			validated, next := yesNo(filter.Validated), ""
			detail := appendDetail("resource "+filter.ResourceID, "format "+filter.Format)
			detail = appendDetail(detail, "target "+filter.Target)
			detail = appendDetail(detail, appendDetail(filter.SourceHost, enabledLabel(filter.Enabled)))
			if filter.Validated {
				detail = appendDetail(detail, "validated")
			}
			if filter.HashPrefix != "" {
				detail = appendDetail(detail, "hash "+filter.HashPrefix)
			}
			if filter.Destination != "" {
				detail = appendDetail(detail, "provider "+filter.Destination)
			}
			if filter.LastSuccess > 0 {
				detail = appendDetail(detail, "success "+timeLabel(filter.LastSuccess))
			}
			if filter.NextDue > 0 {
				next = timeLabel(filter.NextDue)
				detail = appendDetail(detail, "next "+next)
			}
			if filter.LastFailure != "" {
				detail = appendDetail(detail, "needs attention")
			}
			rows = append(rows, Row{ID: "filter:" + filter.ID, Title: filter.ID, Detail: detail,
				Cells: []string{filter.ID, filter.Format, filter.Target, filter.SourceHost, enabledLabel(filter.Enabled), validated, next},
				kind:  rowFilter, filterID: filter.ID})
		}
		return rows
	case TabResources:
		rows := make([]Row, 0, len(snapshot.Resources))
		for _, resource := range snapshot.Resources {
			validated, next := yesNo(resource.Validated), ""
			if resource.NextDue > 0 {
				next = timeLabel(resource.NextDue)
			}
			rows = append(rows, Row{ID: "resource:" + resource.ID, Title: resource.ID,
				Cells: []string{resource.ID, resource.Kind, resource.Format, resource.SourceHost, enabledLabel(resource.Enabled), validated, next},
				kind:  rowResource, resourceID: resource.ID})
		}
		return rows
	case TabSettings:
		monitor := snapshot.Monitor
		binaryDetail := fmt.Sprintf("Desired %s; observed %s; capabilities %s",
			fallback(snapshot.Binary.Desired, "unspecified"), fallback(snapshot.Binary.ObservedVersion, "unknown"),
			fallback(strings.Join(snapshot.Binary.Capabilities, ", "), "none verified"))
		if snapshot.Binary.LastCompatibilityFailure != "" {
			binaryDetail += "; compatibility issue reported"
		}
		testURLValue, testURLDetail := "not configured", "No test URL is configured."
		if monitor.TestURL != "" {
			testURLValue, testURLDetail = "configured", "Test URL configured."
		}
		if strings.HasPrefix(strings.ToLower(monitor.TestURL), "http://") {
			testURLDetail += " Plain HTTP can be intercepted."
		}
		proxyAction := "system_proxy_enable"
		if snapshot.SystemProxy.Enabled {
			proxyAction = "system_proxy_disable"
		}
		monitorAction := "monitor_enable"
		if monitor.Enabled {
			monitorAction = "monitor_disable"
		}
		rows := []Row{
			{ID: "setting:binary", Title: "Mihomo binary", Detail: binaryDetail, Cells: []string{"Mihomo binary", fallback(snapshot.Binary.Desired, "unspecified"), ""}, kind: rowSettingBinary, action: "edit_binary"},
			{ID: "setting:system-proxy", Title: "System proxy", Detail: fmt.Sprintf("System proxy requested %t; currently active %t.", snapshot.SystemProxy.Enabled, snapshot.SystemProxy.Active), Cells: []string{"System proxy", fmt.Sprintf("requested %t; active %t", snapshot.SystemProxy.Enabled, snapshot.SystemProxy.Active), ""}, action: proxyAction},
			{ID: "setting:monitor", Title: "Monitor", Detail: enabledLabel(monitor.Enabled), Cells: []string{"Monitor", enabledLabel(monitor.Enabled), ""}, action: monitorAction},
		}
		for _, setting := range []struct {
			id, title, value, action string
			field                    monitorField
		}{
			{"setting:monitor:test-url", "Monitor test URL", testURLValue, "edit_monitor_url", monitorTestURL},
			{"setting:interval", "Monitor interval", strconv.FormatInt(monitor.IntervalSeconds, 10) + " seconds", "edit_monitor_interval", monitorInterval},
			{"setting:monitor:timeout", "Monitor timeout", strconv.FormatInt(monitor.TimeoutMillis, 10) + " ms", "edit_monitor_timeout", monitorTimeout},
			{"setting:monitor:concurrency", "Monitor concurrency", strconv.Itoa(monitor.Concurrency), "edit_monitor_concurrency", monitorConcurrency},
			{"setting:monitor:threshold", "Monitor threshold", strconv.FormatInt(monitor.ThresholdMillis, 10) + " ms", "edit_monitor_threshold", monitorThreshold},
			{"setting:alert-threshold", "Alert threshold", strconv.FormatInt(monitor.AlertThresholdMillis, 10) + " ms", "edit_alert_threshold", monitorAlertThreshold},
			{"setting:monitor:bad-samples", "Consecutive bad samples", strconv.Itoa(monitor.ConsecutiveBadSamples), "edit_monitor_bad_samples", monitorBadSamples},
			{"setting:monitor:improvement", "Minimum improvement", strconv.FormatInt(monitor.MinImprovementMillis, 10) + " ms", "edit_monitor_improvement", monitorImprovement},
			{"setting:monitor:cooldown", "Monitor cooldown", strconv.FormatInt(monitor.CooldownSeconds, 10) + " seconds", "edit_monitor_cooldown", monitorCooldown},
			{"setting:monitor:jitter", "Monitor jitter", strconv.FormatInt(monitor.JitterMillis, 10) + " ms", "edit_monitor_jitter", monitorJitter},
		} {
			detail := setting.value
			if setting.field == monitorTestURL {
				detail = testURLDetail
			} else if setting.field == monitorAlertThreshold {
				detail += "; high alert above threshold"
			}
			rows = append(rows, Row{ID: setting.id, Title: setting.title, Detail: detail,
				Cells: []string{setting.title, setting.value, ""}, kind: rowSettingMonitor, monitor: setting.field, action: setting.action})
		}
		rows = append(rows, Row{ID: "setting:dns-listen", Title: "DNS listener", Detail: snapshot.DNS.Listen,
			Cells: []string{"DNS listener", snapshot.DNS.Listen, ""}, kind: rowSettingDNSListen, action: "edit_dns_listen"})
		for _, set := range snapshot.DNS.ResolverSets {
			value := fmt.Sprintf("DNSCrypt %s; %d endpoint(s)", yesNo(set.DNSCrypt), len(set.Endpoints))
			rows = append(rows, Row{ID: "dns:set:" + set.ID, Title: "DNS resolver set " + set.ID,
				Detail: value, Cells: []string{"DNS resolver set " + set.ID, value, ""}, kind: rowDNSResolver, dnsID: set.ID, action: "edit_dns_entry"})
		}
		for _, route := range snapshot.DNS.Routes {
			matcher, value := dnsRouteMatcherFields(route.Suffix, route.GeoSite, route.Resource)
			id := dnsRouteIdentity(route.Suffix, route.GeoSite, route.Resource)
			name := "DNS route " + matcher + " " + value
			resolver := "resolver " + route.ResolverSet
			rows = append(rows, Row{ID: id, Title: name, Detail: resolver,
				Cells: []string{name, resolver, ""}, kind: rowDNSRoute, dnsID: id, action: "edit_dns_entry"})
		}
		return rows
	default:
		return nil
	}
}

func timeLabel(unixSeconds int64) string {
	return time.Unix(unixSeconds, 0).UTC().Format("2006-01-02 15:04Z")
}

func shortProxyID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func binaryDetail(desired, observed, failure string, capabilities []string) string {
	detail := appendDetail("desired "+fallback(desired, "unspecified"), "observed "+fallback(observed, "unknown"))
	capabilityText := "none verified"
	if len(capabilities) > 0 {
		capabilityText = strings.Join(capabilities, ", ")
	}
	detail = appendDetail(detail, "capabilities "+capabilityText)
	return appendDetail(detail, failure)
}

func proxyDetail(latency int64, outcome string) string {
	if latency > 0 {
		return fmt.Sprintf("%s | %d ms", outcome, latency)
	}
	return outcome
}

func appendDetail(current, addition string) string {
	if addition == "" {
		return current
	}
	if current == "" {
		return addition
	}
	return current + " | " + addition
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func fallback(value, replacement string) string {
	if value != "" {
		return value
	}
	return replacement
}

func rowIndex(rows []Row, id string) int {
	for index, row := range rows {
		if row.ID == id {
			return index
		}
	}
	return -1
}

func hasGroup(event ipc.Event, groupID string) bool {
	if groupID == "" {
		return false
	}
	for _, group := range event.Snapshot.Groups {
		if group.ID == groupID {
			return true
		}
	}
	return false
}

func groupAutomation(event ipc.Event, groupID string) (bool, bool) {
	for _, group := range event.Snapshot.Groups {
		if group.ID == groupID {
			return group.AutomationEnabled, true
		}
	}
	return false, false
}

func firstGroupID(event ipc.Event) string {
	if len(event.Snapshot.Groups) > 0 {
		return event.Snapshot.Groups[0].ID
	}
	return ""
}

func knownTab(tab Tab) bool {
	for _, candidate := range tabs {
		if tab == candidate {
			return true
		}
	}
	return false
}

func nextTab(tab Tab, delta int) Tab {
	index := 0
	for i, candidate := range tabs {
		if candidate == tab {
			index = i
			break
		}
	}
	index = (index + delta + len(tabs)) % len(tabs)
	return tabs[index]
}
