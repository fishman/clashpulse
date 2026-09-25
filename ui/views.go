package ui

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

type sendIntent func(ipc.Command)

type proxyPage struct {
	view fyne.CanvasObject

	groups          []core.GroupSnapshot
	groupOptions    map[string]string
	selectedGroupID string
	choices         []string
	snapshot        core.Snapshot
	refreshing      bool

	groupSelect *widget.Select
	automation  *widget.Check
	probe       *widget.Button
	groupMode   *widget.Label
	list        *widget.List
	empty       *widget.Label
	send        sendIntent
	items       map[*fyne.Container]*proxyItem
}

type proxyItem struct {
	name    *widget.Label
	state   *widget.Label
	latency *widget.Label
}

func newProxyPage(send sendIntent) *proxyPage {
	p := &proxyPage{send: send, groupOptions: make(map[string]string), items: make(map[*fyne.Container]*proxyItem)}
	p.groupSelect = widget.NewSelect(nil, func(label string) {
		if p.refreshing {
			return
		}
		if groupID, ok := p.groupOptions[label]; ok {
			p.selectedGroupID = groupID
			p.updateSelectedGroup()
		}
	})
	p.automation = widget.NewCheck("Enable group automation", func(enabled bool) {
		if p.refreshing || p.selectedGroupID == "" || !p.managedSelector() {
			return
		}
		p.send(ipc.Command{Kind: ipc.CommandSetAutomation, GroupID: p.selectedGroupID, Automation: &ipc.AutomationSetting{Enabled: enabled}})
	})
	p.probe = widget.NewButton("Probe group", func() {
		if p.selectedGroupID != "" && p.managedSelector() {
			p.send(ipc.Command{Kind: ipc.CommandManualProbe, GroupID: p.selectedGroupID})
		}
	})
	p.groupMode = widget.NewLabel("")
	p.list = widget.NewList(
		func() int { return len(p.choices) },
		func() fyne.CanvasObject {
			item := &proxyItem{name: widget.NewLabel(""), state: widget.NewLabel(""), latency: widget.NewLabel("")}
			row := container.NewGridWithColumns(3, item.name, item.state, item.latency)
			p.items[row] = item
			return row
		},
		func(id widget.ListItemID, object fyne.CanvasObject) {
			item := p.items[object.(*fyne.Container)]
			if id < 0 || id >= len(p.choices) {
				item.name.SetText("")
				item.state.SetText("")
				item.latency.SetText("")
				return
			}
			choice := p.choices[id]
			selected := false
			for _, group := range p.groups {
				if group.ID == p.selectedGroupID {
					selected = choice == group.Selected
					break
				}
			}
			name := choice
			for _, proxy := range p.snapshot.Proxies {
				if proxy.GroupID == p.selectedGroupID && proxy.ID == choice && proxy.Label != "" {
					name = proxy.Label
					break
				}
			}
			item.name.SetText(name)
			if selected {
				item.state.SetText("Active")
			} else {
				item.state.SetText("")
			}
			item.latency.SetText(proxyLatency(p.snapshot, p.selectedGroupID, choice))
		},
	)
	p.list.OnSelected = func(id widget.ListItemID) {
		if id >= 0 && id < len(p.choices) && p.selectedGroupID != "" && p.managedSelector() {
			p.send(ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: p.selectedGroupID, ChoiceID: p.choices[id]})
		}
	}
	p.empty = widget.NewLabel("No proxy groups are available")
	header := container.NewVBox(
		widget.NewLabelWithStyle("Proxy groups and choices", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		p.groupSelect,
		p.groupMode,
		container.NewHBox(p.automation, p.probe),
	)
	p.view = container.NewBorder(header, nil, nil, nil, container.NewStack(p.empty, p.list))
	p.list.Hide()
	return p
}

func (p *proxyPage) update(snapshot core.Snapshot) {
	p.groups = snapshot.Groups
	p.snapshot = snapshot
	p.groupOptions = make(map[string]string, len(p.groups))
	p.groupSelect.Options = make([]string, 0, len(p.groups))
	selectedExists := false
	for _, group := range p.groups {
		label := groupLabel(group)
		p.groupOptions[label] = group.ID
		p.groupSelect.Options = append(p.groupSelect.Options, label)
		if group.ID == p.selectedGroupID {
			selectedExists = true
		}
	}
	if !selectedExists {
		p.selectedGroupID = ""
		if len(p.groups) > 0 {
			p.selectedGroupID = p.groups[0].ID
		}
	}
	p.groupSelect.Selected = ""
	for _, group := range p.groups {
		if group.ID == p.selectedGroupID {
			p.groupSelect.Selected = groupLabel(group)
			break
		}
	}
	p.groupSelect.Refresh()
	p.updateSelectedGroup()
}

func (p *proxyPage) updateSelectedGroup() {
	p.choices = nil
	p.refreshing = true
	if group := p.selectedGroup(); group != nil {
		p.choices = append(p.choices, group.Proxies...)
		p.automation.SetChecked(group.AutomationEnabled)
	} else {
		p.automation.SetChecked(false)
	}
	p.refreshing = false
	if p.managedSelector() {
		p.groupMode.SetText("Manual selection and health probes are available")
		p.automation.Enable()
		p.probe.Enable()
	} else {
		p.groupMode.SetText("Mihomo manages selection for this group")
		p.automation.Disable()
		p.probe.Disable()
	}
	if len(p.groups) == 0 {
		p.empty.Show()
		p.list.Hide()
	} else {
		p.empty.Hide()
		p.list.Show()
	}
	p.list.UnselectAll()
	p.list.Refresh()
}

func (p *proxyPage) selectedGroup() *core.GroupSnapshot {
	for i := range p.groups {
		if p.groups[i].ID == p.selectedGroupID {
			return &p.groups[i]
		}
	}
	return nil
}

func (p *proxyPage) managedSelector() bool {
	group := p.selectedGroup()
	return group != nil && (group.Type == "Selector" || group.Type == "select")
}

type subscriptionPage struct {
	view   fyne.CanvasObject
	rows   []core.SubscriptionSnapshot
	list   *widget.List
	add    *widget.Button
	empty  *widget.Label
	items  map[*fyne.Container]*subscriptionItem
	window fyne.Window
	send   sendIntent
	editor *subscriptionEditor
}

func newSubscriptionPage(send sendIntent, window fyne.Window) *subscriptionPage {
	p := &subscriptionPage{send: send, window: window, items: make(map[*fyne.Container]*subscriptionItem)}
	p.add = widget.NewButton("Add subscription", func() { p.openSubscriptionEditor(nil) })
	p.list = widget.NewList(
		func() int { return len(p.rows) },
		func() fyne.CanvasObject {
			item := &subscriptionItem{name: widget.NewLabel(""), source: widget.NewLabel(""), status: widget.NewLabel("")}
			item.refresh = widget.NewButton("Refresh", func() {
				if item.id != "" {
					p.send(ipc.Command{Kind: ipc.CommandRefreshSubscription, SubscriptionID: item.id})
				}
			})
			item.activate = widget.NewButton("Activate", func() {
				if item.id != "" {
					p.send(ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: item.id})
				}
			})
			item.delete = widget.NewButton("Delete", func() {
				if item.id == "" {
					return
				}
				id := item.id
				dialog.ShowConfirm("Delete subscription", "Delete subscription "+id+" and its private snapshot?", func(confirmed bool) {
					if confirmed {
						p.send(ipc.Command{Kind: ipc.CommandDeleteSubscription, SubscriptionID: id})
					}
				}, p.window)
			})
			item.edit = widget.NewButton("Edit", func() { p.editSubscription(item.id) })
			item.details = widget.NewButton("Details", func() {
				for _, sub := range p.rows {
					if sub.ID == item.id {
						dialog.ShowInformation("Subscription "+sub.ID, subscriptionDetails(sub), p.window)
						return
					}
				}
			})
			row := container.NewVBox(container.NewGridWithColumns(3, item.name, item.source, item.status), container.NewHBox(item.refresh, item.activate, item.edit, item.delete, item.details), widget.NewSeparator())
			p.items[row] = item
			return row
		},
		func(id widget.ListItemID, object fyne.CanvasObject) {
			item := p.items[object.(*fyne.Container)]
			if id < 0 || id >= len(p.rows) {
				item.id = ""
				item.name.SetText("")
				item.source.SetText("")
				item.status.SetText("")
				return
			}
			subscription := p.rows[id]
			item.id = subscription.ID
			item.name.SetText(subscription.Name)
			item.source.SetText(sourceHostLabel(subscription.SourceHost))
			item.status.SetText(subscriptionStatus(subscription))
			if subscription.Active {
				item.delete.Disable()
			} else {
				item.delete.Enable()
			}
		},
	)
	p.empty = widget.NewLabel("No subscriptions are available")
	p.list.Hide()
	title := widget.NewLabelWithStyle("Subscriptions", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.view = container.NewBorder(container.NewVBox(title, p.add), nil, nil, nil, container.NewStack(p.empty, p.list))
	return p
}

func (p *subscriptionPage) update(rows []core.SubscriptionSnapshot) {
	p.rows = rows
	if len(rows) == 0 {
		p.empty.Show()
		p.list.Hide()
	} else {
		p.empty.Hide()
		p.list.Show()
	}
	p.list.Refresh()
}

func (p *subscriptionPage) editSubscription(id string) {
	for i := range p.rows {
		if p.rows[i].ID == id {
			subscription := p.rows[i]
			p.openSubscriptionEditor(&subscription)
			return
		}
	}
}

func (p *subscriptionPage) openSubscriptionEditor(existing *core.SubscriptionSnapshot) {
	if p.editor != nil {
		return
	}
	e := &subscriptionEditor{existing: existing}
	e.name = widget.NewEntry()
	e.name.SetPlaceHolder("Display name")
	e.source = widget.NewPasswordEntry()
	e.agent = widget.NewPasswordEntry()
	e.refresh = widget.NewEntry()
	e.timeout = widget.NewEntry()
	e.enabled = widget.NewCheck("Enabled", nil)
	routes := []string{"Direct", "System proxy", "Mihomo proxy"}
	policies := []string{"HTTPS only", "Allow HTTP (insecure)"}
	tlsPolicies := []string{"Verify certificates", "Allow invalid TLS certificates (insecure)"}
	if existing != nil {
		routes = append([]string{"Keep current"}, routes...)
		policies = append([]string{"Keep current"}, policies...)
		tlsPolicies = append([]string{"Keep current"}, tlsPolicies...)
	}
	e.route = widget.NewSelect(routes, nil)
	e.http = widget.NewSelect(policies, nil)
	e.tls = widget.NewSelect(tlsPolicies, nil)
	items := make([]*widget.FormItem, 0, 10)
	if existing == nil {
		e.id = widget.NewEntry()
		e.id.SetPlaceHolder("Stable subscription ID")
		e.source.SetPlaceHolder("HTTPS URL; insecure HTTP requires explicit opt-in")
		e.agent.SetPlaceHolder("Optional User-Agent; default clash-pulse")
		e.refresh.SetText("43200")
		e.timeout.SetText("30")
		e.enabled.SetChecked(true)
		e.route.SetSelected("Direct")
		e.http.SetSelected("HTTPS only")
		e.tls.SetSelected("Verify certificates")
		items = append(items, widget.NewFormItem("Stable ID", e.id))
	} else {
		e.name.SetText(existing.Name)
		e.source.SetPlaceHolder("Leave blank to keep the current private URL")
		if existing.RefreshIntervalSeconds > 0 {
			e.refresh.SetText(strconv.FormatUint(uint64(existing.RefreshIntervalSeconds), 10))
		}
		e.agent.SetPlaceHolder("Blank keeps current; '-' resets to clash-pulse")
		if existing.TimeoutSeconds > 0 {
			e.timeout.SetText(strconv.FormatUint(uint64(existing.TimeoutSeconds), 10))
		}
		e.enabled.SetChecked(existing.Enabled)
		switch existing.Route {
		case "mihomo_proxy":
			e.route.SetSelected("Mihomo proxy")
		case "system_proxy":
			e.route.SetSelected("System proxy")
		default:
			e.route.SetSelected("Direct")
		}
		if existing.AllowHTTP {
			e.http.SetSelected("Allow HTTP (insecure)")
		} else {
			e.http.SetSelected("HTTPS only")
		}
		if existing.AllowInvalidTLS {
			e.tls.SetSelected("Allow invalid TLS certificates (insecure)")
		} else {
			e.tls.SetSelected("Verify certificates")
		}
		items = append(items, widget.NewFormItem("Stable ID", widget.NewLabel(existing.ID)))
	}
	items = append(items,
		widget.NewFormItem("Display name", e.name),
		widget.NewFormItem("Source URL", e.source),
		widget.NewFormItem("User-Agent", e.agent),
		widget.NewFormItem("", e.enabled),
		widget.NewFormItem("Refresh interval (seconds)", e.refresh),
		widget.NewFormItem("Timeout (seconds)", e.timeout),
		widget.NewFormItem("Connection route", e.route),
		widget.NewFormItem("HTTP policy", e.http),
		widget.NewFormItem("TLS policy", e.tls),
	)
	p.editor = e
	title, confirm := "Add Subscription", "Add"
	if existing != nil {
		title, confirm = "Edit Subscription", "Save"
	}
	e.dialog = dialog.NewForm(title, confirm, "Cancel", items, func(confirmed bool) {
		if !confirmed {
			e.source.SetText("")
			e.agent.SetText("")
			p.editor = nil
			return
		}
		command, changed, err := e.command()
		e.source.SetText("")
		e.agent.SetText("")
		p.editor = nil
		if err != nil {
			dialog.ShowError(err, p.window)
			return
		}
		if changed {
			p.send(command)
		}
	}, p.window)
	e.dialog.Show()
}

type subscriptionEditor struct {
	existing *core.SubscriptionSnapshot
	id       *widget.Entry
	name     *widget.Entry
	source   *widget.Entry
	agent    *widget.Entry
	enabled  *widget.Check
	refresh  *widget.Entry
	timeout  *widget.Entry
	route    *widget.Select
	http     *widget.Select
	tls      *widget.Select
	dialog   *dialog.FormDialog
}

func (e *subscriptionEditor) command() (ipc.Command, bool, error) {
	creating := e.existing == nil
	id := strings.TrimSpace(e.existingID())
	if creating {
		id = strings.TrimSpace(e.id.Text)
		if !validSubscriptionID(id) {
			return ipc.Command{}, false, fmt.Errorf("stable ID must start with a letter or number and contain only letters, numbers, '.', '_' or '-' (1-64 characters)")
		}
	}
	name := strings.TrimSpace(e.name.Text)
	if name == "" || len(name) > 128 {
		return ipc.Command{}, false, fmt.Errorf("display name must contain 1-128 characters")

	}
	patch := &ipc.SubscriptionEdit{}
	if creating || name != e.existing.Name {
		patch.Name = &name
	}
	urlValue := strings.TrimSpace(e.source.Text)
	allowHTTP, httpChanged, err := policyValue(e.http.Selected, creating, "HTTPS only", "Allow HTTP (insecure)")
	if err != nil {
		return ipc.Command{}, false, err
	}
	if creating || urlValue != "" {
		permittedHTTP := allowHTTP != nil && *allowHTTP || !httpChanged && !creating && e.existing.AllowHTTP
		if !validSubscriptionURL(urlValue, permittedHTTP) {
			return ipc.Command{}, false, fmt.Errorf("source URL must be HTTPS; HTTP requires explicit opt-in")
		}
		patch.URL = &urlValue
	}
	if value := strings.TrimSpace(e.agent.Text); value != "" {
		if value == "-" {
			value = ""
		}
		if len(value) > 256 {
			return ipc.Command{}, false, fmt.Errorf("User-Agent must be printable ASCII at most 256 bytes")
		}
		for _, r := range value {
			if r < 0x20 || r > 0x7e {
				return ipc.Command{}, false, fmt.Errorf("User-Agent must be printable ASCII at most 256 bytes")
			}
		}
		patch.UserAgent = &value
	}
	if creating || e.enabled.Checked != e.existing.Enabled {
		enabled := e.enabled.Checked
		patch.Enabled = &enabled
	}
	if patch.RefreshIntervalSeconds, err = subscriptionSeconds(e.refresh, creating, "refresh interval"); err != nil {
		return ipc.Command{}, false, err
	}
	if !creating && patch.RefreshIntervalSeconds != nil && *patch.RefreshIntervalSeconds == e.existing.RefreshIntervalSeconds {
		patch.RefreshIntervalSeconds = nil
	}
	if patch.TimeoutSeconds, err = subscriptionSeconds(e.timeout, creating, "timeout"); err != nil {
		return ipc.Command{}, false, err
	}
	if !creating && patch.TimeoutSeconds != nil && *patch.TimeoutSeconds == e.existing.TimeoutSeconds {
		patch.TimeoutSeconds = nil
	}
	if e.route.Selected != "Keep current" {
		var route string
		switch e.route.Selected {
		case "Direct":
			route = "direct"
		case "System proxy":
			route = "system_proxy"
		case "Mihomo proxy":
			route = "mihomo_proxy"
		default:
			return ipc.Command{}, false, fmt.Errorf("select a connection route")
		}
		if creating || route != e.existing.Route && !(route == "direct" && e.existing.Route == "") {
			patch.Route = &route
		}
	}
	if creating || httpChanged && *allowHTTP != e.existing.AllowHTTP {
		patch.AllowHTTP = allowHTTP
	}
	allowTLS, tlsChanged, err := policyValue(e.tls.Selected, creating, "Verify certificates", "Allow invalid TLS certificates (insecure)")
	if err != nil {
		return ipc.Command{}, false, err
	}
	if creating || tlsChanged && *allowTLS != e.existing.AllowInvalidTLS {
		patch.AllowInvalidTLS = allowTLS
	}
	changed := patch.Name != nil || patch.URL != nil || patch.UserAgent != nil || patch.Enabled != nil || patch.RefreshIntervalSeconds != nil || patch.TimeoutSeconds != nil || patch.Route != nil || patch.AllowHTTP != nil || patch.AllowInvalidTLS != nil
	if !changed {
		return ipc.Command{}, false, nil
	}
	return ipc.Command{Kind: ipc.CommandPutSubscription, SubscriptionID: id, Subscription: patch}, true, nil
}

func (e *subscriptionEditor) existingID() string {
	if e.existing == nil {
		return ""
	}
	return e.existing.ID
}

func policyValue(selected string, creating bool, deny, allow string) (*bool, bool, error) {
	if selected == "Keep current" && !creating {
		return nil, false, nil
	}
	var value bool
	switch selected {
	case deny:
		value = false
	case allow:
		value = true
	default:
		return nil, false, fmt.Errorf("select a security policy")
	}
	return &value, true, nil
}

func subscriptionSeconds(input *widget.Entry, required bool, label string) (*uint32, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" && !required {
		return nil, nil
	}
	value, err := strconv.ParseUint(text, 10, 32)
	min, max := uint64(1), uint64(300)
	if label == "refresh interval" {
		min, max = 60, 86400*30
	}
	if err != nil || value < min || value > max {
		return nil, fmt.Errorf("%s must be between %d and %d seconds", label, min, max)
	}
	seconds := uint32(value)
	return &seconds, nil
}

func validSubscriptionURL(raw string, allowHTTP bool) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return false
	}
	return parsed.Scheme == "https" || allowHTTP && parsed.Scheme == "http"
}

func validSubscriptionID(id string) bool {
	if len(id) == 0 || len(id) > 64 || !subscriptionIDChar(id[0], true) {
		return false
	}
	for i := 1; i < len(id); i++ {
		if !subscriptionIDChar(id[i], false) {
			return false
		}
	}
	return true
}

func subscriptionIDChar(char byte, first bool) bool {
	alphanumeric := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
	return alphanumeric || !first && (char == '.' || char == '_' || char == '-')
}

type subscriptionItem struct {
	id       string
	name     *widget.Label
	source   *widget.Label
	status   *widget.Label
	refresh  *widget.Button
	activate *widget.Button
	edit     *widget.Button
	delete   *widget.Button
	details  *widget.Button
}

type resourcePage struct {
	view   fyne.CanvasObject
	rows   []core.ResourceSnapshot
	list   *widget.List
	add    *widget.Button
	empty  *widget.Label
	items  map[*fyne.Container]*resourceItem
	window fyne.Window
	send   sendIntent
	editor *resourceEditor
}

func newResourcePage(send sendIntent, window fyne.Window) *resourcePage {
	p := &resourcePage{send: send, window: window, items: make(map[*fyne.Container]*resourceItem)}
	p.add = widget.NewButton("Add resource", func() { p.openResourceEditor(nil) })
	p.list = widget.NewList(
		func() int { return len(p.rows) },
		func() fyne.CanvasObject {
			item := &resourceItem{name: widget.NewLabel(""), source: widget.NewLabel(""), status: widget.NewLabel("")}
			item.refresh = widget.NewButton("Refresh", func() {
				if item.id != "" {
					p.send(ipc.Command{Kind: ipc.CommandRefreshResource, ResourceID: item.id})
				}
			})
			item.details = widget.NewButton("Details", func() {
				for _, resource := range p.rows {
					if resource.ID == item.id {
						dialog.ShowInformation("Resource "+resource.ID, resourceDetails(resource), p.window)
						return
					}
				}
			})
			item.edit = widget.NewButton("Edit", func() { p.editResource(item.id) })
			row := container.NewVBox(container.NewGridWithColumns(3, item.name, item.source, item.status), container.NewHBox(item.refresh, item.edit, item.details), widget.NewSeparator())
			p.items[row] = item
			return row
		},
		func(id widget.ListItemID, object fyne.CanvasObject) {
			item := p.items[object.(*fyne.Container)]
			if id < 0 || id >= len(p.rows) {
				item.id = ""
				item.name.SetText("")
				item.source.SetText("")
				item.status.SetText("")
				item.refresh.Disable()
				item.edit.Disable()
				return
			}
			resource := p.rows[id]
			item.id = resource.ID
			item.name.SetText(resource.ID)
			item.source.SetText(sourceHostLabel(resource.SourceHost))
			item.status.SetText(resourceStatus(resource))
			if resource.Enabled {
				item.refresh.Enable()
			} else {
				item.refresh.Disable()
			}
			item.edit.Enable()
		},
	)
	p.empty = widget.NewLabel("No data resources are available")
	p.list.Hide()
	p.view = container.NewBorder(container.NewVBox(widget.NewLabelWithStyle("Data resources", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), p.add), nil, nil, nil, container.NewStack(p.empty, p.list))
	return p
}

func (p *resourcePage) update(rows []core.ResourceSnapshot) {
	p.rows = rows
	if len(rows) == 0 {
		p.empty.Show()
		p.list.Hide()
	} else {
		p.empty.Hide()
		p.list.Show()
	}
	p.list.Refresh()

}

func (p *resourcePage) editResource(id string) {
	for i := range p.rows {
		if p.rows[i].ID == id {
			resource := p.rows[i]
			p.openResourceEditor(&resource)
			return
		}
	}
}

type resourceItem struct {
	id      string
	name    *widget.Label
	source  *widget.Label
	status  *widget.Label
	refresh *widget.Button
	edit    *widget.Button
	details *widget.Button
}

type filterPage struct {
	view   fyne.CanvasObject
	rows   []core.FilterSnapshot
	list   *widget.List
	add    *widget.Button
	empty  *widget.Label
	items  map[*fyne.Container]*filterItem
	window fyne.Window
	send   sendIntent
	editor *filterEditor
}

func newFilterPage(send sendIntent, window fyne.Window) *filterPage {
	p := &filterPage{send: send, window: window, items: make(map[*fyne.Container]*filterItem)}
	p.add = widget.NewButton("Add filter", func() { p.openFilterEditor(nil) })
	p.list = widget.NewList(
		func() int { return len(p.rows) },
		func() fyne.CanvasObject {
			item := &filterItem{name: widget.NewLabel(""), format: widget.NewLabel(""), status: widget.NewLabel("")}
			item.refresh = widget.NewButton("Refresh", func() {
				if item.id != "" {
					p.send(ipc.Command{Kind: ipc.CommandRefreshFilter, FilterID: item.id})
				}
			})
			item.details = widget.NewButton("Details", func() {
				for _, filter := range p.rows {
					if filter.ID == item.id {
						dialog.ShowInformation("Filter "+filter.ID, filterDetails(filter), p.window)
						return
					}
				}
			})
			item.edit = widget.NewButton("Edit", func() { p.editFilter(item.id) })
			row := container.NewVBox(container.NewGridWithColumns(3, item.name, item.format, item.status), container.NewHBox(item.refresh, item.edit, item.details), widget.NewSeparator())
			p.items[row] = item
			return row
		},
		func(id widget.ListItemID, object fyne.CanvasObject) {
			item := p.items[object.(*fyne.Container)]
			if id < 0 || id >= len(p.rows) {
				item.id = ""
				item.name.SetText("")
				item.format.SetText("")
				item.status.SetText("")
				item.refresh.Disable()
				item.edit.Disable()
				return
			}
			filter := p.rows[id]
			item.id = filter.ID
			item.name.SetText(filter.ID)
			item.format.SetText(filter.Format)
			item.status.SetText(filterStatus(filter))
			if filter.Enabled {
				item.refresh.Enable()
			} else {
				item.refresh.Disable()
			}
			item.edit.Enable()
		},
	)
	p.empty = widget.NewLabel("No filter lists are available")
	p.list.Hide()
	p.view = container.NewBorder(container.NewVBox(widget.NewLabelWithStyle("Filter lists", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), p.add), nil, nil, nil, container.NewStack(p.empty, p.list))
	return p
}

func (p *filterPage) update(rows []core.FilterSnapshot) {
	p.rows = rows
	if len(rows) == 0 {
		p.empty.Show()
		p.list.Hide()
	} else {
		p.empty.Hide()
		p.list.Show()
	}
	p.list.Refresh()
}

func (p *filterPage) editFilter(id string) {
	for i := range p.rows {
		if p.rows[i].ID == id {
			filter := p.rows[i]
			p.openFilterEditor(&filter)
			return
		}
	}
}

type filterItem struct {
	id      string
	name    *widget.Label
	format  *widget.Label
	status  *widget.Label
	refresh *widget.Button
	edit    *widget.Button
	details *widget.Button
}

func (p *resourcePage) openResourceEditor(existing *core.ResourceSnapshot) {
	if p.editor != nil {
		return
	}
	e := &resourceEditor{existing: existing}
	e.source, e.interval, e.pin = widget.NewPasswordEntry(), widget.NewEntry(), widget.NewEntry()
	e.enabled = widget.NewCheck("Enabled", nil)
	e.kind = widget.NewSelect([]string{"geoip.dat", "geosite.dat", "Country.mmdb", "rule-set", "rule-provider"}, nil)
	e.format = widget.NewSelect([]string{"dat", "mmdb", "yaml", "text", "mrs"}, nil)
	e.ruleType = widget.NewSelect([]string{"None", "domain", "ipcidr", "classical"}, nil)
	items := make([]*widget.FormItem, 0, 9)
	if existing == nil {
		e.id = widget.NewEntry()
		e.id.SetPlaceHolder("Stable resource ID")
		e.source.SetPlaceHolder("HTTPS URL or local path")
		e.interval.SetText("43200")
		e.pin.SetPlaceHolder("Optional 64-character SHA-256")
		e.enabled.SetChecked(true)
		e.kind.SetSelected("rule-provider")
		e.format.SetSelected("yaml")
		e.ruleType.SetSelected("domain")
		items = append(items, widget.NewFormItem("Stable ID", e.id))
	} else {
		e.source.SetPlaceHolder("Leave blank to keep the current private URL")
		e.interval.SetPlaceHolder("Leave blank to keep current")
		e.pin.SetPlaceHolder("Leave blank to keep current")
		e.clearPin = widget.NewCheck("Clear existing pin", nil)
		e.enabled.SetChecked(existing.Enabled)
		e.kind.SetSelected(existing.Kind)
		e.format.SetSelected(existing.Format)
		ruleType := existing.RuleType
		if ruleType == "" {
			ruleType = "None"
		}
		e.ruleType.SetSelected(ruleType)
		items = append(items, widget.NewFormItem("Stable ID", widget.NewLabel(existing.ID)))
	}
	items = append(items, widget.NewFormItem("Kind", e.kind), widget.NewFormItem("Format", e.format), widget.NewFormItem("Rule type", e.ruleType), widget.NewFormItem("Private URL / local path", e.source), widget.NewFormItem("", e.enabled), widget.NewFormItem("Update interval (seconds)", e.interval), widget.NewFormItem("Optional SHA-256 pin", e.pin))
	if e.clearPin != nil {
		items = append(items, widget.NewFormItem("", e.clearPin))
	}
	p.editor = e
	title, confirm := "Add Data Resource", "Add"
	if existing != nil {
		title, confirm = "Edit Data Resource", "Save"
	}
	e.dialog = dialog.NewForm(title, confirm, "Cancel", items, func(confirmed bool) {
		if !confirmed {
			e.source.SetText("")
			p.editor = nil
			return
		}
		command, changed, err := e.command()
		e.source.SetText("")
		p.editor = nil
		if err != nil {
			dialog.ShowError(err, p.window)
			return
		}
		if changed {
			p.send(command)
		}
	}, p.window)
	e.dialog.Show()
}

type resourceEditor struct {
	existing *core.ResourceSnapshot
	id       *widget.Entry
	kind     *widget.Select
	format   *widget.Select
	ruleType *widget.Select
	source   *widget.Entry
	enabled  *widget.Check
	interval *widget.Entry
	pin      *widget.Entry
	clearPin *widget.Check
	dialog   *dialog.FormDialog
}

func (e *resourceEditor) command() (ipc.Command, bool, error) {
	creating := e.existing == nil
	id := ""
	if creating {
		id = strings.TrimSpace(e.id.Text)
	} else {
		id = e.existing.ID
	}
	if !validSubscriptionID(id) {
		return ipc.Command{}, false, fmt.Errorf("stable ID must start with a letter or number and contain only letters, numbers, '.', '_' or '-' (1-64 characters)")
	}
	kind, format, ruleType := e.kind.Selected, e.format.Selected, e.ruleType.Selected
	if ruleType == "None" {
		ruleType = ""
	}
	if !validResourceCombination(kind, format, ruleType) {
		return ipc.Command{}, false, fmt.Errorf("select a compatible resource kind, format, and rule type")
	}
	patch := &ipc.ResourceEdit{}
	if creating || kind != e.existing.Kind {
		patch.Kind = &kind
	}
	if creating || format != e.existing.Format {
		patch.Format = &format
	}
	if creating || ruleType != e.existing.RuleType {
		patch.RuleType = &ruleType
	}
	source := strings.TrimSpace(e.source.Text)
	if creating || source != "" {
		if !validResourceSource(source) {
			return ipc.Command{}, false, fmt.Errorf("source must be an HTTPS URL without user information or an absolute local path")
		}
		patch.URL = &source
	}
	if creating || e.enabled.Checked != e.existing.Enabled {
		enabled := e.enabled.Checked
		patch.Enabled = &enabled
	}
	interval, err := subscriptionSeconds(e.interval, creating, "refresh interval")
	if err != nil {
		return ipc.Command{}, false, err
	}
	patch.IntervalSeconds = interval
	pin := strings.TrimSpace(e.pin.Text)
	if pin != "" && !validResourcePin(pin) {
		return ipc.Command{}, false, fmt.Errorf("SHA-256 pin must contain exactly 64 hexadecimal characters")
	}
	if e.clearPin != nil && e.clearPin.Checked && pin != "" {
		return ipc.Command{}, false, fmt.Errorf("enter a new pin or clear the existing pin, not both")
	}
	if pin != "" {
		patch.SHA256 = &pin
	} else if e.clearPin != nil && e.clearPin.Checked {
		cleared := ""
		patch.SHA256 = &cleared
	}
	changed := patch.Kind != nil || patch.Format != nil || patch.RuleType != nil || patch.URL != nil || patch.Enabled != nil || patch.IntervalSeconds != nil || patch.SHA256 != nil
	if !changed {
		return ipc.Command{}, false, nil
	}
	return ipc.Command{Kind: ipc.CommandPutResource, ResourceID: id, Resource: patch}, true, nil
}

func validResourceCombination(kind, format, ruleType string) bool {
	switch kind {
	case "geoip.dat", "geosite.dat":
		return format == "dat" && ruleType == ""
	case "Country.mmdb":
		return format == "mmdb" && ruleType == ""
	case "rule-set", "rule-provider":
		return (format == "yaml" || format == "text" || format == "mrs") && (ruleType == "domain" || ruleType == "ipcidr" || ruleType == "classical") && (format != "mrs" || ruleType != "classical")
	default:
		return false
	}
}

func validResourceSource(raw string) bool {
	if raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\x00\r\n") {
		return false
	}
	if filepath.IsAbs(raw) {
		return filepath.Separator != '\\' || !strings.HasPrefix(raw, `\\`)
	}
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Opaque == ""
}

func validResourcePin(pin string) bool {
	if len(pin) != 64 {
		return false
	}
	_, err := hex.DecodeString(pin)
	return err == nil
}

func (p *filterPage) openFilterEditor(existing *core.FilterSnapshot) {
	if p.editor != nil {
		return
	}
	e := &filterEditor{existing: existing}
	e.resource, e.target = widget.NewEntry(), widget.NewEntry()
	e.format = widget.NewSelect([]string{"Keep current", "yaml", "text", "mrs"}, nil)
	e.enabled = widget.NewCheck("Enabled", nil)
	items := make([]*widget.FormItem, 0, 5)
	if existing == nil {
		e.id = widget.NewEntry()
		e.id.SetPlaceHolder("Stable filter ID")
		e.resource.SetPlaceHolder("Managed rule resource ID")
		e.format = widget.NewSelect([]string{"yaml", "text", "mrs"}, nil)
		e.format.SetSelected("yaml")
		e.enabled.SetChecked(true)
		items = append(items, widget.NewFormItem("Stable ID", e.id))
	} else {
		e.resource.SetText(existing.ResourceID)
		e.target.SetText(existing.Target)
		if existing.Format != "" {
			e.format.SetSelected(existing.Format)
		} else {
			e.format.SetSelected("Keep current")
		}
		e.enabled.SetChecked(existing.Enabled)
		items = append(items, widget.NewFormItem("Stable ID", widget.NewLabel(existing.ID)))
	}
	items = append(items, widget.NewFormItem("Managed resource ID", e.resource), widget.NewFormItem("Format", e.format), widget.NewFormItem("Target", e.target), widget.NewFormItem("", e.enabled))
	p.editor = e
	title, confirm := "Add Filter List", "Add"
	if existing != nil {
		title, confirm = "Edit Filter List", "Save"
	}
	e.dialog = dialog.NewForm(title, confirm, "Cancel", items, func(confirmed bool) {
		if !confirmed {
			p.editor = nil
			return
		}
		command, changed, err := e.command()
		p.editor = nil
		if err != nil {
			dialog.ShowError(err, p.window)
			return
		}
		if changed {
			p.send(command)
		}
	}, p.window)
	e.dialog.Show()
}

type filterEditor struct {
	existing *core.FilterSnapshot
	id       *widget.Entry
	resource *widget.Entry
	format   *widget.Select
	target   *widget.Entry
	enabled  *widget.Check
	dialog   *dialog.FormDialog
}

func (e *filterEditor) command() (ipc.Command, bool, error) {
	creating := e.existing == nil
	id := ""
	if creating {
		id = strings.TrimSpace(e.id.Text)
	} else {
		id = e.existing.ID
	}
	if !validSubscriptionID(id) {
		return ipc.Command{}, false, fmt.Errorf("stable ID must start with a letter or number and contain only letters, numbers, '.', '_' or '-' (1-64 characters)")
	}
	resourceID := strings.TrimSpace(e.resource.Text)
	if !validSubscriptionID(resourceID) {
		return ipc.Command{}, false, fmt.Errorf("managed resource ID must be a stable ID")
	}
	target := strings.TrimSpace(e.target.Text)
	if target == "" || len(target) > 128 || strings.ContainsAny(target, ",\r\n\t\x00") {
		return ipc.Command{}, false, fmt.Errorf("target must contain 1-128 characters and no commas or control characters")
	}
	patch := &ipc.FilterEdit{}
	if creating || resourceID != e.existing.ResourceID {
		patch.ResourceID = &resourceID
	}
	if e.format.Selected != "Keep current" && (creating || e.format.Selected != e.existing.Format) {
		format := e.format.Selected
		if format != "yaml" && format != "text" && format != "mrs" {
			return ipc.Command{}, false, fmt.Errorf("select a filter format")
		}
		patch.Format = &format
	} else if creating {
		return ipc.Command{}, false, fmt.Errorf("select a filter format")
	}
	if creating || target != e.existing.Target {
		patch.Target = &target
	}
	if creating || e.enabled.Checked != e.existing.Enabled {
		enabled := e.enabled.Checked
		patch.Enabled = &enabled
	}
	changed := patch.ResourceID != nil || patch.Format != nil || patch.Target != nil || patch.Enabled != nil
	if !changed {
		return ipc.Command{}, false, nil
	}
	return ipc.Command{Kind: ipc.CommandPutFilter, FilterID: id, Filter: patch}, true, nil
}

type settingsLayout struct{}

func (settingsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(fyne.Max(280, objects[1].MinSize().Width), 240)
}

func (settingsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	sidebar, dropdown, content := objects[0], objects[1], objects[2]
	gap := theme.Padding()
	sideWidth := sidebar.MinSize().Width
	if size.Width >= sideWidth+gap+content.MinSize().Width {
		dropdown.Hide()
		sidebar.Show()
		sidebar.Move(fyne.NewPos(0, 0))
		sidebar.Resize(fyne.NewSize(sideWidth, size.Height))
		content.Move(fyne.NewPos(sideWidth+gap, 0))
		content.Resize(fyne.NewSize(size.Width-sideWidth-gap, size.Height))
		return
	}
	sidebar.Hide()
	dropdown.Show()
	dropdown.Move(fyne.NewPos(0, 0))
	dropdown.Resize(fyne.NewSize(size.Width, dropdown.MinSize().Height))
	content.Move(fyne.NewPos(0, dropdown.Size().Height+gap))
	content.Resize(fyne.NewSize(size.Width, size.Height-dropdown.Size().Height-gap))
}

type settingsPage struct {
	view                fyne.CanvasObject
	sidebar             *widget.RadioGroup
	sectionSelect       *widget.Select
	sectionContent      *fyne.Container
	sectionViews        map[string]fyne.CanvasObject
	binary              *widget.Form
	binaryValues        [4]*widget.Label
	binaryPath          string
	threshold           *widget.Label
	monitor             *widget.Check
	monitorState        core.MonitorSnapshot
	systemProxy         *widget.Check
	systemProxyState    core.SystemProxySnapshot
	proxyStatus         *widget.Label
	interval            *widget.Select
	intervals           map[string]uint32
	refreshing          bool
	settingsInitialized bool
	send                sendIntent
	window              fyne.Window
	dns                 *dnsSettings
}

func newSettingsPage(send sendIntent, window fyne.Window) *settingsPage {
	p := &settingsPage{send: send, window: window, intervals: make(map[string]uint32)}
	p.dns = newDNSSettings(send, window)
	p.binary, p.binaryValues = newBinaryForm()
	p.threshold = widget.NewLabel("Not configured")
	p.systemProxy = widget.NewCheck("Enable system proxy", func(enabled bool) {
		if p.refreshing {
			return
		}
		value := enabled
		p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{SystemProxyEnabled: &value}})
	})
	p.proxyStatus = widget.NewLabel("System proxy inactive")
	p.monitor = widget.NewCheck("Enable latency monitor", func(enabled bool) {
		if p.refreshing {
			return
		}
		value := enabled
		p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{MonitorEnabled: &value}})
	})
	p.interval = widget.NewSelect(nil, func(label string) {
		if p.refreshing {
			return
		}
		if seconds, ok := p.intervals[label]; ok {
			value := seconds
			p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{MonitorIntervalSeconds: &value}})
		}
	})
	sections := []string{"Mihomo binary", "System Proxy", "Monitor", "DNS"}
	p.sectionViews = map[string]fyne.CanvasObject{
		"Mihomo binary": container.NewVBox(
			widget.NewLabelWithStyle("Mihomo binary", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			p.binary,
			container.NewVBox(
				widget.NewButton("Use system Mihomo", func() {
					value := "system"
					p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{Binary: &value}})
				}),
				widget.NewButton("Use bundled Mihomo", func() {
					value := "bundled"
					p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{Binary: &value}})
				}),
				widget.NewButton("Choose executable path", p.editBinaryPath),
			),
		),
		"System Proxy": container.NewVBox(
			widget.NewLabelWithStyle("System proxy", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			p.systemProxy, p.proxyStatus,
		),
		"Monitor": container.NewVBox(
			widget.NewLabelWithStyle("Monitor", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			p.monitor,
			container.NewHBox(widget.NewLabel("Monitor interval"), p.interval),
			container.NewHBox(widget.NewLabel("Alert threshold"), p.threshold, widget.NewButton("Change", p.editThreshold)),
			widget.NewButton("Edit probe policy", p.editMonitorPolicy),
		),
		"DNS": p.dns.view,
	}
	p.sectionContent = container.NewStack(p.sectionViews[sections[0]])
	p.sidebar = widget.NewRadioGroup(sections, p.selectSection)
	p.sectionSelect = widget.NewSelect(sections, p.selectSection)
	p.sidebar.SetSelected(sections[0])
	p.sectionSelect.SetSelected(sections[0])
	p.view = container.New(settingsLayout{}, p.sidebar, p.sectionSelect, container.NewVScroll(p.sectionContent))
	return p
}

func (p *settingsPage) selectSection(name string) {
	view, ok := p.sectionViews[name]
	if !ok || p.sectionContent.Objects[0] == view {
		return
	}
	p.sectionContent.Objects = []fyne.CanvasObject{view}
	p.view.Refresh()
	if p.sidebar.Selected != name {
		p.sidebar.SetSelected(name)
	}
	if p.sectionSelect.Selected != name {
		p.sectionSelect.SetSelected(name)
	}
}

func (p *settingsPage) editBinaryPath() {
	input := widget.NewEntry()
	input.SetPlaceHolder("Absolute path to Mihomo")
	if filepath.IsAbs(p.binaryPath) {
		input.SetText(p.binaryPath)
	}
	dialog.NewForm("Mihomo binary", "Select", "Cancel", []*widget.FormItem{widget.NewFormItem("Executable path", input)}, func(confirmed bool) {
		if !confirmed {
			return
		}
		path := strings.TrimSpace(input.Text)
		if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			dialog.ShowError(fmt.Errorf("select an absolute executable path"), p.window)
			return
		}
		p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{Binary: &path}})
	}, p.window).Show()
}

func (p *settingsPage) editThreshold() {
	input := widget.NewEntry()
	input.SetPlaceHolder("Milliseconds (1-60000)")
	current := strings.TrimSuffix(p.threshold.Text, " ms")
	if current != p.threshold.Text {
		input.SetText(current)
	}
	dialog.NewForm("Alert threshold", "Apply", "Cancel", []*widget.FormItem{widget.NewFormItem("Milliseconds", input)}, func(confirmed bool) {
		if !confirmed {
			return
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(input.Text), 10, 32)
		if err != nil || parsed < 1 || parsed > 60000 {
			dialog.ShowError(fmt.Errorf("alert threshold must be 1-60000 ms"), p.window)
			return
		}
		value := uint32(parsed)
		p.send(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{AlertThresholdMillis: &value}})
	}, p.window).Show()
}

func (p *settingsPage) update(binary core.BinarySnapshot, monitor core.MonitorSnapshot, systemProxy core.SystemProxySnapshot, dns core.DNSSnapshot) {
	p.dns.update(dns)
	monitorChanged := !p.settingsInitialized || p.monitorState != monitor
	proxyChanged := !p.settingsInitialized || p.systemProxyState != systemProxy
	p.settingsInitialized = true
	p.monitorState, p.systemProxyState = monitor, systemProxy
	p.binaryPath = binary.Desired
	for i, value := range binaryFieldValues(binary) {
		if p.binaryValues[i].Text != value {
			p.binaryValues[i].SetText(value)
		}
	}
	if monitorChanged {
		p.threshold.SetText(monitorThresholdLabel(monitor))
	}
	if monitorChanged || proxyChanged {
		p.refreshing = true
		if proxyChanged {
			p.systemProxy.SetChecked(systemProxy.Enabled)
			if systemProxy.Active {
				p.proxyStatus.SetText("Active on the local Mihomo listener")
			} else if systemProxy.Enabled {
				p.proxyStatus.SetText("Requested; waiting for a ready listener")
			} else {
				p.proxyStatus.SetText("Inactive")
			}
		}
		if monitorChanged {
			p.monitor.SetChecked(monitor.Enabled)
			p.intervals = make(map[string]uint32)
			p.interval.Options = nil
			for _, seconds := range []uint32{5, 15, 30, 60, 300, 900} {
				p.addInterval(seconds)
			}
			if monitor.IntervalSeconds > 0 && monitor.IntervalSeconds <= 86400 {
				p.addInterval(uint32(monitor.IntervalSeconds))
			}
			p.interval.Selected = ""
			if monitor.IntervalSeconds > 0 && monitor.IntervalSeconds <= 86400 {
				p.interval.Selected = fmt.Sprintf("%d seconds", monitor.IntervalSeconds)
			}
			p.interval.Refresh()
		}
		p.refreshing = false
	}
}

func (p *settingsPage) addInterval(seconds uint32) {
	label := strconv.FormatUint(uint64(seconds), 10) + " seconds"
	if _, exists := p.intervals[label]; exists {
		return
	}
	p.intervals[label] = seconds
	p.interval.Options = append(p.interval.Options, label)
}
