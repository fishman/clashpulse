package mihomo

import "os"

// os.Process.Signal on Windows delivers only Kill.
func terminate(process *os.Process) error {
	return process.Kill()
}
