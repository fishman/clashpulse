package tun

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const capNetAdminBit = 1 << 12

func vfsCap(magic, permitted uint32, rootID *uint32) []byte {
	b := binary.LittleEndian.AppendUint32(nil, magic)
	b = binary.LittleEndian.AppendUint32(b, permitted)
	b = binary.LittleEndian.AppendUint32(b, 0) // inheritable low
	b = binary.LittleEndian.AppendUint32(b, 0) // permitted high
	b = binary.LittleEndian.AppendUint32(b, 0) // inheritable high
	if rootID != nil {
		b = binary.LittleEndian.AppendUint32(b, *rootID)
	}
	return b
}

func TestNetAdminFromXattr(t *testing.T) {
	zero, user := uint32(0), uint32(1000)
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"v2 permitted and effective", vfsCap(0x02000001, capNetAdminBit, nil), true},
		{"v2 without effective flag", vfsCap(0x02000000, capNetAdminBit, nil), false},
		{"v2 other capability only", vfsCap(0x02000001, 1<<13, nil), false},
		{"v3 root namespace", vfsCap(0x03000001, capNetAdminBit, &zero), true},
		{"v3 other namespace root", vfsCap(0x03000001, capNetAdminBit, &user), false},
		{"v2 truncated", vfsCap(0x02000001, capNetAdminBit, nil)[:8], false},
		{"unknown revision", vfsCap(0x01000001, capNetAdminBit, nil), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := netAdminFromXattr(tt.data); got != tt.want {
				t.Fatalf("netAdminFromXattr = %v, want %v", got, tt.want)
			}
		})
	}
}

type fixture struct {
	m      *Manager
	source Source
	bin    string
	caps   []byte
	flags  int64
}

// newFixture puts a fake pkexec (running script) and a fake setcap first on
// PATH, and fakes the capability xattr and statfs reads.
func newFixture(t *testing.T, script string) *fixture {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	pkexec := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + filepath.Join(bin, "argv") + "\"\n" + script + "\n"
	writeExec(t, filepath.Join(bin, "pkexec"), pkexec)
	writeExec(t, filepath.Join(bin, "setcap"), "#!/bin/sh\nexit 99\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := filepath.Join(root, "mihomo-source")
	writeExec(t, src, "#!/bin/sh\necho v1.19.31\n")
	f := &fixture{
		m:      NewManager(filepath.Join(root, "state", "tun")),
		source: Source{Path: src, Version: "v1.19.31"},
		bin:    bin,
		caps:   vfsCap(0x02000001, capNetAdminBit, nil),
	}
	f.m.getxattr = func(path, attr string, dest []byte) (int, error) {
		if path != f.m.Path() || attr != "security.capability" {
			t.Fatalf("getxattr(%q, %q)", path, attr)
		}
		if _, err := os.Stat(path); err != nil {
			return 0, err
		}
		if f.caps == nil {
			return 0, unix.ENODATA
		}
		return copy(dest, f.caps), nil
	}
	f.m.statfs = func(path string, st *unix.Statfs_t) error {
		if path != filepath.Dir(f.m.Path()) {
			t.Fatalf("statfs(%q)", path)
		}
		st.Flags = f.flags
		return nil
	}
	return f
}

func writeExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func TestPrepareCopiesRecordsGrantAndRunsSetcap(t *testing.T) {
	f := newFixture(t, "exit 0")
	if err := f.m.Prepare(context.Background(), f.source); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	source, err := os.ReadFile(f.source.Path)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(f.m.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != string(source) {
		t.Fatalf("copy content = %q, want %q", copied, source)
	}
	dir := filepath.Dir(f.m.Path())
	assertMode(t, dir, 0o700)
	assertMode(t, f.m.Path(), 0o700)
	assertMode(t, filepath.Join(dir, "grant.json"), 0o600)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"grant.json", "mihomo"}) {
		t.Fatalf("state entries = %v, want only grant.json and mihomo", names)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "grant.json"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct{ Source, SHA256, Version string }
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(source)
	if g.Source != f.source.Path || g.SHA256 != hex.EncodeToString(sum[:]) || g.Version != f.source.Version {
		t.Fatalf("grant = %+v", g)
	}

	argv, err := os.ReadFile(filepath.Join(f.bin, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(f.bin, "setcap") + "\ncap_net_admin+ep\n" + f.m.Path() + "\n"
	if string(argv) != want {
		t.Fatalf("pkexec argv = %q, want %q", argv, want)
	}
}

func TestPrepareWithoutPkexecLeavesCopyForManualSetcap(t *testing.T) {
	f := newFixture(t, "exit 0")
	if err := os.Remove(filepath.Join(f.bin, "pkexec")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", f.bin)
	if err := f.m.Prepare(context.Background(), f.source); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("Prepare without pkexec = %v, want ErrNoAgent", err)
	}
	for _, path := range []string{f.m.Path(), f.m.grantPath()} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("manual setcap needs %s: %v", path, err)
		}
	}
}

func TestPrepareReplacesExistingCopy(t *testing.T) {
	f := newFixture(t, "exit 0")
	if err := f.m.Prepare(context.Background(), f.source); err != nil {
		t.Fatal(err)
	}
	writeExec(t, f.source.Path, "#!/bin/sh\necho v1.19.32\n")
	if err := f.m.Prepare(context.Background(), f.source); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(f.m.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(copied), "v1.19.32") {
		t.Fatalf("copy not replaced: %q", copied)
	}
}

func TestPrepareOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		script string
		noCaps bool
		want   error
	}{
		{name: "dismissed", script: "exit 126", want: ErrDeclined},
		{name: "no agent", script: "echo 'Error executing command as another user: No authentication agent found.' >&2; exit 127", want: ErrNoAgent},
		// pkexec is gettext-localized; only the C locale yields the matched text.
		{name: "no agent under a translated desktop locale", script: "if [ \"$LC_ALL\" = C ]; then echo 'No authentication agent found.' >&2; else echo 'translated agent error' >&2; fi; exit 127", want: ErrNoAgent},
		{name: "not authorized", script: "echo 'Error executing command as another user: Not authorized' >&2; exit 127", want: ErrDeclined},
		{name: "setcap failed", script: "exit 1"},
		{name: "capability not applied", script: "exit 0", noCaps: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.script)
			if tt.noCaps {
				f.caps = nil
			}
			err := f.m.Prepare(context.Background(), f.source)
			if err == nil {
				t.Fatal("Prepare succeeded")
			}
			for _, sentinel := range []error{ErrDeclined, ErrNoAgent, ErrUnsupported} {
				if errors.Is(err, sentinel) != (sentinel == tt.want) {
					t.Fatalf("Prepare error %v: errors.Is(%v) = %v", err, sentinel, errors.Is(err, sentinel))
				}
			}
			if strings.Contains(err.Error(), "Error executing") {
				t.Fatalf("Prepare error leaks pkexec stderr: %v", err)
			}
		})
	}
}

func TestPrepareTimeoutIsDeclined(t *testing.T) {
	f := newFixture(t, "exec sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := f.m.Prepare(ctx, f.source)
	if !errors.Is(err, ErrDeclined) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Prepare error = %v, want ErrDeclined wrapping deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Prepare returned after %v", elapsed)
	}
}

func TestPrepareRejectsNonExecutableSource(t *testing.T) {
	f := newFixture(t, "exit 0")
	if err := os.Chmod(f.source.Path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Prepare(context.Background(), f.source); err == nil {
		t.Fatal("Prepare accepted a non-executable source")
	}
	if _, err := os.Stat(filepath.Join(f.bin, "argv")); !os.IsNotExist(err) {
		t.Fatalf("pkexec ran for an invalid source: %v", err)
	}
}

func TestCheck(t *testing.T) {
	f := newFixture(t, "exit 0")
	if got := f.m.Check(f.source); got != PrivilegeMissing {
		t.Fatalf("Check before Prepare = %q, want missing", got)
	}
	if err := f.m.Prepare(context.Background(), f.source); err != nil {
		t.Fatal(err)
	}
	if got := f.m.Check(f.source); got != PrivilegeReady {
		t.Fatalf("Check after Prepare = %q, want ready", got)
	}

	f.caps = vfsCap(0x02000000, capNetAdminBit, nil)
	if got := f.m.Check(f.source); got != PrivilegeMissing {
		t.Fatalf("Check without effective capability = %q, want missing", got)
	}
	f.caps = vfsCap(0x02000001, capNetAdminBit, nil)

	if got := f.m.Check(Source{Path: f.source.Path, Version: "v1.19.32"}); got != PrivilegeStale {
		t.Fatalf("Check with new version = %q, want stale", got)
	}

	f.flags = unix.ST_NOSUID
	if got := f.m.Check(f.source); got != PrivilegeNoSUID {
		t.Fatalf("Check on nosuid mount = %q, want nosuid", got)
	}
	f.flags = 0

	writeExec(t, f.source.Path, "#!/bin/sh\necho upgraded\n")
	if got := f.m.Check(f.source); got != PrivilegeStale {
		t.Fatalf("Check after source upgrade = %q, want stale", got)
	}
}

func TestCheckMissingCopy(t *testing.T) {
	f := newFixture(t, "exit 0")
	if err := f.m.Prepare(context.Background(), f.source); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.m.Path()); err != nil {
		t.Fatal(err)
	}
	if got := f.m.Check(f.source); got != PrivilegeMissing {
		t.Fatalf("Check without copy = %q, want missing", got)
	}
}

func TestRevokeRemovesCopyAndGrant(t *testing.T) {
	f := newFixture(t, "exit 0")
	if err := f.m.Prepare(context.Background(), f.source); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.m.Revoke(); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
	}
	for _, path := range []string{f.m.Path(), filepath.Join(filepath.Dir(f.m.Path()), "grant.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still present after Revoke: %v", path, err)
		}
	}
	if got := f.m.Check(f.source); got != PrivilegeMissing {
		t.Fatalf("Check after Revoke = %q, want missing", got)
	}
}
