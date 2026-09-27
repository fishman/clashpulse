package app

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/fishman/notmutt/lib/xdg"
)

// ErrWindowRunning reports that this user already has a ClashPulse window.
// Terminal clients are not window holders: any number of them may attach to the
// same service.
var ErrWindowRunning = errors.New("clashpulse: another ClashPulse window is already running; use its tray icon to show it")

// AcquireWindowLock marks this user's state directory as hosting the single
// desktop window and returns its release function. It is separate from the
// service owner lock, because a window may attach to a service it did not start.
func AcquireWindowLock() (func(), error) {
	stateHome := xdg.StateHome()
	if stateHome == "" {
		return nil, fmt.Errorf("clashpulse: cannot resolve the private state directory")
	}
	return acquireWindowLock(filepath.Join(stateHome, "clashpulse"))
}

func acquireWindowLock(stateDir string) (func(), error) {
	if err := privateDirectory(stateDir); err != nil {
		return nil, err
	}
	file, err := openOwnerLock(filepath.Join(stateDir, ".window.lock"))
	if err != nil {
		if errors.Is(err, errStateInUse) {
			return nil, ErrWindowRunning
		}
		return nil, fmt.Errorf("clashpulse: acquire window lock: %w", err)
	}
	return func() { _ = file.Close() }, nil
}
