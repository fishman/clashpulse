package app

import (
	"errors"
	"fmt"
	"path/filepath"
)

var errStateInUse = errors.New("clashpulse: private state is in use; close the desktop service before refreshing")

func acquireOwnerLock(stateDir string) (func(), error) {
	if err := privateDirectory(stateDir); err != nil {
		return nil, err
	}
	file, err := openOwnerLock(filepath.Join(stateDir, ".owner.lock"))
	if err != nil {
		if errors.Is(err, errStateInUse) {
			return nil, errStateInUse
		}
		return nil, fmt.Errorf("clashpulse: acquire private-state lock: %w", err)
	}
	return func() { _ = file.Close() }, nil
}
