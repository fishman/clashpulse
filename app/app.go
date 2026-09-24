package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fishman/notmutt/lib/xdg"
)

func Run(ctx context.Context) error {
	configHome, stateHome := xdg.ConfigHome(), xdg.StateHome()
	if configHome == "" || stateHome == "" {
		return fmt.Errorf("clashpulse: cannot resolve private configuration and state directories")
	}
	return RunAt(ctx, filepath.Join(configHome, "clashpulse"), filepath.Join(stateHome, "clashpulse"), "")
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("clashpulse: private state must be a real directory")
	}
	return os.Chmod(path, 0o700)
}
