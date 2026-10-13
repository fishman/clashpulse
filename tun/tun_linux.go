//go:build linux

package tun

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Manager owns the privileged copy under one private directory. getxattr and
// statfs are the kernel reads Check depends on; tests replace them.
type Manager struct {
	dir      string
	getxattr func(path, attr string, dest []byte) (int, error)
	statfs   func(path string, st *unix.Statfs_t) error
}

// NewManager creates a manager for the privileged copy under dir.
func NewManager(dir string) *Manager {
	return &Manager{dir: dir, getxattr: unix.Getxattr, statfs: unix.Statfs}
}

type grant struct {
	Source  string `json:"source"`
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}

// Check reports whether the privileged copy may run for source.
func (m *Manager) Check(source Source) Privilege {
	raw, err := os.ReadFile(m.grantPath())
	if err != nil {
		return PrivilegeMissing
	}
	var g grant
	if err := json.Unmarshal(raw, &g); err != nil {
		return PrivilegeMissing
	}
	if ok, err := m.hasNetAdmin(); err != nil || !ok {
		return PrivilegeMissing
	}
	if sum, err := fileSHA256(source.Path); err != nil || sum != g.SHA256 || source.Version != g.Version {
		return PrivilegeStale
	}
	// File capabilities are ignored on a nosuid mount.
	var st unix.Statfs_t
	if err := m.statfs(m.dir, &st); err != nil {
		return PrivilegeMissing
	}
	if st.Flags&unix.ST_NOSUID != 0 {
		return PrivilegeNoSUID
	}
	return PrivilegeReady
}

// Prepare copies source to the private location, records its identity, and
// asks polkit to grant cap_net_admin to the copy. ctx bounds the dialog;
// expiry or cancellation is ErrDeclined.
func (m *Manager) Prepare(ctx context.Context, source Source) error {
	info, err := os.Stat(source.Path)
	if err != nil {
		return fmt.Errorf("tun: inspect source binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("tun: source binary %s is not a regular executable", source.Path)
	}
	setcap, err := exec.LookPath("setcap")
	if err != nil {
		return fmt.Errorf("tun: find setcap: %w", err)
	}
	if setcap, err = filepath.Abs(setcap); err != nil {
		return fmt.Errorf("tun: find setcap: %w", err)
	}

	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return fmt.Errorf("tun: create state directory: %w", err)
	}
	if err := os.Chmod(m.dir, 0o700); err != nil {
		return fmt.Errorf("tun: secure state directory: %w", err)
	}
	in, err := os.Open(source.Path)
	if err != nil {
		return fmt.Errorf("tun: open source binary: %w", err)
	}
	defer in.Close()
	hash := sha256.New()
	if err := m.writeAtomic(m.Path(), 0o700, func(w io.Writer) error {
		_, err := io.Copy(io.MultiWriter(w, hash), in)
		return err
	}); err != nil {
		return fmt.Errorf("tun: copy source binary: %w", err)
	}
	record, err := json.Marshal(grant{Source: source.Path, SHA256: hex.EncodeToString(hash.Sum(nil)), Version: source.Version})
	if err != nil {
		return fmt.Errorf("tun: encode grant: %w", err)
	}
	if err := m.writeAtomic(m.grantPath(), 0o600, func(w io.Writer) error {
		_, err := w.Write(record)
		return err
	}); err != nil {
		return fmt.Errorf("tun: write grant: %w", err)
	}

	// Without polkit the copy and grant stay in place so the user can apply
	// the manual setcap command to them, exactly as with a missing agent.
	pkexec, err := exec.LookPath("pkexec")
	if err != nil {
		return ErrNoAgent
	}
	// stderr is read only to classify the failure; it is never logged. pkexec
	// translates it, so classify under the C locale; the polkit agent shows the
	// dialog in the desktop's own language regardless.
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, pkexec, setcap, "cap_net_admin+ep", m.Path())
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=")
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ErrDeclined, ctxErr)
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 126:
		return ErrDeclined
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 127:
		// pkexec uses 127 for both "not authorized" and agent errors.
		if bytes.Contains(stderr.Bytes(), []byte("No authentication agent found")) {
			return ErrNoAgent
		}
		return ErrDeclined
	case errors.As(err, &exitErr):
		return fmt.Errorf("tun: pkexec setcap exited with status %d", exitErr.ExitCode())
	default:
		return fmt.Errorf("tun: run pkexec: %w", err)
	}

	ok, err := m.hasNetAdmin()
	if err != nil {
		return fmt.Errorf("tun: verify capability: %w", err)
	}
	if !ok {
		return errors.New("tun: setcap did not grant cap_net_admin")
	}
	return nil
}

// writeAtomic writes path through a temporary file in the same directory, so
// a partial write is never visible under the final name.
func (m *Manager) writeAtomic(path string, mode os.FileMode, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(m.dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m *Manager) hasNetAdmin() (bool, error) {
	buf := make([]byte, vfsCapSizeV3)
	n, err := m.getxattr(m.Path(), "security.capability", buf)
	if errors.Is(err, unix.ENODATA) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return netAdminFromXattr(buf[:n]), nil
}

// vfs_cap_data layout from linux/capability.h: little-endian magic_etc, then
// {permitted, inheritable} for the low and high capability words, then rootid
// in revision 3.
const (
	vfsCapRevisionMask = 0xff000000
	vfsCapRevision2    = 0x02000000
	vfsCapRevision3    = 0x03000000
	vfsCapEffective    = 0x000001
	vfsCapSizeV2       = 20
	vfsCapSizeV3       = 24
	capNetAdmin        = 12
)

// netAdminFromXattr reports whether a security.capability value puts
// cap_net_admin in the permitted set and raises it into the effective set.
func netAdminFromXattr(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	magic := binary.LittleEndian.Uint32(b)
	switch magic & vfsCapRevisionMask {
	case vfsCapRevision2:
		if len(b) != vfsCapSizeV2 {
			return false
		}
	case vfsCapRevision3:
		// A nonzero rootid scopes the capability to another user namespace.
		if len(b) != vfsCapSizeV3 || binary.LittleEndian.Uint32(b[20:]) != 0 {
			return false
		}
	default:
		return false
	}
	permitted := binary.LittleEndian.Uint32(b[4:])
	return magic&vfsCapEffective != 0 && permitted&(1<<capNetAdmin) != 0
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
