package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/resources"
	"github.com/fishman/clashpulse/subscriptions"
	"github.com/fishman/notmutt/lib/xdg"
)

// Refresh runs one source update without IPC or a running Mihomo process.
func Refresh(ctx context.Context, kind, id string) error {
	configHome, stateHome := xdg.ConfigHome(), xdg.StateHome()
	if configHome == "" || stateHome == "" {
		return fmt.Errorf("clashpulse: cannot resolve private configuration and state directories")
	}
	return RefreshAt(ctx, filepath.Join(configHome, "clashpulse"), filepath.Join(stateHome, "clashpulse"), kind, id)
}

// RefreshAt is the private-state variant used by the CLI and isolated tests.
func RefreshAt(ctx context.Context, configDir, stateDir, kind, id string) error {
	if ctx == nil {
		return fmt.Errorf("clashpulse: context is required")
	}
	if kind != "subscription" && kind != "resource" {
		return fmt.Errorf("clashpulse: refresh requires subscription or resource")
	}
	if !validDiagnosticID(id) {
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
	s, err := newRuntimeService(configDir, stateDir, initial)
	if err != nil {
		return fmt.Errorf("clashpulse: refresh state unavailable")
	}
	if err := ctx.Err(); err != nil {
		return directRefreshFailure(kind, id, err)
	}
	if kind == "subscription" {
		if _, err := s.subs.Refresh(ctx, id); err != nil {
			return directRefreshFailure(kind, id, err)
		}
		return nil
	}
	prepared, err := s.prepareResourceRefresh(ctx, []string{id})
	if err != nil {
		return directRefreshFailure(kind, id, err)
	}
	if err := ctx.Err(); err != nil {
		_ = prepared.plan.Abort()
		return directRefreshFailure(kind, id, err)
	}
	if _, err := prepared.plan.Commit(); err != nil {
		_ = prepared.plan.Abort()
		return directRefreshFailure(kind, id, err)
	}
	return nil
}

func hasSubscription(snapshot config.Snapshot, id string) bool {
	for _, subscription := range snapshot.Subscriptions {
		if subscription.ID == id {
			return true
		}
	}
	return false
}

func directRefreshFailure(kind, id string, err error) error {
	message := safeDiagnostic("refresh_"+kind, id, err).Message
	switch {
	case errors.Is(err, subscriptions.ErrNotFound):
		message = "source is not available"
	case errors.Is(err, resources.ErrPinMismatch):
		message = "SHA-256 pin mismatch"
	}
	return fmt.Errorf("clashpulse: refresh %s %s: %s", kind, id, message)
}
