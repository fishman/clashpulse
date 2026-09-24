package subscriptions

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/download"
)

var (
	ErrInvalid          = errors.New("subscriptions: invalid subscription")
	ErrActive           = errors.New("subscriptions: active subscription cannot be deleted")
	ErrNotFound         = errors.New("subscriptions: not found")
	ErrAlreadyExists    = errors.New("subscriptions: already exists")
	ErrNoSnapshot       = errors.New("subscriptions: no validated snapshot")
	ErrSourceChanged    = errors.New("subscriptions: source changed during refresh")
	ErrFetch            = errors.New("subscriptions: refresh failed")
	ErrInvalidProfile   = errors.New("subscriptions: invalid profile")
	ErrRender           = errors.New("subscriptions: candidate generation failed")
	ErrValidation       = errors.New("subscriptions: candidate validation failed")
	ErrActivation       = errors.New("subscriptions: activation failed")
	ErrRestore          = errors.New("subscriptions: previous runtime restore failed")
	ErrStore            = errors.New("subscriptions: private state unavailable")
	ErrSchedulerRunning = errors.New("subscriptions: scheduler already running")
)

// Entry is the public, secret-free view of a subscription. It never contains
// the URL, conditional request headers, profile bytes, or generated config.
type Entry struct {
	ID              string
	Name            string
	SourceHost      string
	Enabled         bool
	RefreshInterval time.Duration
	Timeout         time.Duration
	Route           string
	AllowHTTP       bool
	AllowInvalidTLS bool
	CheckedAt       time.Time
	LastSuccess     time.Time
	LastFailure     string
	LastFailureAt   time.Time
	NextDue         time.Time
	Hash            string
	AppliedHash     string
	HasSnapshot     bool
	// Active describes the running subscription, even when a newer snapshot exists.
	Active            bool
	PendingActivation bool
	Usage             *Usage
}

// Usage holds numeric values from a subscription usage header, when available.
type Usage struct {
	Upload    int64
	Download  int64
	Total     int64
	ExpiresAt int64
}

// Result reports a refresh without exposing source URLs or response data.
type Result struct {
	ID        string
	Changed   bool
	CheckedAt time.Time
	Hash      string
	Usage     *Usage
}

// TransportFactory receives both the requested route and explicit per-
// subscription TLS intent. It should honor allowInvalidTLS only for this
// request; the service never enables insecure TLS itself.
type TransportFactory func(route download.Route, allowInvalidTLS bool) (http.RoundTripper, error)

type RenderFunc func(context.Context, []byte) ([]byte, error)
type ValidateFunc func(context.Context, []byte) error

// ApplyFunc receives a private validated source profile, never its stale
// generated candidate from a prior executable or controller session.
type ApplyFunc func(context.Context, []byte) error

// RestoreFunc reapplies the previous active source after a durable activation
// commit fails. previousProfile is nil when no subscription was active.
type RestoreFunc func(context.Context, []byte) error

// CurrentFunc resolves the latest app-owned config entry. When supplied, every
// refresh consults it immediately before fetching, avoiding stale credential-
// bearing links after config.Store has been updated.
type CurrentFunc func(id string) (config.Subscription, bool)

// Options binds the service to application-owned transports, rendering,
// validation, activation, rollback, and latest config snapshots.
type Options struct {
	Transport TransportFactory
	Render    RenderFunc
	Validate  ValidateFunc
	Apply     ApplyFunc
	Restore   RestoreFunc
	Current   CurrentFunc
	MaxBytes  int64
	// OnChange is called after a changed promotion or persisted refresh failure.
	// It must be nonblocking; callers should enqueue notification work.
	OnChange func()
}

// Service coordinates private subscription records and transactional refreshes.
type Service struct {
	store        *Store
	options      Options
	wake         chan struct{}
	refreshMu    sync.Mutex
	refreshes    map[string]map[uint64]context.CancelFunc
	nextRefresh  uint64
	activationMu sync.Mutex
}

// NewService requires a rollback callback so a failed durable activation commit
// cannot leave the runtime on a profile that private state does not select.
func NewService(store *Store, options Options) (*Service, error) {
	if store == nil || options.Transport == nil || options.Render == nil || options.Validate == nil || options.Apply == nil || options.Restore == nil {
		return nil, ErrInvalid
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = defaultMaxProfileBytes
	}
	if options.MaxBytes < 0 || options.MaxBytes > maxProfileBytes {
		return nil, ErrInvalid
	}
	return &Service{store: store, options: options, wake: make(chan struct{}, 1), refreshes: make(map[string]map[uint64]context.CancelFunc)}, nil
}

func safeContextError(ctx context.Context) error {
	err := ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if err != nil {
		return context.Canceled
	}
	return nil
}
func (s *Service) signalWake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) registerRefresh(parent context.Context, id string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	s.refreshMu.Lock()
	s.nextRefresh++
	token := s.nextRefresh
	if s.refreshes[id] == nil {
		s.refreshes[id] = make(map[uint64]context.CancelFunc)
	}
	s.refreshes[id][token] = cancel
	s.refreshMu.Unlock()
	return ctx, func() {
		cancel()
		s.refreshMu.Lock()
		delete(s.refreshes[id], token)
		if len(s.refreshes[id]) == 0 {
			delete(s.refreshes, id)
		}
		s.refreshMu.Unlock()
	}
}

func (s *Service) cancelRefreshes(id string) {
	s.refreshMu.Lock()
	for _, cancel := range s.refreshes[id] {
		cancel()
	}
	s.refreshMu.Unlock()
}

func (s *Service) cancelIfSubscriptionChanges(id string, subscription config.Subscription) {
	latest, err := normalizeSubscription(subscription, false)
	record, ok := s.store.get(id)
	if err != nil || !ok || record.Subscription != latest {
		s.cancelRefreshes(id)
	}
}

// CancelChangedSources cancels refreshes whose complete configured subscription
// intent changed or was removed. It takes no per-ID lock, so a blocked fetch can
// release that lock before the config writer waits to update the record.
func (s *Service) CancelChangedSources(before, after []config.Subscription) {
	if s == nil {
		return
	}
	afterSubscriptions := make(map[string]config.Subscription, len(after))
	for _, subscription := range after {
		afterSubscriptions[subscription.ID] = subscription
	}
	for _, subscription := range before {
		if updated, ok := afterSubscriptions[subscription.ID]; !ok || updated != subscription {
			s.cancelRefreshes(subscription.ID)
		}
	}
}

func (s *Service) sourceIsCurrent(expected config.Subscription) bool {
	if s.options.Current == nil {
		return true
	}
	current, ok := s.options.Current(expected.ID)
	if !ok || current.ID != expected.ID {
		return false
	}
	normalized, err := normalizeSubscription(current, false)
	return err == nil && normalized == expected
}

func (s *Service) recordFailure(id string, checkedAt time.Time, failure error) {
	if failure == context.Canceled {
		return
	}
	if s.store.touchFailure(id, checkedAt, failure.Error()) && s.options.OnChange != nil {
		s.options.OnChange()
	}
	s.signalWake()
}

// Add persists a new subscription. An empty ID is replaced by a random private
// ID; the returned Entry intentionally omits the URL.
func (s *Service) Add(subscription config.Subscription) (Entry, error) {
	normalized, err := normalizeSubscription(subscription, true)
	if err != nil {
		return Entry{}, err
	}
	record := privateRecord{Subscription: normalized}
	unlock := s.store.lockID(normalized.ID)
	defer unlock()
	if err := s.store.add(record); err != nil {
		return Entry{}, err
	}
	s.signalWake()
	return publicEntry(record), nil
}

// Update atomically replaces an existing subscription's private config. It
// retains its last-known-good snapshots; changing URL cancels in-flight refreshes,
// clears conditional validators, and makes the new link immediately due.
func (s *Service) Update(subscription config.Subscription) (Entry, error) {
	normalized, err := normalizeSubscription(subscription, false)
	if err != nil {
		return Entry{}, err
	}
	if record, ok := s.store.get(normalized.ID); ok && record.Subscription.URL != normalized.URL {
		s.cancelRefreshes(normalized.ID)
	}
	unlock := s.store.lockID(normalized.ID)
	defer unlock()
	record, ok := s.store.get(normalized.ID)
	if !ok {
		return Entry{}, ErrNotFound
	}
	if record.Subscription.URL != normalized.URL {
		s.cancelRefreshes(normalized.ID)
	}
	if record.Subscription != normalized {
		if record.Subscription.URL != normalized.URL {
			record.ETag = ""
			record.LastModified = ""
			record.CheckedAt = time.Time{}
		}
		record.Subscription = normalized
		if err := s.store.replace(record); err != nil {
			return Entry{}, err
		}
	}
	s.signalWake()
	return publicEntry(record), nil
}

func (s *Service) List() []Entry {
	records := s.store.list()
	entries := make([]Entry, 0, len(records))
	for _, record := range records {
		entries = append(entries, publicEntry(record))
	}
	return entries
}

func (s *Service) Rename(id, name string) (Entry, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 256 {
		return Entry{}, ErrInvalid
	}
	unlock := s.store.lockID(id)
	defer unlock()
	record, ok := s.store.get(id)
	if !ok {
		return Entry{}, ErrNotFound
	}
	if record.Subscription.Name != name {
		record.Subscription.Name = name
		if err := s.store.replace(record); err != nil {
			return Entry{}, err
		}
	}
	s.signalWake()
	return publicEntry(record), nil
}

func (s *Service) SetEnabled(id string, enabled bool) (Entry, error) {
	if !enabled {
		s.cancelRefreshes(id)
	}
	unlock := s.store.lockID(id)
	defer unlock()
	if !enabled {
		s.cancelRefreshes(id)
	}
	record, ok := s.store.get(id)
	if !ok {
		return Entry{}, ErrNotFound
	}
	if record.Subscription.Enabled != enabled {
		record.Subscription.Enabled = enabled
		if err := s.store.replace(record); err != nil {
			return Entry{}, err
		}
	}
	s.signalWake()
	return publicEntry(record), nil
}

func (s *Service) Delete(id string) error {
	if s.store.isActive(id) {
		return ErrActive
	}
	s.cancelRefreshes(id)
	unlock := s.store.lockID(id)
	defer unlock()
	if s.store.isActive(id) {
		return ErrActive
	}
	if err := s.store.delete(id); err != nil {
		return err
	}
	s.signalWake()
	return nil
}

// ActiveID returns the persisted active subscription ID.
func (s *Service) ActiveID() (string, error) { return s.store.ActiveID() }

// Profile returns a clone of a validated last-known-good source profile.
func (s *Service) Profile(id string) ([]byte, error) { return s.store.Profile(id) }

// ActiveProfile returns the active ID and a clone of its validated LKG source.
func (s *Service) ActiveProfile() (string, []byte, error) {
	return s.store.ActiveProfile()
}

// NextDue returns the earliest enabled subscription deadline, or zero when none
// are enabled. Subscriptions without a successful check are due immediately.
func (s *Service) NextDue() time.Time { return s.store.nextDue(nil) }

func (s *Service) dueIDs(now time.Time, excluded map[string]bool) ([]string, time.Time) {
	return s.store.dueIDs(now, excluded)
}

func (s *Service) wakeups() <-chan struct{} { return s.wake }

func (s *Service) touch(id string, checkedAt time.Time, etag, lastModified string, usage *Usage) {
	s.store.touchSuccess(id, checkedAt, etag, lastModified, usage)
	s.signalWake()
}
