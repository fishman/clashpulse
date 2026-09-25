package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
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
