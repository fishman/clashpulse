//go:build windows

package config

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// IsExecutable reports whether info describes a regular file the OS would run.
// Windows has no execute bit and runs a file by its extension. Only .exe
// qualifies: .bat and .cmd run through cmd.exe, which would reparse argv.
func IsExecutable(path string, info fs.FileInfo) bool {
	return info.Mode().IsRegular() && strings.EqualFold(filepath.Ext(path), ".exe")
}
