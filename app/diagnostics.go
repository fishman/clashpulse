package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/subscriptions"
)

type diagnosticEvent struct {
	Severity, Kind, SourceID, Message string
	At                                time.Time
}

// unsupportedSystemProxy reports a desktop environment this build cannot
// configure, as opposed to a System Proxy apply that failed and can be retried.
// ponytail: stable sysproxy message prefix; move to a sentinel if sysproxy grows one.
func unsupportedSystemProxy(err error) bool {
	return err != nil && strings.Contains(err.Error(), "sysproxy: unsupported")
}

func safeActivationReason(err error) (string, core.ActivationStage) {
	if errors.Is(err, subscriptions.ErrRestore) {
		return core.ActivationRollback.Message(), core.ActivationRollback
	}
	if public, ok := core.PublicActivation(err); ok {
		return public.Error(), public.Stage
	}
	switch {
	case errors.Is(err, subscriptions.ErrActivationCleanupPending):
		return "activation committed; cleanup pending", core.ActivationStateCommit
	case errors.Is(err, subscriptions.ErrStore):
		return core.ActivationStateCommit.Message(), core.ActivationStateCommit
	default:
		return "", ""
	}
}

func safeDiagnostic(kind, sourceID string, err error) diagnosticEvent {
	if !validDiagnosticID(kind) {
		kind = "service"
	}
	if !validDiagnosticID(sourceID) {
		sourceID = ""
	}
	event := diagnosticEvent{Kind: kind, SourceID: sourceID, Severity: "info", Message: "recovered", At: time.Now().UTC()}
	if err == nil {
		return event
	}
	event.Severity, event.Message = "error", "operation failed"
	if kind == "activate_subscription" {
		if message, _ := safeActivationReason(err); message != "" {
			event.Message = message
			return event
		}
	}
	if status, ok := download.StatusErrorFrom(err); ok {
		event.Message = status.Error()
		return event
	}
	switch {
	case errors.Is(err, subscriptions.ErrFetch):
		event.Message = "fetch failed"
	case errors.Is(err, subscriptions.ErrInvalidProfile):
		event.Message = "invalid proxy profile"
	case errors.Is(err, subscriptions.ErrValidation):
		event.Message = "candidate validation failed"
	case errors.Is(err, context.DeadlineExceeded):
		event.Message = "operation timed out"
	case errors.Is(err, context.Canceled):
		event.Message = "operation cancelled"
	}
	return event
}

func validDiagnosticID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i := range len(value) {
		c := value[i]
		letterOrDigit := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !letterOrDigit && (i == 0 || c != '.' && c != '_' && c != '-') {
			return false
		}
	}
	return true
}

func (s *runtimeService) appendDiagnostic(event diagnosticEvent) {
	if event.Message == "" {
		return
	}
	if len(event.Message) > 160 || strings.ContainsAny(event.Message, "\x00\r\n") || strings.Contains(event.Message, "://") {
		event.Message = "operation failed"
	}
	entry := core.DiagnosticSnapshot{
		At: event.At.Unix(), Severity: event.Severity, Kind: event.Kind, SourceID: event.SourceID, Message: event.Message,
	}
	if len(s.snapshot.Diagnostics) < core.MaxDiagnostics {
		s.snapshot.Diagnostics = append(s.snapshot.Diagnostics, entry)
		return
	}
	copy(s.snapshot.Diagnostics, s.snapshot.Diagnostics[1:])
	s.snapshot.Diagnostics[len(s.snapshot.Diagnostics)-1] = entry
}

func (s *runtimeService) upsertIssue(issue core.ErrorSnapshot) bool {
	for i := range s.snapshot.Errors {
		if s.snapshot.Errors[i].Kind == issue.Kind && s.snapshot.Errors[i].SourceID == issue.SourceID {
			if s.snapshot.Errors[i] == issue {
				return false
			}
			s.snapshot.Errors[i] = issue
			return true
		}
	}
	s.snapshot.Errors = append(s.snapshot.Errors, issue)
	return true
}

func (s *runtimeService) resolveIssue(kind, sourceID string) bool {
	for i := range s.snapshot.Errors {
		if s.snapshot.Errors[i].Kind == kind && s.snapshot.Errors[i].SourceID == sourceID {
			s.snapshot.Errors = slices.Delete(s.snapshot.Errors, i, i+1)
			s.appendDiagnostic(safeDiagnostic(kind, sourceID, nil))
			return true
		}
	}
	return false
}

func (s *runtimeService) reconcileSubscriptionFailures(entries []subscriptions.Entry) {
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !validDiagnosticID(entry.ID) {
			continue
		}
		seen[entry.ID] = true
		if entry.LastFailure == "" {
			s.resolveIssue("refresh_subscription", entry.ID)
			continue
		}
		issue := core.ErrorSnapshot{Kind: "refresh_subscription", Key: "subscription", SourceID: entry.ID, Message: publicFailureLabel("subscription", entry.LastFailure)}
		if s.upsertIssue(issue) {
			var err error = subscriptions.ErrFetch
			if status, ok := download.ParseStatus(entry.LastFailure); ok {
				err = status
			}
			s.appendDiagnostic(safeDiagnostic("refresh_subscription", entry.ID, err))
		}
	}
	for i := len(s.snapshot.Errors) - 1; i >= 0; i-- {
		issue := s.snapshot.Errors[i]
		if issue.Kind == "refresh_subscription" && issue.SourceID != "" && !seen[issue.SourceID] {
			s.resolveIssue(issue.Kind, issue.SourceID)
		}
	}
}

func (s *runtimeService) completeScheduledResourceRefresh() {
	s.resolveIssue("resource", "")
}
