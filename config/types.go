package config

import "time"

type Snapshot struct {
	App           App
	Mihomo        Mihomo
	Monitor       Monitor
	DNS           DNS
	Subscriptions []Subscription
	Resources     []Resource
	Filters       []Filter
}

type App struct {
	SystemProxy SystemProxy
}

type SystemProxy struct {
	Enabled bool
}

type Mihomo struct {
	Binary string
}

type Monitor struct {
	Enabled               bool
	TestURL               string
	Interval              time.Duration
	Timeout               time.Duration
	Concurrency           int
	Threshold             time.Duration
	AlertThreshold        time.Duration
	ConsecutiveBadSamples int
	MinImprovement        time.Duration
	Cooldown              time.Duration
	Jitter                time.Duration
	AutomatedGroups       []string
}

type DNS struct {
	Listen       string
	ResolverSets []ResolverSet
	Routes       []DNSRoute
}

type Subscription struct {
	ID              string
	Name            string
	URL             string
	Enabled         bool
	RefreshInterval time.Duration
	Timeout         time.Duration
	Route           string
	AllowHTTP       bool
	AllowInvalidTLS bool
}

type ResourceKind string

const (
	ResourceGeoIP        ResourceKind = "geoip.dat"
	ResourceGeoSite      ResourceKind = "geosite.dat"
	ResourceMMDB         ResourceKind = "Country.mmdb"
	ResourceRuleSet      ResourceKind = "rule-set"
	ResourceRuleProvider ResourceKind = "rule-provider"
)

type ResourceFormat string

const (
	FormatDAT  ResourceFormat = "dat"
	FormatMMDB ResourceFormat = "mmdb"
	FormatYAML ResourceFormat = "yaml"
	FormatText ResourceFormat = "text"
	FormatMRS  ResourceFormat = "mrs"
)

type RuleType string

const (
	RuleDomain    RuleType = "domain"
	RuleIPCIDR    RuleType = "ipcidr"
	RuleClassical RuleType = "classical"
)

type Resource struct {
	ID       string
	Kind     ResourceKind
	Format   ResourceFormat
	RuleType RuleType
	URL      string
	Enabled  bool
	Interval time.Duration
	SHA256   string
}

type ResolverSet struct {
	ID        string
	Endpoints []string
	DNSCrypt  bool
}

type DNSRoute struct {
	Suffix      string
	GeoSite     string
	Resource    string
	ResolverSet string
}

type Filter struct {
	ID       string
	Resource string
	Format   ResourceFormat
	Target   string
	Enabled  bool
}

type Change struct {
	Section  string
	Sections []string
	Before   Snapshot
	After    Snapshot
}

func cloneSnapshot(s Snapshot) Snapshot {
	out := s
	out.Subscriptions = cloneSubscriptions(s.Subscriptions)
	out.Resources = cloneResources(s.Resources)
	out.Filters = cloneFilters(s.Filters)
	out.Monitor.AutomatedGroups = cloneStrings(s.Monitor.AutomatedGroups)
	out.DNS.ResolverSets = cloneResolverSets(s.DNS.ResolverSets)
	out.DNS.Routes = cloneDNSRoutes(s.DNS.Routes)
	return out
}

func cloneSubscriptions(in []Subscription) []Subscription {
	if len(in) == 0 {
		return nil
	}
	out := make([]Subscription, len(in))
	copy(out, in)
	return out
}

func cloneResources(in []Resource) []Resource {
	if len(in) == 0 {
		return nil
	}
	out := make([]Resource, len(in))
	copy(out, in)
	return out
}

func cloneFilters(in []Filter) []Filter {
	if len(in) == 0 {
		return nil
	}
	out := make([]Filter, len(in))
	copy(out, in)
	return out
}

func cloneResolverSets(in []ResolverSet) []ResolverSet {
	if len(in) == 0 {
		return nil
	}
	out := make([]ResolverSet, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Endpoints = cloneStrings(in[i].Endpoints)
	}
	return out
}

func cloneDNSRoutes(in []DNSRoute) []DNSRoute {
	if len(in) == 0 {
		return nil
	}
	out := make([]DNSRoute, len(in))
	copy(out, in)
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
