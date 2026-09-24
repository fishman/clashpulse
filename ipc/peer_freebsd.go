//go:build freebsd

package ipc

import (
	"errors"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func peerAuthAvailable() bool { return true }

func checkPeer(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("ipc: peer authentication failed")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return errors.New("ipc: peer authentication failed")
	}
	var uid uint32
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			socketErr = err
			return
		}
		uid = credentials.Uid
	}); err != nil || socketErr != nil || uid != uint32(os.Getuid()) {
		return errors.New("ipc: peer authentication failed")
	}
	return nil
}
