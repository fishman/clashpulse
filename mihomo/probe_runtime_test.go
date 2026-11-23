package mihomo

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// connectProxy is an HTTP proxy node that answers every tunnelled request
// with its own name, so a response tells which node carried it.
func connectProxy(t *testing.T, name string) int {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			_, _ = io.WriteString(w, name)
			return
		}
		conn, buffered, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		request, err := http.ReadRequest(bufio.NewReader(buffered))
		if err != nil {
			return
		}
		_ = request.Body.Close()
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(name), name)
	}))
	t.Cleanup(server.Close)
	return server.Listener.Addr().(*net.TCPAddr).Port
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestProbeListenersFollowOnlyTheirOwnGroup(t *testing.T) {
	binary, err := exec.LookPath("mihomo")
	if err != nil {
		t.Skip("mihomo not installed")
	}
	dir := t.TempDir()
	profile := fmt.Sprintf("proxies:\n  - {name: node-a, type: http, server: 127.0.0.1, port: %d}\n  - {name: node-b, type: http, server: 127.0.0.1, port: %d}\nrules:\n  - MATCH,DIRECT\n",
		connectProxy(t, "node-a"), connectProxy(t, "node-b"))
	probes := make([]ProbeListener, ProbeSlots)
	for i := range probes {
		probes[i] = ProbeListener{Port: freePort(t), Username: fmt.Sprintf("user%d", i), Password: fmt.Sprintf("pass%d", i)}
	}
	address := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	settings := ControllerSettings{Address: address, Secret: "runtime-secret", HomeDir: dir, ProxyPort: freePort(t), Probes: probes}
	generated, err := Render([]byte(profile), servicesIntent(true), nil, settings, Capability{SupportsPinnedListener: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, generated, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-d", dir, "-f", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	controller, err := NewController("http://"+address, "runtime-secret", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pool := NewProbePool(controller, probes)
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, _, err := controller.Proxies(ctx); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("controller never became ready: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	through := func(lease *ProbeLease) string {
		client := &http.Client{Transport: lease.Transport(), Timeout: 5 * time.Second}
		response, err := client.Get((&url.URL{Scheme: "http", Host: "192.0.2.10"}).String())
		if err != nil {
			t.Fatalf("request through slot %d: %v", lease.slot, err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return string(body)
	}
	for _, order := range [][2]string{{"node-a", "node-b"}, {"node-b", "node-a"}} {
		first, err := pool.Acquire(ctx, order[0])
		if err != nil {
			t.Fatal(err)
		}
		second, err := pool.Acquire(ctx, order[1])
		if err != nil {
			t.Fatal(err)
		}
		if got := through(first); got != order[0] {
			t.Fatalf("slot %d pinned to %s answered from %q", first.slot, order[0], got)
		}
		if got := through(second); got != order[1] {
			t.Fatalf("slot %d pinned to %s answered from %q", second.slot, order[1], got)
		}
		first.Release()
		second.Release()
	}
}
