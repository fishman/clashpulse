package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

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
		return "Active local profile"
	}
	if snapshot.ActiveSource == "none" {
		return "No active profile"
	}
	for _, subscription := range snapshot.Subscriptions {
		if !subscription.Active {
			continue
		}
		label := "Profile " + subscription.ID + ": "
		if latency, ok := selectedProfileLatency(snapshot); ok {
			return label + "last selected median " + strconv.FormatInt(latency, 10) + " ms"
		}
		return label + "not measured"
	}
	return "No active profile"
}

func trayProxyLatency(snapshot core.Snapshot, groupID, proxyID string) string {
	for _, proxy := range snapshot.Proxies {
		if proxy.GroupID == groupID && proxy.ID == proxyID {
			if proxy.Outcome == "success" && proxy.LatencyMillis > 0 {
				return fmt.Sprintf("%d ms", proxy.LatencyMillis)
			}
			if proxy.Outcome == "timeout" || proxy.Outcome == "error" {
				return "unavailable"
			}
			break
		}
	}
	return "not measured"
}

func (d *desktopUI) trayMenu(snapshot core.Snapshot) *fyne.Menu {
	if !d.connected {
		status := fyne.NewMenuItem("Disconnected - state unavailable", nil)
		status.Disabled = true
		return fyne.NewMenu("ClashPulse", status, fyne.NewMenuItemSeparator(), fyne.NewMenuItem("Show ClashPulse", d.window.Show), fyne.NewMenuItem("Quit", d.quit))
	}
	status := fyne.NewMenuItem(profileTrayTitle(snapshot), nil)
	status.Disabled = true
	profiles := make([]*fyne.MenuItem, 0, len(snapshot.Subscriptions))
	for _, subscription := range snapshot.Subscriptions {
		id := subscription.ID
		label := id + " (" + sourceHostLabel(subscription.SourceHost) + ")"
		if subscription.PendingActivation {
			label += " - update ready"
		}
		item := fyne.NewMenuItem(label, func() { d.enqueue(ipc.Command{Kind: ipc.CommandActivateSubscription, SubscriptionID: id}) })
		item.Checked = subscription.Active
		item.Disabled = !subscription.Enabled || subscription.HashPrefix == ""
		profiles = append(profiles, item)
	}
	if len(profiles) == 0 {
		empty := fyne.NewMenuItem("No downloaded profiles", nil)
		empty.Disabled = true
		profiles = append(profiles, empty)
	}
	profileMenu := fyne.NewMenuItem("Profiles", nil)
	profileMenu.ChildMenu = fyne.NewMenu("Profiles", profiles...)

	groups := make([]*fyne.MenuItem, 0, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		if group.Type != "Selector" && group.Type != "select" {
			continue
		}
		groupID := group.ID
		choices := make([]*fyne.MenuItem, 0, len(group.Proxies))
		for i, proxyID := range group.Proxies {
			id := proxyID
			name := fmt.Sprintf("Proxy %d [%s]", i+1, shortID(id))
			for _, proxy := range snapshot.Proxies {
				if proxy.GroupID == groupID && proxy.ID == id && proxy.Label != "" {
					name = proxy.Label
					break
				}
			}
			label := name + " - " + trayProxyLatency(snapshot, groupID, id)
			item := fyne.NewMenuItem(label, func() { d.enqueue(ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: groupID, ChoiceID: id}) })
			item.Checked = id == group.Selected
			choices = append(choices, item)
		}
		if len(choices) == 0 {
			continue
		}
		name := group.Label
		if name == "" {
			name = "Group " + shortID(groupID)
		}
		item := fyne.NewMenuItem(name, nil)
		item.ChildMenu = fyne.NewMenu(name, choices...)
		groups = append(groups, item)
	}
	if len(groups) == 0 {
		empty := fyne.NewMenuItem("No managed select groups", nil)
		empty.Disabled = true
		groups = append(groups, empty)
	}
	proxyMenu := fyne.NewMenuItem("Proxies", nil)
	proxyMenu.ChildMenu = fyne.NewMenu("Proxies", groups...)
	systemProxy := fyne.NewMenuItem("System Proxy", func() {
		enabled := !snapshot.SystemProxy.Enabled
		if snapshot.SystemProxy.Active && !snapshot.SystemProxy.Enabled {
			enabled = false
		}
		d.enqueue(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{SystemProxyEnabled: &enabled}})
	})
	systemProxy.Checked = snapshot.SystemProxy.Enabled
	if snapshot.SystemProxy.Enabled && !snapshot.SystemProxy.Active {
		systemProxy.Label = "System Proxy (requested, inactive)"
	} else if !snapshot.SystemProxy.Enabled && snapshot.SystemProxy.Active {
		systemProxy.Label = "System Proxy (restore needed)"
	}
	return fyne.NewMenu("ClashPulse", status, profileMenu, proxyMenu, systemProxy, fyne.NewMenuItemSeparator(), fyne.NewMenuItem("Show ClashPulse", d.window.Show), fyne.NewMenuItem("Quit", d.quit))
}

func trayStateSignature(snapshot core.Snapshot, connected bool) string {
	if !connected {
		return "disconnected"
	}
	var key strings.Builder
	key.WriteString(profileTrayTitle(snapshot))
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
