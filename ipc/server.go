package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/fishman/clashpulse/core"
)

const (
	maxClients       = 32
	writeTimeout     = 750 * time.Millisecond
	handshakeTimeout = 5 * time.Second
	handlerTimeout   = 2 * time.Second
)

var (
	ErrServerClosed  = errors.New("ipc: server is closed")
	ErrAlreadyServed = errors.New("ipc: server can only be served once")
)

// Handler must enqueue the command and return; it must not perform the requested
// work inline. A nil error means the intent was accepted into the app queue.
type Handler func(context.Context, Command) error

type ServerOptions struct {
	// Endpoint is a Unix socket path. Empty selects the per-user default.
	Endpoint string
	Handler  Handler
	// InitialSnapshot is the sanitized snapshot delivered on connect and on
	// explicit snapshot requests until Publish supplies a newer value.
	InitialSnapshot core.Snapshot
}

// Server hosts the local, versioned command and snapshot protocol.
type Server struct {
	endpoint string
	handler  Handler

	mu       sync.Mutex
	snapshot core.Snapshot
	clients  map[*serverClient]struct{}
	listener net.Listener
	cancel   context.CancelFunc
	served   bool
	closed   bool
	closeOne sync.Once
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.Handler == nil {
		return nil, errors.New("ipc: command handler is required")
	}
	if err := validateSnapshot(options.InitialSnapshot); err != nil {
		return nil, err
	}
	endpoint, err := normalizeEndpoint(options.Endpoint)
	if err != nil {
		return nil, err
	}
	return &Server{
		endpoint: endpoint,
		handler:  options.Handler,
		snapshot: core.CloneSnapshot(options.InitialSnapshot),
		clients:  make(map[*serverClient]struct{}),
	}, nil
}

func (s *Server) Endpoint() string { return s.endpoint }

// Publish replaces the current state and non-blockingly coalesces each
// subscriber's pending event to the newest snapshot.
func (s *Server) Publish(snapshot core.Snapshot) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	snapshot = core.CloneSnapshot(snapshot)
	s.mu.Lock()
	s.snapshot = snapshot
	for client := range s.clients {
		client.offer(snapshot)
	}
	s.mu.Unlock()
	return nil
}

// Serve accepts authenticated local peers until the context is cancelled or
// the server is closed. Each connection is isolated from snapshot producers.
func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrServerClosed
	}
	if s.served {
		s.mu.Unlock()
		return ErrAlreadyServed
	}
	s.served = true
	serveCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	defer func() {
		cancel()
		s.Close()
		s.mu.Lock()
		s.listener = nil
		s.mu.Unlock()
	}()

	listener, err := listenLocal(serveCtx, s.endpoint)
	if err != nil {
		if serveCtx.Err() != nil {
			return nil
		}
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = listener.Close()
		return nil
	}
	s.listener = listener
	s.mu.Unlock()

	sem := make(chan struct{}, maxClients)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if serveCtx.Err() != nil {
				return nil
			}
			return fmt.Errorf("ipc: local listener stopped: %w", err)
		}
		select {
		case sem <- struct{}{}:
			go func(conn net.Conn) {
				defer func() { <-sem }()
				s.serveConnection(serveCtx, conn)
			}(conn)
		default:
			_ = conn.Close()
		}
	}
}

func (s *Server) Close() error {
	s.closeOne.Do(func() {
		s.mu.Lock()
		s.closed = true
		cancel := s.cancel
		listener := s.listener
		clients := make([]*serverClient, 0, len(s.clients))
		for client := range s.clients {
			clients = append(clients, client)
		}
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if listener != nil {
			_ = listener.Close()
		}
		for _, client := range clients {
			client.shutdown()
		}
	})
	return nil
}

func (s *Server) currentSnapshot() core.Snapshot {
	s.mu.Lock()
	snapshot := core.CloneSnapshot(s.snapshot)
	s.mu.Unlock()
	return snapshot
}

func (s *Server) addClient(client *serverClient, subscribe bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if subscribe {
		client.offer(s.snapshot)
	}
	s.clients[client] = struct{}{}
	return true
}

func (s *Server) removeClient(client *serverClient) {
	s.mu.Lock()
	delete(s.clients, client)
	s.mu.Unlock()
	client.shutdown()
}

func (s *Server) serveConnection(ctx context.Context, conn net.Conn) {
	if err := checkPeer(conn); err != nil {
		_ = conn.Close()
		return
	}
	if err := conn.SetReadDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		_ = conn.Close()
		return
	}
	var hello clientFrame
	if err := readFrame(conn, &hello); err != nil {
		_ = conn.Close()
		return
	}
	if hello.Type != "hello" || hello.RequestID != "" || hello.Operation != "" || hello.Command != nil {
		_ = writeControlFrame(conn, serverFrame{Type: "error", Error: &protocolError{Code: "invalid_hello", Message: "invalid protocol handshake"}})
		_ = conn.Close()
		return
	}
	if hello.Version != ProtocolVersion {
		message := fmt.Sprintf("incompatible protocol version: client %d, server %d", hello.Version, ProtocolVersion)
		_ = writeControlFrame(conn, serverFrame{Type: "error", Version: ProtocolVersion, Error: &protocolError{Code: "incompatible_version", Message: message}})
		_ = conn.Close()
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return
	}

	client := newServerClient(conn, hello.Subscribe)
	if !s.addClient(client, hello.Subscribe) {
		client.shutdown()
		return
	}
	defer s.removeClient(client)
	if err := client.write(serverFrame{Type: "ready", Version: ProtocolVersion}); err != nil {
		return
	}
	if hello.Subscribe {
		go client.writeSnapshots()
	}

	for {
		var request clientFrame
		if err := readConnFrame(conn, &request); err != nil {
			return
		}
		if request.Type != "request" || !requestIDPattern.MatchString(request.RequestID) || request.Version != 0 || request.Subscribe {
			if client.write(serverFrame{Type: "response", RequestID: safeRequestID(request.RequestID), Error: &protocolError{Code: "invalid_request", Message: "invalid request"}}) != nil {
				return
			}
			continue
		}
		switch request.Operation {
		case "snapshot":
			if request.Command != nil {
				if client.write(invalidRequestResponse(request.RequestID)) != nil {
					return
				}
				continue
			}
			snapshot := s.currentSnapshot()
			if client.write(serverFrame{Type: "response", RequestID: request.RequestID, Snapshot: &snapshot}) != nil {
				return
			}
		case "command":
			if request.Command == nil || request.Command.validate() != nil {
				if client.write(serverFrame{Type: "response", RequestID: request.RequestID, Error: &protocolError{Code: "invalid_command", Message: "invalid command"}}) != nil {
					return
				}
				continue
			}
			handlerCtx, cancel := context.WithTimeout(ctx, handlerTimeout)
			err := s.handler(handlerCtx, *request.Command)
			cancel()
			if err != nil {
				if client.write(serverFrame{Type: "response", RequestID: request.RequestID, Error: &protocolError{Code: "command_rejected", Message: "command was not queued"}}) != nil {
					return
				}
				continue
			}
			ack := Acknowledgement{Queued: true}
			if client.write(serverFrame{Type: "response", RequestID: request.RequestID, Acknowledgement: &ack}) != nil {
				return
			}
		default:
			if client.write(invalidRequestResponse(request.RequestID)) != nil {
				return
			}
		}
	}
}

func safeRequestID(id string) string {
	if requestIDPattern.MatchString(id) {
		return id
	}
	return ""
}

func invalidRequestResponse(id string) serverFrame {
	return serverFrame{Type: "response", RequestID: id, Error: &protocolError{Code: "invalid_request", Message: "invalid request"}}
}

func writeControlFrame(conn net.Conn, frame serverFrame) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	defer conn.SetWriteDeadline(time.Time{})
	return writeFrame(conn, frame)
}

type serverClient struct {
	conn       net.Conn
	subscribed bool
	events     chan core.Snapshot
	done       chan struct{}
	writeMu    sync.Mutex
	closeOne   sync.Once
}

func newServerClient(conn net.Conn, subscribed bool) *serverClient {
	return &serverClient{conn: conn, subscribed: subscribed, events: make(chan core.Snapshot, 1), done: make(chan struct{})}
}

func (c *serverClient) offer(snapshot core.Snapshot) {
	if !c.subscribed {
		return
	}
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.events <- snapshot:
	default:
		select {
		case <-c.events:
		default:
		}
		select {
		case c.events <- snapshot:
		default:
		}
	}
}

func (c *serverClient) writeSnapshots() {
	for {
		select {
		case snapshot := <-c.events:
			if c.write(serverFrame{Type: "event", Snapshot: &snapshot}) != nil {
				c.shutdown()
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *serverClient) write(frame serverFrame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	defer c.conn.SetWriteDeadline(time.Time{})
	return writeFrame(c.conn, frame)
}

func (c *serverClient) shutdown() {
	c.closeOne.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}
