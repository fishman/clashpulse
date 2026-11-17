//go:build !windows

package config

import "io/fs"

// IsExecutable reports whether info describes a regular file the OS would run.
func IsExecutable(_ string, info fs.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
