//go:build unix

package ipc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fishman/clashpulse/core"
)

func startTestServer(t *testing.T, handler Handler, initial core.Snapshot) (*Server, *Client, context.Context) {
	t.Helper()
	endpoint := filepath.Join(t.TempDir(), "private", "ipc.sock")
	server, err := NewServer(ServerOptions{Endpoint: endpoint, Handler: handler, InitialSnapshot: initial})
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
				t.Errorf("serve returned: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("IPC server did not stop")
		}
	})

	dialCtx, stopDial := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopDial()
	for {
		client, err = Dial(dialCtx, endpoint)
		if err == nil {
			break
		}
		if dialCtx.Err() != nil {
			t.Fatalf("connect to IPC server: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return server, client, ctx
}

func TestCommandAcknowledgesQueuedIntent(t *testing.T) {
	queued := make(chan Command, 1)
	_, client, ctx := startTestServer(t, func(_ context.Context, command Command) error {
		queued <- command
		return nil
	}, core.Snapshot{})

	command := Command{Kind: CommandSelectGroup, GroupID: "select-main", ChoiceID: "proxy-a"}
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ack, err := client.Send(callCtx, command)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !ack.Queued {
		t.Fatal("command was not acknowledged as queued")
	}
	select {
	case got := <-queued:
		if got != command {
			t.Fatalf("queued command = %#v, want %#v", got, command)
		}
	case <-callCtx.Done():
		t.Fatal("handler did not enqueue command before acknowledgement")
	}
}

func TestServerRejectsIncompatibleVersion(t *testing.T) {
	server, _, _ := startTestServer(t, func(context.Context, Command) error { return nil }, core.Snapshot{})
	conn, err := dialLocal(context.Background(), server.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := writeFrame(conn, clientFrame{Type: "hello", Version: ProtocolVersion + 1}); err != nil {
		t.Fatal(err)
	}
	var response serverFrame
	if err := readFrame(conn, &response); err != nil {
		t.Fatalf("read version response: %v", err)
	}
	if response.Type != "error" || response.Error == nil || response.Error.Code != "incompatible_version" || !strings.Contains(response.Error.Message, "incompatible protocol version") {
		t.Fatalf("version response = %#v", response)
	}
}

func TestClientRejectsUnsupportedProtocol(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "ipc.sock")
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		var hello clientFrame
		if err := readFrame(conn, &hello); err != nil {
			serverDone <- err
			return
		}
		serverDone <- writeFrame(conn, serverFrame{Type: "error", Version: ProtocolVersion + 1, Error: &protocolError{Code: "incompatible_version"}})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = Dial(ctx, endpoint)
	if !errors.Is(err, ErrIncompatibleVersion) {
		t.Fatalf("Dial accepted incompatible server: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("fake server: %v", err)
	}
}

func TestClientReceivesSnapshotEvent(t *testing.T) {
	server, client, ctx := startTestServer(t, func(context.Context, Command) error { return nil }, core.Snapshot{Revision: 1})
	select {
	case event := <-client.Events():
		if event.Snapshot.Revision != 1 {
			t.Fatalf("initial event revision = %d, want 1", event.Snapshot.Revision)
		}
	case <-ctx.Done():
		t.Fatal("server context ended before initial snapshot event")
	case <-time.After(time.Second):
		t.Fatal("initial snapshot event was not delivered")
	}
	if err := server.Publish(core.Snapshot{Revision: 2, Errors: []core.ErrorSnapshot{{Message: "sanitized"}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-client.Events():
		if event.Snapshot.Revision != 2 || event.Snapshot.Errors[0].Message != "sanitized" {
			t.Fatalf("snapshot event = %#v", event.Snapshot)
		}
	case <-ctx.Done():
		t.Fatal("server context ended before update event")
	case <-time.After(time.Second):
		t.Fatal("updated snapshot event was not delivered")
	}
}

func TestSnapshotIsCopiedAndSocketIsPrivate(t *testing.T) {
	groups := []core.GroupSnapshot{{ID: "main", Selected: "proxy-a", Proxies: []string{"proxy-a"}}}
	usage := &core.UsageSnapshot{TotalBytes: 17}
	subscriptions := []core.SubscriptionSnapshot{{ID: "subscription", SourceHost: "provider.invalid", Usage: usage}}
	capabilities := []string{"capability-a"}
	initial := core.Snapshot{
		Revision:      3,
		Groups:        groups,
		Subscriptions: subscriptions,
		Binary:        core.BinarySnapshot{Capabilities: capabilities},
	}
	server, client, ctx := startTestServer(t, func(context.Context, Command) error { return nil }, initial)
	groups[0].ID = "mutated-after-server-creation"
	groups[0].Proxies[0] = "mutated-proxy"
	usage.TotalBytes = 99
	capabilities[0] = "mutated-capability"

	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	snapshot, err := client.Snapshot(callCtx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 3 || len(snapshot.Groups) != 1 || snapshot.Groups[0].ID != "main" || snapshot.Groups[0].Proxies[0] != "proxy-a" {
		t.Fatalf("initial groups were not deep-copied: %#v", snapshot.Groups)
	}
	if snapshot.Subscriptions[0].Usage == nil || snapshot.Subscriptions[0].Usage.TotalBytes != 17 || snapshot.Binary.Capabilities[0] != "capability-a" {
		t.Fatalf("initial snapshot fields were not deep-copied: %#v", snapshot)
	}

	groups[0].ID = "published"
	groups[0].Proxies[0] = "proxy-b"
	usage.TotalBytes = 23
	capabilities[0] = "capability-b"
	if err := server.Publish(core.Snapshot{Revision: 4, Groups: groups, Subscriptions: subscriptions, Binary: core.BinarySnapshot{Capabilities: capabilities}}); err != nil {
		t.Fatal(err)
	}
	groups[0].ID = "mutated-after-publish"
	groups[0].Proxies[0] = "mutated-after-publish"
	usage.TotalBytes = 101
	capabilities[0] = "mutated-after-publish"
	snapshot, err = client.Snapshot(callCtx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 4 || snapshot.Groups[0].ID != "published" || snapshot.Groups[0].Proxies[0] != "proxy-b" {
		t.Fatalf("published groups were not deep-copied: %#v", snapshot.Groups)
	}
	if snapshot.Subscriptions[0].Usage == nil || snapshot.Subscriptions[0].Usage.TotalBytes != 23 || snapshot.Binary.Capabilities[0] != "capability-b" {
		t.Fatalf("published snapshot fields were not deep-copied: %#v", snapshot)
	}

	info, err := os.Stat(server.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket permissions = %04o, want 0600", info.Mode().Perm())
	}
	info, err = os.Stat(filepath.Dir(server.Endpoint()))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("socket directory permissions = %04o, want 0700", info.Mode().Perm())
	}
}

func TestOversizedSnapshotIsRejected(t *testing.T) {
	server, err := NewServer(ServerOptions{
		Endpoint: filepath.Join(t.TempDir(), "private", "ipc.sock"),
		Handler:  func(context.Context, Command) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := core.Snapshot{Errors: []core.ErrorSnapshot{{Message: strings.Repeat("x", MaxFrameSize)}}}
	if err := server.Publish(snapshot); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("Publish error = %v, want oversized snapshot error", err)
	}
}
func TestSlowSubscriberDoesNotBlockSnapshotProducer(t *testing.T) {
	conn := &blockingConn{started: make(chan struct{}), unblock: make(chan struct{})}
	client := newServerClient(conn, true)
	defer client.shutdown()
	server := &Server{clients: map[*serverClient]struct{}{client: {}}, snapshot: core.Snapshot{}}
	go client.writeSnapshots()
	client.offer(core.Snapshot{Revision: 1})
	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("event writer did not reach blocked connection")
	}

	published := make(chan struct{})
	go func() {
		for revision := uint64(2); revision <= 1000; revision++ {
			server.Publish(core.Snapshot{Revision: revision})
		}
		close(published)
	}()
	select {
	case <-published:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("snapshot publishing blocked on slow client")
	}
	select {
	case latest := <-client.events:
		if latest.Revision != 1000 {
			t.Fatalf("coalesced snapshot revision = %d, want 1000", latest.Revision)
		}
	default:
		t.Fatal("slow client did not retain latest snapshot")
	}
	client.shutdown()
}

func TestHandlerErrorDoesNotLeakDetails(t *testing.T) {
	_, client, ctx := startTestServer(t, func(context.Context, Command) error {
		return errors.New("failed fetching https://user:password@secret.invalid/profile")
	}, core.Snapshot{})
	callCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err := client.Send(callCtx, Command{Kind: CommandStart})
	if err == nil {
		t.Fatal("Send succeeded after handler rejection")
	}
	if strings.Contains(err.Error(), "secret.invalid") || strings.Contains(err.Error(), "password") {
		t.Fatalf("handler error leaked through IPC: %v", err)
	}
}

type blockingConn struct {
	started   chan struct{}
	unblock   chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func (c *blockingConn) Read([]byte) (int, error) {
	<-c.unblock
	return 0, io.EOF
}

func (c *blockingConn) Write(body []byte) (int, error) {
	c.startOnce.Do(func() { close(c.started) })
	<-c.unblock
	return len(body), nil
}

func (c *blockingConn) Close() error {
	c.closeOnce.Do(func() { close(c.unblock) })
	return nil
}

func (c *blockingConn) LocalAddr() net.Addr              { return testAddr("local") }
func (c *blockingConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (c *blockingConn) SetDeadline(time.Time) error      { return nil }
func (c *blockingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *blockingConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }
