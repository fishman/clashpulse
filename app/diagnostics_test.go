package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/subscriptions"
)

func TestDiagnosticsBoundedAndRedacted(t *testing.T) {
	service := &runtimeService{}
	secret := "https://private.invalid/profile?token=secret"
	event := safeDiagnostic("refresh_subscription", "alpha", errors.New(secret))
	if strings.Contains(event.Message, "secret") || strings.Contains(event.Message, "private.invalid") {
		t.Fatal("raw error escaped diagnostic boundary")
	}
	for i := range 201 {
		event.At = time.Unix(int64(i+1), 0)
		service.appendDiagnostic(event)
	}
	if got := service.snapshot.Diagnostics; len(got) != 200 || got[0].At != 2 || got[199].At != 201 {
		t.Fatal("session log did not evict its oldest entry")
	}
	status := safeDiagnostic("refresh_subscription", "alpha", download.StatusError{Code: 406})
	if !strings.Contains(status.Message, "HTTP 406") {
		t.Fatal("safe HTTP status was not available in the session log")
	}
}

func TestIssueResolutionPreservesOtherSource(t *testing.T) {
	service := &runtimeService{snapshot: core.Snapshot{Errors: []core.ErrorSnapshot{
		{Kind: "refresh_subscription", Key: "subscription", SourceID: "alpha", Message: "HTTP 406"},
		{Kind: "resource", Key: "resource", SourceID: "beta", Message: "resource update failed"},
	}}}
	service.resolveIssue("refresh_subscription", "alpha")
	if len(service.snapshot.Errors) != 1 || service.snapshot.Errors[0].SourceID != "beta" {
		t.Fatal("resolving subscription failure cleared an unrelated issue")
	}
}

func TestRepeatedIssueDoesNotDuplicateActiveState(t *testing.T) {
	service := &runtimeService{}
	issue := core.ErrorSnapshot{Kind: "refresh_subscription", Key: "refresh_subscription", SourceID: "alpha", Message: "HTTP 406"}
	if !service.upsertIssue(issue) || service.upsertIssue(issue) || len(service.snapshot.Errors) != 1 {
		t.Fatal("unchanged issue emitted duplicate state")
	}
}

func TestReconcileSubscriptionFailureEmitsSingleRecovery(t *testing.T) {
	service := &runtimeService{snapshot: core.Snapshot{Errors: []core.ErrorSnapshot{{Kind: "resource", SourceID: "beta", Message: "resource update failed"}}}}
	failed := []subscriptions.Entry{{ID: "alpha", LastFailure: "HTTP 406"}}
	service.reconcileSubscriptionFailures(failed)
	service.reconcileSubscriptionFailures(failed)
	if len(service.snapshot.Diagnostics) != 1 || len(service.snapshot.Errors) != 2 {
		t.Fatalf("repeated failure duplicated events: %+v", service.snapshot)
	}
	service.reconcileSubscriptionFailures([]subscriptions.Entry{{ID: "alpha"}})
	service.reconcileSubscriptionFailures([]subscriptions.Entry{{ID: "alpha"}})
	if len(service.snapshot.Diagnostics) != 2 || service.snapshot.Diagnostics[1].Message != "recovered" || len(service.snapshot.Errors) != 1 || service.snapshot.Errors[0].SourceID != "beta" {
		t.Fatalf("recovery altered unrelated issue or repeated event: %+v", service.snapshot)
	}
}

func TestPublicSubscriptionFailureShowsStatusWithoutRawDetail(t *testing.T) {
	if got := publicFailureLabel("subscription", "HTTP 406"); got != "subscription HTTP 406" {
		t.Fatalf("safe status not visible: %q", got)
	}
	if got := publicFailureLabel("subscription", "HTTP 406 token=private"); strings.Contains(got, "private") {
		t.Fatal("untrusted failure detail escaped to IPC")
	}
}

func TestScheduledResourceRefreshResolvesActiveIssue(t *testing.T) {
	service := &runtimeService{snapshot: core.Snapshot{Errors: []core.ErrorSnapshot{{Kind: "resource", Message: "resource update failed"}}}}
	service.completeScheduledResourceRefresh()
	if len(service.snapshot.Errors) != 0 || len(service.snapshot.Diagnostics) != 1 || service.snapshot.Diagnostics[0].Message != "recovered" {
		t.Fatalf("scheduled success did not resolve resource issue: %+v", service.snapshot)
	}
}

func TestUnresolvedIssuesSurviveDiagnosticHistoryLimit(t *testing.T) {
	service := &runtimeService{}
	for i := range core.MaxDiagnostics + 1 {
		service.upsertIssue(core.ErrorSnapshot{Kind: "refresh_subscription", SourceID: fmt.Sprintf("feed-%03d", i), Message: "HTTP 406"})
	}
	if len(service.snapshot.Errors) != core.MaxDiagnostics+1 || service.snapshot.Errors[0].SourceID != "feed-000" {
		t.Fatalf("old unresolved issue was evicted: count=%d first=%+v", len(service.snapshot.Errors), service.snapshot.Errors[0])
	}
}

func TestDiagnosticsExposeSafeActivationStage(t *testing.T) {
	service, err := newRuntimeService(t.TempDir(), t.TempDir(), config.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	server, err := ipc.NewServer(ipc.ServerOptions{
		Endpoint:        filepath.Join(t.TempDir(), "service.sock"),
		Handler:         func(context.Context, ipc.Command) error { return nil },
		InitialSnapshot: service.snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	service.server = server
	service.snapshot.Errors = []core.ErrorSnapshot{{Kind: "resource", SourceID: "other", Message: "resource update failed"}}
	public, _ := core.PublicActivation(core.WrapActivationResource(core.ActivationResources, "geosite", errors.New("https://feed.invalid/?token=private")))
	service.reportErrorScoped("activate_subscription", "feed", errors.Join(subscriptions.ErrActivation, public))
	if len(service.snapshot.Errors) != 2 || service.snapshot.Errors[1].Message != public.Error() ||
		len(service.snapshot.Diagnostics) != 1 || service.snapshot.Diagnostics[0].Message != public.Error() ||
		strings.Contains(fmt.Sprint(service.snapshot.Errors, service.snapshot.Diagnostics), "private") {
		t.Fatalf("activation stage missing or leaked: %+v %+v", service.snapshot.Errors, service.snapshot.Diagnostics)
	}
	service.resolveIssue("activate_subscription", "feed")
	if len(service.snapshot.Errors) != 1 || service.snapshot.Errors[0].SourceID != "other" {
		t.Fatalf("activation recovery cleared unrelated issue: %+v", service.snapshot.Errors)
	}
	service.reportErrorScoped("config_reload", "", errors.New("bad config"))
	if service.snapshot.Errors[1].Message == public.Error() {
		t.Fatal("activation stage leaked into config reload issue")
	}
	service.reportErrorScoped("activate_subscription", "feed", subscriptions.ErrStore)
	if got := service.snapshot.Errors[len(service.snapshot.Errors)-1].Message; got != core.ActivationStateCommit.Message() {
		t.Fatalf("store failure stage = %q", got)
	}
	if got := service.snapshot.Diagnostics[len(service.snapshot.Diagnostics)-1].Message; got != core.ActivationStateCommit.Message() {
		t.Fatalf("store diagnostic stage = %q", got)
	}
	service.reportErrorScoped("activate_subscription", "feed", errors.Join(subscriptions.ErrStore, subscriptions.ErrRestore))
	if got := service.snapshot.Errors[len(service.snapshot.Errors)-1].Message; got != core.ActivationRollback.Message() {
		t.Fatalf("rollback failure stage = %q", got)
	}
	message, stage := safeActivationReason(errors.Join(core.WrapActivation(core.ActivationConfigValidation, errors.New("candidate rejected")), subscriptions.ErrRestore))
	if stage != core.ActivationRollback || message != core.ActivationRollback.Message() {
		t.Fatalf("rollback sentinel hidden by original failure: %s, %s", stage, message)
	}
}
