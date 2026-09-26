package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/resources"
	"github.com/fishman/clashpulse/subscriptions"
	"github.com/fishman/notmutt/lib/xdg"
)

// RefreshOptions controls local-only refresh diagnostics.
type RefreshOptions struct{ ShowResponse bool }

// Refresh runs one source update, a source kind, or all enabled sources offline.
func Refresh(ctx context.Context, kind, id string) error {
	return RefreshWithOptions(ctx, kind, id, RefreshOptions{})
}

func RefreshWithOptions(ctx context.Context, kind, id string, options RefreshOptions) error {
	configHome, stateHome := xdg.ConfigHome(), xdg.StateHome()
	if configHome == "" || stateHome == "" {
		return fmt.Errorf("clashpulse: cannot resolve private configuration and state directories")
	}
	return RefreshAtWithOptions(ctx, filepath.Join(configHome, "clashpulse"), filepath.Join(stateHome, "clashpulse"), kind, id, options)
}

func RefreshAll(ctx context.Context) error { return Refresh(ctx, "", "") }

func RefreshAllAt(ctx context.Context, configDir, stateDir string) error {
	return RefreshAt(ctx, configDir, stateDir, "", "")
}

// RefreshAt is the private-state variant used by the CLI and isolated tests.
// Empty kind refreshes all enabled sources; empty id refreshes the selected kind.
func RefreshAt(ctx context.Context, configDir, stateDir, kind, id string) error {
	return RefreshAtWithOptions(ctx, configDir, stateDir, kind, id, RefreshOptions{})
}

// RefreshAtWithOptions optionally captures a bounded HTTP error response body.
func RefreshAtWithOptions(ctx context.Context, configDir, stateDir, kind, id string, options RefreshOptions) error {
	if ctx == nil {
		return fmt.Errorf("clashpulse: context is required")
	}
	if kind != "" && kind != "subscription" && kind != "resource" || kind == "" && id != "" {
		return fmt.Errorf("clashpulse: refresh requires subscription or resource")
	}
	if id != "" && !validDiagnosticID(id) {
		return fmt.Errorf("clashpulse: invalid refresh source ID")
	}
	release, err := acquireOwnerLock(stateDir)
	if err != nil {
		return err
	}
	defer release()
	if err := privateDirectory(configDir); err != nil {
		return fmt.Errorf("clashpulse: configuration unavailable")
	}
	if err := config.Seed(configDir); err != nil {
		return fmt.Errorf("clashpulse: configuration unavailable")
	}
	initial, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("clashpulse: configuration is invalid")
	}
	if id != "" {
		switch kind {
		case "subscription":
			if !hasSubscription(initial, id) {
				return fmt.Errorf("clashpulse: subscription %s is not configured", id)
			}
		case "resource":
			if !configuredResource(initial, id) {
				return fmt.Errorf("clashpulse: resource %s is not enabled", id)
			}
		}
	}
	s, err := newRuntimeServiceWithResponseCapture(configDir, stateDir, initial, options.ShowResponse)
	if err != nil {
		return fmt.Errorf("clashpulse: refresh state unavailable")
	}
	if err := ctx.Err(); err != nil {
		return directRefreshFailure(kind, id, err)
	}
	switch kind {
	case "":
		return errors.Join(s.refreshSubscriptions(ctx), s.refreshResources(ctx, initial, nil))
	case "subscription":
		if id == "" {
			return s.refreshSubscriptions(ctx)
		}
		return s.refreshSubscription(ctx, id)
	case "resource":
		if id == "" {
			return s.refreshResources(ctx, initial, nil)
		}
		return s.refreshResources(ctx, initial, []string{id})
	default:
		return fmt.Errorf("clashpulse: unsupported refresh target")
	}
}

func (s *runtimeService) refreshSubscription(ctx context.Context, id string) error {
	if _, err := s.subs.Refresh(ctx, id); err != nil {
		return directRefreshFailure("subscription", id, err)
	}
	return nil
}

func (s *runtimeService) refreshSubscriptions(ctx context.Context) error {
	var failures []error
	for _, subscription := range s.subs.List() {
		if !subscription.Enabled {
			continue
		}
		if err := ctx.Err(); err != nil {
			failures = append(failures, directRefreshFailure("subscription", subscription.ID, err))
			break
		}
		if err := s.refreshSubscription(ctx, subscription.ID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *runtimeService) refreshResources(ctx context.Context, snapshot config.Snapshot, ids []string) error {
	if ids == nil {
		for _, resource := range snapshot.Resources {
			if resource.Enabled {
				ids = append(ids, resource.ID)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	prepared, err := s.prepareResourceRefresh(ctx, ids)
	if err != nil {
		return s.resourceRefreshFailure(snapshot, ids, err)
	}
	if err := ctx.Err(); err != nil {
		_ = prepared.plan.Abort()
		return directRefreshFailure("resource", ids[0], err)
	}
	if _, err := prepared.plan.Commit(); err != nil {
		_ = prepared.plan.Abort()
		return directRefreshFailure("resource", ids[0], err)
	}
	return nil
}

func (s *runtimeService) resourceRefreshFailure(snapshot config.Snapshot, ids []string, cause error) error {
	statuses, statusErr := s.registry.Status(snapshot)
	if statusErr == nil {
		byID := make(map[string]resources.ResourceStatus, len(statuses))
		for _, status := range statuses {
			byID[status.ID] = status
		}
		var failures []error
		for _, id := range ids {
			status := byID[id]
			if status.LastFailure == "" || status.LastFailure == "no committed resource version" || status.LastFailure == "committed resource is unavailable or invalid" {
				continue
			}
			message := "resource update failed"
			if httpStatus, ok := download.ParseStatus(status.LastFailure); ok {
				message = httpStatusMessage(httpStatus)
				if len(ids) == 1 {
					if captured, found := download.StatusErrorFrom(cause); found && captured.Code == httpStatus.Code {
						failures = append(failures, refreshFailure{message: fmt.Sprintf("clashpulse: refresh resource %s: %s", id, message), status: captured})
						continue
					}
				}
			} else if status.LastFailure == resources.ErrPinMismatch.Error() {
				message = "SHA-256 pin mismatch"
			}
			if len(ids) == 1 {
				if response, found := download.HTTPResponseFrom(cause); found && !response.Valid() {
					failures = append(failures, refreshFailure{message: fmt.Sprintf("clashpulse: refresh resource %s: %s", id, message), status: response})
					continue
				}
			}
			failures = append(failures, fmt.Errorf("clashpulse: refresh resource %s: %s", id, message))
		}
		if len(failures) > 0 {
			return errors.Join(failures...)
		}
	}
	return directRefreshFailure("resource", ids[0], cause)
}

func httpStatusMessage(status download.StatusError) string {
	if reason := http.StatusText(status.Code); reason != "" {
		return fmt.Sprintf("HTTP %d %s", status.Code, reason)
	}
	return status.Error()
}

func hasSubscription(snapshot config.Snapshot, id string) bool {
	for _, subscription := range snapshot.Subscriptions {
		if subscription.ID == id {
			return true
		}
	}
	return false
}

type refreshFailure struct {
	message string
	status  download.StatusError
}

func (e refreshFailure) Error() string { return e.message }
func (e refreshFailure) Unwrap() error { return e.status }

func directRefreshFailure(kind, id string, err error) error {
	status, hasStatus := download.StatusErrorFrom(err)
	response, hasResponse := download.HTTPResponseFrom(err)
	message := safeDiagnostic("refresh_"+kind, id, err).Message
	if hasStatus {
		message = httpStatusMessage(status)
	} else {
		switch {
		case errors.Is(err, subscriptions.ErrNotFound):
			message = "source is not available"
		case errors.Is(err, subscriptions.ErrNoSnapshot):
			message = "no active validated profile for resource validation"
		case errors.Is(err, subscriptions.ErrRender):
			message = "candidate profile generation failed"
		case errors.Is(err, resources.ErrPinMismatch):
			message = "SHA-256 pin mismatch"
		}
	}
	scope := kind
	if id != "" {
		scope += " " + id
	} else if scope == "" {
		scope = "all enabled sources"
	} else {
		scope += "s"
	}
	fullMessage := fmt.Sprintf("clashpulse: refresh %s: %s", scope, message)
	if hasResponse {
		return refreshFailure{message: fullMessage, status: response}
	}
	return errors.New(fullMessage)
}
