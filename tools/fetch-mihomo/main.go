// Command fetch-mihomo downloads one Mihomo asset pinned in
// packaging/mihomo.lock, verifies its SHA-256, and writes the executable (or,
// for the source target, the archive itself). Release packaging uses it; it
// never resolves "latest".
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

const (
	upstream       = "https://github.com/MetaCubeX/mihomo"
	maxArchiveSize = 64 << 20
	maxExecutable  = 128 << 20
)

type entry struct{ asset, sum string }

type lock struct {
	version string
	entries map[string]entry
}

type result struct{ Version, Asset, ArchiveSHA256, SHA256, URL string }

func parseLock(r io.Reader) (lock, error) {
	l := lock{entries: map[string]entry{}}
	scanner := bufio.NewScanner(r)
	for line := 1; scanner.Scan(); line++ {
		fields := strings.Fields(scanner.Text())
		switch {
		case len(fields) == 0 || strings.HasPrefix(fields[0], "#"):
		case fields[0] == "version" && len(fields) == 2 && l.version == "":
			l.version = fields[1]
		case len(fields) == 3 && fields[0] != "version":
			target, asset, sum := fields[0], fields[1], fields[2]
			if _, dup := l.entries[target]; dup {
				return lock{}, fmt.Errorf("lock line %d: duplicate target %q", line, target)
			}
			if raw, err := hex.DecodeString(sum); err != nil || len(raw) != sha256.Size {
				return lock{}, fmt.Errorf("lock line %d: invalid sha256", line)
			}
			if asset != filepath.Base(asset) || strings.ContainsAny(asset, `/\`) || strings.HasPrefix(asset, ".") {
				return lock{}, fmt.Errorf("lock line %d: invalid asset name %q", line, asset)
			}
			l.entries[target] = entry{asset: asset, sum: sum}
		default:
			return lock{}, fmt.Errorf("lock line %d: malformed", line)
		}
	}
	if err := scanner.Err(); err != nil {
		return lock{}, err
	}
	if l.version == "" {
		return lock{}, errors.New("lock: missing version")
	}
	return l, nil
}

func fetch(ctx context.Context, client *http.Client, base string, l lock, target, out string) (result, error) {
	e, ok := l.entries[target]
	if !ok {
		return result{}, fmt.Errorf("unknown target %q", target)
	}
	url := base + "/releases/download/" + l.version + "/" + e.asset
	if target == "source" {
		url = base + "/archive/refs/tags/" + e.asset
	}
	archive, err := download(ctx, client, url)
	if err != nil {
		return result{}, err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != e.sum {
		return result{}, fmt.Errorf("%s: checksum mismatch: got %x", e.asset, got)
	}
	data, mode := archive, os.FileMode(0o644)
	if target != "source" {
		if data, err = extract(e.asset, archive); err != nil {
			return result{}, err
		}
		mode = 0o755
	}
	if err := writeFile(out, data, mode); err != nil {
		return result{}, err
	}
	sum := sha256.Sum256(data)
	return result{Version: l.version, Asset: e.asset, ArchiveSHA256: e.sum, SHA256: hex.EncodeToString(sum[:]), URL: url}, nil
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return readCapped(resp.Body, maxArchiveSize)
}

func extract(asset string, archive []byte) ([]byte, error) {
	switch {
	case strings.HasSuffix(asset, ".gz"):
		r, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", asset, err)
		}
		return readCapped(r, maxExecutable)
	case strings.HasSuffix(asset, ".zip"):
		z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", asset, err)
		}
		if len(z.File) != 1 || !z.File[0].Mode().IsRegular() || strings.ContainsAny(z.File[0].Name, `/\`) {
			return nil, fmt.Errorf("%s: want exactly one top-level executable", asset)
		}
		r, err := z.File[0].Open()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return readCapped(r, maxExecutable)
	default:
		return nil, fmt.Errorf("%s: unsupported archive", asset)
	}
}

func readCapped(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("content exceeds %d bytes", limit)
	}
	return data, nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fetch-mihomo-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func main() {
	lockPath := flag.String("lock", "packaging/mihomo.lock", "lock file")
	target := flag.String("target", "", "darwin-arm64, darwin-amd64, windows-amd64, or source")
	out := flag.String("out", "", "output file")
	provenance := flag.String("provenance", "", "optional provenance file to write")
	flag.Parse()
	if *target == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*lockPath, *target, *out, *provenance); err != nil {
		fmt.Fprintln(os.Stderr, "fetch-mihomo:", err)
		os.Exit(1)
	}
}

func run(lockPath, target, out, provenance string) error {
	file, err := os.Open(lockPath)
	if err != nil {
		return err
	}
	l, err := parseLock(file)
	file.Close()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	got, err := fetch(ctx, http.DefaultClient, upstream, l, target, out)
	if err != nil {
		return err
	}
	if provenance != "" {
		text := fmt.Sprintf("Mihomo %s (GPL-3.0), unmodified upstream release build.\nAsset: %s\nURL: %s\nArchive SHA-256: %s\nExecutable SHA-256: %s\nSource: %s/archive/refs/tags/%s.tar.gz\n",
			got.Version, got.Asset, got.URL, got.ArchiveSHA256, got.SHA256, upstream, got.Version)
		if err := writeFile(provenance, []byte(text), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("version=%s\nsha256=%s\n", got.Version, got.SHA256)
	return nil
}
