//go:build unix

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/fishman/notmutt/lib/localipc"
)

func defaultEndpoint() string {
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "clashpulse.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("clashpulse-%d", os.Getuid()), "ipc.sock")
}

func normalizeEndpoint(endpoint string) (string, error) {
	if !peerAuthAvailable() {
		return "", ErrUnsupportedPlatform
	}
	if endpoint == "" {
		endpoint = defaultEndpoint()
	}
	if endpoint == "" {
		return "", ErrUnsupportedPlatform
	}
	path, err := filepath.Abs(endpoint)
	if err != nil {
		return "", errors.New("ipc: invalid Unix socket endpoint")
	}
	if filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return "", errors.New("ipc: endpoint must name a Unix socket")
	}
	return path, nil
}

func listenLocal(ctx context.Context, endpoint string) (net.Listener, error) {
	if !peerAuthAvailable() {
		return nil, ErrUnsupportedPlatform
	}
	if err := ensurePrivateDirectory(filepath.Dir(endpoint)); err != nil {
		return nil, err
	}
	listener, err := localipc.Listen(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("ipc: could not listen on Unix socket: %w", err)
	}
	return listener, nil
}

func dialLocal(ctx context.Context, endpoint string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("ipc: could not connect to Unix socket: %w", err)
	}
	return conn, nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			return errors.New("ipc: endpoint directory must be a private 0700 directory")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return errors.New("ipc: could not inspect endpoint directory")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.New("ipc: could not create private endpoint directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return errors.New("ipc: could not secure endpoint directory")
	}
	info, err = os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("ipc: endpoint directory must be a private 0700 directory")
	}
	return nil
}
