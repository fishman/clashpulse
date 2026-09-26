package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fishman/clashpulse/examples"
)

// Seed creates missing user configuration files and migrates the pristine old resource seed.
// Existing customized files are never replaced.
func Seed(dir string) error {
	for _, name := range [...]string{"config.toml", "subscriptions.toml", "resources.toml", "filters.toml"} {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); err == nil {
			if name == "resources.toml" {
				if err := migrateDefaultResources(path); err != nil {
					return fmt.Errorf("migrate %s: %w", name, err)
				}
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("seed %s: %w", name, err)
		}
		content, err := examples.Files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
		if err := seedFile(path, content); err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
	}
	return nil
}

// Only the byte-identical pre-enabled seed is eligible for migration.
const disabledResourceSeedHash = "85219c809a212952b0441cb05e4593e8169120cffd75b004203dcf106ab74979"

func migrateDefaultResources(path string) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(current)) != disabledResourceSeedHash {
		return nil
	}
	updated, err := examples.Files.ReadFile("resources.toml")
	if err != nil {
		return err
	}
	return Write(path, updated)
}

func seedFile(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(file.Name(), path); errors.Is(err, os.ErrExist) {
		return nil
	} else if err != nil {
		return err
	}
	return nil
}
