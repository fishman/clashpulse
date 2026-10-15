package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

// A refresh run while the GUI owns the state goes through the service and
// waits for each job's outcome instead of failing on the state lock.
func TestServiceRefreshWaitsForEachJobOutcome(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "private", "ipc.sock")
	if handled, err := serviceRefresh(context.Background(), endpoint, "", "", false); handled || err != nil {
		t.Fatalf("without a service: handled=%v err=%v, want the offline path", handled, err)
	}
	state := core.Snapshot{Subscriptions: []core.SubscriptionSnapshot{
		{ID: "daily", Enabled: true, HashPrefix: "abc123"},
		{ID: "spare", Enabled: false},
		{ID: "weekly", Enabled: true},
	}}
	var server *ipc.Server
	sent := make(chan ipc.Command, 4)
	server, err := ipc.NewServer(ipc.ServerOptions{Endpoint: endpoint, InitialSnapshot: state, Handler: func(_ context.Context, command ipc.Command) error {
		sent <- command
		outcome := "succeeded"
		if command.SubscriptionID == "daily" {
			outcome = "failed"
			state.Subscriptions[0].LastFailure = "subscriptions: invalid profile"
		}
		state.FinishedJobs = append(state.FinishedJobs, core.JobSnapshot{ID: command.JobID, Kind: string(command.Kind), State: outcome})
		next := core.CloneSnapshot(state)
		// Publish after the acknowledgement, as the real service does.
		go func() { time.Sleep(20 * time.Millisecond); _ = server.Publish(next) }()
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	t.Cleanup(func() { _ = server.Close() })
	<-server.Ready()

	handled, err := serviceRefresh(ctx, endpoint, "subscription", "", false)
	if !handled || err == nil {
		t.Fatalf("handled=%v err=%v, want the daily failure", handled, err)
	}
	for _, want := range []string{"subscription daily", "invalid profile", "kept last good version abc123"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "weekly") {
		t.Fatalf("succeeded subscription reported as failed: %v", err)
	}
	close(sent)
	var ids []string
	for command := range sent {
		ids = append(ids, command.SubscriptionID)
	}
	if strings.Join(ids, ",") != "daily,weekly" {
		t.Fatalf("refreshed %v, want only enabled subscriptions", ids)
	}
	if handled, err := serviceRefresh(ctx, endpoint, "subscription", "missing", false); !handled || err == nil || !strings.Contains(err.Error(), `"missing" is not configured`) {
		t.Fatalf("unknown id: handled=%v err=%v", handled, err)
	}
}
