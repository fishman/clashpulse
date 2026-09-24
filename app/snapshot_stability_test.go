package app

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/subscriptions"
)

func TestEmptyDNSPolicyDoesNotChangeAcrossClonedSnapshots(t *testing.T) {
	store, err := subscriptions.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := subscriptions.NewService(store, subscriptions.Options{
		Transport: func(download.Route, bool) (http.RoundTripper, error) { return http.DefaultTransport, nil },
		Render:    func(context.Context, []byte) ([]byte, error) { return []byte("valid"), nil },
		Validate:  func(context.Context, []byte) error { return nil },
		Apply:     func(context.Context, []byte) error { return nil },
		Restore:   func(context.Context, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &runtimeService{store: config.NewStore(config.Snapshot{}), subs: service}
	first := core.CloneSnapshot(s.stateSnapshot())
	if next := s.stateSnapshot(); !reflect.DeepEqual(first, next) {
		t.Fatalf("unchanged DNS policy changed snapshot after cloning: before=%+v after=%+v", first.DNS, next.DNS)
	}
}
