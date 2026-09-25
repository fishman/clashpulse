package core

const MaxDiagnostics = 200

type GroupSnapshot struct {
	ID                string
	Label             string
	Type              string
	Selected          string
	Proxies           []string
	AutomationEnabled bool
	ManualOverride    bool
}

type ProxySnapshot struct {
	ID            string
	Label         string
	GroupID       string
	LatencyMillis int64
	FinishedAt    int64
	Outcome       string
}

type UsageSnapshot struct {
	UploadedBytes   uint64
	DownloadedBytes uint64
	TotalBytes      uint64
	ExpiresAt       int64
}

type SubscriptionSnapshot struct {
	ID, Name, SourceHost, HashPrefix, AppliedHashPrefix string
	Route                                               string
	RefreshIntervalSeconds, TimeoutSeconds              uint32
	AllowHTTP, AllowInvalidTLS                          bool
	Enabled, Active, PendingActivation                  bool
	LastCheck, LastSuccess, NextDue                     int64
	LastFailure                                         string
	Usage                                               *UsageSnapshot
}

type ResourceSnapshot struct {
	ID, Kind, Format, RuleType, SourceHost, HashPrefix, Destination string
	Validated, Enabled                                              bool
	LastResult                                                      string
	LastCheck, LastSuccess, NextDue                                 int64
}

type FilterSnapshot struct {
	ID, ResourceID, Format, Target, SourceHost, HashPrefix, Destination, LastFailure string
	Enabled, Validated                                                               bool
	LastSuccess, NextDue                                                             int64
}

type BinarySnapshot struct {
	Desired, ObservedVersion, LastCompatibilityFailure string
	Capabilities                                       []string
}

type MonitorSnapshot struct {
	Enabled               bool
	TestURL               string
	IntervalSeconds       int64
	TimeoutMillis         int64
	Concurrency           int
	ThresholdMillis       int64
	AlertThresholdMillis  int64
	ConsecutiveBadSamples int
	MinImprovementMillis  int64
	CooldownSeconds       int64
	JitterMillis          int64
}

type ResolverSetSnapshot struct {
	ID        string
	Endpoints []string
	DNSCrypt  bool
}

type DNSRouteSnapshot struct {
	Suffix, GeoSite, Resource, ResolverSet string
}

type DNSSnapshot struct {
	Listen       string
	ResolverSets []ResolverSetSnapshot
	Routes       []DNSRouteSnapshot
}

type SystemProxySnapshot struct {
	Enabled bool
	Active  bool
}

type ProbeSnapshot struct {
	ProxyID       string
	FinishedAt    int64
	LatencyMillis int64
	Outcome       string
}

type SwitchSnapshot struct {
	GroupID, OldID, NewID, Reason string
	At                            int64
	Evidence                      []ProbeSnapshot
}

type JobSnapshot struct {
	ID, Kind, State string
}
type ErrorSnapshot struct {
	File, Key, Kind, SourceID, Message string
}

type DiagnosticSnapshot struct {
	At                                int64
	Severity, Kind, SourceID, Message string
}

type Snapshot struct {
	Revision      uint64
	Groups        []GroupSnapshot
	Proxies       []ProxySnapshot
	Subscriptions []SubscriptionSnapshot
	Resources     []ResourceSnapshot
	Filters       []FilterSnapshot
	Binary        BinarySnapshot
	Monitor       MonitorSnapshot
	DNS           DNSSnapshot
	SystemProxy   SystemProxySnapshot
	Jobs          []JobSnapshot
	Switches      []SwitchSnapshot
	Errors        []ErrorSnapshot
	Diagnostics   []DiagnosticSnapshot
}

func NewSnapshot(groups []GroupSnapshot) Snapshot {
	return CloneSnapshot(Snapshot{Groups: groups})
}

func CloneSnapshot(snapshot Snapshot) Snapshot {
	out := snapshot
	out.Groups = append([]GroupSnapshot(nil), snapshot.Groups...)
	for i := range out.Groups {
		out.Groups[i].Proxies = append([]string(nil), snapshot.Groups[i].Proxies...)
	}
	out.Proxies = append([]ProxySnapshot(nil), snapshot.Proxies...)
	out.Subscriptions = append([]SubscriptionSnapshot(nil), snapshot.Subscriptions...)
	for i := range out.Subscriptions {
		if snapshot.Subscriptions[i].Usage != nil {
			usage := *snapshot.Subscriptions[i].Usage
			out.Subscriptions[i].Usage = &usage
		}
	}
	out.Resources = append([]ResourceSnapshot(nil), snapshot.Resources...)
	out.Filters = append([]FilterSnapshot(nil), snapshot.Filters...)
	out.DNS.ResolverSets = append([]ResolverSetSnapshot(nil), snapshot.DNS.ResolverSets...)
	for i := range out.DNS.ResolverSets {
		out.DNS.ResolverSets[i].Endpoints = append([]string(nil), snapshot.DNS.ResolverSets[i].Endpoints...)
	}
	out.DNS.Routes = append([]DNSRouteSnapshot(nil), snapshot.DNS.Routes...)
	out.Binary.Capabilities = append([]string(nil), snapshot.Binary.Capabilities...)
	out.Jobs = append([]JobSnapshot(nil), snapshot.Jobs...)
	out.Errors = append([]ErrorSnapshot(nil), snapshot.Errors...)
	out.Diagnostics = append([]DiagnosticSnapshot(nil), snapshot.Diagnostics...)
	out.Switches = append([]SwitchSnapshot(nil), snapshot.Switches...)
	for i := range out.Switches {
		out.Switches[i].Evidence = append([]ProbeSnapshot(nil), snapshot.Switches[i].Evidence...)
	}
	return out
}
