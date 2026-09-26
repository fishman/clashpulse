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
		Binary           string  `toml:"binary"`
		URLTestInterval  *string `toml:"url_test_interval"`
		URLTestTolerance *string `toml:"url_test_tolerance"`
	} `toml:"mihomo"`
	Monitor struct {
		Enabled               *bool    `toml:"enabled"`
		TestURL               *string  `toml:"test_url"`
		SwitchPolicy          *string  `toml:"switch_policy"`
		Interval              *string  `toml:"interval"`
		Timeout               *string  `toml:"timeout"`
		Concurrency           *int     `toml:"concurrency"`
		Threshold             *string  `toml:"threshold"`
		AlertThreshold        *string  `toml:"alert_threshold"`
		ConsecutiveBadSamples *int     `toml:"consecutive_bad_samples"`
		MinImprovement        *string  `toml:"min_improvement"`
		Cooldown              *string  `toml:"cooldown"`
		Jitter                *string  `toml:"jitter"`
		AutomatedGroups       []string `toml:"automated_groups"`
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
	UserAgent       string  `toml:"user_agent"`
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
	Format   string  `toml:"format"`
	RuleType string  `toml:"rule_type"`
	URL      string  `toml:"url"`
	Enabled  bool    `toml:"enabled"`
	Interval *string `toml:"interval"`
	SHA256   string  `toml:"sha256"`
}

type resolverSetDoc struct {
	ID        string   `toml:"id"`
	Endpoints []string `toml:"endpoints"`
	DNSCrypt  bool     `toml:"dnscrypt"`
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
	ID       string `toml:"id"`
	Resource string `toml:"resource"`
	Format   string `toml:"format"`
	Target   string `toml:"target"`
	Enabled  bool   `toml:"enabled"`
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
	urlTestInterval, err := durationValue(path, "mihomo.url_test_interval", doc.Mihomo.URLTestInterval)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	urlTestTolerance, err := nonnegativeDurationValue(path, "mihomo.url_test_tolerance", doc.Mihomo.URLTestTolerance)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	timeout, err := durationValue(path, "monitor.timeout", doc.Monitor.Timeout)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	threshold, err := durationValue(path, "monitor.threshold", doc.Monitor.Threshold)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	alertThreshold, err := durationValue(path, "monitor.alert_threshold", doc.Monitor.AlertThreshold)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	minImprovement, err := durationValue(path, "monitor.min_improvement", doc.Monitor.MinImprovement)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	cooldown, err := nonnegativeDurationValue(path, "monitor.cooldown", doc.Monitor.Cooldown)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	if doc.Monitor.Cooldown == nil {
		cooldown = 5 * time.Minute
	}
	jitter, err := nonnegativeDurationValue(path, "monitor.jitter", doc.Monitor.Jitter)
	if err != nil {
		return App{}, Mihomo{}, Monitor{}, DNS{}, err
	}
	if doc.Monitor.Jitter == nil {
		effectiveInterval := interval
		if effectiveInterval == 0 {
			effectiveInterval = time.Minute
		}
		jitter = 10 * time.Second
		if jitter >= effectiveInterval {
			jitter = effectiveInterval / 10
		}
	}
	if doc.Monitor.Concurrency != nil && (*doc.Monitor.Concurrency < 1 || *doc.Monitor.Concurrency > maxMonitorConcurrency) {
		return App{}, Mihomo{}, Monitor{}, DNS{}, fmt.Errorf("%s: monitor.concurrency: must be between 1 and %d", path, maxMonitorConcurrency)
	}
	if doc.Monitor.ConsecutiveBadSamples != nil && (*doc.Monitor.ConsecutiveBadSamples < 1 || *doc.Monitor.ConsecutiveBadSamples > maxMonitorConsecutiveBadSamples) {
		return App{}, Mihomo{}, Monitor{}, DNS{}, fmt.Errorf("%s: monitor.consecutive_bad_samples: must be between 1 and %d", path, maxMonitorConsecutiveBadSamples)
	}
	concurrency, badSamples := 0, 0
	if doc.Monitor.Concurrency != nil {
		concurrency = *doc.Monitor.Concurrency
	}
	if doc.Monitor.ConsecutiveBadSamples != nil {
		badSamples = *doc.Monitor.ConsecutiveBadSamples
	}
	enabled := true
	if doc.Monitor.Enabled != nil {
		enabled = *doc.Monitor.Enabled
	}
	testURL := ""
	if doc.Monitor.TestURL != nil {
		testURL = *doc.Monitor.TestURL
		if testURL == "" {
			return App{}, Mihomo{}, Monitor{}, DNS{}, fmt.Errorf("%s: monitor.test_url: required", path)
		}
	}
	switchPolicy := ""
	if doc.Monitor.SwitchPolicy != nil {
		switchPolicy = *doc.Monitor.SwitchPolicy
	}
	return App{SystemProxy: SystemProxy{Enabled: doc.SystemProxy.Enabled}}, Mihomo{
		Binary:           doc.Mihomo.Binary,
		URLTestInterval:  urlTestInterval,
		URLTestTolerance: urlTestTolerance,
	}, Monitor{
		Enabled:               enabled,
		TestURL:               testURL,
		SwitchPolicy:          switchPolicy,
		Interval:              interval,
		Timeout:               timeout,
		Concurrency:           concurrency,
		Threshold:             threshold,
		AlertThreshold:        alertThreshold,
		ConsecutiveBadSamples: badSamples,
		MinImprovement:        minImprovement,
		Cooldown:              cooldown,
		Jitter:                jitter,
		AutomatedGroups:       append([]string(nil), doc.Monitor.AutomatedGroups...),
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
			UserAgent:       entry.UserAgent,
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
			Kind:     ResourceKind(entry.Kind),
			Format:   ResourceFormat(entry.Format),
			RuleType: RuleType(entry.RuleType),
			URL:      entry.URL,
			Enabled:  entry.Enabled,
			Interval: interval,
			SHA256:   entry.SHA256,
		})
	}
	resolverSets := make([]ResolverSet, 0, len(doc.ResolverSets))
	for _, entry := range doc.ResolverSets {
		resolverSets = append(resolverSets, ResolverSet{ID: entry.ID, Endpoints: cloneStrings(entry.Endpoints), DNSCrypt: entry.DNSCrypt})
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
		items = append(items, Filter{
			ID:       entry.ID,
			Resource: entry.Resource,
			Format:   ResourceFormat(entry.Format),
			Target:   entry.Target,
			Enabled:  entry.Enabled,
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

func nonnegativeDurationValue(path, field string, raw *string) (time.Duration, error) {
	if raw == nil {
		return 0, nil
	}
	d, err := time.ParseDuration(*raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %s: %w", path, field, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s: %s: must be nonnegative", path, field)
	}
	return d, nil
}
