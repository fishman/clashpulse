package app

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/fishman/clashpulse/mihomo"
)

// controllerEndpoint keeps Mihomo's controller on a random loopback port;
// Mihomo offers no Unix-socket controller on Windows.
func controllerEndpoint(_ string, mixedPort int) (address, socket string, err error) {
	for {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", "", fmt.Errorf("clashpulse: reserve controller port: %w", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			return "", "", fmt.Errorf("clashpulse: reserve controller port: %w", err)
		}
		if port != mixedPort {
			return fmt.Sprintf("127.0.0.1:%d", port), "", nil
		}
	}
}

func (s *runtimeService) newController() (*mihomo.Controller, error) {
	return mihomo.NewController("http://"+s.controllerAddress, s.secret, &http.Client{Timeout: time.Second})
}

func (s *runtimeService) clearControllerEndpoint() error { return nil }
