package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fishman/clashpulse/core"
)

var ErrClientClosed = errors.New("ipc: client is closed")

// Client is safe for concurrent Send and Snapshot calls. Events is a
// single-consumer stream whose pending snapshot is replaced by newer state.
type Client struct {
	conn    net.Conn
	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan serverFrame
	nextID    atomic.Uint64
	events    chan Event
	done      chan struct{}
	closeOnce sync.Once
	terminal  error
}

// DefaultEndpoint returns the platform's default local endpoint. It is empty
// when the current platform has no implemented local transport.
func DefaultEndpoint() string { return defaultEndpoint() }

// Dial establishes a local connection, negotiates the first-message schema
// version, and subscribes to coalesced snapshot events.
func Dial(ctx context.Context, endpoint string) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path, err := normalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := dialLocal(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	cancellationDone := make(chan struct{})
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
		close(cancellationDone)
	})
	var stopOnce sync.Once
	stopHandshakeCancellation := func() {
		stopOnce.Do(func() {
			if !stopCancellation() {
				<-cancellationDone
			}
		})
	}
	defer stopHandshakeCancellation()

	deadline := time.Now().Add(handshakeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, errors.New("ipc: could not set handshake deadline")
	}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := writeFrame(conn, clientFrame{Type: "hello", Version: ProtocolVersion, Subscribe: true}); err != nil {
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("ipc: could not send protocol handshake")
	}
	var ready serverFrame
	if err := readFrame(conn, &ready); err != nil {
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("ipc: protocol handshake failed")
	}
	stopHandshakeCancellation()
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if ready.Type == "error" {
		_ = conn.Close()
		return nil, errorFromProtocol(ready.Error, ready.Version)
	}
	if ready.Type != "ready" || ready.Version != ProtocolVersion {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: client supports %d, server supports %d", ErrIncompatibleVersion, ProtocolVersion, ready.Version)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, errors.New("ipc: could not clear handshake deadline")
	}
	client := &Client{
		conn:    conn,
		pending: make(map[string]chan serverFrame),
		events:  make(chan Event, 1),
		done:    make(chan struct{}),
	}
	go client.readLoop()
	return client, nil
}

// Send queues a typed command and returns once the application Handler accepts
// or rejects the intent. It never waits for the requested operation to finish.
func (c *Client) Send(ctx context.Context, command Command) (Acknowledgement, error) {
	if err := command.validate(); err != nil {
		return Acknowledgement{}, errors.New("ipc: invalid command")
	}
	frame, err := c.request(ctx, clientFrame{Type: "request", Operation: "command", Command: &command})
	if err != nil {
		return Acknowledgement{}, err
	}
	if frame.Acknowledgement == nil || !frame.Acknowledgement.Queued || frame.Snapshot != nil {
		return Acknowledgement{}, errors.New("ipc: invalid command acknowledgement")
	}
	return *frame.Acknowledgement, nil
}

// Snapshot requests the latest immutable, sanitized core snapshot.
func (c *Client) Snapshot(ctx context.Context) (core.Snapshot, error) {
	frame, err := c.request(ctx, clientFrame{Type: "request", Operation: "snapshot"})
	if err != nil {
		return core.Snapshot{}, err
	}
	if frame.Snapshot == nil || frame.Acknowledgement != nil || validateSnapshot(*frame.Snapshot) != nil {
		return core.Snapshot{}, errors.New("ipc: invalid snapshot response")
	}
	return core.CloneSnapshot(*frame.Snapshot), nil
}

// Events yields snapshot changes. When the consumer is slow, intermediate
// snapshots are discarded and only the latest pending state is retained.
func (c *Client) Events() <-chan Event { return c.events }

func (c *Client) Close() error {
	c.finish(ErrClientClosed)
	return nil
}

func (c *Client) request(ctx context.Context, request clientFrame) (serverFrame, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.clientError(); err != nil {
		return serverFrame{}, err
	}
	request.RequestID = strconv.FormatUint(c.nextID.Add(1), 10)
	response := make(chan serverFrame, 1)
	c.pendingMu.Lock()
	select {
	case <-c.done:
		c.pendingMu.Unlock()
		return serverFrame{}, c.clientError()
	default:
	}
	c.pending[request.RequestID] = response
	c.pendingMu.Unlock()

	if err := c.writeRequest(ctx, request); err != nil {
		c.removePending(request.RequestID)
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return serverFrame{}, ctxErr
		}
		c.finish(err)
		return serverFrame{}, errors.New("ipc: request could not be sent")
	}
	select {
	case frame := <-response:
		if frame.Error != nil {
			return serverFrame{}, errorFromProtocol(frame.Error, frame.Version)
		}
		return frame, nil
	case <-ctx.Done():
		c.removePending(request.RequestID)
		return serverFrame{}, ctx.Err()
	case <-c.done:
		return serverFrame{}, c.clientError()
	}
}

func (c *Client) writeRequest(ctx context.Context, request clientFrame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(writeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	err := writeFrame(c.conn, request)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

func (c *Client) readLoop() {
	defer close(c.events)
	for {
		var frame serverFrame
		if err := readConnFrame(c.conn, &frame); err != nil {
			if errors.Is(err, net.ErrClosed) {
				c.finish(ErrClientClosed)
			} else {
				c.finish(errors.New("ipc: connection ended"))
			}
			return
		}
		switch frame.Type {
		case "response":
			if !requestIDPattern.MatchString(frame.RequestID) {
				c.finish(errors.New("ipc: invalid server response"))
				return
			}
			c.pendingMu.Lock()
			response := c.pending[frame.RequestID]
			delete(c.pending, frame.RequestID)
			c.pendingMu.Unlock()
			if response != nil {
				response <- frame
			}
		case "event":
			if frame.Snapshot == nil || validateSnapshot(*frame.Snapshot) != nil || frame.Acknowledgement != nil || frame.Error != nil {
				c.finish(errors.New("ipc: invalid snapshot event"))
				return
			}
			c.offerEvent(Event{Snapshot: core.CloneSnapshot(*frame.Snapshot)})
		default:
			c.finish(errors.New("ipc: invalid server message"))
			return
		}
	}
}

func (c *Client) offerEvent(event Event) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.events <- event:
	default:
		select {
		case <-c.events:
		default:
		}
		select {
		case c.events <- event:
		default:
		}
	}
}

func (c *Client) removePending(id string) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *Client) finish(err error) {
	c.closeOnce.Do(func() {
		c.terminal = err
		close(c.done)
		c.pendingMu.Lock()
		clear(c.pending)
		c.pendingMu.Unlock()
		_ = c.conn.Close()
	})
}

func (c *Client) clientError() error {
	select {
	case <-c.done:
		if c.terminal != nil {
			return c.terminal
		}
		return ErrClientClosed
	default:
		return nil
	}
}

func errorFromProtocol(remote *protocolError, serverVersion uint16) error {
	if remote == nil {
		return errors.New("ipc: invalid protocol error response")
	}
	switch remote.Code {
	case "incompatible_version":
		return fmt.Errorf("%w: client supports %d, server supports %d", ErrIncompatibleVersion, ProtocolVersion, serverVersion)
	case "invalid_hello", "invalid_request":
		return errors.New("ipc: invalid protocol request")
	case "invalid_command":
		return errors.New("ipc: invalid command")
	case "command_rejected":
		return errors.New("ipc: command was not queued")
	default:
		return errors.New("ipc: protocol error")
	}
}
