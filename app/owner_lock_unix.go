//go:build !windows

package app

import (
	"errors"
	"os"
	"syscall"
)

func openOwnerLock(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	fail := func(err error) (*os.File, error) { _ = file.Close(); return nil, err }
	var info syscall.Stat_t
	if err := syscall.Fstat(fd, &info); err != nil {
		return fail(err)
	}
	if info.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return fail(errors.New("state lock is not a regular file"))
	}
	if err := syscall.Fchmod(fd, 0o600); err != nil {
		return fail(err)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return fail(errStateInUse)
		}
		return fail(err)
	}
	return file, nil
}
