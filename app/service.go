package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
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

func newRuntimeService(configDir, stateDir string, initial config.Snapshot) (*runtimeService, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("clashpulse: controller secret: %w", err)
	}
	controllerPort, proxyPort, err := reserveLoopbackPorts()
	if err != nil {
		return nil, err
	}
	s := &runtimeService{
		configDir: configDir, stateDir: stateDir, store: config.NewStore(initial), proxy: sysproxy.NewManager(),
		intents: make(chan ipc.Command, intentQueueSize), changes: make(chan config.Change, 1), batches: make(chan monitor.Batch, 1),
		resourceRefresh: make(chan []string, 1), resourceAttempts: make(map[string]time.Time), stateChanged: make(chan struct{}, 1),
		backgroundErrors: make(chan error, 2), configErrors: make(chan error, 1), notificationErrors: make(chan error, 1),
		secret: hex.EncodeToString(secret), controllerAddress: fmt.Sprintf("127.0.0.1:%d", controllerPort), proxyPort: proxyPort,
		groups: make(map[string]mihomo.Group), proxies: make(map[string]string), automation: make(map[string]bool),
		lastSwitch: make(map[string]time.Time), manualOverride: make(map[string]bool), notifications: make(chan time.Duration, 1),
		notificationDone: make(chan struct{}),
	}
	for _, id := range initial.Monitor.AutomatedGroups {
		s.automation[id] = true
	}
	subStore, err := subscriptions.NewStore(filepath.Join(stateDir, "subscriptions"))
	if err != nil {
		return nil, err
	}
	downloader := download.NewClient(func(route download.Route) (http.RoundTripper, error) { return s.transport(route, false) })
	s.registry, err = resources.NewRegistry(filepath.Join(stateDir, "resources"), downloader)
	if err != nil {
		return nil, err
	}
	s.subs, err = subscriptions.NewService(subStore, subscriptions.Options{
		Transport: s.transport, Render: s.renderProfile, Validate: s.validateGenerated,
		Apply: s.applyGenerated, Restore: s.restoreActivation,
		OnChange: func() {
			select {
			case s.stateChanged <- struct{}{}:
			default:
			}
		},
		Current: func(id string) (config.Subscription, bool) {
			for _, item := range s.store.Snapshot().Subscriptions {
				if item.ID == id {
					return item, true
				}
			}
			return config.Subscription{}, false
		},
	})
	if err != nil {
		return nil, err
	}
	if err := s.syncSubscriptions(initial); err != nil {
		return nil, err
	}
	s.resourceDirty.Store(true)
	s.snapshot = s.stateSnapshot()
	s.reconcileSubscriptionFailures(s.subs.List())
	s.lastPublished = core.CloneSnapshot(s.snapshot)
	s.lastAppliedSettings = s.store.Snapshot()
	return s, nil
}
