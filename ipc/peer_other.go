//go:build unix && !linux && !darwin && !freebsd

package ipc

import (
	"errors"
	"net"
)

func peerAuthAvailable() bool { return false }

func checkPeer(net.Conn) error { return errors.New("ipc: peer authentication unavailable") }
