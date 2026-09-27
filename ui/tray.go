package ui

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/systray"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
)

// primeTrayTitle sets the StatusNotifier title before Fyne starts the tray.
// Fyne applies the title from a goroutine that races item registration, and
// systray falls back to "systray_<pid>" as the item id when the title is still
// empty, so a panel can name the icon after a pid. Priming with the value Fyne
// will use makes the id the same on every launch.
func primeTrayTitle(a fyne.App) {
	switch runtime.GOOS {
	case "linux", "openbsd", "freebsd", "netbsd":
	default:
		return
	}
	title := a.Metadata().Name
	if title == "" {
		title = a.UniqueID()
	}
	systray.SetTitle(title)
}

// Profile latency is the median of last successful selected-proxy measurements
// across select and Mihomo URLTest groups. The UI makes no network requests.
func selectedProfileLatency(snapshot core.Snapshot) (int64, bool) {
	latencies := make([]int64, 0, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		if group.Type != "Selector" && group.Type != "select" && group.Type != "URLTest" && group.Type != "url-test" {
			continue
		}
		found := false
		for _, proxy := range snapshot.Proxies {
			if proxy.GroupID == group.ID && proxy.ID == group.Selected && proxy.Outcome == "success" && proxy.LatencyMillis > 0 {
				latencies = append(latencies, proxy.LatencyMillis)
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	if len(latencies) == 0 {
		return 0, false
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	middle := len(latencies) / 2
	if len(latencies)%2 != 0 {
		return latencies[middle], true
	}
	return latencies[middle-1] + (latencies[middle]-latencies[middle-1])/2, true
}

func profileTrayTitle(snapshot core.Snapshot) string {
	if snapshot.ActiveSource == "local" {
		return localize.T("Active local profile")
	}
	if snapshot.ActiveSource == "none" {
		return localize.T("No active profile")
	}
	for _, subscription := range snapshot.Subscriptions {
		if !subscription.Active {
			continue
		}
		if latency, ok := selectedProfileLatency(snapshot); ok {
			return fmt.Sprintf(localize.T("Profile %s: last selected median %d ms"), subscription.ID, latency)
		}
		return fmt.Sprintf(localize.T("Profile %s: not measured"), subscription.ID)
	}
	return localize.T("No active profile")
}

const (
	trayRankMeasured = iota
	trayRankUnmeasured
	trayRankUnavailable
)

// trayProxyMeasurement is the tray's label and sort key for one group member.
// rank orders measured proxies first, then unmeasured, then known failures, so a
// mixed list still reads fastest-first instead of listing unknowns up front.
type trayProxyMeasurement struct {
	text    string
	latency int64
	rank    int
}

func trayProxyMeasurementOf(snapshot core.Snapshot, groupID, proxyID string) trayProxyMeasurement {
	for _, proxy := range snapshot.Proxies {
		if proxy.GroupID != groupID || proxy.ID != proxyID {
			continue
		}
		if proxy.Outcome == "success" && proxy.LatencyMillis > 0 {
			return trayProxyMeasurement{text: fmt.Sprintf(localize.T("%d ms"), proxy.LatencyMillis), latency: proxy.LatencyMillis, rank: trayRankMeasured}
		}
		if proxy.Outcome == "timeout" || proxy.Outcome == "error" {
			return trayProxyMeasurement{text: localize.T("unavailable"), rank: trayRankUnavailable}
		}
		if proxy.MihomoMillis > 0 {
			// Reported by mihomo, not measured here, so it never reorders the menu.
			return trayProxyMeasurement{text: fmt.Sprintf(localize.T("%d ms (mihomo)"), proxy.MihomoMillis), rank: trayRankUnmeasured}
		}
		break
	}
	return trayProxyMeasurement{text: localize.T("not measured"), rank: trayRankUnmeasured}
}

func trayProxyLatency(snapshot core.Snapshot, groupID, proxyID string) string {
	return trayProxyMeasurementOf(snapshot, groupID, proxyID).text
}

type trayProxyChoice struct {
	item    *fyne.MenuItem
	latency int64
	rank    int
}

// trayMenu always lists every section. A disconnected or stopped service keeps
// the structure and disables what it cannot serve, so the menu never reshapes
// under the pointer.
func (d *desktopUI) trayMenu(snapshot core.Snapshot) *fyne.Menu {
	status := fyne.NewMenuItem(profileTrayTitle(snapshot), nil)
	status.Disabled = true
	if !d.connected {
		status.Label = localize.T("Disconnected - state unavailable")
	}
	profiles := make([]*fyne.MenuItem, 0, len(snapshot.Subscriptions))
	for _, subscription := range snapshot.Subscriptions {
		id := subscription.ID
		label := fmt.Sprintf(localize.T("%s (%s)"), id, sourceHostLabel(subscription.SourceHost))
		if subscription.PendingActivation {
			label += localize.T(" - update ready")
		}
		item := fyne.NewMenuItem(label, func() { d.enqueue(ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: id}) })
		item.Checked = subscription.Active
		item.Disabled = !d.connected || !subscription.Enabled || subscription.HashPrefix == ""
		profiles = append(profiles, item)
	}
	if len(profiles) == 0 {
		empty := fyne.NewMenuItem(localize.T("No downloaded profiles"), nil)
		empty.Disabled = true
		profiles = append(profiles, empty)
	}
	profileMenu := fyne.NewMenuItem(localize.T("Profiles"), nil)
	profileMenu.ChildMenu = fyne.NewMenu(localize.T("Profiles"), profiles...)

	groups := make([]*fyne.MenuItem, 0, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		if group.Type != "Selector" && group.Type != "select" {
			continue
		}
		groupID := group.ID
		choices := make([]*fyne.MenuItem, 0, len(group.Proxies))
		ordered := make([]trayProxyChoice, 0, len(group.Proxies))
		for i, proxyID := range group.Proxies {
			id := proxyID
			// The numbered fallback keeps the profile's own position, so a label
			// stays stable as the latency order changes around it.
			name := fmt.Sprintf(localize.T("Proxy %d [%s]"), i+1, shortID(id))
			for _, proxy := range snapshot.Proxies {
				if proxy.GroupID == groupID && proxy.ID == id && proxy.Label != "" {
					name = proxy.Label
					break
				}
			}
			measurement := trayProxyMeasurementOf(snapshot, groupID, id)
			item := fyne.NewMenuItem(fmt.Sprintf(localize.T("%s - %s"), name, measurement.text), func() { d.enqueue(ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: groupID, ChoiceID: id}) })
			item.Checked = id == group.Selected
			item.Disabled = !d.connected
			ordered = append(ordered, trayProxyChoice{item: item, rank: measurement.rank, latency: measurement.latency})
		}
		// SliceStable keeps the profile's own order between equal measurements.
		sort.SliceStable(ordered, func(i, j int) bool {
			if ordered[i].rank != ordered[j].rank {
				return ordered[i].rank < ordered[j].rank
			}
			return ordered[i].latency < ordered[j].latency
		})
		for _, choice := range ordered {
			choices = append(choices, choice.item)
		}
		if len(choices) == 0 {
			continue
		}
		name := group.Label
		if name == "" {
			name = fmt.Sprintf(localize.T("Group %s"), shortID(groupID))
		}
		item := fyne.NewMenuItem(name, nil)
		item.ChildMenu = fyne.NewMenu(name, choices...)
		groups = append(groups, item)
	}
	if len(groups) == 0 {
		empty := fyne.NewMenuItem(localize.T("No managed select groups"), nil)
		empty.Disabled = true
		groups = append(groups, empty)
	}
	proxyMenu := fyne.NewMenuItem(localize.T("Proxies"), nil)
	proxyMenu.ChildMenu = fyne.NewMenu(localize.T("Proxies"), groups...)
	service := fyne.NewMenuItem(localize.T("Service"), func() {
		kind := ipc.CommandStart
		if snapshot.ServiceRunning {
			kind = ipc.CommandStop
		}
		d.enqueue(ipc.Command{Kind: kind})
	})
	service.Checked = snapshot.ServiceRunning
	systemProxy := fyne.NewMenuItem(localize.T("System Proxy"), func() {
		enabled := !snapshot.SystemProxy.Enabled
		if snapshot.SystemProxy.Active && !snapshot.SystemProxy.Enabled {
			enabled = false
		}
		d.enqueue(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{SystemProxyEnabled: &enabled}})
	})
	systemProxy.Checked = snapshot.SystemProxy.Enabled
	systemProxy.Disabled = !d.connected
	if snapshot.SystemProxy.Enabled && !snapshot.SystemProxy.Active {
		systemProxy.Label = localize.T("System Proxy (requested, inactive)")
	} else if !snapshot.SystemProxy.Enabled && snapshot.SystemProxy.Active {
		systemProxy.Label = localize.T("System Proxy (restore needed)")
	}
	return fyne.NewMenu("ClashPulse", status, profileMenu, proxyMenu, service, systemProxy, fyne.NewMenuItemSeparator(), fyne.NewMenuItem(localize.T("Show ClashPulse"), d.window.Show), fyne.NewMenuItem(localize.T("Quit"), d.quit))
}

func trayStateSignature(snapshot core.Snapshot, connected bool) string {
	if !connected {
		return "disconnected"
	}
	var key strings.Builder
	key.WriteString(profileTrayTitle(snapshot))
	fmt.Fprintf(&key, "|service:%t", snapshot.ServiceRunning)
	fmt.Fprintf(&key, "|system-proxy:%t:%t", snapshot.SystemProxy.Enabled, snapshot.SystemProxy.Active)
	for _, sub := range snapshot.Subscriptions {
		fmt.Fprintf(&key, "|%s:%s:%t:%t:%t:%s", sub.ID, sub.SourceHost, sub.Enabled, sub.Active, sub.PendingActivation, sub.HashPrefix)
	}
	for _, group := range snapshot.Groups {
		fmt.Fprintf(&key, "|%s:%s:%s:%s", group.ID, group.Label, group.Type, group.Selected)
		for _, id := range group.Proxies {
			fmt.Fprintf(&key, "|%s:%s", id, trayProxyLatency(snapshot, group.ID, id))
		}
	}
	return key.String()
}

func (d *desktopUI) updateTray(snapshot core.Snapshot) {
	if d.tray == nil {
		return
	}
	signature := trayStateSignature(snapshot, d.connected)
	if signature == d.traySignature {
		return
	}
	d.traySignature = signature
	d.tray.SetSystemTrayMenu(d.trayMenu(snapshot))
}
