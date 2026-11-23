package mihomo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fishman/clashpulse/internal/bundled"
)

var (
	errBundledMissing  = errors.New("bundled Mihomo is not installed")
	errBundledMismatch = errors.New("bundled Mihomo failed verification")
)

// InstallBundled verifies the Mihomo executable shipped next to ClashPulse
// against the build's recorded digest and returns its private copy under
// stateDir. Running the copy keeps Mihomo independent of the package, which
// macOS may translocate and the user may replace while ClashPulse runs.
func InstallBundled(stateDir string) (string, error) {
	if !bundled.Available() {
		return "", errBundledMissing
	}
	exe, err := os.Executable()
	if err == nil {
		// A symlinked command (for example on PATH) must still find the package.
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %v", errBundledMissing, err)
	}
	return installBundled(packagedPath(runtime.GOOS, exe), filepath.Join(stateDir, "mihomo"), bundled.Version, bundled.SHA256)
}

// PruneBundled removes private copies of other bundled versions. A copy that
// cannot be removed, such as a running executable on Windows, stays until a
// later start.
func PruneBundled(stateDir, keep string) {
	root := filepath.Join(stateDir, "mihomo")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	active := filepath.Base(filepath.Dir(keep))
	for _, entry := range entries {
		if entry.Name() != active {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}

func packagedPath(goos, exe string) string {
	dir := filepath.Dir(exe)
	switch goos {
	case "darwin":
		return filepath.Join(dir, "..", "Resources", "mihomo")
	case "windows":
		return filepath.Join(dir, "mihomo.exe")
	default:
		return filepath.Join(dir, "mihomo")
	}
}

func installBundled(packaged, root, version, sum string) (string, error) {
	if len(sum) < 12 {
		return "", errBundledMismatch
	}
	dir := filepath.Join(root, version+"-"+sum[:12])
	for _, path := range []string{root, dir} {
		if err := privateDir(path); err != nil {
			return "", fmt.Errorf("install bundled Mihomo: %w", err)
		}
	}
	// The verified copy is enough on its own: the package is read only when the
	// copy is missing or altered, so a moved or deleted package keeps working.
	dest := filepath.Join(dir, filepath.Base(packaged))
	if existing, err := fileSHA256(dest); err == nil && existing == sum {
		return dest, nil
	}
	source, err := os.Open(packaged)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errBundledMissing, err)
	}
	defer source.Close()
	tmp, err := os.CreateTemp(dir, ".mihomo-*")
	if err != nil {
		return "", fmt.Errorf("install bundled Mihomo: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, digest), source); err != nil {
		return "", fmt.Errorf("install bundled Mihomo: %w", err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != sum {
		return "", errBundledMismatch
	}
	if err := tmp.Chmod(0o700); err != nil {
		return "", fmt.Errorf("install bundled Mihomo: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("install bundled Mihomo: %w", err)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", fmt.Errorf("install bundled Mihomo: %w", err)
	}
	return dest, nil
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", path)
	}
	return os.Chmod(path, 0o700)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
