package ui

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fishman/clashpulse/core"
)

func sourceHostLabel(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/\\@?#\r\n\t") {
		return "Unknown source"
	}
	parsed, err := url.Parse("https://" + host)
	if err != nil || parsed.Host != host || parsed.User != nil || parsed.Path != "" {
		return "Unknown source"
	}
	return host
}

func subscriptionStatus(s core.SubscriptionSnapshot) string {
	switch {
	case !s.Enabled:
		return "Disabled"
	case s.LastFailure != "":
		if s.Active {
			return "Active (Needs attention)"
		}
		return "Needs attention"
	case s.Active && s.PendingActivation:
		return "Active (update ready)"
	case s.Active:
		return "Active"
	case s.LastSuccess > 0:
		return "Ready"
	default:
		return "Pending"
	}
}

func resourceStatus(r core.ResourceSnapshot) string {
	switch {
	case !r.Enabled:
		return "Disabled"
	case r.LastResult != "":
		return "Needs attention"
	case !r.Validated:
		return "Not validated"
	default:
		return "Ready"
	}
}

func filterStatus(f core.FilterSnapshot) string {
	if f.Enabled {
		return "Enabled"
	}
	return "Disabled"
}

func proxyLatency(snapshot core.Snapshot, groupID, proxyID string) string {
	for _, proxy := range snapshot.Proxies {
		if proxy.GroupID != groupID || proxy.ID != proxyID {
			continue
		}
		if proxy.LatencyMillis > 0 {
			return fmt.Sprintf("%d ms", proxy.LatencyMillis)
		}
		if proxy.MihomoMillis > 0 {
			return fmt.Sprintf("%d ms (mihomo)", proxy.MihomoMillis)
		}
	}
	return ""
}

func monitorThresholdLabel(monitor core.MonitorSnapshot) string {
	if monitor.AlertThresholdMillis <= 0 {
		return "Not configured"
	}
	return fmt.Sprintf("%d ms", monitor.AlertThresholdMillis)
}

func compatibilityLabel(failure string) string {
	if failure == "" {
		return "No compatibility issue reported"
	}
	if failure == "bundled Mihomo is not installed" {
		return failure
	}
	if strings.HasPrefix(failure, "resource ") {
		id, capability, ok := strings.Cut(strings.TrimPrefix(failure, "resource "), " requires ")
		if ok && len(id) > 0 && len(id) <= 64 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == "" {
			for _, known := range []string{"geoip.dat", "geosite.dat", "Country.mmdb"} {
				if capability == known {
					return failure
				}
			}
		}
	}
	return "Compatibility issue reported"
}

func groupLabel(group core.GroupSnapshot) string {
	label := group.Label
	if label == "" {
		label = group.ID
	}
	if group.Type == "" {
		return label
	}
	return label + " (" + group.Type + ")"
}

func lastSwitchSummary(snapshot core.Snapshot) string {
	if len(snapshot.Switches) == 0 {
		return "No automatic switches recorded"
	}
	event := snapshot.Switches[len(snapshot.Switches)-1]
	measurements := make([]string, 0, len(event.Evidence))
	for _, sample := range event.Evidence {
		measurements = append(measurements, fmt.Sprintf("%s %s %d ms", shortID(sample.ProxyID), sample.Outcome, sample.LatencyMillis))
	}
	return fmt.Sprintf("Last switch %s -> %s: %s. Measurements: %s", shortID(event.OldID), shortID(event.NewID), event.Reason, strings.Join(measurements, "; "))
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func detailTime(value int64) string {
	if value <= 0 {
		return "not yet"
	}
	return time.Unix(value, 0).UTC().Format("2006-01-02 15:04 UTC")
}

func subscriptionDetails(item core.SubscriptionSnapshot) string {
	lines := []string{
		"Source host: " + sourceHostLabel(item.SourceHost),
		"State: " + subscriptionStatus(item),
		"Last check: " + detailTime(item.LastCheck),
		"Last success: " + detailTime(item.LastSuccess),
		"Next refresh: " + detailTime(item.NextDue),
		"Downloaded hash: " + item.HashPrefix,
		"Applied hash: " + item.AppliedHashPrefix,
	}
	if item.LastFailure != "" {
		lines = append(lines, "Last failure: needs attention")
	}
	if item.Usage != nil {
		lines = append(lines, fmt.Sprintf("Traffic: up %d, down %d, total %d bytes", item.Usage.UploadedBytes, item.Usage.DownloadedBytes, item.Usage.TotalBytes))
		if item.Usage.ExpiresAt > 0 {
			lines = append(lines, "Expires: "+detailTime(item.Usage.ExpiresAt))
		}
	}
	return strings.Join(lines, "\n")
}

func resourceDetails(item core.ResourceSnapshot) string {
	lines := []string{
		"Kind: " + item.Kind, "Format: " + item.Format, "Rule behavior: " + item.RuleType,
		"Source host: " + sourceHostLabel(item.SourceHost), "State: " + resourceStatus(item),
		"Current hash: " + item.HashPrefix, "Destination: " + item.Destination,
		"Last check: " + detailTime(item.LastCheck), "Last success: " + detailTime(item.LastSuccess),
		"Next update: " + detailTime(item.NextDue),
	}
	if resourceStatus(item) == "Needs attention" {
		lines = append(lines, "Last failure: needs attention")
	}
	return strings.Join(lines, "\n")
}

func filterDetails(item core.FilterSnapshot) string {
	lines := []string{
		"Resource: " + item.ResourceID, "Format: " + item.Format, "Target: " + item.Target,
		"Source host: " + sourceHostLabel(item.SourceHost), "State: " + filterStatus(item),
		"Current hash: " + item.HashPrefix, "Provider: " + item.Destination,
		"Last success: " + detailTime(item.LastSuccess), "Next update: " + detailTime(item.NextDue),
	}
	if item.LastFailure != "" {
		lines = append(lines, "Last failure: needs attention")
	}
	return strings.Join(lines, "\n")
}
