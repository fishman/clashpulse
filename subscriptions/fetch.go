package subscriptions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
	"gopkg.in/yaml.v3"
)

// Fetching reuses the project's download.Client and its yaml.v3 dependency;
// the package adds no HTTP or YAML dependency of its own.
// Refresh uses the latest app-owned entry when Options.Current is configured.
// Without a resolver it uses the private record last written by Add or Update.
func (s *Service) Refresh(ctx context.Context, id string) (Result, error) {
	if s.options.Current != nil {
		current, ok := s.options.Current(id)
		if !ok {
			return Result{}, ErrNotFound
		}
		if current.ID != id {
			return Result{}, ErrInvalid
		}
		s.cancelIfSubscriptionChanges(id, current)
	}
	return s.refresh(ctx, id, nil)
}

// RefreshWith installs the caller's latest config entry before fetching. A
// subscription change never removes the prior good snapshot.
func (s *Service) RefreshWith(ctx context.Context, subscription config.Subscription) (Result, error) {
	s.cancelIfSubscriptionChanges(subscription.ID, subscription)
	return s.refresh(ctx, subscription.ID, &subscription)
}

func (s *Service) refresh(ctx context.Context, id string, supplied *config.Subscription) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !validID(id) {
		return Result{}, ErrInvalid
	}
	refreshCtx, finishRefresh := s.registerRefresh(ctx, id)
	defer finishRefresh()
	unlock := s.store.lockID(id)
	defer unlock()
	if err := safeContextError(refreshCtx); err != nil {
		return Result{}, err
	}
	var latest *config.Subscription
	if supplied != nil {
		if supplied.ID != id {
			return Result{}, ErrInvalid
		}
		normalized, err := normalizeSubscription(*supplied, false)
		if err != nil {
			return Result{}, err
		}
		latest = &normalized
	} else if s.options.Current != nil {
		current, ok := s.options.Current(id)
		if !ok || current.ID != id {
			return Result{}, ErrNotFound
		}
		normalized, err := normalizeSubscription(current, false)
		if err != nil {
			return Result{}, err
		}
		latest = &normalized
	}
	if latest != nil {
		if err := s.installLatestLocked(*latest); err != nil {
			return Result{}, err
		}
	}
	record, ok := s.store.get(id)
	if !ok {
		return Result{}, ErrNotFound
	}
	operationCtx, cancel := context.WithTimeout(refreshCtx, record.Subscription.Timeout)
	defer cancel()
	if err := safeContextError(operationCtx); err != nil {
		return Result{}, err
	}
	client := download.NewClient(func(route download.Route) (http.RoundTripper, error) {
		return s.options.Transport(route, record.Subscription.AllowInvalidTLS)
	})
	response, err := client.Fetch(operationCtx, download.Request{
		URL:              record.Subscription.URL,
		Route:            download.Route(record.Subscription.Route),
		ETag:             record.ETag,
		LastModified:     record.LastModified,
		MaxBytes:         s.options.MaxBytes,
		AllowHTTP:        record.Subscription.AllowHTTP,
		UserAgent:        record.Subscription.UserAgent,
		CaptureErrorBody: s.options.CaptureErrorBody,
	})
	if err != nil {
		checkedAt := time.Now()
		if contextErr := s.recordContextFailure(operationCtx, id, checkedAt); contextErr != nil {
			return Result{}, contextErr
		}
		responseStatus, hasResponse := download.HTTPResponseFrom(err)
		if status, ok := download.StatusErrorFrom(err); ok {
			s.recordFailure(id, checkedAt, status)
			return Result{}, fmt.Errorf("%w: %w", ErrFetch, status)
		}
		s.recordFailure(id, checkedAt, ErrFetch)
		if hasResponse {
			return Result{}, errors.Join(ErrFetch, responseStatus)
		}
		return Result{}, ErrFetch
	}
	checkedAt := time.Now()
	if contextErr := s.recordContextFailure(operationCtx, id, checkedAt); contextErr != nil {
		return Result{}, contextErr
	}
	usage := parseUsage(response.SubscriptionUserInfo)
	if response.StatusCode == http.StatusNotModified {
		if record.Hash == "" || record.CandidateHash == "" {
			s.recordFailure(id, checkedAt, ErrNoSnapshot)
			return Result{}, ErrNoSnapshot
		}
		if err := s.touch(id, checkedAt, response.ETag, response.LastModified, usage); err != nil {
			return Result{}, err
		}
		if usage == nil {
			usage = record.Usage
		}
		return Result{ID: id, CheckedAt: checkedAt, Hash: record.Hash, Usage: cloneUsage(usage)}, nil
	}
	profile := response.Body
	profileHash := hashBytes(profile)
	if record.Hash != "" && profileHash == record.Hash {
		// Equal bytes do not regenerate config or restart Mihomo. Recovery
		// persists the cleared failure; healthy checks remain write-free.
		if err := s.touch(id, checkedAt, response.ETag, response.LastModified, usage); err != nil {
			return Result{}, err
		}
		if usage == nil {
			usage = record.Usage
		}
		return Result{ID: id, CheckedAt: checkedAt, Hash: profileHash, Usage: cloneUsage(usage)}, nil
	}
	if !validProfileYAML(profile) {
		s.recordFailure(id, checkedAt, ErrInvalidProfile)
		return Result{}, s.responseFailure(ErrInvalidProfile, response)
	}
	candidate, err := s.options.Render(operationCtx, bytes.Clone(profile))
	if err != nil {
		if contextErr := s.recordContextFailure(operationCtx, id, checkedAt); contextErr != nil {
			return Result{}, contextErr
		}
		s.recordFailure(id, checkedAt, ErrRender)
		return Result{}, s.responseFailure(ErrRender, response)
	}
	if contextErr := s.recordContextFailure(operationCtx, id, checkedAt); contextErr != nil {
		return Result{}, contextErr
	}
	if len(candidate) == 0 || int64(len(candidate)) > maxCandidateBytes {
		s.recordFailure(id, checkedAt, ErrRender)
		return Result{}, s.responseFailure(ErrRender, response)
	}
	candidate = bytes.Clone(candidate)
	if err := s.options.Validate(operationCtx, bytes.Clone(candidate)); err != nil {
		if contextErr := s.recordContextFailure(operationCtx, id, checkedAt); contextErr != nil {
			return Result{}, contextErr
		}
		s.recordFailure(id, checkedAt, ErrValidation)
		return Result{}, s.responseFailure(ErrValidation, response)
	}
	if contextErr := s.recordContextFailure(operationCtx, id, checkedAt); contextErr != nil {
		return Result{}, contextErr
	}
	if !s.sourceIsCurrent(record.Subscription) {
		return Result{}, s.responseFailure(ErrSourceChanged, response)
	}
	previous, err := s.store.promote(id, profile, candidate, checkedAt, response.ETag, response.LastModified, usage)
	if err != nil {
		return Result{}, s.responseFailure(ErrStore, response)
	}
	if !s.sourceIsCurrent(record.Subscription) {
		if rollbackErr := s.store.rollbackPromotion(id, previous, profileHash, hashBytes(candidate)); rollbackErr != nil {
			return Result{}, errors.Join(s.responseFailure(ErrSourceChanged, response), ErrStore)
		}
		return Result{}, s.responseFailure(ErrSourceChanged, response)
	}
	s.store.finalizePromotion(id)
	if s.options.OnChange != nil {
		s.options.OnChange()
	}
	s.signalWake()
	return Result{ID: id, Changed: true, CheckedAt: checkedAt, Hash: profileHash, Usage: cloneUsage(usage)}, nil
}

func (s *Service) recordContextFailure(ctx context.Context, id string, checkedAt time.Time) error {
	if err := safeContextError(ctx); err != nil {
		s.recordFailure(id, checkedAt, err)
		return err
	}
	return nil
}

func (s *Service) responseFailure(err error, response download.Response) error {
	if !s.options.CaptureErrorBody {
		return err
	}
	return errors.Join(err, download.StatusError{Code: response.StatusCode, ResponseBody: response.DiagnosticBody})
}

func (s *Service) installLatestLocked(subscription config.Subscription) error {
	record, exists := s.store.get(subscription.ID)
	if !exists {
		if err := s.store.add(privateRecord{Subscription: subscription}); err != nil {
			return err
		}
		s.signalWake()
		return nil
	}
	if record.Subscription == subscription {
		return nil
	}
	if record.Subscription.URL != subscription.URL {
		record.ETag = ""
		record.LastModified = ""
		record.CheckedAt = time.Time{}
	}
	record.Subscription = subscription
	if err := s.store.replace(record); err != nil {
		return err
	}
	s.signalWake()
	return nil
}

// Activate re-applies a validated source profile through the current binary,
// controller, and managed resources. Repeated activation may be runtime no-op.
func (s *Service) Activate(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !validID(id) {
		return ErrInvalid
	}
	s.activationMu.Lock()
	defer s.activationMu.Unlock()
	unlock := s.store.lockID(id)
	defer unlock()
	record, profile, candidate, err := s.store.recordSnapshot(id)
	if err != nil {
		return err
	}
	if hashBytes(candidate) != record.CandidateHash {
		return ErrStore
	}
	var previousProfile []byte
	_, previousProfile, err = s.store.ActiveProfile()
	if err != nil && err != ErrNoSnapshot {
		return ErrStore
	}
	if err := safeContextError(ctx); err != nil {
		return err
	}
	if err := s.options.Apply(ctx, bytes.Clone(profile)); err != nil {
		if contextErr := safeContextError(ctx); contextErr != nil {
			return contextErr
		}
		return ErrActivation
	}
	if s.store.isActive(id) && record.AppliedProfileHash == record.Hash && record.AppliedCandidateHash == record.CandidateHash {
		return nil
	}
	if err := s.store.setApplied(id, record.Hash, record.CandidateHash); err != nil {
		if restoreErr := s.options.Restore(context.WithoutCancel(ctx), bytes.Clone(previousProfile)); restoreErr != nil {
			return errors.Join(ErrStore, ErrRestore)
		}
		return ErrStore
	}
	return nil
}

func validProfileYAML(profile []byte) bool {
	var document map[string]any
	if len(profile) == 0 || yaml.Unmarshal(profile, &document) != nil || document == nil {
		return false
	}
	proxies, hasProxies := document["proxies"]
	providers, hasProviders := document["proxy-providers"]
	if !hasProxies && !hasProviders {
		return false
	}
	if hasProxies {
		if _, ok := proxies.([]any); !ok {
			return false
		}
	}
	if hasProviders {
		entries, ok := providers.(map[string]any)
		if !ok {
			return false
		}
		for _, value := range entries {
			if _, ok := value.(map[string]any); !ok {
				return false
			}
		}
	}
	return true
}
