//go:build linux

package ipc

import (
	"errors"
	"net"

	"github.com/fishman/notmutt/lib/localipc"
)

func peerAuthAvailable() bool { return true }

func checkPeer(conn net.Conn) error {
	if err := localipc.CheckPeer(conn); err != nil {
		return errors.New("ipc: peer authentication failed")
	}
	return nil
}
