//go:build !unix && !windows

package ipc

import (
	"context"
	"net"
)

func defaultEndpoint() string { return "" }

func normalizeEndpoint(string) (string, error) {
	return "", ErrUnsupportedPlatform
}

func listenLocal(context.Context, string) (net.Listener, error) {
	return nil, ErrUnsupportedPlatform
}

func dialLocal(context.Context, string) (net.Conn, error) {
	return nil, ErrUnsupportedPlatform
}

func checkPeer(net.Conn) error { return ErrUnsupportedPlatform }
