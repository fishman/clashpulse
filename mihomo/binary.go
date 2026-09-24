package mihomo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Selection struct {
	Kind string
	Path string
}

type Capability struct {
	Path               string
	Version            string
	SupportsGeoIPDat   bool
	SupportsGeoSiteDat bool
	SupportsMMDB       bool
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

	command := exec.CommandContext(ctx, path, "-v")
	command.WaitDelay = 200 * time.Millisecond
	output, err := command.Output()
	if err != nil {
		return Capability{}, fmt.Errorf("inspect mihomo: %w", err)
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return Capability{}, fmt.Errorf("inspect mihomo: empty version")
	}
	dir, err := os.MkdirTemp("", "clashpulse-check-")
	if err != nil {
		return Capability{}, fmt.Errorf("inspect mihomo: create private check directory: %w", err)
	}
	defer os.RemoveAll(dir)
	check := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(check, []byte("proxies:\n  - name: DIRECT\n    type: direct\n"), 0600); err != nil {
		return Capability{}, fmt.Errorf("inspect mihomo: create check configuration: %w", err)
	}
	if err := exec.CommandContext(ctx, path, "-t", "-f", check).Run(); err != nil {
		return Capability{}, fmt.Errorf("inspect mihomo: configuration check unsupported: %w", err)
	}
	geo := documentedGeodataVersion(version)
	return Capability{Path: path, Version: version, SupportsGeoIPDat: geo, SupportsGeoSiteDat: geo, SupportsMMDB: geo}, nil
}

var mihomoVersion = regexp.MustCompile(`\bv(\d+)\.(\d+)\.(\d+)\b`)

func documentedGeodataVersion(identity string) bool {
	parts := mihomoVersion.FindStringSubmatch(identity)
	if len(parts) != 4 {
		return false
	}
	major, errMajor := strconv.Atoi(parts[1])
	minor, errMinor := strconv.Atoi(parts[2])
	patch, errPatch := strconv.Atoi(parts[3])
	if errMajor != nil || errMinor != nil || errPatch != nil {
		return false
	}
	return major == 1 && minor == 19 && patch == 31
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
