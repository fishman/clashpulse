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
	Path                   string
	Version                string
	SupportsGeoIPDat       bool
	SupportsGeoSiteDat     bool
	SupportsMMDB           bool
	SupportsTUN            bool
	SupportsPinnedListener bool
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
	pinned := filepath.Join(dir, "pinned.yaml")
	if err := os.WriteFile(pinned, []byte(pinnedListenerFixture), 0600); err != nil {
		return Capability{}, fmt.Errorf("inspect mihomo: create check configuration: %w", err)
	}
	supportsPinned := exec.CommandContext(ctx, path, "-t", "-f", pinned).Run() == nil
	documented := documentedVersion(version)
	return Capability{Path: path, Version: version, SupportsGeoIPDat: documented, SupportsGeoSiteDat: documented, SupportsMMDB: documented, SupportsTUN: documented, SupportsPinnedListener: supportsPinned}, nil
}

// pinnedListenerFixture proves the binary accepts a listener pinned to a group
// with authenticated users, which steady_stream renders.
const pinnedListenerFixture = "proxies:\n  - name: clashpulse-inspect\n    type: direct\nproxy-groups:\n  - name: clashpulse-inspect-group\n    type: select\n    proxies: [clashpulse-inspect]\nlisteners:\n  - name: clashpulse-inspect-listener\n    type: mixed\n    listen: 127.0.0.1\n    port: 1\n    proxy: clashpulse-inspect-group\n    users:\n      - username: inspect\n        password: inspect\n"

// Mihomo prints "Mihomo Meta 1.19.30 ... with go1.26.6"; the leading v is
// optional and the anchor keeps the Go toolchain version from matching.
var mihomoVersion = regexp.MustCompile(`(?:^|\s)v?(\d+)\.(\d+)\.(\d+)\b`)

// ponytail: geoip.dat, geosite.dat, Country.mmdb, and the gvisor, mixed, and
// system TUN stacks ship in every Mihomo release, so this is a floor, not a
// pin. An exact-build check rejected working binaries (1.19.30 failed a
// 1.19.31 pin) and would reject every later release.
var minimumDocumentedVersion = [3]int{1, 19, 0}

func documentedVersion(identity string) bool {
	parts := mihomoVersion.FindStringSubmatch(identity)
	if len(parts) != 4 {
		return false
	}
	for i, floor := range minimumDocumentedVersion {
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
