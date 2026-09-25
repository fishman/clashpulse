package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/ipc"
)

func (s *runtimeService) editUserFile(ctx context.Context, name string, mutate func(string, config.Snapshot) error) error {
	path := filepath.Join(s.configDir, name)
	previous, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	current := s.lastAppliedSettings
	if err := mutate(path, current); err != nil {
		return err
	}
	next, err := s.reloadConfig()
	if err == nil {
		err = s.applyChange(ctx, config.NewStore(current).Replace(next))
	}
	if err == nil {
		return nil
	}
	var restoreErr error
	if readErr == nil {
		restoreErr = config.Write(path, previous)
	} else {
		restoreErr = os.Remove(path)
		if errors.Is(restoreErr, os.ErrNotExist) {
			restoreErr = nil
		}
	}
	s.store.Replace(current)
	if restoreErr != nil {
		return errors.Join(err, fmt.Errorf("clashpulse: restore previous %s: %w", name, restoreErr))
	}
	return err
}

func (s *runtimeService) putSubscription(ctx context.Context, id string, edit *ipc.SubscriptionEdit) error {
	if edit == nil {
		return fmt.Errorf("clashpulse: subscription edit is required")
	}
	change := config.SubscriptionEdit{
		Name: edit.Name, URL: edit.URL, UserAgent: edit.UserAgent, Enabled: edit.Enabled, Route: edit.Route,
		AllowHTTP: edit.AllowHTTP, AllowInvalidTLS: edit.AllowInvalidTLS,
	}
	if edit.RefreshIntervalSeconds != nil {
		interval := time.Duration(*edit.RefreshIntervalSeconds) * time.Second
		change.RefreshInterval = &interval
	}
	if edit.TimeoutSeconds != nil {
		timeout := time.Duration(*edit.TimeoutSeconds) * time.Second
		change.Timeout = &timeout
	}
	return s.editUserFile(ctx, "subscriptions.toml", func(path string, current config.Snapshot) error {
		return config.PatchSubscription(path, current, id, change)
	})
}

func (s *runtimeService) putResource(ctx context.Context, id string, edit *ipc.ResourceEdit) error {
	if edit == nil {
		return fmt.Errorf("clashpulse: resource edit is required")
	}
	change := config.ResourceEdit{URL: edit.URL, Enabled: edit.Enabled, SHA256: edit.SHA256}
	if edit.Kind != nil {
		kind := config.ResourceKind(*edit.Kind)
		change.Kind = &kind
	}
	if edit.Format != nil {
		format := config.ResourceFormat(*edit.Format)
		change.Format = &format
	}
	if edit.RuleType != nil {
		ruleType := config.RuleType(*edit.RuleType)
		change.RuleType = &ruleType
	}
	if edit.IntervalSeconds != nil {
		interval := time.Duration(*edit.IntervalSeconds) * time.Second
		change.Interval = &interval
	}
	return s.editUserFile(ctx, "resources.toml", func(path string, current config.Snapshot) error {
		return config.PatchResource(path, current, id, change)
	})
}

func (s *runtimeService) putFilter(ctx context.Context, id string, edit *ipc.FilterEdit) error {
	if edit == nil {
		return fmt.Errorf("clashpulse: filter edit is required")
	}
	change := config.FilterEdit{Resource: edit.ResourceID, Target: edit.Target, Enabled: edit.Enabled}
	if edit.Format != nil {
		format := config.ResourceFormat(*edit.Format)
		change.Format = &format
	}
	return s.editUserFile(ctx, "filters.toml", func(path string, current config.Snapshot) error {
		return config.PatchFilter(path, current, id, change)
	})
}

func (s *runtimeService) setDNSRouting(ctx context.Context, edit *ipc.DNSRoutingEdit) error {
	if edit == nil {
		return fmt.Errorf("clashpulse: DNS routing edit is required")
	}
	sets := make([]config.ResolverSet, 0, len(edit.ResolverSets))
	for _, set := range edit.ResolverSets {
		sets = append(sets, config.ResolverSet{ID: set.ID, Endpoints: append([]string(nil), set.Endpoints...), DNSCrypt: set.DNSCrypt})
	}
	routes := make([]config.DNSRoute, 0, len(edit.Routes))
	for _, route := range edit.Routes {
		routes = append(routes, config.DNSRoute{Suffix: route.Suffix, GeoSite: route.GeoSite, Resource: route.Resource, ResolverSet: route.ResolverSet})
	}
	return s.editUserFile(ctx, "resources.toml", func(path string, current config.Snapshot) error {
		return config.ReplaceDNSRouting(path, current, sets, routes)
	})
}
