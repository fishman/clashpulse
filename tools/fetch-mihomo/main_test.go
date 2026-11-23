package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

func zipped(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte("exe " + name))
	}
	w.Close()
	return buf.Bytes()
}

// serve publishes assets at the upstream release and archive paths.
func serve(t *testing.T, assets map[string][]byte) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		data, ok := assets[name]
		if !ok || !(strings.HasPrefix(r.URL.Path, "/releases/download/v9.9.9/") || strings.HasPrefix(r.URL.Path, "/archive/refs/tags/")) {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func lockFor(lines ...string) lock {
	parsed, err := parseLock(strings.NewReader("version v9.9.9\n" + strings.Join(lines, "\n") + "\n"))
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestFetchExtractsGzipAndZipExecutables(t *testing.T) {
	exe := []byte("darwin exe")
	gz := gzipped(t, exe)
	zp := zipped(t, "mihomo-windows-amd64-v1.exe")
	base := serve(t, map[string][]byte{"m.gz": gz, "m.zip": zp})
	l := lockFor("darwin-arm64 m.gz "+digest(gz), "windows-amd64 m.zip "+digest(zp))
	dir := t.TempDir()

	out := filepath.Join(dir, "mihomo")
	got, err := fetch(context.Background(), http.DefaultClient, base, l, "darwin-arm64", out)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); !bytes.Equal(data, exe) || got.SHA256 != digest(exe) || got.Version != "v9.9.9" {
		t.Fatalf("gz result %+v, file %q", got, data)
	}

	out = filepath.Join(dir, "mihomo.exe")
	got, err = fetch(context.Background(), http.DefaultClient, base, l, "windows-amd64", out)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); string(data) != "exe mihomo-windows-amd64-v1.exe" || got.SHA256 != digest(data) {
		t.Fatalf("zip result %+v, file %q", got, data)
	}
}

func TestFetchRejectsMismatchAndAmbiguousArchives(t *testing.T) {
	gz := gzipped(t, []byte("exe"))
	two := zipped(t, "a.exe", "b.exe")
	base := serve(t, map[string][]byte{"m.gz": gz, "two.zip": two})
	l := lockFor("darwin-arm64 m.gz "+strings.Repeat("0", 64), "windows-amd64 two.zip "+digest(two))
	dir := t.TempDir()
	for target, want := range map[string]string{"darwin-arm64": "checksum", "windows-amd64": "exactly one", "linux-amd64": "unknown target"} {
		out := filepath.Join(dir, target)
		if _, err := fetch(context.Background(), http.DefaultClient, base, l, target, out); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: error %v, want %q", target, err, want)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("%s: failed fetch left output", target)
		}
	}
}

func TestFetchSourceKeepsArchive(t *testing.T) {
	archive := []byte("source tarball")
	base := serve(t, map[string][]byte{"v9.9.9.tar.gz": archive})
	l := lockFor("source v9.9.9.tar.gz " + digest(archive))
	out := filepath.Join(t.TempDir(), "src.tar.gz")
	if _, err := fetch(context.Background(), http.DefaultClient, base, l, "source", out); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); !bytes.Equal(data, archive) {
		t.Fatalf("source = %q", data)
	}
}

func TestParseLockRejectsMalformedInput(t *testing.T) {
	sum := strings.Repeat("a", 64)
	for name, text := range map[string]string{
		"no version": "source v.tar.gz " + sum + "\n",
		"duplicate":  "version v1\nsource a " + sum + "\nsource b " + sum + "\n",
		"short sum":  "version v1\nsource a abc\n",
		"path asset": "version v1\nsource ../a " + sum + "\n",
	} {
		if _, err := parseLock(strings.NewReader(text)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
