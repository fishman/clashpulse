//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
)

func TestWindowsDefaultEndpointIsStable(t *testing.T) {
	endpoint := DefaultEndpoint()
	if endpoint == "" {
		t.Fatal("DefaultEndpoint is empty")
	}
	if got := DefaultEndpoint(); got != endpoint {
		t.Fatalf("DefaultEndpoint changed from %q to %q", endpoint, got)
	}
	if got, err := normalizeEndpoint(""); err != nil || got != endpoint {
		t.Fatalf("normalizeEndpoint(\"\") = %q, %v; want %q", got, err, endpoint)
	}
}

func TestWindowsNamedPipeRoundTrip(t *testing.T) {
	queued := make(chan Command, 1)
	endpoint := fmt.Sprintf(`\\.\pipe\clashpulse-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	server, err := NewServer(ServerOptions{
		Endpoint: endpoint,
		Handler: func(_ context.Context, command Command) error {
			queued <- command
			return nil
		},
		InitialSnapshot: core.Snapshot{Revision: 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()
	var client *Client
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
		cancel()
		_ = server.Close()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("Serve returned: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("IPC server did not stop")
		}
	})

	dialCtx, stopDial := context.WithTimeout(ctx, 5*time.Second)
	defer stopDial()
	client, err = Dial(dialCtx, endpoint)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	snapshot, err := client.Snapshot(dialCtx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Revision != 7 {
		t.Fatalf("snapshot revision = %d, want 7", snapshot.Revision)
	}

	command := Command{Kind: CommandSelectGroup, GroupID: "select-main", ChoiceID: "proxy-a"}
	ack, err := client.Send(dialCtx, command)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !ack.Queued {
		t.Fatal("command was not acknowledged as queued")
	}
	select {
	case got := <-queued:
		if !reflect.DeepEqual(got, command) {
			t.Fatalf("queued command = %#v, want %#v", got, command)
		}
	case <-dialCtx.Done():
		t.Fatalf("handler did not receive command: %v", dialCtx.Err())
	}
}

func TestWindowsNamedPipeRejectsNonlocalEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		`\\server\pipe\clashpulse`,
		`\\?\pipe\clashpulse`,
		`\\.\pipe\clashpulse\child`,
		`\\.\pipe\`,
		`relative`,
		`\\.\pipe\bad:name`,
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := normalizeEndpoint(endpoint); err == nil {
				t.Fatal("normalizeEndpoint accepted a nonlocal or malformed endpoint")
			}
			if _, err := Dial(context.Background(), endpoint); err == nil || errors.Is(err, ErrUnsupportedPlatform) {
				t.Fatalf("Dial error = %v, want endpoint rejection", err)
			}
		})
	}
}

func TestWindowsPipeProcessOwnerRejectsDifferentSID(t *testing.T) {
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	pid := uint32(os.Getpid())
	if err := verifyProcessOwner(pid, sid); err != nil {
		t.Fatalf("same-user process rejected: %v", err)
	}
	if err := verifyProcessOwner(pid, "S-1-0-0"); err == nil {
		t.Fatal("different SID accepted")
	}
}
