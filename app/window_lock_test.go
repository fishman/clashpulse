package app

import (
	"errors"
	"testing"
)

func TestWindowLockHoldsOneWindow(t *testing.T) {
	stateDir := t.TempDir()
	release, err := acquireWindowLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := acquireWindowLock(stateDir); !errors.Is(err, ErrWindowRunning) {
		if second != nil {
			second()
		}
		t.Fatalf("second window was not rejected: %v", err)
	}
	release()
	relaunch, err := acquireWindowLock(stateDir)
	if err != nil {
		t.Fatalf("relaunch after release: %v", err)
	}
	relaunch()
}
