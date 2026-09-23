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

func loadConfig(path string) (App, Mihomo, Monitor, DNS, error) {
	var app App
	var mihomo Mihomo
	var monitor Monitor
	var dns DNS
	err := loadTables(path, map[string]tomlTableKind{"mihomo": tomlSingleTable, "monitor": tomlSingleTable, "dns": tomlSingleTable, "system_proxy": tomlSingleTable}, func(table tomlTable) error {
		switch table.name {
		case "mihomo":
			return decodeMihomo(table, &mihomo)
		case "monitor":
			return decodeMonitor(table, &monitor)
		case "dns":
			return decodeDNS(table, &dns)
		case "system_proxy":
			return decodeSystemProxy(table, &app)
		default:
			return fmt.Errorf("%s: unknown table %q", table.path, table.name)
		}
	})
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	return app, mihomo, monitor, dns, nil
}

func loadSubscriptions(path string) ([]Subscription, error) {
	var items []Subscription
	err := loadTables(path, map[string]tomlTableKind{"subscription": tomlArrayTable}, func(table tomlTable) error {
		item, err := decodeSubscription(table)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func loadResources(path string) ([]Resource, []ResolverSet, []DNSRoute, error) {
	var resources []Resource
	var resolverSets []ResolverSet
	var routes []DNSRoute
	err := loadTables(path, map[string]tomlTableKind{"resource": tomlArrayTable, "resolver_set": tomlArrayTable, "dns_route": tomlArrayTable}, func(table tomlTable) error {
		switch table.name {
		case "resource":
			item, err := decodeResource(table)
			if err != nil {
				return err
			}
			resources = append(resources, item)
		case "resolver_set":
			item, err := decodeResolverSet(table)
			if err != nil {
				return err
			}
			resolverSets = append(resolverSets, item)
		case "dns_route":
			item, err := decodeDNSRoute(table)
			if err != nil {
				return err
			}
			routes = append(routes, item)
		default:
			return fmt.Errorf("%s: unknown table %q", table.path, table.name)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return resources, resolverSets, routes, nil
}

func loadFilters(path string) ([]Filter, error) {
	var items []Filter
	err := loadTables(path, map[string]tomlTableKind{"filter": tomlArrayTable}, func(table tomlTable) error {
		item, err := decodeFilter(table)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func loadTables(path string, expected map[string]tomlTableKind, fn func(tomlTable) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	tables, err := parseTOMLFile(path, data)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, table := range tables {
		want, ok := expected[table.name]
		if !ok {
			return fmt.Errorf("%s: unknown table %q", table.path, table.name)
		}
		if table.kind != want {
			return fmt.Errorf("%s: table %q has wrong kind", table.path, table.name)
		}
		if seen[table.name] {
			return fmt.Errorf("%s: duplicate table %q", table.path, table.name)
		}
		seen[table.name] = true
		if err := fn(table); err != nil {
			return err
		}
	}
	return nil
}

func decodeMihomo(table tomlTable, out *Mihomo) error {
	for key := range table.values {
		switch key {
		case "binary":
			value, err := stringField(table, key)
			if err != nil {
				return err
			}
			out.Binary = value
		default:
			return fieldError(table, key, "unknown key")
		}
	}
	return nil
}

func decodeMonitor(table tomlTable, out *Monitor) error {
	for key := range table.values {
		switch key {
		case "enabled":
			value, err := boolField(table, key)
			if err != nil {
				return err
			}
			out.Enabled = value
		case "test_url":
			value, err := stringField(table, key)
			if err != nil {
				return err
			}
			out.TestURL = value
		case "interval":
			value, err := durationField(table, key)
			if err != nil {
				return err
			}
			if value <= 0 {
				return fieldError(table, key, "must be positive")
			}
			out.Interval = value
		default:
			return fieldError(table, key, "unknown key")
		}
	}
	return nil
}

func decodeDNS(table tomlTable, out *DNS) error {
	for key := range table.values {
		switch key {
		case "listen":
			value, err := stringField(table, key)
			if err != nil {
				return err
			}
			out.Listen = value
		default:
			return fieldError(table, key, "unknown key")
		}
	}
	return nil
}

func decodeSystemProxy(table tomlTable, out *App) error {
	for key := range table.values {
		switch key {
		case "enabled":
			value, err := boolField(table, key)
			if err != nil {
				return err
			}
			out.SystemProxy.Enabled = value
		default:
			return fieldError(table, key, "unknown key")
		}
	}
	return nil
}

func decodeSubscription(table tomlTable) (Subscription, error) {
	var item Subscription
	for key := range table.values {
		switch key {
		case "id":
			value, err := stringField(table, key)
			if err != nil {
				return Subscription{}, err
			}
			item.ID = value
		case "name":
			value, err := stringField(table, key)
			if err != nil {
				return Subscription{}, err
			}
			item.Name = value
		case "url":
			value, err := stringField(table, key)
			if err != nil {
				return Subscription{}, err
			}
			item.URL = value
		case "enabled":
			value, err := boolField(table, key)
			if err != nil {
				return Subscription{}, err
			}
			item.Enabled = value
		case "refresh_interval":
			value, err := durationField(table, key)
			if err != nil {
				return Subscription{}, err
			}
			if value <= 0 {
				return Subscription{}, fieldError(table, key, "must be positive")
			}
			item.RefreshInterval = value
		case "timeout":
			value, err := durationField(table, key)
			if err != nil {
				return Subscription{}, err
			}
			if value <= 0 {
				return Subscription{}, fieldError(table, key, "must be positive")
			}
			item.Timeout = value
		case "route":
			value, err := stringField(table, key)
			if err != nil {
				return Subscription{}, err
			}
			item.Route = value
		case "allow_http":
			value, err := boolField(table, key)
			if err != nil {
				return Subscription{}, err
			}
			item.AllowHTTP = value
		case "allow_invalid_tls":
			value, err := boolField(table, key)
			if err != nil {
				return Subscription{}, err
			}
			item.AllowInvalidTLS = value
		default:
			return Subscription{}, fieldError(table, key, "unknown key")
		}
	}
	if item.ID == "" {
		return Subscription{}, fieldError(table, "id", "required")
	}
	if item.URL == "" {
		return Subscription{}, fieldError(table, "url", "required")
	}
	return item, nil
}

func decodeResource(table tomlTable) (Resource, error) {
	var item Resource
	for key := range table.values {
		switch key {
		case "id":
			value, err := stringField(table, key)
			if err != nil {
				return Resource{}, err
			}
			item.ID = value
		case "kind":
			value, err := stringField(table, key)
			if err != nil {
				return Resource{}, err
			}
			item.Kind = value
		case "url":
			value, err := stringField(table, key)
			if err != nil {
				return Resource{}, err
			}
			item.URL = value
		case "enabled":
			value, err := boolField(table, key)
			if err != nil {
				return Resource{}, err
			}
			item.Enabled = value
		case "interval":
			value, err := durationField(table, key)
			if err != nil {
				return Resource{}, err
			}
			if value <= 0 {
				return Resource{}, fieldError(table, key, "must be positive")
			}
			item.Interval = value
		case "sha256":
			value, err := stringField(table, key)
			if err != nil {
				return Resource{}, err
			}
			item.SHA256 = value
		default:
			return Resource{}, fieldError(table, key, "unknown key")
		}
	}
	if item.ID == "" {
		return Resource{}, fieldError(table, "id", "required")
	}
	if item.Kind == "" {
		return Resource{}, fieldError(table, "kind", "required")
	}
	if item.URL == "" {
		return Resource{}, fieldError(table, "url", "required")
	}
	if _, ok := resourceKinds[item.Kind]; !ok {
		return Resource{}, fieldError(table, "kind", "unknown kind")
	}
	if err := validateSourceOrPath(item.URL, true); err != nil {
		return Resource{}, fieldError(table, "url", err.Error())
	}
	return item, nil
}

func decodeResolverSet(table tomlTable) (ResolverSet, error) {
	var item ResolverSet
	for key := range table.values {
		switch key {
		case "id":
			value, err := stringField(table, key)
			if err != nil {
				return ResolverSet{}, err
			}
			item.ID = value
		case "endpoints":
			value, err := stringListField(table, key)
			if err != nil {
				return ResolverSet{}, err
			}
			item.Endpoints = value
		default:
			return ResolverSet{}, fieldError(table, key, "unknown key")
		}
	}
	if item.ID == "" {
		return ResolverSet{}, fieldError(table, "id", "required")
	}
	if len(item.Endpoints) == 0 {
		return ResolverSet{}, fieldError(table, "endpoints", "required")
	}
	for _, endpoint := range item.Endpoints {
		if err := validateResolverEndpoint(endpoint); err != nil {
			return ResolverSet{}, fieldError(table, "endpoints", err.Error())
		}
	}
	return item, nil
}

func decodeDNSRoute(table tomlTable) (DNSRoute, error) {
	var item DNSRoute
	for key := range table.values {
		switch key {
		case "suffix":
			value, err := stringField(table, key)
			if err != nil {
				return DNSRoute{}, err
			}
			item.Suffix = value
		case "geosite":
			value, err := stringField(table, key)
			if err != nil {
				return DNSRoute{}, err
			}
			item.GeoSite = value
		case "resource":
			value, err := stringField(table, key)
			if err != nil {
				return DNSRoute{}, err
			}
			item.Resource = value
		case "resolver_set":
			value, err := stringField(table, key)
			if err != nil {
				return DNSRoute{}, err
			}
			item.ResolverSet = value
		default:
			return DNSRoute{}, fieldError(table, key, "unknown key")
		}
	}
	if item.ResolverSet == "" {
		return DNSRoute{}, fieldError(table, "resolver_set", "required")
	}
	if item.Suffix == "" && item.GeoSite == "" && item.Resource == "" {
		return DNSRoute{}, fieldError(table, "suffix", "required")
	}
	return item, nil
}

func decodeFilter(table tomlTable) (Filter, error) {
	var item Filter
	for key := range table.values {
		switch key {
		case "id":
			value, err := stringField(table, key)
			if err != nil {
				return Filter{}, err
			}
			item.ID = value
		case "resource":
			value, err := stringField(table, key)
			if err != nil {
				return Filter{}, err
			}
			item.Resource = value
		case "url":
			value, err := stringField(table, key)
			if err != nil {
				return Filter{}, err
			}
			item.URL = value
		case "path":
			value, err := stringField(table, key)
			if err != nil {
				return Filter{}, err
			}
			item.Path = value
		case "format":
			value, err := stringField(table, key)
			if err != nil {
				return Filter{}, err
			}
			item.Format = value
		case "enabled":
			value, err := boolField(table, key)
			if err != nil {
				return Filter{}, err
			}
			item.Enabled = value
		case "interval":
			value, err := durationField(table, key)
			if err != nil {
				return Filter{}, err
			}
			if value <= 0 {
				return Filter{}, fieldError(table, key, "must be positive")
			}
			item.Interval = value
		case "sha256":
			value, err := stringField(table, key)
			if err != nil {
				return Filter{}, err
			}
			item.SHA256 = value
		default:
			return Filter{}, fieldError(table, key, "unknown key")
		}
	}
	if item.ID == "" {
		return Filter{}, fieldError(table, "id", "required")
	}
	if item.Resource == "" {
		return Filter{}, fieldError(table, "resource", "required")
	}
	if item.URL != "" {
		if err := validateSourceOrPath(item.URL, true); err != nil {
			return Filter{}, fieldError(table, "url", err.Error())
		}
	}
	if item.Path != "" {
		if err := validateSourceOrPath(item.Path, true); err != nil {
			return Filter{}, fieldError(table, "path", err.Error())
		}
	}
	return item, nil
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

func fieldError(table tomlTable, key, msg string) error {
	return fmt.Errorf("%s: %s.%s: %s", table.path, table.name, key, msg)
}

func stringField(table tomlTable, key string) (string, error) {
	value, ok := table.values[key]
	if !ok {
		return "", fmt.Errorf("%s: %s.%s: required", table.path, table.name, key)
	}
	if value.kind != tomlString {
		return "", fmt.Errorf("%s: %s.%s: expected string", table.path, table.name, key)
	}
	return value.stringValue, nil
}

func boolField(table tomlTable, key string) (bool, error) {
	value, ok := table.values[key]
	if !ok {
		return false, fmt.Errorf("%s: %s.%s: required", table.path, table.name, key)
	}
	if value.kind != tomlBool {
		return false, fmt.Errorf("%s: %s.%s: expected boolean", table.path, table.name, key)
	}
	return value.boolValue, nil
}

func durationField(table tomlTable, key string) (time.Duration, error) {
	value, ok := table.values[key]
	if !ok {
		return 0, nil
	}
	if value.kind != tomlString {
		return 0, fmt.Errorf("%s: %s.%s: expected duration string", table.path, table.name, key)
	}
	return time.ParseDuration(value.stringValue)
}

func stringListField(table tomlTable, key string) ([]string, error) {
	value, ok := table.values[key]
	if !ok {
		return nil, fmt.Errorf("%s: %s.%s: required", table.path, table.name, key)
	}
	if value.kind != tomlStringList {
		return nil, fmt.Errorf("%s: %s.%s: expected string array", table.path, table.name, key)
	}
	return cloneStrings(value.strings), nil
}
