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
	if err := os.WriteFile(check, []byte("proxies:\n  - name: clashpulse-inspect\n    type: direct\n"), 0600); err != nil {
		return Capability{}, fmt.Errorf("inspect mihomo: create check configuration: %w", err)
	}
	if err := exec.CommandContext(ctx, path, "-t", "-f", check).Run(); err != nil {
		return Capability{}, fmt.Errorf("inspect mihomo: configuration check unsupported: %w", err)
	}
	geo := documentedGeodataVersion(version)
	return Capability{Path: path, Version: version, SupportsGeoIPDat: geo, SupportsGeoSiteDat: geo, SupportsMMDB: geo}, nil
}

// Mihomo prints "Mihomo Meta 1.19.30 ... with go1.26.6"; the leading v is
// optional and the anchor keeps the Go toolchain version from matching.
var mihomoVersion = regexp.MustCompile(`(?:^|\s)v?(\d+)\.(\d+)\.(\d+)\b`)

// ponytail: geoip.dat, geosite.dat, and Country.mmdb ship in every Mihomo
// release, so this is a floor, not a pin. An exact-build check rejected working
// binaries (1.19.30 failed a 1.19.31 pin) and would reject every later release.
var minimumGeodataVersion = [3]int{1, 19, 0}

func documentedGeodataVersion(identity string) bool {
	parts := mihomoVersion.FindStringSubmatch(identity)
	if len(parts) != 4 {
		return false
	}
	for i, floor := range minimumGeodataVersion {
		value, err := strconv.Atoi(parts[i+1])
		if err != nil {
			return false
		}
		if value != floor {
			return value > floor
		}
	}
	return true
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
