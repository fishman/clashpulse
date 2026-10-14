package ipc

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
)

func TestCommandValidationBoundsIdentifiersAndSettings(t *testing.T) {
	validCommands := []Command{
		{Kind: CommandStart},
		{Kind: CommandTestNotification},
		{Kind: CommandSetAutoFastest, Automation: &AutomationSetting{Enabled: true}},
		{Kind: CommandUpdateConfiguration, Config: &ConfigPatch{MonitorEnabled: new(true)}},
		{Kind: CommandManualProbe, GroupID: "main"},
		{Kind: CommandSelectGroup, GroupID: "main", ChoiceID: "proxy-a"},
		{Kind: CommandSetAutomation, GroupID: "main", Automation: &AutomationSetting{Enabled: true}},
		{Kind: CommandRefreshSubscription, SubscriptionID: "primary"},
		{Kind: CommandActivateSubscription, SubscriptionID: "primary"},
		{Kind: CommandDeleteSubscription, SubscriptionID: "primary"},
		{Kind: CommandRefreshResource, ResourceID: "geo"},
		{Kind: CommandRefreshFilter, FilterID: "ads"},
	}
	for _, command := range validCommands {
		if err := command.validate(); err != nil {
			t.Errorf("valid command %#v rejected: %v", command, err)
		}
	}
	invalidCommands := []Command{
		{Kind: CommandManualProbe, GroupID: "https://secret.invalid/profile"},
		{Kind: CommandRefreshSubscription, SubscriptionID: "https://secret.invalid/profile"},
		{Kind: CommandRefreshResource, ResourceID: "resource", FilterID: "filter"},
		{Kind: CommandUpdateConfiguration, Config: &ConfigPatch{MonitorIntervalSeconds: new(uint32(86401))}},
		{Kind: CommandTestNotification, GroupID: "main"},
		{Kind: CommandTestNotification, Config: &ConfigPatch{MonitorEnabled: new(true)}},
		{Kind: CommandSetAutoFastest},
		{Kind: CommandSetAutoFastest, GroupID: "main", Automation: &AutomationSetting{Enabled: true}},
	}
	for _, command := range invalidCommands {
		if err := command.validate(); err == nil {
			t.Errorf("invalid command accepted: %#v", command)
		}
	}
}

func TestSubscriptionEditCommandRequiresScopedFields(t *testing.T) {
	name := "Daily provider"
	source := "https://provider.invalid/profile?token=private"
	if err := (Command{Kind: CommandPutSubscription, SubscriptionID: "daily", Subscription: &SubscriptionEdit{Name: &name, URL: &source}}).validate(); err != nil {
		t.Fatalf("authenticated subscription edit rejected: %v", err)
	}
	if err := (Command{Kind: CommandStart, Subscription: &SubscriptionEdit{URL: &source}}).validate(); err == nil {
		t.Fatal("unrelated command accepted sensitive subscription payload")
	}
	if err := (Command{Kind: CommandPutSubscription, SubscriptionID: "daily", Subscription: &SubscriptionEdit{}}).validate(); err == nil {
		t.Fatal("empty subscription edit accepted")
	}
}

func TestSubscriptionUserAgentEditRejectsHeaderInjection(t *testing.T) {
	good := "clash-verge/v2.5.6"
	bad := "client\r\nAuthorization: private"
	if err := (Command{Kind: CommandPutSubscription, SubscriptionID: "daily", Subscription: &SubscriptionEdit{UserAgent: &good}}).validate(); err != nil {
		t.Fatalf("safe source agent rejected: %v", err)
	}
	if err := (Command{Kind: CommandPutSubscription, SubscriptionID: "daily", Subscription: &SubscriptionEdit{UserAgent: &bad}}).validate(); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestSnapshotRejectsInvalidSubscriptionPolicy(t *testing.T) {
	valid := core.Snapshot{Subscriptions: []core.SubscriptionSnapshot{{ID: "feed", Route: "direct", RefreshIntervalSeconds: 3600, TimeoutSeconds: 30}}}
	if err := validateSnapshot(valid); err != nil {
		t.Fatal(err)
	}
	for _, item := range []core.SubscriptionSnapshot{
		{ID: "feed", Route: "remote"},
	} {
		if err := validateSnapshot(core.Snapshot{Subscriptions: []core.SubscriptionSnapshot{item}}); err == nil {
			t.Fatal("invalid subscription policy escaped local IPC boundary")
		}
	}
}

func TestSnapshotAcceptsExistingLongSubscriptionPolicy(t *testing.T) {
	snapshot := core.Snapshot{Subscriptions: []core.SubscriptionSnapshot{{ID: "feed", Route: "direct", RefreshIntervalSeconds: 31 * 86400, TimeoutSeconds: 301}}}
	if err := validateSnapshot(snapshot); err != nil {
		t.Fatalf("valid persisted subscription durations blocked IPC startup: %v", err)
	}
}

func TestDiagnosticSnapshotBoundsAndSafety(t *testing.T) {
	valid := core.Snapshot{Diagnostics: []core.DiagnosticSnapshot{{At: 1, Severity: "error", Kind: "subscription", SourceID: "feed", Message: "HTTP 406"}}}
	if err := validateSnapshot(valid); err != nil {
		t.Fatal(err)
	}
	valid.Diagnostics[0].Message = strings.Repeat("x", 161)
	if validateSnapshot(valid) == nil {
		t.Fatal("oversized diagnostic message accepted")
	}
	valid.Diagnostics[0].Message = "HTTP 406"
	valid.Diagnostics = make([]core.DiagnosticSnapshot, 201)
	if validateSnapshot(valid) == nil {
		t.Fatal("unbounded session log escaped IPC")
	}
}

func TestResourceAndFilterEditCommandsAreScoped(t *testing.T) {
	source := "https://resource.invalid/cn.yaml?token=private"
	resourceID := "cn"
	if err := (Command{Kind: CommandPutResource, ResourceID: resourceID, Resource: &ResourceEdit{URL: &source}}).validate(); err != nil {
		t.Fatalf("authenticated resource edit rejected: %v", err)
	}
	if err := (Command{Kind: CommandRefreshResource, ResourceID: resourceID, Resource: &ResourceEdit{URL: &source}}).validate(); err == nil {
		t.Fatal("refresh accepted sensitive edit payload")
	}
	if err := (Command{Kind: CommandPutFilter, FilterID: "ads", Filter: &FilterEdit{ResourceID: &resourceID}}).validate(); err != nil {
		t.Fatalf("filter edit rejected: %v", err)
	}
	if err := (Command{Kind: CommandPutFilter, FilterID: "ads", Filter: &FilterEdit{}}).validate(); err == nil {
		t.Fatal("empty filter edit accepted")
	}
}

func TestMonitorPolicyPatchBoundsControlInputs(t *testing.T) {
	for _, value := range []uint32{1, 3, 64} {
		if err := (Command{Kind: CommandUpdateConfiguration, Config: &ConfigPatch{MonitorConcurrency: &value}}).validate(); err != nil {
			t.Fatalf("concurrency %d rejected: %v", value, err)
		}
	}
	for _, value := range []uint32{0, 65} {
		if err := (Command{Kind: CommandUpdateConfiguration, Config: &ConfigPatch{MonitorConcurrency: &value}}).validate(); err == nil {
			t.Fatalf("invalid concurrency %d accepted", value)
		}
	}
	for name, patch := range map[string]ConfigPatch{
		"window 2":         {MonitorWindowSize: new(uint32(2))},
		"window 20":        {MonitorWindowSize: new(uint32(20))},
		"candidates 2":     {MonitorMinCandidateSamples: new(uint32(2))},
		"candidates 20":    {MonitorMinCandidateSamples: new(uint32(20))},
		"failures 1":       {MonitorMaxFailures: new(uint32(1))},
		"failures 20":      {MonitorMaxFailures: new(uint32(20))},
		"bad samples 20":   {MonitorConsecutiveBadSamples: new(uint32(20))},
		"failover only":    {MonitorSwitchPolicy: new("failover")},
		"lowest latency":   {MonitorSwitchPolicy: new("lowest_latency")},
		"url-test only":    {URLTestIntervalSeconds: new(uint32(60))},
		"tolerance only":   {URLTestToleranceMillis: new(uint32(50))},
		"mixed port 1024":  {MixedPort: new(uint32(1024))},
		"mixed port 65535": {MixedPort: new(uint32(65535))},
	} {
		if err := (Command{Kind: CommandUpdateConfiguration, Config: &patch}).validate(); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	for name, patch := range map[string]ConfigPatch{
		"window 1":         {MonitorWindowSize: new(uint32(1))},
		"window 21":        {MonitorWindowSize: new(uint32(21))},
		"candidates 1":     {MonitorMinCandidateSamples: new(uint32(1))},
		"candidates 21":    {MonitorMinCandidateSamples: new(uint32(21))},
		"failures 0":       {MonitorMaxFailures: new(uint32(0))},
		"failures 21":      {MonitorMaxFailures: new(uint32(21))},
		"bad samples 0":    {MonitorConsecutiveBadSamples: new(uint32(0))},
		"bad samples 21":   {MonitorConsecutiveBadSamples: new(uint32(21))},
		"unknown policy":   {MonitorSwitchPolicy: new("fastest")},
		"empty policy":     {MonitorSwitchPolicy: new("")},
		"mixed port 80":    {MixedPort: new(uint32(80))},
		"mixed port 65536": {MixedPort: new(uint32(65536))},
		"empty patch":      {},
	} {
		if err := (Command{Kind: CommandUpdateConfiguration, Config: &patch}).validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSnapshotBoundsProxyStability(t *testing.T) {
	valid := []core.ProxySnapshot{
		{ID: "a"},
		{ID: "a", Samples: 20, Failures: 20},
		{ID: "a", Samples: 5, Failures: 1, MedianMillis: 130, JitterMillis: 20},
	}
	for _, proxy := range valid {
		if err := validateSnapshot(core.Snapshot{Proxies: []core.ProxySnapshot{proxy}}); err != nil {
			t.Errorf("valid stability %+v rejected: %v", proxy, err)
		}
	}
	invalid := []core.ProxySnapshot{
		{ID: "a", Samples: 21, Failures: 0},
		{ID: "a", Samples: 3, Failures: 4},
		{ID: "a", Samples: -1},
		{ID: "a", Samples: 2, Failures: -1},
		{ID: "a", Samples: 2, MedianMillis: -1},
		{ID: "a", Samples: 2, JitterMillis: -1},
	}
	for _, proxy := range invalid {
		if err := validateSnapshot(core.Snapshot{Proxies: []core.ProxySnapshot{proxy}}); err == nil {
			t.Errorf("invalid stability %+v accepted", proxy)
		}
	}
}

func TestTunPatchAndAuthorizeCommandValidation(t *testing.T) {
	for name, command := range map[string]Command{
		"authorize":      {Kind: CommandAuthorizeTun},
		"enable only":    {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunEnabled: new(false)}},
		"stack gvisor":   {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunStack: new("gvisor")}},
		"stack mixed":    {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunStack: new("mixed")}},
		"stack system":   {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunStack: new("system")}},
		"dns profile":    {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunDNSMode: new("profile")}},
		"dns fake-ip":    {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunDNSMode: new("fake-ip")}},
		"dns redir-host": {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunDNSMode: new("redir-host")}},
	} {
		if err := command.validate(); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	for name, command := range map[string]Command{
		"authorize with patch": {Kind: CommandAuthorizeTun, Config: &ConfigPatch{TunEnabled: new(true)}},
		"authorize with group": {Kind: CommandAuthorizeTun, GroupID: "main"},
		"unknown stack":        {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunStack: new("lwip")}},
		"empty stack":          {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunStack: new("")}},
		"unknown dns mode":     {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunDNSMode: new("normal")}},
		"empty dns mode":       {Kind: CommandUpdateConfiguration, Config: &ConfigPatch{TunDNSMode: new("")}},
	} {
		if err := command.validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSnapshotBoundsTunState(t *testing.T) {
	valid := core.TunSnapshot{Enabled: true, Stack: "gvisor", DNSMode: "profile", Privilege: core.TunPrivilegeNoAgent, PrivilegedPath: "/tmp/clashpulse/tun/mihomo", Device: "clashpulse0", LastFailure: core.ActivationTunStart.Message()}
	for _, tun := range []core.TunSnapshot{{}, valid} {
		if err := validateSnapshot(core.Snapshot{Tun: tun}); err != nil {
			t.Errorf("valid tun %+v rejected: %v", tun, err)
		}
	}
	for name, mutate := range map[string]func(*core.TunSnapshot){
		"unknown stack":     func(s *core.TunSnapshot) { s.Stack = "lwip" },
		"unknown dns mode":  func(s *core.TunSnapshot) { s.DNSMode = "normal" },
		"unknown privilege": func(s *core.TunSnapshot) { s.Privilege = "root" },
		"relative path":     func(s *core.TunSnapshot) { s.PrivilegedPath = "tun/mihomo" },
		"path newline":      func(s *core.TunSnapshot) { s.PrivilegedPath = "/tmp/mihomo\nsudo" },
		"long path":         func(s *core.TunSnapshot) { s.PrivilegedPath = "/" + strings.Repeat("a", 4096) },
		"device newline":    func(s *core.TunSnapshot) { s.Device = "clashpulse0\n" },
		"long device":       func(s *core.TunSnapshot) { s.Device = strings.Repeat("a", 16) },
		"failure newline":   func(s *core.TunSnapshot) { s.LastFailure = "failed\nprivate" },
		"long failure":      func(s *core.TunSnapshot) { s.LastFailure = strings.Repeat("x", 161) },
	} {
		tun := valid
		mutate(&tun)
		if err := validateSnapshot(core.Snapshot{Tun: tun}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestDNSRoutingEditIsScopedAndUsesTypedResolverSets(t *testing.T) {
	policy := &DNSRoutingEdit{
		ResolverSets: []DNSResolverSet{{ID: "domestic", Endpoints: []string{"udp://127.0.0.1:5353"}, DNSCrypt: true}},
		Routes:       []DNSRoute{{Resource: "cn", ResolverSet: "domestic"}},
	}
	if err := (Command{Kind: CommandSetDNSRouting, DNSRouting: policy}).validate(); err != nil {
		t.Fatalf("typed DNS route edit rejected: %v", err)
	}
	if err := (Command{Kind: CommandStart, DNSRouting: policy}).validate(); err == nil {
		t.Fatal("lifecycle command accepted DNS policy payload")
	}
}

func TestAlertThresholdMillisBounds(t *testing.T) {
	for _, millis := range []uint32{1, 250, 60000} {
		command := Command{Kind: CommandUpdateConfiguration, Config: &ConfigPatch{AlertThresholdMillis: new(millis)}}
		if err := command.validate(); err != nil {
			t.Errorf("alert threshold %d was rejected: %v", millis, err)
		}
	}
	for _, millis := range []uint32{0, 60001} {
		command := Command{Kind: CommandUpdateConfiguration, Config: &ConfigPatch{AlertThresholdMillis: new(millis)}}
		if err := command.validate(); err == nil {
			t.Errorf("out-of-range alert threshold %d was accepted", millis)
		}
	}
}

func TestBinarySelectionIntentRejectsURLsAndRelativePaths(t *testing.T) {
	for _, value := range []string{"system", "bundled", "/usr/local/bin/mihomo"} {
		if err := (Command{Kind: CommandUpdateConfiguration, Config: &ConfigPatch{Binary: new(value)}}).validate(); err != nil {
			t.Fatalf("binary %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "mihomo", "https://secret.example/mihomo", "/tmp/bin\nother"} {
		if err := (Command{Kind: CommandUpdateConfiguration, Config: &ConfigPatch{Binary: new(value)}}).validate(); err == nil {
			t.Fatalf("invalid binary %q accepted", value)
		}
	}
}

func TestSnapshotRejectsSourcePath(t *testing.T) {
	for _, source := range []string{"", "none", "local", "subscription"} {
		if err := validateSnapshot(core.Snapshot{ActiveSource: source}); err != nil {
			t.Fatalf("source %q rejected: %v", source, err)
		}
	}
	for _, source := range []string{"/tmp/password=private.yaml", "https://secret.invalid/profile", "password=private", "local\nprivate"} {
		if err := validateSnapshot(core.Snapshot{ActiveSource: source}); err == nil || strings.Contains(err.Error(), source) {
			t.Fatalf("unsafe source %q accepted or reflected: %v", source, err)
		}
	}
}

func TestConfigOverrideSnapshotRejectsPrivateValues(t *testing.T) {
	valid := core.Snapshot{ConfigOverrides: []core.ConfigOverrideSnapshot{{Key: "dns.listen", Change: "replaced"}, {Key: "secret", Change: "added"}}}
	if err := validateSnapshot(valid); err != nil {
		t.Fatalf("safe override report rejected: %v", err)
	}
	for _, override := range []core.ConfigOverrideSnapshot{{Key: "password=private", Change: "added"}, {Key: "dns.listen", Change: "https://secret.invalid"}, {Key: "/tmp/private.yaml", Change: "removed"}} {
		err := validateSnapshot(core.Snapshot{ConfigOverrides: []core.ConfigOverrideSnapshot{override}})
		if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret.invalid") {
			t.Fatalf("unsafe override passed or leaked: %v", err)
		}
	}
}

func TestConfigOverrideSnapshotAcceptsEveryReportedField(t *testing.T) {
	var overrides []core.ConfigOverrideSnapshot
	for _, key := range []string{
		"mixed-port", "port", "socks-port", "external-controller", "secret", "allow-lan", "bind-address",
		"dns.listen", "external-controller-unix", "external-controller-tls", "external-controller-cors", "external-ui", "dns.nameserver-policy",
		"rule-providers", "proxy-groups", "rules",
		"tun", "dns.enable", "dns.enhanced-mode", "dns.fake-ip-range", "dns.fake-ip-range6", "dns.fake-ip-filter",
	} {
		overrides = append(overrides, core.ConfigOverrideSnapshot{Key: key, Change: "replaced"})
	}
	if err := validateSnapshot(core.Snapshot{ConfigOverrides: overrides}); err != nil {
		t.Fatalf("full override report rejected: %v", err)
	}
	overrides = append(overrides, overrides[0])
	if validateSnapshot(core.Snapshot{ConfigOverrides: overrides}) == nil {
		t.Fatal("override report beyond the reported field set accepted")
	}
}
func TestReadFrameRejectsOversizedLengthBeforeBody(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxFrameSize+1)
	var frame clientFrame
	if err := readFrame(bytes.NewReader(header[:]), &frame); err != errFrameTooLarge {
		t.Fatalf("readFrame error = %v, want %v", err, errFrameTooLarge)
	}
}
