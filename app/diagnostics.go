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
	var status download.StatusError
	switch {
	case errors.As(err, &status) && status.Valid():
		event.Message = status.Error()
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
	if len(s.snapshot.Errors) > core.MaxDiagnostics {
		s.snapshot.Errors = s.snapshot.Errors[len(s.snapshot.Errors)-core.MaxDiagnostics:]
	}
	return true
}

func (s *runtimeService) resolveIssue(kind, sourceID string) {
	for i := range s.snapshot.Errors {
		if s.snapshot.Errors[i].Kind == kind && s.snapshot.Errors[i].SourceID == sourceID {
			s.snapshot.Errors = slices.Delete(s.snapshot.Errors, i, i+1)
			s.appendDiagnostic(safeDiagnostic(kind, sourceID, nil))
			return
		}
	}
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
