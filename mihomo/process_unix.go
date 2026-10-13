//go:build !windows

package mihomo

import (
	"os"
	"syscall"
)

func terminate(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}
