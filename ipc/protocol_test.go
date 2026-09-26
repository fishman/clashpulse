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
func TestReadFrameRejectsOversizedLengthBeforeBody(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxFrameSize+1)
	var frame clientFrame
	if err := readFrame(bytes.NewReader(header[:]), &frame); err != errFrameTooLarge {
		t.Fatalf("readFrame error = %v, want %v", err, errFrameTooLarge)
	}
}
