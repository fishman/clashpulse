package config

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type tomlBlock struct {
	name   string
	line   int
	values map[string]tomlValue
}

type tomlValue struct {
	raw  string
	line int
}

var resourceKinds = map[string]struct{}{
	"geoip.dat":     {},
	"geosite.dat":   {},
	"Country.mmdb":  {},
	"rule-provider": {},
	"rule-set":      {},
	"dns-route":     {},
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

	blocks, err := readBlocks(path)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	seen := map[string]bool{}
	for _, block := range blocks {
		switch block.name {
		case "mihomo":
			if seen[block.name] {
				return App{}, Mihomo{}, Monitor{}, DNS{}, fmt.Errorf("%s: duplicate section %q", path, block.name)
			}
			seen[block.name] = true
			if err := decodeMihomo(path, block, &mihomo); err != nil {
				return App{}, Mihomo{}, Monitor{}, DNS{}, err
			}
		case "monitor":
			if seen[block.name] {
				return App{}, Mihomo{}, Monitor{}, DNS{}, fmt.Errorf("%s: duplicate section %q", path, block.name)
			}
			seen[block.name] = true
			if err := decodeMonitor(path, block, &monitor); err != nil {
				return App{}, Mihomo{}, Monitor{}, DNS{}, err
			}
		case "dns":
			if seen[block.name] {
				return App{}, Mihomo{}, Monitor{}, DNS{}, fmt.Errorf("%s: duplicate section %q", path, block.name)
			}
			seen[block.name] = true
			if err := decodeDNS(path, block, &dns); err != nil {
				return App{}, Mihomo{}, Monitor{}, DNS{}, err
			}
		case "system_proxy":
			if seen[block.name] {
				return App{}, Mihomo{}, Monitor{}, DNS{}, fmt.Errorf("%s: duplicate section %q", path, block.name)
			}
			seen[block.name] = true
			if err := decodeSystemProxy(path, block, &app); err != nil {
				return App{}, Mihomo{}, Monitor{}, DNS{}, err
			}
		default:
			return App{}, Mihomo{}, Monitor{}, DNS{}, fmt.Errorf("%s: unknown section %q", path, block.name)
		}
	}
	return app, mihomo, monitor, dns, nil
}

func loadSubscriptions(path string) ([]Subscription, error) {
	blocks, err := readBlocks(path)
	if err != nil {
		return nil, err
	}
	var items []Subscription
	for _, block := range blocks {
		if block.name != "subscription" {
			return nil, fmt.Errorf("%s: unknown section %q", path, block.name)
		}
		item, err := decodeSubscription(path, block)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func loadResources(path string) ([]Resource, []ResolverSet, []DNSRoute, error) {
	blocks, err := readBlocks(path)
	if err != nil {
		return nil, nil, nil, err
	}
	var resources []Resource
	var resolverSets []ResolverSet
	var routes []DNSRoute
	for _, block := range blocks {
		switch block.name {
		case "resource":
			item, err := decodeResource(path, block)
			if err != nil {
				return nil, nil, nil, err
			}
			resources = append(resources, item)
		case "resolver_set":
			item, err := decodeResolverSet(path, block)
			if err != nil {
				return nil, nil, nil, err
			}
			resolverSets = append(resolverSets, item)
		case "dns_route":
			item, err := decodeDNSRoute(path, block)
			if err != nil {
				return nil, nil, nil, err
			}
			routes = append(routes, item)
		default:
			return nil, nil, nil, fmt.Errorf("%s: unknown section %q", path, block.name)
		}
	}
	return resources, resolverSets, routes, nil
}

func loadFilters(path string) ([]Filter, error) {
	blocks, err := readBlocks(path)
	if err != nil {
		return nil, err
	}
	var items []Filter
	for _, block := range blocks {
		if block.name != "filter" {
			return nil, fmt.Errorf("%s: unknown section %q", path, block.name)
		}
		item, err := decodeFilter(path, block)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func readBlocks(path string) ([]tomlBlock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseBlocks(path, data)
}

func parseBlocks(path string, data []byte) ([]tomlBlock, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var blocks []tomlBlock
	var current *tomlBlock
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]"):
			name := strings.TrimSpace(line[2 : len(line)-2])
			if name == "" {
				return nil, fmt.Errorf("%s: line %d: empty table name", path, lineNo)
			}
			blocks = append(blocks, tomlBlock{name: name, line: lineNo, values: map[string]tomlValue{}})
			current = &blocks[len(blocks)-1]
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name == "" {
				return nil, fmt.Errorf("%s: line %d: empty table name", path, lineNo)
			}
			blocks = append(blocks, tomlBlock{name: name, line: lineNo, values: map[string]tomlValue{}})
			current = &blocks[len(blocks)-1]
		default:
			if current == nil {
				return nil, fmt.Errorf("%s: line %d: key outside table", path, lineNo)
			}
			key, raw, ok := strings.Cut(line, "=")
			if !ok {
				return nil, fmt.Errorf("%s: line %d: expected key = value", path, lineNo)
			}
			key = strings.TrimSpace(key)
			raw = strings.TrimSpace(raw)
			if key == "" || raw == "" {
				return nil, fmt.Errorf("%s: line %d: expected key = value", path, lineNo)
			}
			if _, exists := current.values[key]; exists {
				return nil, fmt.Errorf("%s: line %d: duplicate key %q", path, lineNo, key)
			}
			current.values[key] = tomlValue{raw: raw, line: lineNo}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return blocks, nil
}

func stripComment(line string) string {
	inString := false
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '#':
			return line[:i]
		}
	}
	return line
}

func decodeMihomo(path string, block tomlBlock, out *Mihomo) error {
	for key, item := range block.values {
		switch key {
		case "binary":
			value, err := parseString(item.raw)
			if err != nil {
				return blockValueError(path, block.name, key, err.Error())
			}
			out.Binary = value
		default:
			return blockValueError(path, block.name, key, "unknown key")
		}
	}
	if out.Binary == "" {
		out.Binary = "system"
	}
	return nil
}

func decodeMonitor(path string, block tomlBlock, out *Monitor) error {
	for key, item := range block.values {
		switch key {
		case "enabled":
			value, err := parseBool(item.raw)
			if err != nil {
				return blockValueError(path, block.name, key, err.Error())
			}
			out.Enabled = value
		case "test_url":
			value, err := parseString(item.raw)
			if err != nil {
				return blockValueError(path, block.name, key, err.Error())
			}
			out.TestURL = value
		case "interval":
			value, err := parseDuration(item.raw)
			if err != nil {
				return blockValueError(path, block.name, key, err.Error())
			}
			out.Interval = value
		default:
			return blockValueError(path, block.name, key, "unknown key")
		}
	}
	if out.TestURL == "" {
		out.TestURL = "https://example.invalid/generate_204"
	}
	if out.Interval == 0 {
		out.Interval = 5 * time.Minute
	}
	return nil
}

func decodeDNS(path string, block tomlBlock, out *DNS) error {
	for key, item := range block.values {
		switch key {
		case "listen":
			value, err := parseString(item.raw)
			if err != nil {
				return blockValueError(path, block.name, key, err.Error())
			}
			out.Listen = value
		default:
			return blockValueError(path, block.name, key, "unknown key")
		}
	}
	if out.Listen == "" {
		out.Listen = "127.0.0.1:53"
	}
	return nil
}

func decodeSystemProxy(path string, block tomlBlock, out *App) error {
	for key, item := range block.values {
		switch key {
		case "enabled":
			value, err := parseBool(item.raw)
			if err != nil {
				return blockValueError(path, block.name, key, err.Error())
			}
			out.SystemProxy.Enabled = value
		default:
			return blockValueError(path, block.name, key, "unknown key")
		}
	}
	return nil
}

func decodeSubscription(path string, block tomlBlock) (Subscription, error) {
	var item Subscription
	for key, value := range block.values {
		switch key {
		case "id":
			raw, err := parseString(value.raw)
			if err != nil {
				return Subscription{}, blockValueError(path, block.name, key, err.Error())
			}
			item.ID = raw
		case "name":
			raw, err := parseString(value.raw)
			if err != nil {
				return Subscription{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Name = raw
		case "url":
			raw, err := parseString(value.raw)
			if err != nil {
				return Subscription{}, blockValueError(path, block.name, key, err.Error())
			}
			item.URL = raw
		case "enabled":
			raw, err := parseBool(value.raw)
			if err != nil {
				return Subscription{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Enabled = raw
		case "refresh_interval":
			raw, err := parseDuration(value.raw)
			if err != nil {
				return Subscription{}, blockValueError(path, block.name, key, err.Error())
			}
			item.RefreshInterval = raw
		case "timeout":
			raw, err := parseDuration(value.raw)
			if err != nil {
				return Subscription{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Timeout = raw
		case "route":
			raw, err := parseString(value.raw)
			if err != nil {
				return Subscription{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Route = raw
		case "allow_http":
			raw, err := parseBool(value.raw)
			if err != nil {
				return Subscription{}, blockValueError(path, block.name, key, err.Error())
			}
			item.AllowHTTP = raw
		case "allow_invalid_tls":
			raw, err := parseBool(value.raw)
			if err != nil {
				return Subscription{}, blockValueError(path, block.name, key, err.Error())
			}
			item.AllowInvalidTLS = raw
		default:
			return Subscription{}, blockValueError(path, block.name, key, "unknown key")
		}
	}
	if item.ID == "" {
		return Subscription{}, blockValueError(path, block.name, "id", "required")
	}
	if item.URL == "" {
		return Subscription{}, blockValueError(path, block.name, "url", "required")
	}
	if item.Name == "" {
		item.Name = item.ID
	}
	if item.RefreshInterval == 0 {
		item.RefreshInterval = 12 * time.Hour
	}
	if item.Route == "" {
		item.Route = "direct"
	}
	if _, ok := subscriptionRoutes[item.Route]; !ok {
		return Subscription{}, blockValueError(path, block.name, "route", "unknown route")
	}
	if _, ok := block.values["enabled"]; !ok {
		item.Enabled = true
	}
	return item, nil
}

func decodeResource(path string, block tomlBlock) (Resource, error) {
	var item Resource
	for key, value := range block.values {
		switch key {
		case "id":
			raw, err := parseString(value.raw)
			if err != nil {
				return Resource{}, blockValueError(path, block.name, key, err.Error())
			}
			item.ID = raw
		case "kind":
			raw, err := parseString(value.raw)
			if err != nil {
				return Resource{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Kind = raw
		case "url":
			raw, err := parseString(value.raw)
			if err != nil {
				return Resource{}, blockValueError(path, block.name, key, err.Error())
			}
			item.URL = raw
		case "enabled":
			raw, err := parseBool(value.raw)
			if err != nil {
				return Resource{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Enabled = raw
		case "interval":
			raw, err := parseDuration(value.raw)
			if err != nil {
				return Resource{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Interval = raw
		case "sha256":
			raw, err := parseString(value.raw)
			if err != nil {
				return Resource{}, blockValueError(path, block.name, key, err.Error())
			}
			item.SHA256 = raw
		default:
			return Resource{}, blockValueError(path, block.name, key, "unknown key")
		}
	}
	if item.ID == "" {
		return Resource{}, blockValueError(path, block.name, "id", "required")
	}
	if item.Kind == "" {
		return Resource{}, blockValueError(path, block.name, "kind", "required")
	}
	if item.URL == "" {
		return Resource{}, blockValueError(path, block.name, "url", "required")
	}
	if _, ok := resourceKinds[item.Kind]; !ok {
		return Resource{}, blockValueError(path, block.name, "kind", "unknown kind")
	}
	if _, err := parseSourceOrPath(item.URL, true); err != nil {
		return Resource{}, blockValueError(path, block.name, "url", err.Error())
	}
	if _, ok := block.values["enabled"]; !ok {
		item.Enabled = true
	}
	return item, nil
}

func decodeResolverSet(path string, block tomlBlock) (ResolverSet, error) {
	var item ResolverSet
	for key, value := range block.values {
		switch key {
		case "id":
			raw, err := parseString(value.raw)
			if err != nil {
				return ResolverSet{}, blockValueError(path, block.name, key, err.Error())
			}
			item.ID = raw
		case "endpoints":
			raw, err := parseStringArray(value.raw)
			if err != nil {
				return ResolverSet{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Endpoints = raw
		default:
			return ResolverSet{}, blockValueError(path, block.name, key, "unknown key")
		}
	}
	if item.ID == "" {
		return ResolverSet{}, blockValueError(path, block.name, "id", "required")
	}
	if len(item.Endpoints) == 0 {
		return ResolverSet{}, blockValueError(path, block.name, "endpoints", "required")
	}
	for _, endpoint := range item.Endpoints {
		if err := validateResolverEndpoint(endpoint); err != nil {
			return ResolverSet{}, blockValueError(path, block.name, "endpoints", err.Error())
		}
	}
	return item, nil
}

func decodeDNSRoute(path string, block tomlBlock) (DNSRoute, error) {
	var item DNSRoute
	for key, value := range block.values {
		switch key {
		case "suffix":
			raw, err := parseString(value.raw)
			if err != nil {
				return DNSRoute{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Suffix = raw
		case "geosite":
			raw, err := parseString(value.raw)
			if err != nil {
				return DNSRoute{}, blockValueError(path, block.name, key, err.Error())
			}
			item.GeoSite = raw
		case "resource":
			raw, err := parseString(value.raw)
			if err != nil {
				return DNSRoute{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Resource = raw
		case "resolver_set":
			raw, err := parseString(value.raw)
			if err != nil {
				return DNSRoute{}, blockValueError(path, block.name, key, err.Error())
			}
			item.ResolverSet = raw
		default:
			return DNSRoute{}, blockValueError(path, block.name, key, "unknown key")
		}
	}
	if item.ResolverSet == "" {
		return DNSRoute{}, blockValueError(path, block.name, "resolver_set", "required")
	}
	if item.Suffix == "" && item.GeoSite == "" && item.Resource == "" {
		return DNSRoute{}, blockValueError(path, block.name, "suffix", "required")
	}
	return item, nil
}

func decodeFilter(path string, block tomlBlock) (Filter, error) {
	var item Filter
	for key, value := range block.values {
		switch key {
		case "id":
			raw, err := parseString(value.raw)
			if err != nil {
				return Filter{}, blockValueError(path, block.name, key, err.Error())
			}
			item.ID = raw
		case "resource":
			raw, err := parseString(value.raw)
			if err != nil {
				return Filter{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Resource = raw
		case "url":
			raw, err := parseString(value.raw)
			if err != nil {
				return Filter{}, blockValueError(path, block.name, key, err.Error())
			}
			item.URL = raw
		case "path":
			raw, err := parseString(value.raw)
			if err != nil {
				return Filter{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Path = raw
		case "format":
			raw, err := parseString(value.raw)
			if err != nil {
				return Filter{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Format = raw
		case "enabled":
			raw, err := parseBool(value.raw)
			if err != nil {
				return Filter{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Enabled = raw
		case "interval":
			raw, err := parseDuration(value.raw)
			if err != nil {
				return Filter{}, blockValueError(path, block.name, key, err.Error())
			}
			item.Interval = raw
		case "sha256":
			raw, err := parseString(value.raw)
			if err != nil {
				return Filter{}, blockValueError(path, block.name, key, err.Error())
			}
			item.SHA256 = raw
		default:
			return Filter{}, blockValueError(path, block.name, key, "unknown key")
		}
	}
	if item.ID == "" {
		return Filter{}, blockValueError(path, block.name, "id", "required")
	}
	if item.Resource == "" {
		return Filter{}, blockValueError(path, block.name, "resource", "required")
	}
	if _, ok := block.values["enabled"]; !ok {
		item.Enabled = true
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
		if s.Subscriptions[i].Route == "" {
			s.Subscriptions[i].Route = "direct"
		}
	}
}

func validateSnapshot(s Snapshot) error {
	if err := validateMihomo(s.Mihomo); err != nil {
		return err
	}
	if err := validateMonitor(s.Monitor); err != nil {
		return err
	}
	if err := validateDNS(s.DNS); err != nil {
		return err
	}
	ids := map[string]string{}
	recordID := func(kind, id string) error {
		if id == "" {
			return nil
		}
		if prev, ok := ids[id]; ok {
			return fmt.Errorf("duplicate id %q used by %s and %s", id, prev, kind)
		}
		ids[id] = kind
		return nil
	}
	for i := range s.Subscriptions {
		item := s.Subscriptions[i]
		if item.ID == "" {
			return fmt.Errorf("subscriptions.toml: subscription.id: required")
		}
		if item.URL == "" {
			return fmt.Errorf("subscriptions.toml: subscription.url: required")
		}
		if err := recordID("subscription", item.ID); err != nil {
			return fmt.Errorf("subscriptions.toml: subscription.id: %w", err)
		}
		if err := validateHTTPSURL("subscriptions.toml", "subscription.url", item.URL); err != nil {
			return err
		}
		if _, ok := subscriptionRoutes[item.Route]; !ok {
			return fmt.Errorf("subscriptions.toml: subscription.route: unknown route %q", item.Route)
		}
	}
	resourceByID := map[string]Resource{}
	for i := range s.Resources {
		item := s.Resources[i]
		if item.ID == "" {
			return fmt.Errorf("resources.toml: resource.id: required")
		}
		if item.Kind == "" {
			return fmt.Errorf("resources.toml: resource.kind: required")
		}
		if item.URL == "" {
			return fmt.Errorf("resources.toml: resource.url: required")
		}
		if err := recordID("resource", item.ID); err != nil {
			return fmt.Errorf("resources.toml: resource.id: %w", err)
		}
		if _, ok := resourceKinds[item.Kind]; !ok {
			return fmt.Errorf("resources.toml: resource.kind: unknown kind %q", item.Kind)
		}
		if err := validateResourceSource(item.URL); err != nil {
			return fmt.Errorf("resources.toml: resource.url: %w", err)
		}
		resourceByID[item.ID] = item
	}
	for i := range s.DNS.ResolverSets {
		item := s.DNS.ResolverSets[i]
		if item.ID == "" {
			return fmt.Errorf("resources.toml: resolver_set.id: required")
		}
		if len(item.Endpoints) == 0 {
			return fmt.Errorf("resources.toml: resolver_set.endpoints: required")
		}
		if err := recordID("resolver_set", item.ID); err != nil {
			return fmt.Errorf("resources.toml: resolver_set.id: %w", err)
		}
		for _, endpoint := range item.Endpoints {
			if err := validateResolverEndpoint(endpoint); err != nil {
				return fmt.Errorf("resources.toml: resolver_set.endpoints: %w", err)
			}
			if s.DNS.Listen != "" && endpoint == s.DNS.Listen {
				return fmt.Errorf("resources.toml: resolver_set.endpoints: endpoint %q loops back to dns.listen", endpoint)
			}
		}
	}
	for i := range s.DNS.Routes {
		item := s.DNS.Routes[i]
		if item.ResolverSet == "" {
			return fmt.Errorf("resources.toml: dns_route.resolver_set: required")
		}
		if item.Suffix == "" && item.GeoSite == "" && item.Resource == "" {
			return fmt.Errorf("resources.toml: dns_route.suffix: required")
		}
		if _, ok := ids[item.ResolverSet]; !ok {
			return fmt.Errorf("resources.toml: dns_route.resolver_set: unknown resolver set %q", item.ResolverSet)
		}
		if item.Resource != "" {
			resource, ok := resourceByID[item.Resource]
			if !ok {
				return fmt.Errorf("resources.toml: dns_route.resource: unknown resource %q", item.Resource)
			}
			if resource.Kind != "rule-set" && resource.Kind != "dns-route" {
				return fmt.Errorf("resources.toml: dns_route.resource: resource %q has kind %q", item.Resource, resource.Kind)
			}
		}
	}
	for i := range s.Filters {
		item := s.Filters[i]
		if item.ID == "" {
			return fmt.Errorf("filters.toml: filter.id: required")
		}
		if item.Resource == "" {
			return fmt.Errorf("filters.toml: filter.resource: required")
		}
		if err := recordID("filter", item.ID); err != nil {
			return fmt.Errorf("filters.toml: filter.id: %w", err)
		}
		if _, ok := resourceByID[item.Resource]; !ok {
			return fmt.Errorf("filters.toml: filter.resource: unknown resource %q", item.Resource)
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

func validateMihomo(m Mihomo) error {
	if m.Binary == "" {
		return fmt.Errorf("config.toml: mihomo.binary: required")
	}
	if strings.Contains(m.Binary, "://") {
		return fmt.Errorf("config.toml: mihomo.binary: invalid binary %q", m.Binary)
	}
	return nil
}

func validateMonitor(m Monitor) error {
	if m.TestURL != "" {
		if err := validateHTTPSURL("config.toml", "monitor.test_url", m.TestURL); err != nil {
			return err
		}
	}
	if m.Interval < 0 {
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

func validateHTTPSURL(file, key, raw string) error {
	if raw == "" {
		return fmt.Errorf("%s: %s: required", file, key)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: %s: %v", file, key, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s: %s: must use https", file, key)
	}
	return nil
}

func validateResourceSource(raw string) error {
	return validateSourceOrPath(raw, true)
}

func validateSourceOrPath(raw string, allowPath bool) error {
	if raw == "" {
		return fmt.Errorf("required")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		if u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("must use https")
		}
		return nil
	}
	if allowPath {
		return nil
	}
	return fmt.Errorf("must use https")
}

func parseSourceOrPath(raw string, allowPath bool) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("required")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		if u.Scheme != "https" || u.Host == "" {
			return "", fmt.Errorf("must use https")
		}
		return raw, nil
	}
	if allowPath {
		return raw, nil
	}
	return "", fmt.Errorf("must use https")
}

func validateResolverEndpoint(raw string) error {
	if raw == "" {
		return fmt.Errorf("required")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		if u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("invalid endpoint")
		}
	}
	return nil
}

func parseString(raw string) (string, error) {
	if !strings.HasPrefix(raw, "\"") {
		return "", fmt.Errorf("expected quoted string")
	}
	prefix, err := strconv.QuotedPrefix(raw)
	if err != nil || prefix != raw {
		return "", fmt.Errorf("expected quoted string")
	}
	return strconv.Unquote(raw)
}

func parseBool(raw string) (bool, error) {
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected boolean")
	}
}

func parseDuration(raw string) (time.Duration, error) {
	value, err := parseString(raw)
	if err != nil {
		return 0, err
	}
	return time.ParseDuration(value)
}

func parseStringArray(raw string) ([]string, error) {
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil, fmt.Errorf("expected array")
	}
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return nil, nil
	}
	var values []string
	for len(inner) > 0 {
		inner = strings.TrimSpace(inner)
		prefix, err := strconv.QuotedPrefix(inner)
		if err != nil || prefix == "" {
			return nil, fmt.Errorf("expected quoted string")
		}
		value, err := strconv.Unquote(prefix)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		inner = strings.TrimSpace(inner[len(prefix):])
		if inner == "" {
			break
		}
		if inner[0] != ',' {
			return nil, fmt.Errorf("expected comma")
		}
		inner = strings.TrimSpace(inner[1:])
	}
	return values, nil
}

func blockValueError(path, section, key, msg string) error {
	return fmt.Errorf("%s: %s.%s: %s", path, section, key, msg)
}
