package config

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fishman/clashpulse/monitor"
)

const (
	maxMonitorConcurrency           = 64
	maxMonitorConsecutiveBadSamples = 5
	maxMonitorInterval              = 24 * time.Hour
)

var resourceKinds = map[ResourceKind]struct{}{
	ResourceGeoIP:        {},
	ResourceGeoSite:      {},
	ResourceMMDB:         {},
	ResourceRuleProvider: {},
	ResourceRuleSet:      {},
}

var ruleFormats = map[ResourceFormat]struct{}{
	FormatYAML: {},
	FormatText: {},
	FormatMRS:  {},
}

var subscriptionRoutes = map[string]struct{}{
	"":             {},
	"direct":       {},
	"system_proxy": {},
	"mihomo_proxy": {},
}

func Load(dir string) (Snapshot, error) {
	app, mihomo, monitor, dns, err := loadConfig(filepath.Join(dir, "config.toml"))
	if err != nil {
		return Snapshot{}, err
	}
	subscriptions, err := loadSubscriptions(filepath.Join(dir, "subscriptions.toml"))
	if err != nil {
		return Snapshot{}, err
	}
	resources, resolverSets, routes, err := loadResources(filepath.Join(dir, "resources.toml"))
	if err != nil {
		return Snapshot{}, err
	}
	filters, err := loadFilters(filepath.Join(dir, "filters.toml"))
	if err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		App:           app,
		Mihomo:        mihomo,
		Monitor:       monitor,
		DNS:           dns,
		Subscriptions: subscriptions,
		Resources:     resources,
		Filters:       filters,
	}
	snapshot.DNS.ResolverSets = resolverSets
	snapshot.DNS.Routes = routes
	applyDefaults(&snapshot)
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return cloneSnapshot(snapshot), nil
}

func applyDefaults(s *Snapshot) {
	if s.Mihomo.Binary == "" {
		s.Mihomo.Binary = "system"
	}
	if s.Monitor.TestURL == "" {
		s.Monitor.TestURL = "http://cp.cloudflare.com/generate_204"
	}
	if s.Monitor.SwitchPolicy == "" {
		s.Monitor.SwitchPolicy = monitor.SwitchFailover
	}
	if s.Monitor.Interval == 0 {
		s.Monitor.Interval = time.Minute
	}
	if s.Monitor.Timeout == 0 {
		s.Monitor.Timeout = 5 * time.Second
		if s.Monitor.Timeout > s.Monitor.Interval {
			s.Monitor.Timeout = s.Monitor.Interval
		}
	}
	if s.Monitor.Concurrency == 0 {
		s.Monitor.Concurrency = 3
	}
	if s.Monitor.Threshold == 0 {
		s.Monitor.Threshold = 800 * time.Millisecond
	}
	if s.Monitor.ConsecutiveBadSamples == 0 {
		s.Monitor.ConsecutiveBadSamples = 3
	}
	if s.Monitor.MinImprovement == 0 {
		s.Monitor.MinImprovement = 100 * time.Millisecond
	}
	if s.Monitor.AlertThreshold == 0 {
		s.Monitor.AlertThreshold = 250 * time.Millisecond
	}
	if s.DNS.Listen == "" {
		s.DNS.Listen = "127.0.0.1:1053"
	}
	for i := range s.Subscriptions {
		if s.Subscriptions[i].Name == "" {
			s.Subscriptions[i].Name = s.Subscriptions[i].ID
		}
		if s.Subscriptions[i].RefreshInterval == 0 {
			s.Subscriptions[i].RefreshInterval = 12 * time.Hour
		}
		if s.Subscriptions[i].Timeout == 0 {
			s.Subscriptions[i].Timeout = 30 * time.Second
		}
		if s.Subscriptions[i].Route == "" {
			s.Subscriptions[i].Route = "direct"
		}
	}
	for i := range s.Resources {
		if s.Resources[i].Interval == 0 {
			s.Resources[i].Interval = 12 * time.Hour
		}
	}
}

func validateSnapshot(s Snapshot) error {
	if err := validateMihomoBinary(s.Mihomo.Binary); err != nil {
		return err
	}
	if err := validateMonitor(s.Monitor); err != nil {
		return err
	}
	if err := validateMihomoURLTest(s.Mihomo); err != nil {
		return err
	}
	if err := validateDNS(s.DNS); err != nil {
		return err
	}

	subscriptionIDs := map[string]string{}
	resourceIDs := map[string]string{}
	resolverSetIDs := map[string]string{}
	filterIDs := map[string]string{}
	resourcesByID := make(map[string]Resource, len(s.Resources))
	resolverSetsByID := make(map[string]ResolverSet, len(s.DNS.ResolverSets))
	geoDestinations := map[ResourceKind]string{}

	for _, item := range s.Subscriptions {
		if !validStableID(item.ID) {
			return fmt.Errorf("subscriptions.toml: subscription.id: invalid stable ID %q", item.ID)
		}
		if item.URL == "" {
			return fmt.Errorf("subscriptions.toml: subscription.url: required")
		}
		if err := uniqueID(subscriptionIDs, "subscription", item.ID); err != nil {
			return fmt.Errorf("subscriptions.toml: subscription.id: %w", err)
		}
		if err := validateSubscriptionURL(item.URL, item.AllowHTTP); err != nil {
			return fmt.Errorf("subscriptions.toml: subscription.url: %w", err)
		}
		if !ValidSubscriptionUserAgent(item.UserAgent) {
			return fmt.Errorf("subscriptions.toml: subscription.user_agent: must be printable ASCII at most 256 bytes")
		}
		if _, ok := subscriptionRoutes[item.Route]; !ok {
			return fmt.Errorf("subscriptions.toml: subscription.route: unknown route %q", item.Route)
		}
	}
	for _, item := range s.Resources {
		if !validStableID(item.ID) {
			return fmt.Errorf("resources.toml: resource.id: invalid stable ID %q", item.ID)
		}
		if item.URL == "" {
			return fmt.Errorf("resources.toml: resource.url: required")
		}
		if err := uniqueID(resourceIDs, "resource", item.ID); err != nil {
			return fmt.Errorf("resources.toml: resource.id: %w", err)
		}
		if _, ok := resourceKinds[item.Kind]; !ok {
			return fmt.Errorf("resources.toml: resource.kind: unknown kind %q", item.Kind)
		}
		if err := validateResourceFormat(item); err != nil {
			return fmt.Errorf("resources.toml: resource.format: %w", err)
		}
		if err := validateSourceOrPath(item.URL, true); err != nil {
			return fmt.Errorf("resources.toml: resource.url: %w", err)
		}
		if item.Interval <= 0 {
			return fmt.Errorf("resources.toml: resource.interval: must be positive")
		}
		if item.SHA256 != "" {
			if len(item.SHA256) != 64 {
				return fmt.Errorf("resources.toml: resource.sha256: must be 64 hexadecimal characters")
			}
			if _, err := hex.DecodeString(item.SHA256); err != nil {
				return fmt.Errorf("resources.toml: resource.sha256: must be hexadecimal")
			}
		}
		if item.Kind == ResourceGeoIP || item.Kind == ResourceGeoSite || item.Kind == ResourceMMDB {
			if previous, ok := geoDestinations[item.Kind]; ok {
				return fmt.Errorf("resources.toml: resource.kind: %q and %q both target %s", previous, item.ID, item.Kind)
			}
			geoDestinations[item.Kind] = item.ID
		}
		resourcesByID[item.ID] = item
	}
	for _, set := range s.DNS.ResolverSets {
		if !validStableID(set.ID) {
			return fmt.Errorf("resources.toml: resolver_set.id: invalid stable ID %q", set.ID)
		}
		if len(set.Endpoints) == 0 {
			return fmt.Errorf("resources.toml: resolver_set.endpoints: required")
		}
		if err := uniqueID(resolverSetIDs, "resolver_set", set.ID); err != nil {
			return fmt.Errorf("resources.toml: resolver_set.id: %w", err)
		}
		for _, endpoint := range set.Endpoints {
			if err := validateResolverEndpoint(endpoint); err != nil {
				return fmt.Errorf("resources.toml: resolver_set.endpoints: %w", err)
			}
		}
		resolverSetsByID[set.ID] = set
	}
	listenHost, listenPort, _ := net.SplitHostPort(s.DNS.Listen)
	listenPortNumber, _ := strconv.Atoi(listenPort)
	listenPort = strconv.Itoa(listenPortNumber)

	for _, set := range s.DNS.ResolverSets {
		for _, endpoint := range set.Endpoints {
			host, port, scheme, err := parseResolverEndpoint(endpoint)
			if err != nil {
				return fmt.Errorf("resources.toml: resolver_set.endpoints: %w", err)
			}
			if set.DNSCrypt && (!isLoopbackHost(host) || (scheme != "udp" && scheme != "tcp")) {
				return fmt.Errorf("resources.toml: resolver_set.dnscrypt: listener must be a loopback UDP or TCP endpoint")
			}
			if port == listenPort && isLoopbackHost(host) && isLocalListenHost(listenHost) {
				return fmt.Errorf("resources.toml: resolver_set.endpoints: endpoint %q loops back to dns.listen", endpoint)
			}
		}
	}
	for _, route := range s.DNS.Routes {
		if route.ResolverSet == "" {
			return fmt.Errorf("resources.toml: dns_route.resolver_set: required")
		}
		matchers := 0
		if route.Suffix != "" {
			matchers++
			if !validDomain(route.Suffix) {
				return fmt.Errorf("resources.toml: dns_route.suffix: invalid domain %q", route.Suffix)
			}
		}
		if route.GeoSite != "" {
			matchers++
			if !validRuleToken(route.GeoSite) {
				return fmt.Errorf("resources.toml: dns_route.geosite: invalid selector %q", route.GeoSite)
			}
		}
		if route.Resource != "" {
			matchers++
			resource, ok := resourcesByID[route.Resource]
			if !ok || resource.Kind != ResourceRuleSet || resource.RuleType != RuleDomain || !resource.Enabled {
				return fmt.Errorf("resources.toml: dns_route.resource: %q must reference an enabled domain rule-set resource", route.Resource)
			}
		}
		if matchers != 1 {
			return fmt.Errorf("resources.toml: dns_route: specify exactly one of suffix, geosite, or domain rule-set resource")
		}
		if _, ok := resolverSetsByID[route.ResolverSet]; !ok {
			return fmt.Errorf("resources.toml: dns_route.resolver_set: unknown resolver set %q", route.ResolverSet)
		}
	}
	for _, filter := range s.Filters {
		if !validStableID(filter.ID) {
			return fmt.Errorf("filters.toml: filter.id: invalid stable ID %q", filter.ID)
		}
		if filter.Resource == "" {
			return fmt.Errorf("filters.toml: filter.resource: required")
		}
		if filter.Target == "" || strings.TrimSpace(filter.Target) != filter.Target || strings.ContainsAny(filter.Target, ",\r\n\t\x00") {
			return fmt.Errorf("filters.toml: filter.target: required; commas and control characters are not allowed")
		}
		if err := uniqueID(filterIDs, "filter", filter.ID); err != nil {
			return fmt.Errorf("filters.toml: filter.id: %w", err)
		}
		resource, ok := resourcesByID[filter.Resource]
		if !ok || (resource.Kind != ResourceRuleProvider && resource.Kind != ResourceRuleSet) {
			return fmt.Errorf("filters.toml: filter.resource: %q is not a rule resource", filter.Resource)
		}
		if filter.Format == "" {
			return fmt.Errorf("filters.toml: filter.format: required")
		}
		if _, ok := ruleFormats[filter.Format]; !ok || filter.Format != resource.Format {
			return fmt.Errorf("filters.toml: filter.format: must match resource %q format", filter.Resource)
		}
		if filter.Enabled && !resource.Enabled {
			return fmt.Errorf("filters.toml: filter.resource: enabled filter references disabled resource %q", filter.Resource)
		}
	}
	return nil
}

// Mihomo reads these as whole seconds and milliseconds, so a finer value would be
// silently rounded down.
func validateMihomoURLTest(m Mihomo) error {
	if m.URLTestInterval > 0 && (m.URLTestInterval < time.Second || m.URLTestInterval > maxMonitorInterval) {
		return fmt.Errorf("config.toml: mihomo.url_test_interval: must be between one second and 24 hours")
	}
	if m.URLTestTolerance > 0 && (m.URLTestTolerance < time.Millisecond || m.URLTestTolerance > time.Minute) {
		return fmt.Errorf("config.toml: mihomo.url_test_tolerance: must be between one millisecond and one minute")
	}
	return nil
}

func validateMonitor(m Monitor) error {
	if err := validateMonitorTestURL(m.TestURL); err != nil {
		return fmt.Errorf("config.toml: monitor.test_url: %w", err)
	}
	if m.SwitchPolicy != monitor.SwitchFailover && m.SwitchPolicy != monitor.SwitchLowestLatency {
		return fmt.Errorf("config.toml: monitor.switch_policy: must be %q or %q", monitor.SwitchFailover, monitor.SwitchLowestLatency)
	}
	if m.Interval < time.Second || m.Interval > maxMonitorInterval {
		return fmt.Errorf("config.toml: monitor.interval: must be between one second and 24 hours")
	}
	if m.Timeout <= 0 || m.Timeout > m.Interval {
		return fmt.Errorf("config.toml: monitor.timeout: must be positive and no greater than interval")
	}
	if m.Concurrency < 1 || m.Concurrency > maxMonitorConcurrency {
		return fmt.Errorf("config.toml: monitor.concurrency: must be between 1 and %d", maxMonitorConcurrency)
	}
	if m.Threshold <= 0 {
		return fmt.Errorf("config.toml: monitor.threshold: must be positive")
	}
	if m.AlertThreshold <= 0 {
		return fmt.Errorf("config.toml: monitor.alert_threshold: must be positive")
	}
	if m.ConsecutiveBadSamples < 1 || m.ConsecutiveBadSamples > maxMonitorConsecutiveBadSamples {
		return fmt.Errorf("config.toml: monitor.consecutive_bad_samples: must be between 1 and %d", maxMonitorConsecutiveBadSamples)
	}
	if m.MinImprovement <= 0 {
		return fmt.Errorf("config.toml: monitor.min_improvement: must be positive")
	}
	if m.Cooldown < 0 {
		return fmt.Errorf("config.toml: monitor.cooldown: must be nonnegative")
	}
	if m.Jitter < 0 || m.Jitter >= m.Interval {
		return fmt.Errorf("config.toml: monitor.jitter: must be nonnegative and less than interval")
	}
	seen := make(map[string]bool, len(m.AutomatedGroups))
	for _, id := range m.AutomatedGroups {
		if len(id) != 64 || strings.ToLower(id) != id {
			return fmt.Errorf("config.toml: monitor.automated_groups: invalid group identity")
		}
		if _, err := hex.DecodeString(id); err != nil || seen[id] {
			return fmt.Errorf("config.toml: monitor.automated_groups: invalid or duplicate group identity")
		}
		seen[id] = true
	}
	return nil
}

func validateDNS(d DNS) error {
	if d.Listen == "" {
		return fmt.Errorf("config.toml: dns.listen: required")
	}
	host, port, err := net.SplitHostPort(d.Listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || !validPort(port) {
		return fmt.Errorf("config.toml: dns.listen: must be a loopback host and valid port")
	}
	return nil
}

func validateResourceFormat(resource Resource) error {
	switch resource.Kind {
	case ResourceGeoIP, ResourceGeoSite:
		if resource.Format != FormatDAT {
			return fmt.Errorf("kind %q requires format dat", resource.Kind)
		}
		if resource.RuleType != "" {
			return fmt.Errorf("kind %q does not accept rule_type", resource.Kind)
		}
	case ResourceMMDB:
		if resource.Format != FormatMMDB {
			return fmt.Errorf("kind %q requires format mmdb", resource.Kind)
		}
		if resource.RuleType != "" {
			return fmt.Errorf("kind %q does not accept rule_type", resource.Kind)
		}
	case ResourceRuleSet, ResourceRuleProvider:
		if _, ok := ruleFormats[resource.Format]; !ok {
			return fmt.Errorf("kind %q requires format yaml, text, or mrs", resource.Kind)
		}
		switch resource.RuleType {
		case RuleDomain, RuleIPCIDR:
		case RuleClassical:
			if resource.Format == FormatMRS {
				return fmt.Errorf("kind %q cannot use mrs format with classical rules", resource.Kind)
			}
		default:
			return fmt.Errorf("kind %q requires rule_type domain, ipcidr, or classical", resource.Kind)
		}
	default:
		return fmt.Errorf("unknown kind %q", resource.Kind)
	}
	return nil
}

func validStableID(id string) bool {
	if len(id) == 0 || len(id) > 64 || id == "." || id == ".." {
		return false
	}
	for i, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_' || r == '.')) {
			continue
		}
		return false
	}
	return true
}

func validRuleToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func validDomain(value string) bool {
	value = strings.TrimSuffix(strings.TrimPrefix(value, "."), ".")
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	return true
}

func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

func parseResolverEndpoint(raw string) (host, port, scheme string, err error) {
	if !strings.Contains(raw, "://") {
		host, port, err = net.SplitHostPort(raw)
		if err != nil || host == "" || !validPort(port) {
			return "", "", "", fmt.Errorf("invalid endpoint")
		}
		portNumber, _ := strconv.Atoi(port)
		return host, strconv.Itoa(portNumber), "udp", nil
	}
	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.User != nil || u.Host == "" || u.Opaque != "" {
		return "", "", "", fmt.Errorf("invalid endpoint")
	}
	scheme = strings.ToLower(u.Scheme)
	switch scheme {
	case "udp", "tcp", "tls", "https", "quic":
	default:
		return "", "", "", fmt.Errorf("unsupported endpoint scheme %q", u.Scheme)
	}
	host = u.Hostname()
	if host == "" {
		return "", "", "", fmt.Errorf("invalid endpoint")
	}
	port = u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "tls", "quic":
			port = "853"
		default:
			port = "53"
		}
	}
	if !validPort(port) {
		return "", "", "", fmt.Errorf("invalid endpoint port")
	}
	if scheme != "https" && u.Path != "" && u.Path != "/" {
		return "", "", "", fmt.Errorf("unexpected endpoint path")
	}
	portNumber, _ := strconv.Atoi(port)
	return host, strconv.Itoa(portNumber), scheme, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalListenHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func validateMihomoBinary(binary string) error {
	switch binary {
	case "system", "bundled":
		return nil
	}
	if binary == "" {
		return fmt.Errorf("config.toml: mihomo.binary: required")
	}
	if strings.Contains(binary, "://") {
		return fmt.Errorf("config.toml: mihomo.binary: invalid binary %q", binary)
	}
	if !strings.ContainsAny(binary, "/\\") {
		return fmt.Errorf("config.toml: mihomo.binary: unsupported binary %q", binary)
	}
	info, err := os.Stat(binary)
	if err != nil {
		return fmt.Errorf("config.toml: mihomo.binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("config.toml: mihomo.binary: not executable %q", binary)
	}
	return nil
}

func validateMonitorTestURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if strings.ContainsAny(raw, "\x00\r\n#") || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("must use an HTTP or HTTPS URL without credentials or fragment")
	}
	return nil
}

func validateSubscriptionURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil {
		return fmt.Errorf("userinfo not allowed")
	}
	if u.Scheme == "https" {
		if u.Host == "" || u.Opaque != "" {
			return fmt.Errorf("must use https")
		}
		return nil
	}
	if allowHTTP && u.Scheme == "http" && u.Host != "" && u.Opaque == "" {
		return nil
	}
	return fmt.Errorf("must use https")
}

func ValidSubscriptionUserAgent(value string) bool {
	if len(value) > 256 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func validateSourceOrPath(raw string, allowPath bool) error {
	if allowPath && filepath.IsAbs(raw) {
		if filepath.Separator == '\\' && strings.HasPrefix(raw, `\\`) {
			return fmt.Errorf("network share paths are not local resources")
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme == "" {
		return fmt.Errorf("must use https or an absolute local path")
	}
	if u.User != nil {
		return fmt.Errorf("userinfo not allowed")
	}
	if u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("must use https")
	}
	return nil
}

func validateResolverEndpoint(raw string) error {
	if _, _, _, err := parseResolverEndpoint(raw); err != nil {
		return err
	}
	return nil
}

func uniqueID(seen map[string]string, kind, id string) error {
	if prev, ok := seen[id]; ok {
		return fmt.Errorf("duplicate id %q used by %s and %s", id, prev, kind)
	}
	seen[id] = kind
	return nil
}
