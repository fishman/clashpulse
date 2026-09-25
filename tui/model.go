package tui

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fishman/clashpulse/ipc"
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
	ModalMonitorInterval    ModalKind = "monitor_interval"
	ModalAlertThreshold     ModalKind = "alert_threshold"
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

const (
	subscriptionFieldID = iota
	subscriptionFieldName
	subscriptionFieldURL
	subscriptionFieldUserAgent
	subscriptionFieldEnabled
	subscriptionFieldRefreshInterval
	subscriptionFieldTimeout
	subscriptionFieldRoute
	subscriptionFieldAllowHTTP
	subscriptionFieldAllowInvalidTLS
	subscriptionFieldCount
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

type dnsForm struct {
	edit     bool
	targetID string
	step     int
	values   [3]string
}

const (
	dnsSetID = iota
	dnsSetEndpoints
	dnsSetDNSCrypt
)

const (
	dnsRouteMatcher = iota
	dnsRouteValue
	dnsRouteResolver
)

type subscriptionForm struct {
	edit     bool
	targetID string
	step     int
	values   [subscriptionFieldCount]string
}

type managedForm struct {
	edit     bool
	targetID string
	step     int
	values   [resourceFieldCount]string
}

type Modal struct {
	Kind         ModalKind
	Input        string
	TargetID     string
	monitor      monitorField
	dns          dnsForm
	subscription subscriptionForm
	managed      managedForm
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
	Tab       Tab
	Focus     Focus
	Selection map[Tab]string
	Modal     *Modal
	Pending   int
	Notice    string

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
	}
	return rows
}

func (m Model) CurrentGroupID() string { return m.groupID }

func (m Model) Help() []string { return m.keymap.Help(m.Tab) }

// HandleKey applies one normalized key name and optionally returns one IPC
// intent. It performs no I/O.
func (m Model) HandleKey(key string) (Model, *ipc.Command, bool) {
	if m.Modal != nil {
		return m.handleModalKey(key)
	}
	key = normalizeKey(key)
	action, ok := m.keymap.Action(m.Tab, key)
	if !ok {
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
	if key == "esc" {
		m.Modal = nil
		m.Focus = FocusContent
		m.Notice = ""
		return m, nil, false
	}
	if m.Modal.Kind == ModalMonitorSetting || m.Modal.Kind == ModalMonitorInterval || m.Modal.Kind == ModalAlertThreshold {
		return m.handleMonitorSettingKey(key)
	}
	if m.Modal.Kind == ModalDNSListen {
		return m.handleDNSListenKey(key)
	}
	if m.Modal.Kind == ModalDNSResolver || m.Modal.Kind == ModalDNSRoute {
		return m.handleDNSFormKey(key)
	}
	if m.Modal.Kind == ModalSubscription {
		return m.handleSubscriptionKey(key)
	}
	if m.Modal.Kind == ModalResource || m.Modal.Kind == ModalFilter {
		return m.handleManagedKey(key)
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

func (m Model) openMonitorModal(field monitorField) Model {
	kind := ModalMonitorSetting
	if field == monitorInterval {
		kind = ModalMonitorInterval
	} else if field == monitorAlertThreshold {
		kind = ModalAlertThreshold
	}
	m.Modal = &Modal{Kind: kind, monitor: field}
	m.Focus = FocusModal
	m.Notice = "Enter " + monitorPrompt(field) + "."
	return m
}

func monitorPrompt(field monitorField) string {
	switch field {
	case monitorTestURL:
		return "monitor test URL"
	case monitorInterval:
		return "monitor interval in seconds (1-86400)"
	case monitorTimeout:
		return "monitor timeout in milliseconds (1-86400000)"
	case monitorConcurrency:
		return "monitor concurrency (1-64)"
	case monitorThreshold:
		return "monitor latency threshold in milliseconds"
	case monitorAlertThreshold:
		return "alert threshold in milliseconds (1-60000)"
	case monitorBadSamples:
		return "consecutive bad samples (1-5)"
	case monitorImprovement:
		return "minimum improvement in milliseconds"
	case monitorCooldown:
		return "monitor cooldown in seconds (0 or more)"
	case monitorJitter:
		return "monitor jitter in milliseconds (0 or more)"
	default:
		return "monitor setting"
	}
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
func (m Model) handleMonitorSettingKey(key string) (Model, *ipc.Command, bool) {
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
		patch, notice := monitorSettingPatch(modal.monitor, modal.Input)
		if notice != "" {
			m.Notice = notice
			return m, nil, false
		}
		m.Modal = nil
		m.Focus = FocusContent
		return m, configCommand(patch), false
	}
	if modal.monitor == monitorTestURL {
		if utf8.ValidString(key) && utf8.RuneCountInString(key) == 1 && unicode.IsPrint([]rune(key)[0]) && len(modal.Input)+len(key) <= 2048 {
			modal.Input += key
			m.Modal = &modal
		}
	} else if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && len(modal.Input) < 10 {
		modal.Input += key
		m.Modal = &modal
	}
	return m, nil, false
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
	form := dnsForm{edit: edit, targetID: targetID}
	form.values[dnsSetDNSCrypt] = "no"
	if edit {
		found := false
		for _, set := range m.snapshot.Snapshot.DNS.ResolverSets {
			if set.ID == targetID {
				form.values[dnsSetID] = set.ID
				form.values[dnsSetEndpoints] = strings.Join(set.Endpoints, ", ")
				form.values[dnsSetDNSCrypt] = yesNo(set.DNSCrypt)
				found = true
				break
			}
		}
		if !found {
			m.Notice = "DNS resolver set is no longer available."
			return m
		}
	}
	m.Modal = &Modal{Kind: ModalDNSResolver, TargetID: targetID, dns: form, Input: form.values[0]}
	m.Focus = FocusModal
	m.Notice = "Resolver endpoints are comma-separated; credentials are not allowed."
	return m
}

func (m Model) openDNSRouteModal(edit bool, targetID string) Model {
	if !edit && len(m.snapshot.Snapshot.DNS.Routes) >= 1024 {
		m.Notice = "DNS policy already has 1024 routes."
		return m
	}
	form := dnsForm{edit: edit, targetID: targetID}
	form.values[dnsRouteMatcher] = "suffix"
	if edit {
		found := false
		for _, route := range m.snapshot.Snapshot.DNS.Routes {
			if dnsRouteIdentity(route.Suffix, route.GeoSite, route.Resource) == targetID {
				form.values[dnsRouteMatcher], form.values[dnsRouteValue] = dnsRouteMatcherFields(route.Suffix, route.GeoSite, route.Resource)
				form.values[dnsRouteResolver] = route.ResolverSet
				found = true
				break
			}
		}
		if !found {
			m.Notice = "DNS route is no longer available."
			return m
		}
	}
	m.Modal = &Modal{Kind: ModalDNSRoute, TargetID: targetID, dns: form, Input: form.values[0]}
	m.Focus = FocusModal
	m.Notice = "Choose exactly one suffix, GeoSite, or resource matcher."
	return m
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

func (m Model) handleDNSFormKey(key string) (Model, *ipc.Command, bool) {
	modal := *m.Modal
	form := modal.dns
	last := dnsSetDNSCrypt
	if modal.Kind == ModalDNSRoute {
		last = dnsRouteResolver
	}
	switch key {
	case "backspace":
		input := []rune(modal.Input)
		if len(input) > 0 {
			modal.Input = string(input[:len(input)-1])
			m.Modal = &modal
		}
		return m, nil, false
	case "shift+tab":
		if form.step > 0 {
			form.values[form.step] = strings.TrimSpace(modal.Input)
			form.step--
			modal.dns, modal.Input = form, form.values[form.step]
			m.Modal = &modal
		}
		return m, nil, false
	case "enter":
		form.values[form.step] = strings.TrimSpace(modal.Input)
		if notice := validateDNSFormField(modal.Kind, form, form.step); notice != "" {
			m.Notice = notice
			modal.dns = form
			m.Modal = &modal
			return m, nil, false
		}
		if form.step < last {
			form.step++
			modal.dns, modal.Input = form, form.values[form.step]
			m.Modal = &modal
			m.Notice = ""
			return m, nil, false
		}
		return m.finishDNSForm(modal.Kind, form)
	}
	limit := dnsFormFieldLimit(modal.Kind, form)
	if utf8.ValidString(key) && utf8.RuneCountInString(key) == 1 && unicode.IsPrint([]rune(key)[0]) && len(modal.Input)+len(key) <= limit {
		modal.Input += key
		m.Modal = &modal
	}
	return m, nil, false
}

func dnsFormFieldLimit(kind ModalKind, form dnsForm) int {
	if kind == ModalDNSResolver {
		if form.step == dnsSetEndpoints {
			return 16*256 + 15*2
		}
		return 64
	}
	if form.step == dnsRouteValue && form.values[dnsRouteMatcher] == "suffix" {
		return 253
	}
	return 64
}

func validateDNSFormField(kind ModalKind, form dnsForm, field int) string {
	value := form.values[field]
	if kind == ModalDNSResolver {
		switch field {
		case dnsSetID:
			if !validSubscriptionID(value) {
				return "Resolver ID must be a stable ID (max 64 characters)."
			}
		case dnsSetEndpoints:
			if _, err := parseDNSEndpoints(value); err != nil {
				return err.Error()
			}
		case dnsSetDNSCrypt:
			if !validYesNo(value) {
				return "DNSCrypt must be yes or no."
			}
		}
		return ""
	}
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

func (m Model) finishDNSForm(kind ModalKind, form dnsForm) (Model, *ipc.Command, bool) {
	sets := m.dnsResolverSets()
	routes := m.dnsRoutes()
	if !form.edit && ((kind == ModalDNSResolver && len(sets) >= 256) || (kind == ModalDNSRoute && len(routes) >= 1024)) {
		m.Notice = "DNS policy reached its command limit."
		return m, nil, false
	}
	if kind == ModalDNSResolver {
		endpoints, _ := parseDNSEndpoints(form.values[dnsSetEndpoints])
		set := ipc.DNSResolverSet{ID: form.values[dnsSetID], Endpoints: endpoints, DNSCrypt: strings.EqualFold(form.values[dnsSetDNSCrypt], "yes") || strings.EqualFold(form.values[dnsSetDNSCrypt], "y") || strings.EqualFold(form.values[dnsSetDNSCrypt], "true")}
		index := -1
		for i, existing := range sets {
			if form.edit && existing.ID == form.targetID {
				index = i
			}
			if existing.ID == set.ID && (!form.edit || existing.ID != form.targetID) {
				m.Notice = "Resolver ID already exists."
				return m, nil, false
			}
		}
		if form.edit {
			if index < 0 {
				m.Notice = "DNS resolver set is no longer available."
				return m, nil, false
			}
			sets[index] = set
			if set.ID != form.targetID {
				for i := range routes {
					if routes[i].ResolverSet == form.targetID {
						routes[i].ResolverSet = set.ID
					}
				}
			}
		} else {
			sets = append(sets, set)
		}
	} else {
		route := routeFromDNSForm(form)
		index := -1
		for i, existing := range routes {
			if form.edit && dnsRouteID(existing) == form.targetID {
				index = i
			}
			if dnsRouteID(existing) == dnsRouteID(route) && (!form.edit || dnsRouteID(existing) != form.targetID) {
				m.Notice = "DNS route matcher already exists."
				return m, nil, false
			}
		}
		if form.edit {
			if index < 0 {
				m.Notice = "DNS route is no longer available."
				return m, nil, false
			}
			routes[index] = route
		} else {
			routes = append(routes, route)
		}
	}
	m.Modal = nil
	m.Focus = FocusContent
	return m, dnsRoutingCommand(sets, routes), false
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
	form := subscriptionForm{edit: edit, targetID: targetID}
	if edit {
		form.step = subscriptionFieldName
	}
	m.Modal = &Modal{Kind: ModalSubscription, TargetID: targetID, subscription: form}
	m.Focus = FocusModal
	if edit {
		m.Notice = "Blank fields keep existing values; use - to clear the display name."
	} else {
		m.Notice = "Enter a stable ID and source URL; HTTP and invalid TLS are disabled by default."
	}
	return m
}

func (m Model) handleSubscriptionKey(key string) (Model, *ipc.Command, bool) {
	modal := *m.Modal
	form := modal.subscription
	switch normalizeKey(key) {
	case "esc":
		m.Modal = nil
		m.Focus = FocusContent
		m.Notice = ""
		return m, nil, false
	case "backspace":
		input := []rune(modal.Input)
		if len(input) > 0 {
			modal.Input = string(input[:len(input)-1])
			m.Modal = &modal
		}
		return m, nil, false
	case "shift+tab":
		firstEditable := subscriptionFieldID
		if form.edit {
			firstEditable = subscriptionFieldName
		}
		if form.step > firstEditable {
			form.values[form.step] = strings.TrimSpace(modal.Input)
			form.step--
			modal.Input = form.values[form.step]
			modal.subscription = form
			m.Modal = &modal
			m.Notice = ""
		}
		return m, nil, false
	case "enter":
		form.values[form.step] = strings.TrimSpace(modal.Input)
		if form.step == subscriptionFieldID && !form.edit {
			form.targetID = form.values[subscriptionFieldID]
		}
		if notice := validateSubscriptionField(form, form.step); notice != "" {
			m.Notice = notice
			modal.subscription = form
			m.Modal = &modal
			return m, nil, false
		}
		if form.step+1 < subscriptionFieldCount {
			form.step++
			modal.subscription = form
			modal.Input = form.values[form.step]
			m.Modal = &modal
			m.Notice = ""
			return m, nil, false
		}
		intent, notice := subscriptionIntent(form)
		if notice != "" {
			m.Notice = notice
			modal.subscription = form
			m.Modal = &modal
			return m, nil, false
		}
		m.Modal = nil
		m.Focus = FocusContent
		m.Notice = ""
		return m, intent, false
	}
	if utf8.ValidString(key) && utf8.RuneCountInString(key) == 1 && unicode.IsPrint([]rune(key)[0]) && len(modal.Input)+len(key) <= subscriptionFieldMaxBytes(form.step) {
		modal.Input += key
		m.Modal = &modal
	}
	return m, nil, false
}

func subscriptionFieldMaxBytes(field int) int {
	switch field {
	case subscriptionFieldID:
		return 64
	case subscriptionFieldName:
		return 128
	case subscriptionFieldURL:
		return 4096
	case subscriptionFieldUserAgent:
		return 256
	default:
		return 64
	}
}

func validateSubscriptionField(form subscriptionForm, field int) string {
	value := form.values[field]
	switch field {
	case subscriptionFieldID:
		if !form.edit && !validSubscriptionID(value) {
			return "ID must start with a letter or digit and contain only letters, digits, dot, underscore, or hyphen (max 64)."
		}
	case subscriptionFieldName:
		if len(value) > 128 {
			return "Display name must be at most 128 bytes."
		}
	case subscriptionFieldURL:
		if !form.edit && value == "" {
			return "A source URL is required for a new subscription."
		}
		if len(value) > 4096 {
			return "Source URL must be at most 4096 bytes."
		}
	case subscriptionFieldUserAgent:
		if value == "-" && form.edit {
			break
		}
		if len(value) > 256 {
			return "User-Agent must be printable ASCII at most 256 bytes."
		}
		for _, r := range value {
			if r < 0x20 || r > 0x7e {
				return "User-Agent must be printable ASCII at most 256 bytes."
			}
		}
	case subscriptionFieldEnabled, subscriptionFieldAllowHTTP, subscriptionFieldAllowInvalidTLS:
		if value != "" && !validYesNo(value) {
			return "Enter yes or no, or leave blank to keep the current value."
		}
	case subscriptionFieldRefreshInterval:
		if value != "" && !validSubscriptionNumber(value, 60, 86400*30) {
			return "Refresh interval must be between 60 and 2592000 seconds."
		}
	case subscriptionFieldTimeout:
		if value != "" && !validSubscriptionNumber(value, 1, 300) {
			return "Timeout must be between 1 and 300 seconds."
		}
	case subscriptionFieldRoute:
		if value != "" && value != "direct" && value != "system_proxy" && value != "mihomo_proxy" {
			return "Route must be direct, system_proxy, or mihomo_proxy."
		}
	}
	return ""
}

func subscriptionIntent(form subscriptionForm) (*ipc.Command, string) {
	patch := &ipc.SubscriptionEdit{}
	if name := form.values[subscriptionFieldName]; name != "" {
		if form.edit && name == "-" {
			name = ""
		}
		patch.Name = &name
	}
	if source := form.values[subscriptionFieldURL]; source != "" {
		patch.URL = &source
	} else if !form.edit {
		return nil, "A source URL is required for a new subscription."
	}
	if agent := form.values[subscriptionFieldUserAgent]; agent != "" {
		if agent == "-" && form.edit {
			agent = ""
		}
		patch.UserAgent = &agent
	}
	setBool := func(field int, target **bool, createDefault bool) {
		value := strings.ToLower(form.values[field])
		if value == "" {
			if createDefault {
				parsed := false
				*target = &parsed
			}
			return
		}
		parsed := value == "yes" || value == "y" || value == "true"
		*target = &parsed
	}
	if form.values[subscriptionFieldEnabled] == "" && !form.edit {
		enabled := true
		patch.Enabled = &enabled
	} else {
		setBool(subscriptionFieldEnabled, &patch.Enabled, false)
	}
	for _, field := range []struct {
		index  int
		target **bool
	}{{subscriptionFieldAllowHTTP, &patch.AllowHTTP}, {subscriptionFieldAllowInvalidTLS, &patch.AllowInvalidTLS}} {
		setBool(field.index, field.target, !form.edit)
	}
	for _, field := range []struct {
		index  int
		target **uint32
	}{{subscriptionFieldRefreshInterval, &patch.RefreshIntervalSeconds}, {subscriptionFieldTimeout, &patch.TimeoutSeconds}} {
		if value := form.values[field.index]; value != "" {
			parsed, _ := strconv.ParseUint(value, 10, 32)
			result := uint32(parsed)
			*field.target = &result
		}
	}
	if route := form.values[subscriptionFieldRoute]; route != "" {
		patch.Route = &route
	}
	if patch.Name == nil && patch.URL == nil && patch.UserAgent == nil && patch.Enabled == nil && patch.RefreshIntervalSeconds == nil && patch.TimeoutSeconds == nil && patch.Route == nil && patch.AllowHTTP == nil && patch.AllowInvalidTLS == nil {
		return nil, "Enter at least one value to update."
	}
	return &ipc.Command{Kind: ipc.CommandPutSubscription, SubscriptionID: form.targetID, Subscription: patch}, ""
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

func subscriptionModalPrompt(modal *Modal) string {
	form := modal.subscription
	prefix := fmt.Sprintf("New subscription %d/%d", form.step+1, subscriptionFieldCount)
	if form.edit {
		prefix = fmt.Sprintf("Edit %s %d/%d", form.targetID, form.step, subscriptionFieldCount-1)
	}
	field := ""
	switch form.step {
	case subscriptionFieldID:
		field = "ID (required)"
	case subscriptionFieldName:
		if form.edit {
			field = "Name (blank keeps; - clears)"
		} else {
			field = "Name (optional; blank uses ID)"
		}
	case subscriptionFieldURL:
		if form.edit {
			field = "Source URL (blank keeps private URL)"
		} else {
			field = "Source URL (required)"
		}
	case subscriptionFieldUserAgent:
		if form.edit {
			field = "User-Agent (blank keeps; - resets to clash-pulse)"
		} else {
			field = "User-Agent (blank uses clash-pulse)"
		}
	case subscriptionFieldEnabled:
		if form.edit {
			field = "Enabled yes/no (blank keeps)"
		} else {
			field = "Enabled yes/no (blank enables)"
		}
	case subscriptionFieldRefreshInterval:
		if form.edit {
			field = "Refresh seconds 60-2592000 (blank keeps)"
		} else {
			field = "Refresh seconds 60-2592000 (blank 43200)"
		}
	case subscriptionFieldTimeout:
		if form.edit {
			field = "Timeout seconds 1-300 (blank keeps)"
		} else {
			field = "Timeout seconds 1-300 (blank 30)"
		}
	case subscriptionFieldRoute:
		if form.edit {
			field = "Route direct/system_proxy/mihomo_proxy (blank keeps)"
		} else {
			field = "Route direct/system_proxy/mihomo_proxy (blank direct)"
		}
	case subscriptionFieldAllowHTTP:
		if form.edit {
			field = "Allow HTTP yes/no (blank keeps; yes opts in)"
		} else {
			field = "Allow HTTP yes/no (blank no; yes opts in)"
		}
	case subscriptionFieldAllowInvalidTLS:
		if form.edit {
			field = "Allow invalid TLS yes/no (blank keeps; yes opts in)"
		} else {
			field = "Allow invalid TLS yes/no (blank no; yes opts in)"
		}
	}
	return prefix + " - " + field
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
	case ModalAlertThreshold:
		return "alert threshold in milliseconds (1-60000)"
	case ModalMonitorSetting:
		return "monitor setting"
	default:
		return "monitor interval in seconds (1-86400)"
	}
}

func dnsModalPrompt(modal *Modal) string {
	form := modal.dns
	prefix := "New DNS resolver"
	if modal.Kind == ModalDNSRoute {
		prefix = "New DNS route"
	}
	if form.edit {
		prefix = "Edit DNS entry"
	}
	field := ""
	if modal.Kind == ModalDNSResolver {
		switch form.step {
		case dnsSetID:
			field = "resolver ID"
		case dnsSetEndpoints:
			field = "comma-separated endpoints (no credentials)"
		case dnsSetDNSCrypt:
			field = "DNSCrypt yes/no"
		}
	} else {
		switch form.step {
		case dnsRouteMatcher:
			field = "matcher type: suffix/geosite/resource"
		case dnsRouteValue:
			field = "matcher value"
		case dnsRouteResolver:
			field = "resolver set ID"
		}
	}
	return fmt.Sprintf("%s %d/3 - %s", prefix, form.step+1, field)
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
			id := issue.File + ":" + issue.Key
			rows = append(rows, Row{ID: "error:" + id, Title: fallback(issue.Key, issue.File), Detail: issue.Message})
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
			detail := appendDetail(resource.Kind, "format "+resource.Format)
			detail = appendDetail(detail, "rule type "+resource.RuleType)
			detail = appendDetail(detail, resource.SourceHost)
			detail = appendDetail(detail, enabledLabel(resource.Enabled))
			if resource.Validated {
				detail = appendDetail(detail, "validated")
			} else {
				detail = appendDetail(detail, "not validated")
			}
			if resource.HashPrefix != "" {
				detail = appendDetail(detail, "hash "+resource.HashPrefix)
			}
			if resource.Destination != "" {
				detail = appendDetail(detail, "destination "+resource.Destination)
			}
			if resource.LastCheck > 0 {
				detail = appendDetail(detail, "checked "+timeLabel(resource.LastCheck))
			}
			if resource.LastSuccess > 0 {
				detail = appendDetail(detail, "success "+timeLabel(resource.LastSuccess))
			}
			if resource.NextDue > 0 {
				next = timeLabel(resource.NextDue)
				detail = appendDetail(detail, "next "+next)
			}
			if resource.LastResult != "" {
				detail = appendDetail(detail, "needs attention")
			}
			rows = append(rows, Row{ID: "resource:" + resource.ID, Title: resource.ID, Detail: detail,
				Cells: []string{resource.ID, resource.Kind, resource.Format, resource.SourceHost, enabledLabel(resource.Enabled), validated, next},
				kind:  rowResource, resourceID: resource.ID})
		}
		return rows
	case TabSettings:
		monitor := snapshot.Monitor
		testURL := monitor.TestURL
		if len(testURL) >= 7 && strings.EqualFold(testURL[:7], "http://") {
			testURL += " | plain HTTP can be intercepted"
		}
		rows := []Row{
			{ID: "setting:binary", Title: "Mihomo binary", Detail: binaryDetail(snapshot.Binary.Desired, snapshot.Binary.ObservedVersion, snapshot.Binary.LastCompatibilityFailure, snapshot.Binary.Capabilities) + " | press Enter or b to change", kind: rowSettingBinary},
			{ID: "setting:system-proxy", Title: "System proxy", Detail: fmt.Sprintf("requested %t | active %t | e enable, d disable", snapshot.SystemProxy.Enabled, snapshot.SystemProxy.Active)},
			{ID: "setting:monitor", Title: "Monitor", Detail: enabledLabel(monitor.Enabled)},
		}
		for _, setting := range []struct {
			id, title, value, key string
			field                 monitorField
		}{
			{"setting:monitor:test-url", "Monitor test URL", testURL, "u", monitorTestURL},
			{"setting:interval", "Monitor interval", strconv.FormatInt(monitor.IntervalSeconds, 10) + " seconds", "i", monitorInterval},
			{"setting:monitor:timeout", "Monitor timeout", strconv.FormatInt(monitor.TimeoutMillis, 10) + " ms", "o", monitorTimeout},
			{"setting:monitor:concurrency", "Monitor concurrency", strconv.Itoa(monitor.Concurrency), "c", monitorConcurrency},
			{"setting:monitor:threshold", "Monitor threshold", strconv.FormatInt(monitor.ThresholdMillis, 10) + " ms", "h", monitorThreshold},
			{"setting:alert-threshold", "Alert threshold", strconv.FormatInt(monitor.AlertThresholdMillis, 10) + " ms | high alert above threshold", "t", monitorAlertThreshold},
			{"setting:monitor:bad-samples", "Consecutive bad samples", strconv.Itoa(monitor.ConsecutiveBadSamples), "s", monitorBadSamples},
			{"setting:monitor:improvement", "Minimum improvement", strconv.FormatInt(monitor.MinImprovementMillis, 10) + " ms", "p", monitorImprovement},
			{"setting:monitor:cooldown", "Monitor cooldown", strconv.FormatInt(monitor.CooldownSeconds, 10) + " seconds", "z", monitorCooldown},
			{"setting:monitor:jitter", "Monitor jitter", strconv.FormatInt(monitor.JitterMillis, 10) + " ms", "w", monitorJitter},
		} {
			rows = append(rows, Row{ID: setting.id, Title: setting.title, Detail: setting.value + " | press Enter or " + setting.key + " to change", kind: rowSettingMonitor, monitor: setting.field})
		}
		rows = append(rows, Row{ID: "setting:dns-listen", Title: "DNS listener", Detail: snapshot.DNS.Listen + " | press Enter or l to change", kind: rowSettingDNSListen})
		for _, set := range snapshot.DNS.ResolverSets {
			rows = append(rows, Row{ID: "dns:set:" + set.ID, Title: "DNS resolver set " + set.ID, Detail: "DNSCrypt " + yesNo(set.DNSCrypt) + " | " + strings.Join(set.Endpoints, ", ") + " | press Enter or g to edit, x to remove", kind: rowDNSResolver, dnsID: set.ID})
		}
		for _, route := range snapshot.DNS.Routes {
			matcher, value := dnsRouteMatcherFields(route.Suffix, route.GeoSite, route.Resource)
			id := dnsRouteIdentity(route.Suffix, route.GeoSite, route.Resource)
			rows = append(rows, Row{ID: id, Title: "DNS route " + matcher + " " + value, Detail: "resolver " + route.ResolverSet + " | press Enter or g to edit, x to remove", kind: rowDNSRoute, dnsID: id})
		}
		for i := range rows {
			value, _, _ := strings.Cut(rows[i].Detail, " | ")
			rows[i].Cells = []string{rows[i].Title, value}
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
