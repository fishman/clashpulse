package mihomo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/dns"
	"github.com/fishman/clashpulse/filters"
	"gopkg.in/yaml.v3"
)

// ManagedPaths maps stable resource IDs to validated private local files.
type ManagedPaths map[string]string

type CapabilityError struct {
	ResourceID string
	Kind       config.ResourceKind
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("mihomo: resource %q requires %s capability", e.ResourceID, e.Kind)
}

type ControllerSettings struct {
	Address   string
	Secret    string
	HomeDir   string
	ProxyPort int
}

// TunDevice is fixed so a user can match it in a NetworkManager
// unmanaged-devices rule.
const TunDevice = "clashpulse0"

// Render generates a new configuration without modifying the source profile.
func Render(profile []byte, intent config.Snapshot, paths ManagedPaths, controller ControllerSettings, capability Capability) ([]byte, error) {
	host, _, err := net.SplitHostPort(controller.Address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || controller.Secret == "" {
		return nil, fmt.Errorf("mihomo: controller requires a loopback address and secret")
	}
	var document map[string]any
	if err := yaml.Unmarshal(profile, &document); err != nil {
		return nil, fmt.Errorf("mihomo: invalid source profile YAML: %w", err)
	}
	if document == nil || (document["proxies"] == nil && document["proxy-providers"] == nil) {
		return nil, fmt.Errorf("mihomo: source profile requires proxies or proxy-providers")
	}
	if tun, ok := document["tun"].(map[string]any); ok && tun["enable"] == true {
		return nil, fmt.Errorf("mihomo: source profile requests unsupported TUN mode")
	}
	if intent.Tun.Enabled {
		if !capability.SupportsTUN {
			return nil, &CapabilityError{ResourceID: "tun", Kind: "tun"}
		}
		if !slices.Contains(core.TunStacks, intent.Tun.Stack) {
			return nil, fmt.Errorf("mihomo: invalid TUN stack")
		}
		document["tun"] = map[string]any{
			"enable":                true,
			"stack":                 intent.Tun.Stack,
			"device":                TunDevice,
			"auto-route":            true,
			"auto-detect-interface": true,
			"dns-hijack":            []string{"any:53"},
			"mtu":                   1500,
			"strict-route":          false,
			"auto-redirect":         false,
		}
	}
	for _, field := range []string{"listeners", "tunnels"} {
		if raw := document[field]; raw != nil {
			items, ok := raw.([]any)
			if !ok || len(items) > 0 {
				return nil, fmt.Errorf("mihomo: source profile requests unsupported %s", field)
			}
		}
	}
	if controller.ProxyPort < 0 || controller.ProxyPort > 65535 {
		return nil, fmt.Errorf("mihomo: invalid managed proxy port")
	}
	if controller.ProxyPort > 0 {
		document["mixed-port"] = controller.ProxyPort
		document["port"] = 0
		document["socks-port"] = 0
	}
	for _, field := range []string{"redir-port", "tproxy-port"} {
		if raw := document[field]; raw != nil && raw != 0 {
			return nil, fmt.Errorf("mihomo: source profile requests unsupported %s", field)
		}
	}
	// A source profile must never choose a second controller or expose the managed one.
	for _, key := range []string{"external-controller-unix", "external-controller-pipe", "external-controller-tls", "external-controller-cors", "external-controller-routing-mark", "external-doh-server", "external-ui", "external-ui-name", "external-ui-url"} {
		delete(document, key)
	}
	document["external-controller"] = controller.Address
	document["secret"] = controller.Secret
	document["allow-lan"] = false
	document["geo-auto-update"] = false
	document["bind-address"] = "127.0.0.1"

	resources := append([]config.Resource(nil), intent.Resources...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].ID < resources[j].ID })
	providers, err := existingProviders(document)
	if err != nil {
		return nil, err
	}
	registry, err := filters.Build(intent, paths)
	if err != nil {
		return nil, err
	}
	for _, resource := range resources {
		if !resource.Enabled {
			continue
		}
		if resource.Kind == config.ResourceRuleSet || resource.Kind == config.ResourceRuleProvider {
			continue
		}
		path := paths[resource.ID]
		if path == "" || !filepath.IsAbs(path) {
			return nil, fmt.Errorf("mihomo: resource %q has no validated absolute path", resource.ID)
		}
		switch resource.Kind {
		case "geoip.dat", "geosite.dat", "Country.mmdb":
			var supported bool
			switch resource.Kind {
			case "geoip.dat":
				supported = capability.SupportsGeoIPDat
			case "geosite.dat":
				supported = capability.SupportsGeoSiteDat
			case "Country.mmdb":
				supported = capability.SupportsMMDB
			}
			if !supported {
				return nil, &CapabilityError{ResourceID: resource.ID, Kind: resource.Kind}
			}
			if controller.HomeDir == "" || !filepath.IsAbs(controller.HomeDir) || filepath.Clean(filepath.Dir(path)) != filepath.Clean(controller.HomeDir) || filepath.Base(path) != string(resource.Kind) {
				return nil, fmt.Errorf("mihomo: resource %q must use Mihomo home filename %s", resource.ID, resource.Kind)
			}
			document["geo-auto-update"] = false
		default:
			return nil, fmt.Errorf("mihomo: resource %q has unsupported kind", resource.ID)
		}
	}
	for _, provider := range registry.Providers() {
		if _, exists := providers[provider.Name]; exists {
			return nil, fmt.Errorf("mihomo: rule provider %q conflicts with source profile", provider.Name)
		}
		providers[provider.Name] = map[string]any{"type": "file", "behavior": provider.Behavior, "format": provider.Format, "path": provider.Path}
	}
	if len(providers) > 0 {
		document["rule-providers"] = providers
	}
	if err := renderFilters(document, registry); err != nil {
		return nil, err
	}
	if err := renderDNS(document, intent); err != nil {
		return nil, err
	}
	if err := renderURLTestDelays(document, intent.Mihomo); err != nil {
		return nil, err
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("mihomo: encode generated configuration: %w", err)
	}
	return encoded, nil
}

// renderURLTestDelays rewrites the health-check pacing of the groups Mihomo
// owns, which is what decides how quickly a profile switches to a faster node.
// A zero duration leaves the group exactly as the profile wrote it.
func renderURLTestDelays(document map[string]any, settings config.Mihomo) error {
	if settings.URLTestInterval <= 0 && settings.URLTestTolerance <= 0 {
		return nil
	}
	raw, ok := document["proxy-groups"]
	if !ok {
		return nil
	}
	groups, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("mihomo: source proxy-groups must be a list")
	}
	for _, item := range groups {
		group, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("mihomo: source proxy group must be a mapping")
		}
		// Only the automatically tested group types read these keys.
		switch group["type"] {
		case "url-test", "fallback":
		default:
			continue
		}
		if settings.URLTestInterval > 0 {
			group["interval"] = int(settings.URLTestInterval / time.Second)
		}
		if settings.URLTestTolerance > 0 {
			group["tolerance"] = int(settings.URLTestTolerance / time.Millisecond)
		}
	}
	return nil
}

type ConfigOverride struct {
	Key, Change string
}

// ExplainOverrides reports only fixed field identities, never profile or generated values.
func ExplainOverrides(source, generated []byte) ([]ConfigOverride, error) {
	var original, effective map[string]any
	if yaml.Unmarshal(source, &original) != nil || yaml.Unmarshal(generated, &effective) != nil || original == nil || effective == nil {
		return nil, errors.New("mihomo: cannot explain invalid configuration")
	}
	fields := [...]struct{ key, section, name string }{
		{"mixed-port", "", "mixed-port"},
		{"port", "", "port"},
		{"socks-port", "", "socks-port"},
		{"external-controller", "", "external-controller"},
		{"secret", "", "secret"},
		{"allow-lan", "", "allow-lan"},
		{"bind-address", "", "bind-address"},
		{"tun", "", "tun"},
		{"dns.listen", "dns", "listen"},
		{"dns.enable", "dns", "enable"},
		{"dns.enhanced-mode", "dns", "enhanced-mode"},
		{"dns.fake-ip-range", "dns", "fake-ip-range"},
		{"dns.fake-ip-range6", "dns", "fake-ip-range6"},
		{"dns.fake-ip-filter", "dns", "fake-ip-filter"},
		{"external-controller-tls", "", "external-controller-tls"},
		{"external-controller-cors", "", "external-controller-cors"},
		{"external-ui", "", "external-ui"},
		{"dns.nameserver-policy", "dns", "nameserver-policy"},
		{"rule-providers", "", "rule-providers"},
		{"proxy-groups", "", "proxy-groups"},
		{"rules", "", "rules"},
	}
	value := func(document map[string]any, section, name string) (any, bool) {
		if section != "" {
			parent, ok := document[section].(map[string]any)
			if !ok {
				return nil, false
			}
			document = parent
		}
		result, ok := document[name]
		return result, ok
	}
	var overrides []ConfigOverride
	for _, field := range fields {
		before, existed := value(original, field.section, field.name)
		after, present := value(effective, field.section, field.name)
		if existed == present && reflect.DeepEqual(before, after) {
			continue
		}
		change := "replaced"
		if !existed {
			change = "added"
		} else if !present {
			change = "removed"
		}
		overrides = append(overrides, ConfigOverride{Key: field.key, Change: change})
	}
	return overrides, nil
}

func existingProviders(document map[string]any) (map[string]any, error) {
	providers := make(map[string]any)
	if raw, ok := document["rule-providers"]; ok {
		mapping, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mihomo: invalid source rule-providers")
		}
		for key, value := range mapping {
			providers[key] = value
		}
	}
	return providers, nil
}

func renderFilters(document map[string]any, registry *filters.Registry) error {
	references := registry.References()
	if len(references) == 0 {
		return nil
	}
	known := map[string]bool{"DIRECT": true, "REJECT": true}
	if raw := document["proxy-groups"]; raw != nil {
		groups, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("mihomo: source proxy-groups must be a list")
		}
		for _, item := range groups {
			group, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("mihomo: source proxy group must be a mapping")
			}
			if name, ok := group["name"].(string); ok {
				known[name] = true
			}
		}
	}
	if raw, ok := document["proxies"].([]any); ok {
		for _, item := range raw {
			if proxy, ok := item.(map[string]any); ok {
				if name, ok := proxy["name"].(string); ok {
					known[name] = true
				}
			}
		}
	}
	var sourceRules []any
	if raw := document["rules"]; raw != nil {
		var ok bool
		sourceRules, ok = raw.([]any)
		if !ok {
			return fmt.Errorf("mihomo: source rules must be a list")
		}
	}
	managed := make([]any, 0, len(references)+len(sourceRules))
	for _, reference := range references {
		if !known[reference.Target] {
			return fmt.Errorf("mihomo: filter %q has an unknown target", reference.FilterID)
		}
		rule, err := registry.RuleSetReference(reference.FilterID)
		if err != nil {
			return err
		}
		managed = append(managed, rule)
	}
	document["rules"] = append(managed, sourceRules...)
	return nil
}

func renderDNS(document map[string]any, intent config.Snapshot) error {
	settings := intent.DNS
	if len(settings.Routes) == 0 && len(settings.ResolverSets) == 0 && document["dns"] == nil && !intent.Tun.Enabled {
		return nil
	}
	listen := settings.Listen
	if listen == "" {
		listen = "127.0.0.1:1053"
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("mihomo: DNS listener must be loopback")
	}
	var policy dns.Policy
	if len(settings.Routes) > 0 || len(settings.ResolverSets) > 0 {
		intent.DNS.Listen = listen
		policy, err = dns.Build(intent)
		if err != nil {
			return err
		}
	}
	result, ok := document["dns"].(map[string]any)
	if !ok {
		if document["dns"] != nil {
			return fmt.Errorf("mihomo: source DNS must be a mapping")
		}
		result = make(map[string]any)
	}
	result["listen"] = listen
	if len(policy) > 0 {
		result["enable"] = true
		result["nameserver-policy"] = policy
	}
	if intent.Tun.Enabled {
		ipv6 := document["ipv6"] == true || result["ipv6"] == true
		if err := renderTunDNS(result, intent.Tun.DNSMode, ipv6); err != nil {
			return err
		}
	}
	document["dns"] = result
	return nil
}

// renderTunDNS gives dns-hijack a resolver to reach. The profile's own fake-ip
// settings win; defaults only fill what it leaves unset.
func renderTunDNS(result map[string]any, mode string, ipv6 bool) error {
	result["enable"] = true
	switch mode {
	case core.TunDNSProfile:
		if result["enhanced-mode"] == nil {
			result["enhanced-mode"] = "fake-ip"
		}
	case core.TunDNSFakeIP, core.TunDNSRedirHost:
		result["enhanced-mode"] = mode
	default:
		return fmt.Errorf("mihomo: invalid TUN DNS mode")
	}
	// Forcing dns.enable without these makes Mihomo silently fall back to its
	// built-in public resolvers, which the user never chose.
	present := func(endpoint string) bool { return endpoint != "" }
	hostname := func(endpoint string) bool { _, ok := resolverAddr(endpoint); return !ok }
	if !anyEndpoint(result["nameserver"], present) {
		return fmt.Errorf("mihomo: TUN requires dns.nameserver in the profile")
	}
	if !anyEndpoint(result["default-nameserver"], present) && anyResolver(result, bootstrappedResolvers, hostname) {
		return fmt.Errorf("mihomo: TUN requires dns.default-nameserver to resolve DNS server hostnames")
	}
	if result["enhanced-mode"] != "fake-ip" {
		return nil
	}
	if result["fake-ip-range"] == nil {
		result["fake-ip-range"] = "198.18.0.1/16"
	}
	if ipv6 && result["fake-ip-range6"] == nil {
		result["fake-ip-range6"] = "2001:2::0/64"
	}
	// LAN and NTP names must keep their real addresses.
	if result["fake-ip-filter"] == nil {
		result["fake-ip-filter"] = []string{"*.lan", "*.local", "*.arpa", "time.*.com", "ntp.*.com", "+.pool.ntp.org"}
	}
	var ranges []netip.Prefix
	for _, key := range []string{"fake-ip-range", "fake-ip-range6"} {
		if result[key] == nil {
			continue
		}
		raw, _ := result[key].(string)
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return fmt.Errorf("mihomo: invalid dns.%s", key)
		}
		ranges = append(ranges, prefix)
	}
	// Mihomo treats every address in the range as one of its own fake IPs, so a
	// resolver there can never be reached.
	inside := func(endpoint string) bool {
		addr, ok := resolverAddr(endpoint)
		for _, prefix := range ranges {
			if ok && prefix.Contains(addr) {
				return true
			}
		}
		return false
	}
	if anyResolver(result, append([]string{"default-nameserver"}, bootstrappedResolvers...), inside) {
		return fmt.Errorf("mihomo: DNS resolver endpoint lies inside the fake-ip range")
	}
	return nil
}

// bootstrappedResolvers are the dns keys whose hostname endpoints Mihomo
// resolves through default-nameserver; nameserver-policy values are too.
var bootstrappedResolvers = []string{"nameserver", "fallback", "proxy-server-nameserver", "direct-nameserver"}

// anyResolver reports whether match holds for an endpoint under keys or in any
// nameserver-policy value.
func anyResolver(section map[string]any, keys []string, match func(string) bool) bool {
	for _, key := range keys {
		if anyEndpoint(section[key], match) {
			return true
		}
	}
	switch policy := section["nameserver-policy"].(type) {
	case dns.Policy:
		for _, endpoints := range policy {
			if anyEndpoint(endpoints, match) {
				return true
			}
		}
	case map[string]any:
		for _, endpoints := range policy {
			if anyEndpoint(endpoints, match) {
				return true
			}
		}
	}
	return false
}

// anyEndpoint reports whether match holds for an endpoint in value, one Mihomo
// resolver string or a list of them.
func anyEndpoint(value any, match func(string) bool) bool {
	switch value := value.(type) {
	case string:
		return match(value)
	case []string:
		return slices.ContainsFunc(value, match)
	case []any:
		for _, endpoint := range value {
			if anyEndpoint(endpoint, match) {
				return true
			}
		}
	}
	return false
}

// resolverAddr extracts the IP literal from endpoints such as "192.0.2.1",
// "192.0.2.1:53", "tls://192.0.2.1:853", or "https://192.0.2.1/dns-query#DIRECT";
// a hostname reports false.
func resolverAddr(endpoint string) (netip.Addr, bool) {
	endpoint, _, _ = strings.Cut(endpoint, "#")
	host := endpoint
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return netip.Addr{}, false
		}
		host = parsed.Hostname()
	} else if split, _, err := net.SplitHostPort(endpoint); err == nil {
		host = split
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return addr.Unmap(), err == nil
}

// Validate asks the selected executable to check a candidate file, never shelling out.
func Validate(ctx context.Context, capability Capability, configPath string) error {
	return ValidateInHome(ctx, capability, configPath, "")
}

// ValidateInHome checks a candidate using the managed geodata directory.
func ValidateInHome(ctx context.Context, capability Capability, configPath, home string) error {
	if capability.Path == "" || configPath == "" {
		return fmt.Errorf("mihomo: validation requires executable and configuration paths")
	}
	argv := []string{"-t", "-f", configPath}
	if home != "" {
		if !filepath.IsAbs(home) {
			return fmt.Errorf("mihomo: managed data home must be absolute")
		}
		argv = append(argv, "-d", home)
	}
	if err := exec.CommandContext(ctx, capability.Path, argv...).Run(); err != nil {
		return fmt.Errorf("mihomo: configuration validation failed: %w", err)
	}
	return nil
}

// MixedPort reads the loopback mixed port from a generated configuration, so
// callers use the port of the runtime that actually runs.
func MixedPort(generated []byte) (int, error) {
	var document struct {
		MixedPort int `yaml:"mixed-port"`
	}
	if err := yaml.Unmarshal(generated, &document); err != nil {
		return 0, fmt.Errorf("mihomo: invalid generated configuration: %w", err)
	}
	if document.MixedPort <= 0 || document.MixedPort > 65535 {
		return 0, fmt.Errorf("mihomo: generated configuration has no mixed port")
	}
	return document.MixedPort, nil
}
