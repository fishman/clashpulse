//go:build windows

package resources

import (
	"fmt"
	"io/fs"
	"os"
)

// Legacy 0400 resources carry the Windows read-only attribute. Constrain
// attribute changes to the generation before deleting it.
func prepareLegacyRemoval(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return root.Chmod(name, 0o700)
		case entry.Type().IsRegular():
			return root.Chmod(name, 0o600)
		default:
			return fmt.Errorf("resources: unsafe legacy generation entry")
		}
	})
}
