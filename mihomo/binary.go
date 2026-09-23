package mihomo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Selection struct {
	Kind string
	Path string
}

type Capability struct {
	Path    string
	Version string
}

func Resolve(selection Selection) (string, error) {
	switch selection.Kind {
	case "system":
		path, err := exec.LookPath("mihomo")
		if err != nil {
			return "", fmt.Errorf("find system mihomo: %w", err)
		}
		return path, nil
	case "bundled", "path":
		return executable(selection.Path)
	default:
		return "", fmt.Errorf("invalid binary selection %q", selection.Kind)
	}
}

func Inspect(ctx context.Context, selection Selection) (Capability, error) {
	path, err := Resolve(selection)
	if err != nil {
		return Capability{}, err
	}

	output, err := exec.CommandContext(ctx, path, "-v").Output()
	if err != nil {
		return Capability{}, fmt.Errorf("inspect mihomo: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return Capability{}, fmt.Errorf("inspect mihomo: empty version")
	}
	return Capability{Path: path, Version: version}, nil
}

func executable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat mihomo binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("mihomo binary %q is not a regular executable file", path)
	}
	return path, nil
}
