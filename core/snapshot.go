package core

import (
	"slices"

	"fmt"

	"github.com/fishman/clashpulse/localize"
)

const MaxDiagnostics = 200

// MaxFinishedJobs bounds the recent job outcomes a snapshot retains, so a
// client that missed a coalesced event can still read its result.
const MaxFinishedJobs = 32

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
	// MihomoMillis is what mihomo's own url-test last reported for this node,
	// shown where this application never probed the group itself.
	MihomoMillis int64
	// Window statistics over this group's probe history; Samples == 0 means
	// no window yet. Median and jitter cover successful probes only.
	MedianMillis int64
	JitterMillis int64
	Samples      int
	Failures     int
}

// StabilityLabel summarizes the probe window, or "" before any window exists.
// Median and jitter are omitted when every sample failed.
func (p ProxySnapshot) StabilityLabel() string {
	if p.Samples <= 0 {
		return ""
	}
	failed := fmt.Sprintf(localize.T("%d/%d failed"), p.Failures, p.Samples)
	if p.Failures >= p.Samples {
		return failed
	}
	return fmt.Sprintf(localize.T("median %d ms \u00b1%d ms"), p.MedianMillis, p.JitterMillis) + " \u00b7 " + failed
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

type ConfigOverrideSnapshot struct {
	Key, Change string
}

// ConfigOverrideKeys are the profile keys the generated configuration may
// override, in report order.
var ConfigOverrideKeys = []string{
	"mixed-port", "port", "socks-port", "external-controller", "secret", "allow-lan", "bind-address",
	"dns.listen", "external-controller-unix", "external-controller-tls", "external-controller-cors", "external-ui", "dns.nameserver-policy",
	"rule-providers", "proxy-groups", "rules",
	"tun", "dns.enable", "dns.enhanced-mode", "dns.fake-ip-range", "dns.fake-ip-range6", "dns.fake-ip-filter",
	"mode", "listeners",
}

func (o ConfigOverrideSnapshot) Valid() bool {
	if o.Change != "added" && o.Change != "replaced" && o.Change != "removed" {
		return false
	}
	return slices.Contains(ConfigOverrideKeys, o.Key)
}

func (o ConfigOverrideSnapshot) Description() string {
	if !o.Valid() {
		return ""
	}
	return localize.T("override." + o.Key)
}

type MonitorSnapshot struct {
	Enabled                bool
	TestURL                string
	SwitchPolicy           string
	URLTestIntervalSeconds int64
	URLTestToleranceMillis int64
	IntervalSeconds        int64
	TimeoutMillis          int64
	Concurrency            int
	ThresholdMillis        int64
	AlertThresholdMillis   int64
	ConsecutiveBadSamples  int
	WindowSize             int
	MinCandidateSamples    int
	MaxFailures            int
	MinImprovementMillis   int64
	CooldownSeconds        int64
	JitterMillis           int64
	// AutoFastest is true when the monitor runs lowest_latency switching for
	// every managed select group.
	AutoFastest bool
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

// TunSnapshot reports TUN intent and runtime state. Privilege is one of the
// TunPrivilege* values; PrivilegedPath is not secret and lets clients show
// the manual setcap command.
type TunSnapshot struct {
	Enabled        bool
	Active         bool
	Stack          string
	DNSMode        string
	Privilege      string
	PrivilegedPath string
	Device         string
	LastFailure    string
	Leftover       bool
}

const (
	TunPrivilegeReady       = "ready"
	TunPrivilegeMissing     = "missing"
	TunPrivilegeStale       = "stale"
	TunPrivilegeNoSUID      = "nosuid"
	TunPrivilegeNoAgent     = "no-agent"
	TunPrivilegeUnsupported = "unsupported-platform"

	// MixedPortMin and MixedPortMax bound the configured mixed port; Mihomo runs
	// unprivileged and cannot bind below 1024.
	MixedPortMin = 1024
	MixedPortMax = 65535

	ProxyModeRule   = "rule"
	ProxyModeGlobal = "global"
	ProxyModeDirect = "direct"

	TunStackGVisor = "gvisor"
	TunStackMixed  = "mixed"
	TunStackSystem = "system"

	TunDNSProfile   = "profile"
	TunDNSFakeIP    = "fake-ip"
	TunDNSRedirHost = "redir-host"
)

// TunStacks and TunDNSModes are every valid [tun] stack and dns_mode.
var (
	// ProxyModes are Mihomo's routing modes; rule is the default.
	ProxyModes  = []string{ProxyModeRule, ProxyModeGlobal, ProxyModeDirect}
	TunStacks   = []string{TunStackGVisor, TunStackMixed, TunStackSystem}
	TunDNSModes = []string{TunDNSProfile, TunDNSFakeIP, TunDNSRedirHost}
)

// MonitorWindowMin and MonitorWindowMax bound the per-node probe window. The
// window's candidate-success count shares the window's lower bound; its
// failure and consecutive-bad-sample counts start at 1.
const (
	MonitorWindowMin = 2
	MonitorWindowMax = 20
)

// ManualGrantCommand is the command a user runs to grant TUN privilege when
// no polkit agent can, or "" when it cannot help: file capabilities are
// ignored on a nosuid mount whoever sets them.
func (t TunSnapshot) ManualGrantCommand() string {
	if t.Privilege != TunPrivilegeNoAgent || t.PrivilegedPath == "" {
		return ""
	}
	return "sudo setcap cap_net_admin+ep " + t.PrivilegedPath
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
	Revision        uint64
	ActiveSource    string
	ServiceRunning  bool
	ConfigOverrides []ConfigOverrideSnapshot
	Groups          []GroupSnapshot
	Proxies         []ProxySnapshot
	Subscriptions   []SubscriptionSnapshot
	Resources       []ResourceSnapshot
	Filters         []FilterSnapshot
	Binary          BinarySnapshot
	Monitor         MonitorSnapshot
	DNS             DNSSnapshot
	SystemProxy     SystemProxySnapshot
	// MixedPort is the configured loopback port for HTTP, HTTPS, and SOCKS.
	MixedPort int
	// ProxyMode is the configured Mihomo routing mode.
	ProxyMode string
	Tun       TunSnapshot
	Jobs      []JobSnapshot
	// FinishedJobs holds recent outcomes, oldest first: succeeded, failed, or
	// cancelled.
	FinishedJobs []JobSnapshot
	Switches     []SwitchSnapshot
	Errors       []ErrorSnapshot
	Diagnostics  []DiagnosticSnapshot
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
	out.ConfigOverrides = append([]ConfigOverrideSnapshot(nil), snapshot.ConfigOverrides...)
	out.Filters = append([]FilterSnapshot(nil), snapshot.Filters...)
	out.DNS.ResolverSets = append([]ResolverSetSnapshot(nil), snapshot.DNS.ResolverSets...)
	for i := range out.DNS.ResolverSets {
		out.DNS.ResolverSets[i].Endpoints = append([]string(nil), snapshot.DNS.ResolverSets[i].Endpoints...)
	}
	out.DNS.Routes = append([]DNSRouteSnapshot(nil), snapshot.DNS.Routes...)
	out.Binary.Capabilities = append([]string(nil), snapshot.Binary.Capabilities...)
	out.Jobs = append([]JobSnapshot(nil), snapshot.Jobs...)
	out.FinishedJobs = append([]JobSnapshot(nil), snapshot.FinishedJobs...)
	out.Errors = append([]ErrorSnapshot(nil), snapshot.Errors...)
	out.Diagnostics = append([]DiagnosticSnapshot(nil), snapshot.Diagnostics...)
	out.Switches = append([]SwitchSnapshot(nil), snapshot.Switches...)
	for i := range out.Switches {
		out.Switches[i].Evidence = append([]ProbeSnapshot(nil), snapshot.Switches[i].Evidence...)
	}
	return out
}
