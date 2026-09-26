package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/notmutt/lib/xdg"
)

func Run(ctx context.Context) error {
	configHome, stateHome := xdg.ConfigHome(), xdg.StateHome()
	if configHome == "" || stateHome == "" {
		return fmt.Errorf("clashpulse: cannot resolve private configuration and state directories")
	}
	return RunAt(ctx, filepath.Join(configHome, "clashpulse"), filepath.Join(stateHome, "clashpulse"), "")
}

func RunFile(ctx context.Context, path string, ready func() error) error {
	configHome, stateHome := xdg.ConfigHome(), xdg.StateHome()
	if configHome == "" || stateHome == "" {
		return core.WrapActivation(core.ActivationStateCommit, fmt.Errorf("missing private home"))
	}
	return RunFileAt(ctx, filepath.Join(configHome, "clashpulse"), filepath.Join(stateHome, "clashpulse"), "", path, ready)
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
