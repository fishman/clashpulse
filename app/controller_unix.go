//go:build !windows

package app

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fishman/clashpulse/mihomo"
)

// controllerEndpoint puts Mihomo's controller on a Unix socket, so it listens
// on no TCP port. Mihomo creates the socket 0666 and does not check the secret
// on it, so this 0700 directory is the only access control.
func controllerEndpoint(stateDir string, _ int) (address, socket string, err error) {
	dir, err := filepath.Abs(filepath.Join(stateDir, "ctl"))
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("clashpulse: create controller directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("clashpulse: secure controller directory: %w", err)
	}
	return "", filepath.Join(dir, "mihomo.sock"), nil
}

func (s *runtimeService) newController() (*mihomo.Controller, error) {
	return mihomo.NewUnixController(s.controllerSocket, s.secret, &http.Client{Timeout: time.Second})
}

// clearControllerEndpoint removes a socket left by a Mihomo that did not exit
// cleanly; Mihomo cannot listen on an existing path.
func (s *runtimeService) clearControllerEndpoint() error {
	info, err := os.Lstat(s.controllerSocket)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSocket == 0 {
		return errors.New("clashpulse: controller socket path is not a socket")
	}
	return os.Remove(s.controllerSocket)
}
