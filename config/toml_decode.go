package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type configDoc struct {
	Mihomo struct {
		Binary string `toml:"binary"`
	} `toml:"mihomo"`
	Monitor struct {
		Enabled  bool    `toml:"enabled"`
		TestURL  string  `toml:"test_url"`
		Interval *string `toml:"interval"`
	} `toml:"monitor"`
	DNS struct {
		Listen string `toml:"listen"`
	} `toml:"dns"`
	SystemProxy struct {
		Enabled bool `toml:"enabled"`
	} `toml:"system_proxy"`
}

type subscriptionDoc struct {
	ID              string  `toml:"id"`
	Name            string  `toml:"name"`
	URL             string  `toml:"url"`
	Enabled         bool    `toml:"enabled"`
	RefreshInterval *string `toml:"refresh_interval"`
	Timeout         *string `toml:"timeout"`
	Route           string  `toml:"route"`
	AllowHTTP       bool    `toml:"allow_http"`
	AllowInvalidTLS bool    `toml:"allow_invalid_tls"`
}

type subscriptionsDoc struct {
	Subscriptions []subscriptionDoc `toml:"subscription"`
}

type resourceDoc struct {
	ID       string  `toml:"id"`
	Kind     string  `toml:"kind"`
	URL      string  `toml:"url"`
	Enabled  bool    `toml:"enabled"`
	Interval *string `toml:"interval"`
	SHA256   string  `toml:"sha256"`
}

type resolverSetDoc struct {
	ID        string   `toml:"id"`
	Endpoints []string `toml:"endpoints"`
}

type dnsRouteDoc struct {
	Suffix      string `toml:"suffix"`
	GeoSite     string `toml:"geosite"`
	Resource    string `toml:"resource"`
	ResolverSet string `toml:"resolver_set"`
}

type resourcesDoc struct {
	Resources    []resourceDoc    `toml:"resource"`
	ResolverSets []resolverSetDoc `toml:"resolver_set"`
	Routes       []dnsRouteDoc    `toml:"dns_route"`
}

type filterDoc struct {
	ID       string  `toml:"id"`
	Resource string  `toml:"resource"`
	URL      string  `toml:"url"`
	Path     string  `toml:"path"`
	Format   string  `toml:"format"`
	Enabled  bool    `toml:"enabled"`
	Interval *string `toml:"interval"`
	SHA256   string  `toml:"sha256"`
}

type filtersDoc struct {
	Filters []filterDoc `toml:"filter"`
}

func decodeStrict(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	meta, err := toml.Decode(string(data), dst)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		return fmt.Errorf("%s: undecoded keys: %s", path, strings.Join(keys, ", "))
	}
	return nil
}

func loadConfig(path string) (App, Mihomo, Monitor, DNS, error) {
	var doc configDoc
	if err := decodeStrict(path, &doc); err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	interval, err := durationValue(path, "monitor.interval", doc.Monitor.Interval)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	return App{SystemProxy: SystemProxy{Enabled: doc.SystemProxy.Enabled}}, Mihomo{Binary: doc.Mihomo.Binary}, Monitor{
		Enabled:  doc.Monitor.Enabled,
		TestURL:  doc.Monitor.TestURL,
		Interval: interval,
	}, DNS{Listen: doc.DNS.Listen}, nil
}

func loadSubscriptions(path string) ([]Subscription, error) {
	var doc subscriptionsDoc
	if err := decodeStrict(path, &doc); err != nil {
		return nil, err
	}
	items := make([]Subscription, 0, len(doc.Subscriptions))
	for _, entry := range doc.Subscriptions {
		refresh, err := durationValue(path, "subscription.refresh_interval", entry.RefreshInterval)
		if err != nil {
			return nil, err
		}
		timeout, err := durationValue(path, "subscription.timeout", entry.Timeout)
		if err != nil {
			return nil, err
		}
		items = append(items, Subscription{
			ID:              entry.ID,
			Name:            entry.Name,
			URL:             entry.URL,
			Enabled:         entry.Enabled,
			RefreshInterval: refresh,
			Timeout:         timeout,
			Route:           entry.Route,
			AllowHTTP:       entry.AllowHTTP,
			AllowInvalidTLS: entry.AllowInvalidTLS,
		})
	}
	return items, nil
}

func loadResources(path string) ([]Resource, []ResolverSet, []DNSRoute, error) {
	var doc resourcesDoc
	if err := decodeStrict(path, &doc); err != nil {
		return nil, nil, nil, err
	}
	resources := make([]Resource, 0, len(doc.Resources))
	for _, entry := range doc.Resources {
		interval, err := durationValue(path, "resource.interval", entry.Interval)
		if err != nil {
			return nil, nil, nil, err
		}
		resources = append(resources, Resource{
			ID:       entry.ID,
			Kind:     entry.Kind,
			URL:      entry.URL,
			Enabled:  entry.Enabled,
			Interval: interval,
			SHA256:   entry.SHA256,
		})
	}
	resolverSets := make([]ResolverSet, 0, len(doc.ResolverSets))
	for _, entry := range doc.ResolverSets {
		resolverSets = append(resolverSets, ResolverSet{ID: entry.ID, Endpoints: cloneStrings(entry.Endpoints)})
	}
	routes := make([]DNSRoute, 0, len(doc.Routes))
	for _, entry := range doc.Routes {
		routes = append(routes, DNSRoute{Suffix: entry.Suffix, GeoSite: entry.GeoSite, Resource: entry.Resource, ResolverSet: entry.ResolverSet})
	}
	return resources, resolverSets, routes, nil
}

func loadFilters(path string) ([]Filter, error) {
	var doc filtersDoc
	if err := decodeStrict(path, &doc); err != nil {
		return nil, err
	}
	items := make([]Filter, 0, len(doc.Filters))
	for _, entry := range doc.Filters {
		interval, err := durationValue(path, "filter.interval", entry.Interval)
		if err != nil {
			return nil, err
		}
		items = append(items, Filter{
			ID:       entry.ID,
			Resource: entry.Resource,
			URL:      entry.URL,
			Path:     entry.Path,
			Format:   entry.Format,
			Enabled:  entry.Enabled,
			Interval: interval,
			SHA256:   entry.SHA256,
		})
	}
	return items, nil
}

func durationValue(path, field string, raw *string) (time.Duration, error) {
	if raw == nil {
		return 0, nil
	}
	d, err := time.ParseDuration(*raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %s: %w", path, field, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s: %s: must be positive", path, field)
	}
	return d, nil
}
