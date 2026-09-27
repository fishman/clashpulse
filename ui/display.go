package ui

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/localize"
)

func sourceHostLabel(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/\\@?#\r\n\t") {
		return localize.T("Unknown source")
	}
	parsed, err := url.Parse("https://" + host)
	if err != nil || parsed.Host != host || parsed.User != nil || parsed.Path != "" {
		return localize.T("Unknown source")
	}
	return host
}

func subscriptionStatus(s core.SubscriptionSnapshot) string {
	switch {
	case !s.Enabled:
		return localize.T("Disabled")
	case s.LastFailure != "":
		if s.Active {
			return localize.T("Active (Needs attention)")
		}
		return localize.T("Needs attention")
	case s.Active && s.PendingActivation:
		return localize.T("Active (update ready)")
	case s.Active:
		return localize.T("Active")
	case s.LastSuccess > 0:
		return localize.T("Ready")
	default:
		return localize.T("Pending")
	}
}

func resourceStatus(r core.ResourceSnapshot) string {
	switch {
	case !r.Enabled:
		return localize.T("Disabled")
	case r.LastResult != "":
		return localize.T("Needs attention")
	case !r.Validated:
		return localize.T("Not validated")
	default:
		return localize.T("Ready")
	}
}

func filterStatus(f core.FilterSnapshot) string {
	if f.Enabled {
		return localize.T("Enabled")
	}
	return localize.T("Disabled")
}

func proxyLatency(snapshot core.Snapshot, groupID, proxyID string) string {
	for _, proxy := range snapshot.Proxies {
		if proxy.GroupID != groupID || proxy.ID != proxyID {
			continue
		}
		if proxy.LatencyMillis > 0 {
			return fmt.Sprintf(localize.T("%d ms"), proxy.LatencyMillis)
		}
		if proxy.MihomoMillis > 0 {
			return fmt.Sprintf(localize.T("%d ms (mihomo)"), proxy.MihomoMillis)
		}
	}
	return ""
}

func monitorThresholdLabel(monitor core.MonitorSnapshot) string {
	if monitor.AlertThresholdMillis <= 0 {
		return localize.T("Not configured")
	}
	return fmt.Sprintf(localize.T("%d ms"), monitor.AlertThresholdMillis)
}

func compatibilityLabel(failure string) string {
	if failure == "" {
		return localize.T("No compatibility issue reported")
	}
	if failure == "bundled Mihomo is not installed" {
		return localize.T("bundled Mihomo is not installed")
	}
	if strings.HasPrefix(failure, "resource ") {
		id, capability, ok := strings.Cut(strings.TrimPrefix(failure, "resource "), " requires ")
		if ok && len(id) > 0 && len(id) <= 64 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == "" {
			for _, known := range []string{"geoip.dat", "geosite.dat", "Country.mmdb"} {
				if capability == known {
					return fmt.Sprintf(localize.T("resource %s requires %s"), id, capability)
				}
			}
		}
	}
	return localize.T("Compatibility issue reported")
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
		return localize.T("No automatic switches recorded")
	}
	event := snapshot.Switches[len(snapshot.Switches)-1]
	measurements := make([]string, 0, len(event.Evidence))
	for _, sample := range event.Evidence {
		measurements = append(measurements, fmt.Sprintf(localize.T("%s %s %d ms"), shortID(sample.ProxyID), localize.Code("probe.outcome", sample.Outcome), sample.LatencyMillis))
	}
	return fmt.Sprintf(localize.T("Last switch %s -> %s: %s. Measurements: %s"), shortID(event.OldID), shortID(event.NewID), localize.T(event.Reason), strings.Join(measurements, "; "))
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func detailTime(value int64) string {
	if value <= 0 {
		return localize.T("not yet")
	}
	return time.Unix(value, 0).UTC().Format("2006-01-02 15:04 UTC")
}

func subscriptionDetails(item core.SubscriptionSnapshot) string {
	lines := []string{
		fmt.Sprintf(localize.T("Source host: %s"), sourceHostLabel(item.SourceHost)),
		fmt.Sprintf(localize.T("State: %s"), subscriptionStatus(item)),
		fmt.Sprintf(localize.T("Last check: %s"), detailTime(item.LastCheck)),
		fmt.Sprintf(localize.T("Last success: %s"), detailTime(item.LastSuccess)),
		fmt.Sprintf(localize.T("Next refresh: %s"), detailTime(item.NextDue)),
		fmt.Sprintf(localize.T("Downloaded hash: %s"), item.HashPrefix),
		fmt.Sprintf(localize.T("Applied hash: %s"), item.AppliedHashPrefix),
	}
	if item.LastFailure != "" {
		lines = append(lines, localize.T("Last failure: needs attention"))
	}
	if item.Usage != nil {
		lines = append(lines, fmt.Sprintf(localize.T("Traffic: up %d, down %d, total %d bytes"), item.Usage.UploadedBytes, item.Usage.DownloadedBytes, item.Usage.TotalBytes))
		if item.Usage.ExpiresAt > 0 {
			lines = append(lines, fmt.Sprintf(localize.T("Expires: %s"), detailTime(item.Usage.ExpiresAt)))
		}
	}
	return strings.Join(lines, "\n")
}

func resourceDetails(item core.ResourceSnapshot) string {
	lines := []string{
		fmt.Sprintf(localize.T("Kind: %s"), item.Kind), fmt.Sprintf(localize.T("Format: %s"), item.Format), fmt.Sprintf(localize.T("Rule behavior: %s"), item.RuleType),
		fmt.Sprintf(localize.T("Source host: %s"), sourceHostLabel(item.SourceHost)), fmt.Sprintf(localize.T("State: %s"), resourceStatus(item)),
		fmt.Sprintf(localize.T("Current hash: %s"), item.HashPrefix), fmt.Sprintf(localize.T("Destination: %s"), item.Destination),
		fmt.Sprintf(localize.T("Last check: %s"), detailTime(item.LastCheck)), fmt.Sprintf(localize.T("Last success: %s"), detailTime(item.LastSuccess)),
		fmt.Sprintf(localize.T("Next update: %s"), detailTime(item.NextDue)),
	}
	if resourceStatus(item) == localize.T("Needs attention") {
		lines = append(lines, localize.T("Last failure: needs attention"))
	}
	return strings.Join(lines, "\n")
}

func filterDetails(item core.FilterSnapshot) string {
	lines := []string{
		fmt.Sprintf(localize.T("Resource: %s"), item.ResourceID), fmt.Sprintf(localize.T("Format: %s"), item.Format), fmt.Sprintf(localize.T("Target: %s"), item.Target),
		fmt.Sprintf(localize.T("Source host: %s"), sourceHostLabel(item.SourceHost)), fmt.Sprintf(localize.T("State: %s"), filterStatus(item)),
		fmt.Sprintf(localize.T("Current hash: %s"), item.HashPrefix), fmt.Sprintf(localize.T("Provider: %s"), item.Destination),
		fmt.Sprintf(localize.T("Last success: %s"), detailTime(item.LastSuccess)), fmt.Sprintf(localize.T("Next update: %s"), detailTime(item.NextDue)),
	}
	if item.LastFailure != "" {
		lines = append(lines, localize.T("Last failure: needs attention"))
	}
	return strings.Join(lines, "\n")
}
