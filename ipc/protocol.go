package ipc

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fishman/clashpulse/core"
)

const (
	// ProtocolVersion is negotiated in the first frame on every connection.
	ProtocolVersion uint16 = 1
	// MaxFrameSize bounds both incoming and outgoing JSON frames.
	MaxFrameSize = 1 << 20
)

var (
	ErrIncompatibleVersion = errors.New("ipc: incompatible protocol version")
	ErrUnsupportedPlatform = errors.New("ipc: local transport is not supported on this platform")
	ErrSnapshotTooLarge    = errors.New("ipc: snapshot exceeds protocol bounds")
	requestIDPattern       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	stableIDPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type CommandKind string

const (
	CommandStart                CommandKind = "start"
	CommandStop                 CommandKind = "stop"
	CommandRestart              CommandKind = "restart"
	CommandReloadConfiguration  CommandKind = "reload_configuration"
	CommandUpdateConfiguration  CommandKind = "update_configuration"
	CommandManualProbe          CommandKind = "manual_probe"
	CommandSelectGroup          CommandKind = "select_group"
	CommandSetAutomation        CommandKind = "set_automation"
	CommandRefreshSubscription  CommandKind = "refresh_subscription"
	CommandActivateSubscription CommandKind = "activate_subscription"
	CommandDeleteSubscription   CommandKind = "delete_subscription"
	CommandPutSubscription      CommandKind = "put_subscription"
	CommandRefreshResource      CommandKind = "refresh_resource"
	CommandRefreshFilter        CommandKind = "refresh_filter"
	CommandPutResource          CommandKind = "put_resource"
	CommandPutFilter            CommandKind = "put_filter"
	CommandSetDNSRouting        CommandKind = "set_dns_routing"
)

// Command is a deliberately narrow user-intent schema. Sensitive URLs may
// occur only in authenticated local edit requests, never events or responses.
type Command struct {
	Kind           CommandKind        `json:"kind"`
	GroupID        string             `json:"group_id,omitempty"`
	ChoiceID       string             `json:"choice_id,omitempty"`
	SubscriptionID string             `json:"subscription_id,omitempty"`
	ResourceID     string             `json:"resource_id,omitempty"`
	FilterID       string             `json:"filter_id,omitempty"`
	Config         *ConfigPatch       `json:"config,omitempty"`
	Automation     *AutomationSetting `json:"automation,omitempty"`
	Subscription   *SubscriptionEdit  `json:"subscription,omitempty"`
	Resource       *ResourceEdit      `json:"resource,omitempty"`
	Filter         *FilterEdit        `json:"filter,omitempty"`
	DNSRouting     *DNSRoutingEdit    `json:"dns_routing,omitempty"`
}

// ConfigPatch contains only non-sensitive, typed settings that clients may
// change. Pointer fields distinguish an omitted setting from its zero value.
type ConfigPatch struct {
	SystemProxyEnabled           *bool   `json:"system_proxy_enabled,omitempty"`
	DNSListen                    *string `json:"dns_listen,omitempty"`
	MonitorEnabled               *bool   `json:"monitor_enabled,omitempty"`
	MonitorTestURL               *string `json:"monitor_test_url,omitempty"`
	MonitorIntervalSeconds       *uint32 `json:"monitor_interval_seconds,omitempty"`
	MonitorTimeoutMillis         *uint32 `json:"monitor_timeout_millis,omitempty"`
	MonitorConcurrency           *uint32 `json:"monitor_concurrency,omitempty"`
	MonitorThresholdMillis       *uint32 `json:"monitor_threshold_millis,omitempty"`
	AlertThresholdMillis         *uint32 `json:"alert_threshold_millis,omitempty"`
	MonitorConsecutiveBadSamples *uint32 `json:"monitor_consecutive_bad_samples,omitempty"`
	MonitorMinImprovementMillis  *uint32 `json:"monitor_min_improvement_millis,omitempty"`
	MonitorCooldownSeconds       *uint32 `json:"monitor_cooldown_seconds,omitempty"`
	MonitorJitterMillis          *uint32 `json:"monitor_jitter_millis,omitempty"`
	Binary                       *string `json:"binary,omitempty"`
}

// SubscriptionEdit changes only supplied fields. URL is required for a new
// source but omitted for edits that should retain its private stored value.
type SubscriptionEdit struct {
	Name                   *string `json:"name,omitempty"`
	URL                    *string `json:"url,omitempty"`
	Enabled                *bool   `json:"enabled,omitempty"`
	RefreshIntervalSeconds *uint32 `json:"refresh_interval_seconds,omitempty"`
	TimeoutSeconds         *uint32 `json:"timeout_seconds,omitempty"`
	Route                  *string `json:"route,omitempty"`
	AllowHTTP              *bool   `json:"allow_http,omitempty"`
	AllowInvalidTLS        *bool   `json:"allow_invalid_tls,omitempty"`
}

type ResourceEdit struct {
	Kind            *string `json:"kind,omitempty"`
	Format          *string `json:"format,omitempty"`
	RuleType        *string `json:"rule_type,omitempty"`
	URL             *string `json:"url,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
	IntervalSeconds *uint32 `json:"interval_seconds,omitempty"`
	SHA256          *string `json:"sha256,omitempty"`
}

type FilterEdit struct {
	ResourceID *string `json:"resource_id,omitempty"`
	Format     *string `json:"format,omitempty"`
	Target     *string `json:"target,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

type DNSResolverSet struct {
	ID        string   `json:"id"`
	Endpoints []string `json:"endpoints"`
	DNSCrypt  bool     `json:"dnscrypt"`
}

type DNSRoute struct {
	Suffix      string `json:"suffix,omitempty"`
	GeoSite     string `json:"geosite,omitempty"`
	Resource    string `json:"resource,omitempty"`
	ResolverSet string `json:"resolver_set"`
}

type DNSRoutingEdit struct {
	ResolverSets []DNSResolverSet `json:"resolver_sets"`
	Routes       []DNSRoute       `json:"routes"`
}

// AutomationSetting changes the conservative per-group automation toggle.
type AutomationSetting struct {
	Enabled bool `json:"enabled"`
}

// Acknowledgement means the application accepted the intent into its queue;
// it does not mean that the requested work has completed.
type Acknowledgement struct {
	Queued bool `json:"queued"`
}

// Event carries the sanitized core snapshot only. core.Snapshot intentionally
// contains no configuration or transport details.
type Event struct {
	Snapshot core.Snapshot `json:"snapshot"`
}

func validBinaryIntent(value string) bool {
	if value == "system" || value == "bundled" {
		return true
	}
	return value != "" && len(value) <= 4096 && filepath.IsAbs(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func (c Command) validate() error {
	if c.Kind != CommandPutSubscription && c.Subscription != nil {
		return errors.New("unexpected subscription edit fields")
	}
	if c.Kind != CommandPutResource && c.Resource != nil || c.Kind != CommandPutFilter && c.Filter != nil {
		return errors.New("unexpected resource or filter edit fields")
	}
	if c.Kind != CommandSetDNSRouting && c.DNSRouting != nil {
		return errors.New("unexpected DNS routing fields")
	}
	validID := func(id string) bool { return stableIDPattern.MatchString(id) }
	hasOtherTarget := func(want string) bool {
		return (want != "subscription" && c.SubscriptionID != "") ||
			(want != "resource" && c.ResourceID != "") ||
			(want != "filter" && c.FilterID != "")
	}
	switch c.Kind {
	case CommandStart, CommandStop, CommandRestart, CommandReloadConfiguration:
		if c.GroupID != "" || c.ChoiceID != "" || c.Config != nil || c.Automation != nil || hasOtherTarget("") {
			return errors.New("unexpected command fields")
		}
	case CommandUpdateConfiguration:
		if c.GroupID != "" || c.ChoiceID != "" || c.Automation != nil || c.Config == nil || hasOtherTarget("") {
			return errors.New("invalid configuration command")
		}
		p := c.Config
		if p.SystemProxyEnabled == nil && p.DNSListen == nil && p.MonitorEnabled == nil && p.MonitorTestURL == nil && p.MonitorIntervalSeconds == nil && p.MonitorTimeoutMillis == nil && p.MonitorConcurrency == nil && p.MonitorThresholdMillis == nil && p.AlertThresholdMillis == nil && p.MonitorConsecutiveBadSamples == nil && p.MonitorMinImprovementMillis == nil && p.MonitorCooldownSeconds == nil && p.MonitorJitterMillis == nil && p.Binary == nil {
			return errors.New("empty configuration patch")
		}
		if p.MonitorTestURL != nil && (len(*p.MonitorTestURL) > 2048 || strings.ContainsAny(*p.MonitorTestURL, "\x00\r\n")) {
			return errors.New("invalid monitor test URL")
		}
		if p.DNSListen != nil && (len(*p.DNSListen) > 128 || strings.ContainsAny(*p.DNSListen, "\x00\r\n")) {
			return errors.New("invalid DNS listener")
		}
		if p.MonitorConcurrency != nil && (*p.MonitorConcurrency < 1 || *p.MonitorConcurrency > 64) || p.MonitorConsecutiveBadSamples != nil && (*p.MonitorConsecutiveBadSamples < 1 || *p.MonitorConsecutiveBadSamples > 5) {
			return errors.New("monitor worker or sample count out of range")
		}
		if p.MonitorTimeoutMillis != nil && (*p.MonitorTimeoutMillis < 1 || *p.MonitorTimeoutMillis > 86400000) || p.MonitorThresholdMillis != nil && *p.MonitorThresholdMillis == 0 || p.MonitorMinImprovementMillis != nil && *p.MonitorMinImprovementMillis == 0 {
			return errors.New("monitor delay policy out of range")
		}
		if c.Config.MonitorIntervalSeconds != nil && (*c.Config.MonitorIntervalSeconds < 1 || *c.Config.MonitorIntervalSeconds > 86400) {
			return errors.New("monitor interval out of range")
		}
		if c.Config.AlertThresholdMillis != nil && (*c.Config.AlertThresholdMillis < 1 || *c.Config.AlertThresholdMillis > 60000) {
			return errors.New("alert threshold out of range")
		}
		if c.Config.Binary != nil && !validBinaryIntent(*c.Config.Binary) {
			return errors.New("invalid binary selection")
		}
	case CommandManualProbe:
		if !validID(c.GroupID) || c.ChoiceID != "" || c.Config != nil || c.Automation != nil || hasOtherTarget("") {
			return errors.New("invalid probe command")
		}
	case CommandSelectGroup:
		if !validID(c.GroupID) || !validID(c.ChoiceID) || c.Config != nil || c.Automation != nil || hasOtherTarget("") {
			return errors.New("invalid group selection command")
		}
	case CommandSetAutomation:
		if !validID(c.GroupID) || c.ChoiceID != "" || c.Config != nil || c.Automation == nil || hasOtherTarget("") {
			return errors.New("invalid automation command")
		}
	case CommandPutSubscription:
		if !validID(c.SubscriptionID) || c.Subscription == nil || c.GroupID != "" || c.ChoiceID != "" || c.Config != nil || c.Automation != nil || hasOtherTarget("subscription") {
			return errors.New("invalid subscription edit command")
		}
		p := c.Subscription
		if p.Name == nil && p.URL == nil && p.Enabled == nil && p.RefreshIntervalSeconds == nil && p.TimeoutSeconds == nil && p.Route == nil && p.AllowHTTP == nil && p.AllowInvalidTLS == nil {
			return errors.New("empty subscription edit")
		}
		if p.Name != nil && (len(*p.Name) > 128 || strings.ContainsAny(*p.Name, "\x00\r\n")) || p.URL != nil && (len(*p.URL) > 4096 || strings.ContainsAny(*p.URL, "\x00\r\n")) {
			return errors.New("invalid subscription edit field")
		}
		if p.RefreshIntervalSeconds != nil && (*p.RefreshIntervalSeconds < 60 || *p.RefreshIntervalSeconds > 86400*30) || p.TimeoutSeconds != nil && (*p.TimeoutSeconds < 1 || *p.TimeoutSeconds > 300) {
			return errors.New("subscription schedule out of range")
		}
		if p.Route != nil && *p.Route != "direct" && *p.Route != "system_proxy" && *p.Route != "mihomo_proxy" {
			return errors.New("invalid subscription route")
		}
	case CommandRefreshSubscription, CommandActivateSubscription, CommandDeleteSubscription:
		if !validID(c.SubscriptionID) || c.GroupID != "" || c.ChoiceID != "" || c.Config != nil || c.Automation != nil || hasOtherTarget("subscription") {
			return errors.New("invalid subscription command")
		}
	case CommandPutResource:
		if !validID(c.ResourceID) || c.Resource == nil || c.GroupID != "" || c.ChoiceID != "" || c.Config != nil || c.Automation != nil || hasOtherTarget("resource") {
			return errors.New("invalid resource edit command")
		}
		p := c.Resource
		if p.Kind == nil && p.Format == nil && p.RuleType == nil && p.URL == nil && p.Enabled == nil && p.IntervalSeconds == nil && p.SHA256 == nil {
			return errors.New("empty resource edit")
		}
		if p.URL != nil && (len(*p.URL) > 4096 || strings.ContainsAny(*p.URL, "\x00\r\n")) || p.IntervalSeconds != nil && (*p.IntervalSeconds < 60 || *p.IntervalSeconds > 86400*30) {
			return errors.New("invalid resource edit field")
		}
	case CommandPutFilter:
		if !validID(c.FilterID) || c.Filter == nil || c.GroupID != "" || c.ChoiceID != "" || c.Config != nil || c.Automation != nil || hasOtherTarget("filter") {
			return errors.New("invalid filter edit command")
		}
		p := c.Filter
		if p.ResourceID == nil && p.Format == nil && p.Target == nil && p.Enabled == nil {
			return errors.New("empty filter edit")
		}
		if p.ResourceID != nil && !validID(*p.ResourceID) || p.Target != nil && (len(*p.Target) > 128 || strings.ContainsAny(*p.Target, "\x00\r\n")) {
			return errors.New("invalid filter edit field")
		}
	case CommandSetDNSRouting:
		if c.DNSRouting == nil || c.GroupID != "" || c.ChoiceID != "" || c.Config != nil || c.Automation != nil || hasOtherTarget("") {
			return errors.New("invalid DNS routing command")
		}
		if len(c.DNSRouting.ResolverSets) > 256 || len(c.DNSRouting.Routes) > 1024 {
			return errors.New("DNS routing exceeds limits")
		}
		for _, set := range c.DNSRouting.ResolverSets {
			if !validID(set.ID) || len(set.Endpoints) == 0 || len(set.Endpoints) > 16 {
				return errors.New("invalid DNS resolver set")
			}
			for _, endpoint := range set.Endpoints {
				if len(endpoint) == 0 || len(endpoint) > 256 || strings.ContainsAny(endpoint, "\x00\r\n") {
					return errors.New("invalid DNS resolver endpoint")
				}
			}
		}
	case CommandRefreshResource:
		if !validID(c.ResourceID) || c.GroupID != "" || c.ChoiceID != "" || c.Config != nil || c.Automation != nil || hasOtherTarget("resource") {
			return errors.New("invalid resource command")
		}
	case CommandRefreshFilter:
		if !validID(c.FilterID) || c.GroupID != "" || c.ChoiceID != "" || c.Config != nil || c.Automation != nil || hasOtherTarget("filter") {
			return errors.New("invalid filter command")
		}
	default:
		return errors.New("unknown command kind")
	}
	return nil
}

func validateSnapshot(snapshot core.Snapshot) error {
	itemLimit := (MaxFrameSize - 1024) / 256
	items := 0
	addItems := func(count int) bool {
		if count < 0 || count > itemLimit-items {
			return false
		}
		items += count
		return true
	}
	if !addItems(len(snapshot.Groups)) || !addItems(len(snapshot.Proxies)) ||
		!addItems(len(snapshot.Subscriptions)) || !addItems(len(snapshot.Resources)) ||
		!addItems(len(snapshot.Filters)) || !addItems(len(snapshot.DNS.ResolverSets)) || !addItems(len(snapshot.DNS.Routes)) ||
		!addItems(len(snapshot.Binary.Capabilities)) || !addItems(len(snapshot.Jobs)) || !addItems(len(snapshot.Errors)) {
		return ErrSnapshotTooLarge
	}
	textBytes := 0
	addText := func(values ...string) bool {
		for _, value := range values {
			if len(value) > MaxFrameSize/6-textBytes {
				return false
			}
			textBytes += len(value)
		}
		return true
	}
	for _, group := range snapshot.Groups {
		if !addItems(len(group.Proxies)) || !addText(group.ID, group.Label, group.Type, group.Selected) {
			return ErrSnapshotTooLarge
		}
		for _, proxy := range group.Proxies {
			if !addText(proxy) {
				return ErrSnapshotTooLarge
			}
		}
	}
	for _, proxy := range snapshot.Proxies {
		if !addText(proxy.ID, proxy.Label, proxy.GroupID, proxy.Outcome) {
			return ErrSnapshotTooLarge
		}
	}
	for _, subscription := range snapshot.Subscriptions {
		if !addText(subscription.ID, subscription.Name, subscription.SourceHost, subscription.HashPrefix, subscription.LastFailure) {
			return ErrSnapshotTooLarge
		}
	}
	for _, resource := range snapshot.Resources {
		if !addText(resource.ID, resource.Kind, resource.SourceHost, resource.HashPrefix, resource.Destination, resource.LastResult) {
			return ErrSnapshotTooLarge
		}
	}
	for _, filter := range snapshot.Filters {
		if !addText(filter.ID, filter.ResourceID, filter.Format, filter.Target) {
			return ErrSnapshotTooLarge
		}
	}
	for _, set := range snapshot.DNS.ResolverSets {
		if !addItems(len(set.Endpoints)) || !addText(set.ID) {
			return ErrSnapshotTooLarge
		}
		for _, endpoint := range set.Endpoints {
			if !addText(endpoint) {
				return ErrSnapshotTooLarge
			}
		}
	}
	for _, route := range snapshot.DNS.Routes {
		if !addText(route.Suffix, route.GeoSite, route.Resource, route.ResolverSet) {
			return ErrSnapshotTooLarge
		}
	}
	if !addText(snapshot.Binary.Desired, snapshot.Binary.ObservedVersion, snapshot.Binary.LastCompatibilityFailure, snapshot.Monitor.TestURL, snapshot.DNS.Listen) {
		return ErrSnapshotTooLarge
	}
	for _, capability := range snapshot.Binary.Capabilities {
		if !addText(capability) {
			return ErrSnapshotTooLarge
		}
	}
	for _, job := range snapshot.Jobs {
		if !addText(job.ID, job.Kind, job.State) {
			return ErrSnapshotTooLarge
		}
	}
	for _, item := range snapshot.Errors {
		if !addText(item.File, item.Key, item.Message) {
			return ErrSnapshotTooLarge
		}
	}
	if textBytes > (MaxFrameSize-1024-items*256)/6 {
		return ErrSnapshotTooLarge
	}
	frame := serverFrame{Type: "response", RequestID: strings.Repeat("0", 64), Snapshot: &snapshot}
	body, err := json.Marshal(frame)
	if err != nil || len(body) > MaxFrameSize {
		return ErrSnapshotTooLarge
	}
	return nil
}

type clientFrame struct {
	Type      string   `json:"type"`
	Version   uint16   `json:"version,omitempty"`
	Subscribe bool     `json:"subscribe,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
	Operation string   `json:"operation,omitempty"`
	Command   *Command `json:"command,omitempty"`
}

type serverFrame struct {
	Type            string           `json:"type"`
	Version         uint16           `json:"version,omitempty"`
	RequestID       string           `json:"request_id,omitempty"`
	Acknowledgement *Acknowledgement `json:"acknowledgement,omitempty"`
	Snapshot        *core.Snapshot   `json:"snapshot,omitempty"`
	Error           *protocolError   `json:"error,omitempty"`
}

type protocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
