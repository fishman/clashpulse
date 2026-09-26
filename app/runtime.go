package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/mihomo"
	"github.com/fishman/clashpulse/monitor"
	"github.com/fishman/clashpulse/resources"
	"github.com/fishman/clashpulse/subscriptions"
	"github.com/fishman/clashpulse/sysproxy"
)

const intentQueueSize = 32

// Marks a sync failure after at least one removal committed irreversibly.
var errSubscriptionDeletionCommitted = errors.New("clashpulse: subscription deletion committed")

type runtimeService struct {
	configDir, stateDir       string
	store                     *config.Store
	lastAppliedSettings       config.Snapshot
	subs                      *subscriptions.Service
	subScheduler              *subscriptions.Scheduler
	registry                  *resources.Registry
	process                   mihomo.Process
	processCtx                context.Context
	proxy                     *sysproxy.Manager
	server                    *ipc.Server
	intents                   chan ipc.Command
	operationMu               sync.Mutex
	operationCancel           context.CancelFunc
	operationKind             ipc.CommandKind
	stopRequested             bool
	changes                   chan config.Change
	batches                   chan monitor.Batch
	resourceRefresh           chan []string
	stateChanged              chan struct{}
	resourceDirty             atomic.Bool
	resourceScheduleMu        sync.Mutex
	resourceAttempts          map[string]time.Time
	backgroundErrors          chan error
	configErrors              chan error
	configResults             chan error
	notificationErrors        chan error
	controller                *mihomo.Controller
	controllerAddress, secret string
	proxyPort                 int
	cap                       mihomo.Capability
	exited                    <-chan struct{}
	running                   atomic.Bool
	proxyActive               bool
	generated                 []byte
	resourceHome              string
	configOverrides           []core.ConfigOverrideSnapshot
	configReportPending       bool
	localProfile              []byte
	generatedPath             string
	foreground                bool
	forceRestart              bool
	activationBackup          *runtimeBackup
	groups                    map[string]mihomo.Group
	proxies                   map[string]string
	automation                map[string]bool
	monitor                   *monitor.Scheduler
	stopMonitor               context.CancelFunc
	monitorDone               chan error
	snapshot                  core.Snapshot
	lastPublished             core.Snapshot
	alertHigh                 bool
	jobID                     uint64
	lastSwitch                map[string]time.Time
	manualOverride            map[string]bool
	notifications             chan time.Duration
	notificationDone          chan struct{}
}

// RunAt hosts the single lifecycle owner; both desktop and terminal clients use IPC.
func RunAt(ctx context.Context, configDir, stateDir, endpoint string) error {
	return runAt(ctx, configDir, stateDir, endpoint, nil, nil)
}

func RunFileAt(ctx context.Context, configDir, stateDir, endpoint, path string, ready func() error) error {
	profile, err := readLocalProfile(ctx, path)
	if err != nil {
		return err
	}
	return runAt(ctx, configDir, stateDir, endpoint, profile, ready)
}

func runAt(ctx context.Context, configDir, stateDir, endpoint string, profile []byte, ready func() error) (result error) {
	if ctx == nil {
		return fmt.Errorf("clashpulse: context is required")
	}
	releaseOwner, err := acquireOwnerLock(stateDir)
	if err != nil {
		return err
	}
	defer releaseOwner()
	if err := sweepLocalGenerated(stateDir); err != nil {
		return core.WrapActivation(core.ActivationStateCommit, err)
	}
	if err := privateDirectory(configDir); err != nil {
		return fmt.Errorf("clashpulse: config directory: %w", err)
	}
	if err := config.Seed(configDir); err != nil {
		return fmt.Errorf("clashpulse: seed configuration: %w", err)
	}
	initial, err := config.Load(configDir)
	if err != nil {
		return err
	}
	s, err := newRuntimeService(configDir, stateDir, initial)
	if err != nil {
		return err
	}
	if profile != nil {
		file, err := os.CreateTemp(stateDir, "generated-local-*.yaml")
		if err != nil {
			return core.WrapActivation(core.ActivationStateCommit, err)
		}
		s.generatedPath = file.Name()
		if err := file.Close(); err != nil {
			_ = os.Remove(s.generatedPath)
			return core.WrapActivation(core.ActivationStateCommit, err)
		}
		s.localProfile = profile
		s.foreground = true
		defer func(path string) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				result = errors.Join(core.WrapActivation(core.ActivationRollback, err), result)
			}
		}(s.generatedPath)
	}
	s.subScheduler = subscriptions.NewScheduler(s.subs, 2)
	s.subScheduler.ConfigureResources(s.nextResourceDue, s.enqueueResourceRefresh)
	s.server, err = ipc.NewServer(ipc.ServerOptions{Endpoint: endpoint, Handler: func(_ context.Context, cmd ipc.Command) error {
		return s.enqueueIntent(cmd)
	}, InitialSnapshot: core.CloneSnapshot(s.snapshot)})
	if err != nil {
		return err
	}
	if profile != nil {
		return s.run(ctx, s.start, ready)
	}
	return s.run(ctx, nil, nil)
}

func sweepLocalGenerated(stateDir string) error {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "generated-local-") && strings.HasSuffix(name, ".yaml") && !entry.IsDir() {
			if err := os.Remove(filepath.Join(stateDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func reserveLoopbackPorts() (int, int, error) {
	controller, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, 0, fmt.Errorf("clashpulse: reserve controller port: %w", err)
	}
	defer controller.Close()
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, 0, fmt.Errorf("clashpulse: reserve proxy port: %w", err)
	}
	defer proxy.Close()
	return controller.Addr().(*net.TCPAddr).Port, proxy.Addr().(*net.TCPAddr).Port, nil
}

func (s *runtimeService) transport(route download.Route, allowInvalidTLS bool) (http.RoundTripper, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableKeepAlives = true
	tr.MaxConnsPerHost = 4
	tr.MaxIdleConnsPerHost = 2
	tr.MaxIdleConns = 8
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: allowInvalidTLS}
	switch route {
	case download.Direct:
		tr.Proxy = nil
	case download.SystemProxy:
		if s.proxy == nil {
			return nil, fmt.Errorf("clashpulse: system proxy route is unavailable")
		}
		tr.Proxy = func(request *http.Request) (*url.URL, error) {
			if request == nil || request.URL == nil {
				return nil, fmt.Errorf("clashpulse: system proxy route requires a request URL")
			}
			scheme := strings.ToLower(request.URL.Scheme)
			if scheme != "http" && scheme != "https" {
				return nil, fmt.Errorf("clashpulse: system proxy does not support URL scheme %q", request.URL.Scheme)
			}
			proxyCtx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
			defer cancel()
			settings, err := s.proxy.Proxies(proxyCtx)
			if err != nil {
				return nil, fmt.Errorf("clashpulse: system proxy route is unavailable: %w", err)
			}
			proxy := settings.HTTP
			if scheme == "https" {
				proxy = settings.HTTPS
			}
			if proxy == nil {
				return nil, fmt.Errorf("clashpulse: system proxy for %s is unavailable", scheme)
			}
			return proxy, nil
		}
	case download.MihomoProxy:
		if !s.running.Load() {
			return nil, fmt.Errorf("clashpulse: Mihomo proxy route is unavailable")
		}
		proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", s.proxyPort)}
		tr.Proxy = http.ProxyURL(proxyURL)
	default:
		return nil, fmt.Errorf("clashpulse: unsupported download route")
	}
	return tr, nil
}

func (s *runtimeService) offerChange(change config.Change) {
	offerLatest(s.changes, change)
}

func (s *runtimeService) syncSubscriptions(snapshot config.Snapshot) error {
	current := make(map[string]bool, len(snapshot.Subscriptions))
	existing := make(map[string]bool)
	for _, item := range snapshot.Subscriptions {
		current[item.ID] = true
	}
	for _, entry := range s.subs.List() {
		existing[entry.ID] = true
		if entry.Active && !current[entry.ID] {
			return fmt.Errorf("clashpulse: active subscription %q cannot be removed before a validated replacement is activated", entry.ID)
		}
	}
	for _, item := range snapshot.Subscriptions {
		var err error
		if existing[item.ID] {
			_, err = s.subs.Update(item)
		} else {
			_, err = s.subs.Add(item)
		}
		if err != nil {
			return fmt.Errorf("clashpulse: sync subscription %q: %w", item.ID, err)
		}
	}
	removed := false
	var cleanupErr error
	for _, entry := range s.subs.List() {
		if current[entry.ID] {
			continue
		}
		if err := s.subs.Delete(entry.ID); err != nil {
			if errors.Is(err, subscriptions.ErrCleanupPending) {
				removed = true
				cleanupErr = errors.Join(cleanupErr, err)
				if s.subScheduler != nil {
					s.subScheduler.WakeResources()
				}
				continue
			}
			if removed {
				return errors.Join(errSubscriptionDeletionCommitted, cleanupErr, err)
			}
			return err
		}
		removed = true
	}
	if cleanupErr != nil {
		return errors.Join(errSubscriptionDeletionCommitted, cleanupErr)
	}
	return nil
}

func (s *runtimeService) stateSnapshot() core.Snapshot {
	state := core.CloneSnapshot(s.snapshot)
	state.ActiveSource = "none"
	state.ServiceRunning = s.controller != nil
	state.ConfigOverrides = nil
	if s.controller != nil {
		if s.localProfile != nil {
			state.ActiveSource = "local"
		} else {
			state.ActiveSource = "subscription"
		}
		if !s.configReportPending {
			state.ConfigOverrides = append([]core.ConfigOverrideSnapshot(nil), s.configOverrides...)
		}
	}
	settings := s.store.Snapshot()
	state.Monitor = core.MonitorSnapshot{
		Enabled: settings.Monitor.Enabled, TestURL: settings.Monitor.TestURL,
		SwitchPolicy:           settings.Monitor.SwitchPolicy,
		URLTestIntervalSeconds: int64(settings.Mihomo.URLTestInterval.Seconds()),
		URLTestToleranceMillis: settings.Mihomo.URLTestTolerance.Milliseconds(),
		IntervalSeconds:        int64(settings.Monitor.Interval.Seconds()), TimeoutMillis: settings.Monitor.Timeout.Milliseconds(),
		Concurrency: settings.Monitor.Concurrency, ThresholdMillis: settings.Monitor.Threshold.Milliseconds(),
		AlertThresholdMillis: settings.Monitor.AlertThreshold.Milliseconds(), ConsecutiveBadSamples: settings.Monitor.ConsecutiveBadSamples,
		MinImprovementMillis: settings.Monitor.MinImprovement.Milliseconds(), CooldownSeconds: int64(settings.Monitor.Cooldown.Seconds()),
		JitterMillis: settings.Monitor.Jitter.Milliseconds(),
	}
	state.DNS.Listen = settings.DNS.Listen
	state.DNS.ResolverSets = nil
	for _, set := range settings.DNS.ResolverSets {
		state.DNS.ResolverSets = append(state.DNS.ResolverSets, core.ResolverSetSnapshot{ID: set.ID, Endpoints: append([]string(nil), set.Endpoints...), DNSCrypt: set.DNSCrypt})
	}
	state.DNS.Routes = nil
	for _, route := range settings.DNS.Routes {
		state.DNS.Routes = append(state.DNS.Routes, core.DNSRouteSnapshot{Suffix: route.Suffix, GeoSite: route.GeoSite, Resource: route.Resource, ResolverSet: route.ResolverSet})
	}
	state.SystemProxy = core.SystemProxySnapshot{Enabled: settings.App.SystemProxy.Enabled, Active: s.proxyActive}
	state.Binary.Desired = settings.Mihomo.Binary
	state.Binary.ObservedVersion = s.cap.Version
	state.Binary.Capabilities = nil
	if s.cap.SupportsGeoIPDat {
		state.Binary.Capabilities = append(state.Binary.Capabilities, "geoip.dat")
	}
	if s.cap.SupportsGeoSiteDat {
		state.Binary.Capabilities = append(state.Binary.Capabilities, "geosite.dat")
	}
	if s.cap.SupportsMMDB {
		state.Binary.Capabilities = append(state.Binary.Capabilities, "Country.mmdb")
	}
	state.Subscriptions = state.Subscriptions[:0]
	for _, entry := range s.subs.List() {
		sub := core.SubscriptionSnapshot{
			ID: entry.ID, Name: entry.Name, SourceHost: entry.SourceHost,
			Enabled: entry.Enabled, Active: entry.Active && s.localProfile == nil, PendingActivation: entry.PendingActivation, LastCheck: unixSeconds(entry.CheckedAt),
			LastSuccess: unixSeconds(entry.LastSuccess), NextDue: unixSeconds(entry.NextDue), LastFailure: publicFailureLabel("subscription", entry.LastFailure),
		}
		sub.Route = entry.Route
		sub.RefreshIntervalSeconds = uint32(entry.RefreshInterval / time.Second)
		sub.TimeoutSeconds = uint32(entry.Timeout / time.Second)
		sub.AllowHTTP, sub.AllowInvalidTLS = entry.AllowHTTP, entry.AllowInvalidTLS
		sub.AppliedHashPrefix = entry.AppliedHash
		if len(entry.Hash) > 12 {
			sub.HashPrefix = entry.Hash[:12]
		} else {
			sub.HashPrefix = entry.Hash
		}
		if entry.Usage != nil {
			sub.Usage = &core.UsageSnapshot{UploadedBytes: uint64(entry.Usage.Upload), DownloadedBytes: uint64(entry.Usage.Download), TotalBytes: uint64(entry.Usage.Total), ExpiresAt: entry.Usage.ExpiresAt}
		}
		state.Subscriptions = append(state.Subscriptions, sub)
	}
	if s.resourceDirty.Swap(false) {
		state.Resources = state.Resources[:0]
		statuses, statusErr := s.registry.Status(settings)
		if statusErr == nil {
			for _, resource := range statuses {
				view := core.ResourceSnapshot{ID: resource.ID, Kind: string(resource.Kind), Format: string(resource.Format), RuleType: string(resource.RuleType), SourceHost: resource.SourceHost,
					Enabled: resource.Enabled, Validated: resource.Validated, Destination: filepath.Base(resource.Destination), LastResult: publicFailureLabel("resource", resource.LastFailure),
					LastCheck: unixSeconds(resource.LastCheck), LastSuccess: unixSeconds(resource.LastSuccess)}
				if !resource.LastCheck.IsZero() {
					view.NextDue = unixSeconds(resource.LastCheck.Add(resourceInterval(settings, resource.ID)))
				}
				if !resource.Validated {
					view.Destination = ""
				}
				if len(resource.SHA256) > 12 {
					view.HashPrefix = resource.SHA256[:12]
				} else {
					view.HashPrefix = resource.SHA256
				}
				state.Resources = append(state.Resources, view)
			}
		} else {
			for _, resource := range settings.Resources {
				host := "local"
				if parsed, err := url.Parse(resource.URL); err == nil && parsed.Hostname() != "" {
					host = parsed.Hostname()
				}
				state.Resources = append(state.Resources, core.ResourceSnapshot{ID: resource.ID, Kind: string(resource.Kind), Format: string(resource.Format), RuleType: string(resource.RuleType), SourceHost: host, Enabled: resource.Enabled})
			}
		}
	}
	state.Filters = state.Filters[:0]
	resourceByID := make(map[string]core.ResourceSnapshot, len(state.Resources))
	for _, resource := range state.Resources {
		resourceByID[resource.ID] = resource
	}
	for _, filter := range settings.Filters {
		resource := resourceByID[filter.Resource]
		state.Filters = append(state.Filters, core.FilterSnapshot{
			ID: filter.ID, ResourceID: filter.Resource, Format: string(filter.Format), Target: filter.Target, Enabled: filter.Enabled,
			SourceHost: resource.SourceHost, HashPrefix: resource.HashPrefix, Destination: resource.Destination,
			Validated: resource.Validated, LastFailure: resource.LastResult, LastSuccess: resource.LastSuccess, NextDue: resource.NextDue,
		})
	}
	sort.Slice(state.Subscriptions, func(i, j int) bool { return state.Subscriptions[i].ID < state.Subscriptions[j].ID })
	sort.Slice(state.Resources, func(i, j int) bool { return state.Resources[i].ID < state.Resources[j].ID })
	return state
}

func publicFailureLabel(kind, failure string) string {
	if failure == "" {
		return ""
	}
	if status, ok := download.ParseStatus(failure); ok {
		return kind + " " + status.Error()
	}
	return kind + " update failed"
}

func unixSeconds(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}

func resourceInterval(snapshot config.Snapshot, id string) time.Duration {
	for _, item := range snapshot.Resources {
		if item.ID == id {
			return item.Interval
		}
	}
	return 0
}

func (s *runtimeService) publish() {
	next := s.stateSnapshot()
	next.Revision = s.lastPublished.Revision
	if reflect.DeepEqual(next, s.lastPublished) {
		return
	}
	next.Revision++
	if err := s.server.Publish(next); err == nil {
		s.snapshot = next
		s.lastPublished = core.CloneSnapshot(next)
	}
}

func (s *runtimeService) reportError(kind string, err error) {
	s.reportErrorScoped(kind, "", err)
}

func (s *runtimeService) reportErrorScoped(kind, sourceID string, err error) {
	if err == nil {
		return
	}
	message := "operation failed"
	if errors.Is(err, context.Canceled) {
		message = "operation cancelled"
	}
	if issue := compatibilityFailure(err); issue != "" {
		message = issue
		s.snapshot.Binary.LastCompatibilityFailure = issue
	}
	if errors.Is(err, subscriptions.ErrRestore) {
		message = "activation rollback failed; running proxy state needs attention"
	}
	if unsupportedSystemProxy(err) {
		message = core.ActivationSystemProxyUnsupported.Message()
	}
	if strings.Contains(err.Error(), "sysproxy: rollback") {
		message = "system proxy rollback failed; previous settings need attention"
	}
	if kind == "notification" {
		message = "desktop notification unavailable"
	}
	failure := core.ErrorSnapshot{Kind: kind, SourceID: sourceID, Key: kind, Message: message}
	if message == "operation failed" && (kind == "config" || kind == "config_reload" || kind == "reload_configuration" || kind == "update_configuration" || kind == "set_dns_routing" || kind == "delete_subscription" || strings.HasPrefix(kind, "put_")) {
		failure.Message = "configuration change failed; previous settings remain active"
	}
	if strings.Contains(err.Error(), "restore prior runtime") || strings.Contains(err.Error(), "rollback failed") {
		failure.Message = "configuration rollback failed; running state needs attention"
	}
	for _, name := range []string{"config.toml", "subscriptions.toml", "resources.toml", "filters.toml"} {
		if strings.Contains(err.Error(), name) {
			failure.File = name
			break
		}
	}
	if failure.File != "" {
		parts := strings.SplitN(err.Error(), failure.File+": ", 2)
		if len(parts) == 2 {
			key := strings.SplitN(parts[1], ":", 2)[0]
			if len(key) < 64 && strings.Trim(key, "abcdefghijklmnopqrstuvwxyz0123456789._") == "" {
				failure.Key = key
			}
		}
	}
	if errors.Is(err, errSubscriptionDeletionCommitted) {
		failure.File = "subscriptions.toml"
		failure.Key = "subscription.delete"
		failure.Message = "subscription cleanup incomplete; new settings remain active"
	}
	if kind == "refresh_subscription" && sourceID != "" {
		failure.Key = "subscription"
		failure.Message = "subscription update failed"
		if status, ok := download.StatusErrorFrom(err); ok {
			failure.Message = "subscription " + status.Error()
		}
	}
	if kind == "activate_subscription" {
		if message, stage := safeActivationReason(err); message != "" {
			failure.Message = message
			if stage == core.ActivationBinary {
				s.snapshot.Binary.LastCompatibilityFailure = message
			}
		}
	}
	if s.upsertIssue(failure) {
		s.appendDiagnostic(safeDiagnostic(kind, sourceID, err))
	}
	s.publish()
}

func compatibilityFailure(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if strings.Contains(message, "bundled Mihomo is not installed") {
		return "bundled Mihomo is not installed"
	}
	if index := strings.Index(message, `resource "`); index >= 0 {
		resource, rest, ok := strings.Cut(message[index+len(`resource "`):], `" requires `)
		if ok && len(resource) > 0 && len(resource) <= 64 && strings.Trim(resource, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == "" {
			for _, capability := range []string{"geoip.dat", "geosite.dat", "Country.mmdb"} {
				if strings.HasPrefix(rest, capability+" capability") {
					return fmt.Sprintf("resource %s requires %s", resource, capability)
				}
			}
		}
	}
	if strings.Contains(message, "capability") {
		return "selected Mihomo lacks a required capability"
	}
	return ""
}

func (s *runtimeService) shutdown() error {
	if s.stopMonitor != nil {
		s.stopMonitor()
		<-s.monitorDone
		s.stopMonitor = nil
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	proxyErr := s.proxy.Restore(cleanup)
	processErr := s.process.Stop(cleanup)
	return errors.Join(proxyErr, processErr)
}
