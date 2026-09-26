//go:build windows

package app

import (
	"os"
	"syscall"
)

func openLocalProfile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|int(syscall.FILE_FLAG_OPEN_REPARSE_POINT), 0)
}
