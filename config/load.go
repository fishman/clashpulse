package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var resourceKinds = map[string]struct{}{
	"geoip.dat":     {},
	"geosite.dat":   {},
	"Country.mmdb":  {},
	"rule-provider": {},
	"rule-set":      {},
	"dns-route":     {},
}

var filterFormats = map[string]struct{}{
	"rule-provider": {},
	"rule-set":      {},
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
		s.Monitor.TestURL = "https://example.invalid/generate_204"
	}
	if s.Monitor.Interval == 0 {
		s.Monitor.Interval = 5 * time.Minute
	}
	if s.DNS.Listen == "" {
		s.DNS.Listen = "127.0.0.1:53"
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
	for i := range s.Filters {
		if s.Filters[i].Interval == 0 {
			s.Filters[i].Interval = 12 * time.Hour
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
	if err := validateDNS(s.DNS); err != nil {
		return err
	}

	subscriptionIDs := map[string]string{}
	resourceIDs := map[string]string{}
	resolverSetIDs := map[string]string{}
	filterIDs := map[string]string{}
	resourcesByID := map[string]Resource{}
	resolverSetsByID := map[string]ResolverSet{}

	for _, item := range s.Subscriptions {
		if item.ID == "" {
			return fmt.Errorf("subscriptions.toml: subscription.id: required")
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
		if item.Route != "" {
			if _, ok := subscriptionRoutes[item.Route]; !ok {
				return fmt.Errorf("subscriptions.toml: subscription.route: unknown route %q", item.Route)
			}
		}
	}
	for _, item := range s.Resources {
		if item.ID == "" {
			return fmt.Errorf("resources.toml: resource.id: required")
		}
		if item.Kind == "" {
			return fmt.Errorf("resources.toml: resource.kind: required")
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
		if err := validateSourceOrPath(item.URL, true); err != nil {
			return fmt.Errorf("resources.toml: resource.url: %w", err)
		}
		if item.Interval <= 0 {
			return fmt.Errorf("resources.toml: resource.interval: must be positive")
		}
		resourcesByID[item.ID] = item
	}
	for _, item := range s.DNS.ResolverSets {
		if item.ID == "" {
			return fmt.Errorf("resources.toml: resolver_set.id: required")
		}
		if len(item.Endpoints) == 0 {
			return fmt.Errorf("resources.toml: resolver_set.endpoints: required")
		}
		if err := uniqueID(resolverSetIDs, "resolver_set", item.ID); err != nil {
			return fmt.Errorf("resources.toml: resolver_set.id: %w", err)
		}
		for _, endpoint := range item.Endpoints {
			if err := validateResolverEndpoint(endpoint); err != nil {
				return fmt.Errorf("resources.toml: resolver_set.endpoints: %w", err)
			}
		}
		resolverSetsByID[item.ID] = item
	}
	for _, item := range s.DNS.Routes {
		if item.ResolverSet == "" {
			return fmt.Errorf("resources.toml: dns_route.resolver_set: required")
		}
		if item.Suffix == "" && item.GeoSite == "" && item.Resource == "" {
			return fmt.Errorf("resources.toml: dns_route.suffix: required")
		}
		if _, ok := resolverSetsByID[item.ResolverSet]; !ok {
			return fmt.Errorf("resources.toml: dns_route.resolver_set: unknown resolver set %q", item.ResolverSet)
		}
		if item.Resource != "" {
			if _, ok := resourcesByID[item.Resource]; !ok {
				return fmt.Errorf("resources.toml: dns_route.resource: unknown resource %q", item.Resource)
			}
		}
	}
	for _, item := range s.Filters {
		if item.ID == "" {
			return fmt.Errorf("filters.toml: filter.id: required")
		}
		if item.Resource == "" {
			return fmt.Errorf("filters.toml: filter.resource: required")
		}
		if err := uniqueID(filterIDs, "filter", item.ID); err != nil {
			return fmt.Errorf("filters.toml: filter.id: %w", err)
		}
		if _, ok := resourcesByID[item.Resource]; !ok {
			return fmt.Errorf("filters.toml: filter.resource: unknown resource %q", item.Resource)
		}
		if item.Format == "" {
			return fmt.Errorf("filters.toml: filter.format: required")
		}
		if _, ok := filterFormats[item.Format]; !ok {
			return fmt.Errorf("filters.toml: filter.format: unsupported format %q", item.Format)
		}
		if item.Interval <= 0 {
			return fmt.Errorf("filters.toml: filter.interval: must be positive")
		}
		if item.URL != "" {
			if err := validateSourceOrPath(item.URL, true); err != nil {
				return fmt.Errorf("filters.toml: filter.url: %w", err)
			}
		}
		if item.Path != "" {
			if err := validateSourceOrPath(item.Path, true); err != nil {
				return fmt.Errorf("filters.toml: filter.path: %w", err)
			}
		}
	}
	return nil
}

func validateMonitor(m Monitor) error {
	if m.TestURL != "" {
		if err := validateHTTPSURL(m.TestURL); err != nil {
			return fmt.Errorf("config.toml: monitor.test_url: %w", err)
		}
	}
	if m.Interval <= 0 {
		return fmt.Errorf("config.toml: monitor.interval: must be positive")
	}
	return nil
}

func validateDNS(d DNS) error {
	if d.Listen == "" {
		return fmt.Errorf("config.toml: dns.listen: required")
	}
	return nil
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

func validateHTTPSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.User != nil {
		return fmt.Errorf("userinfo not allowed")
	}
	if u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return fmt.Errorf("must use https")
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

func validateSourceOrPath(raw string, allowPath bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme == "" {
		if allowPath {
			return nil
		}
		return fmt.Errorf("must use https")
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
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		if u.Scheme == "" || u.Host == "" || u.Opaque != "" {
			return fmt.Errorf("invalid endpoint")
		}
		return nil
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("invalid endpoint")
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
