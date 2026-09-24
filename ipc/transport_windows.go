//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const namedPipePrefix = `\\.\pipe\`

func defaultEndpoint() string {
	sid, err := currentUserSID()
	if err != nil {
		return ""
	}
	return namedPipePrefix + "clashpulse-" + sid
}

func normalizeEndpoint(endpoint string) (string, error) {
	if endpoint == "" {
		endpoint = defaultEndpoint()
		if endpoint == "" {
			return "", errors.New("ipc: current user SID is unavailable")
		}
	}
	if len(endpoint) <= len(namedPipePrefix) || !strings.EqualFold(endpoint[:len(namedPipePrefix)], namedPipePrefix) {
		return "", errors.New("ipc: endpoint must be a local Windows named pipe")
	}
	name := endpoint[len(namedPipePrefix):]
	if len(name) > 128 || name == "." || name == ".." {
		return "", errors.New("ipc: malformed Windows named-pipe endpoint")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return "", errors.New("ipc: malformed Windows named-pipe endpoint")
		}
	}
	return namedPipePrefix + name, nil
}

func listenLocal(ctx context.Context, endpoint string) (net.Listener, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")",
	})
	if err != nil {
		return nil, fmt.Errorf("ipc: could not listen on Windows named pipe: %w", err)
	}
	context.AfterFunc(ctx, func() { _ = listener.Close() })
	return listener, nil
}

func dialLocal(ctx context.Context, endpoint string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("ipc: could not connect to Windows named pipe: %w", err)
	}
	if err := verifyPipeOwner(conn, true); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ipc: named-pipe server authentication failed: %w", err)
	}
	return conn, nil
}

func peerAuthAvailable() bool { return true }

func checkPeer(conn net.Conn) error { return verifyPipeOwner(conn, false) }

func verifyPipeOwner(conn net.Conn, server bool) error {
	if _, ok := conn.(winio.PipeConn); !ok {
		return errors.New("ipc: peer authentication failed")
	}
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("ipc: pipe handle is unavailable")
	}
	var pid uint32
	var err error
	if server {
		err = windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid)
	} else {
		err = windows.GetNamedPipeClientProcessId(windows.Handle(file.Fd()), &pid)
	}
	if err != nil {
		return fmt.Errorf("ipc: inspect named-pipe peer: %w", err)
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	return verifyProcessOwner(pid, sid)
}

func verifyProcessOwner(pid uint32, expectedSID string) error {
	if pid == 0 {
		return errors.New("ipc: named-pipe peer process is unavailable")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("ipc: inspect named-pipe peer process: %w", err)
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("ipc: inspect named-pipe peer token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("ipc: inspect named-pipe peer owner: %w", err)
	}
	if user.User.Sid.String() != expectedSID {
		return errors.New("ipc: named-pipe peer belongs to another user")
	}
	return nil
}

func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("ipc: could not read current user SID: %w", err)
	}
	sid := user.User.Sid.String()
	if sid == "" {
		return "", errors.New("ipc: could not read current user SID")
	}
	return sid, nil
}
