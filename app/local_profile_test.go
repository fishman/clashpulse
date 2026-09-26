package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fishman/clashpulse/config"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/subscriptions"
)

func TestLocalProfileFileBoundary(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "my profile.yaml")
	body := []byte("proxies:\n  - name: alpha\n    type: direct\n")
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readLocalProfile(context.Background(), source)
	if err != nil || string(loaded) != string(body) {
		t.Fatalf("read local profile = %q, %v", loaded, err)
	}
	if err := os.WriteFile(source, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if string(loaded) != string(body) {
		t.Fatal("source rewrite changed in-memory profile")
	}

	assertRejected := func(path string) {
		t.Helper()
		_, err := readLocalProfile(context.Background(), path)
		public, ok := core.PublicActivation(err)
		if !ok || public.Stage != core.ActivationFileInput || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "password=private") {
			t.Fatalf("unsafe source error = %v", err)
		}
	}
	link := filepath.Join(dir, "password=private")
	if err := os.Symlink(source, link); err != nil {
		t.Logf("symlink unavailable: %v", err)
	} else {
		assertRejected(link)
	}
	assertRejected(dir)
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	assertRejected(source)
	if err := os.Truncate(source, subscriptions.DefaultMaxProfileBytes+1); err != nil {
		t.Fatal(err)
	}
	assertRejected(source)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	assertCancelled := func() {
		t.Helper()
		_, err := readLocalProfile(cancelled, source)
		public, ok := core.PublicActivation(err)
		if !ok || public.Stage != core.ActivationFileInput {
			t.Fatalf("cancelled file input = %v", err)
		}
	}
	assertCancelled()
}

func localOwnerFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("fake Mihomo needs POSIX shell")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	binary := fakeAppMihomo(t, root, python)
	configDir, stateDir := filepath.Join(root, "config"), filepath.Join(root, "state")
	if err := config.Write(filepath.Join(configDir, "config.toml"), []byte("[mihomo]\nbinary = \""+binary+"\"\n[monitor]\nenabled = false\n")); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(filepath.Join(configDir, "resources.toml"), []byte("# no resources\n")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "my profile.yaml")
	if err := os.WriteFile(path, []byte("proxies:\n  - name: node-a\n    type: direct\nproxy-groups:\n  - name: select-main\n    type: select\n    proxies: [node-a]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return configDir, stateDir, filepath.Join(root, "socket", "service.sock"), path
}

func TestLocalProfileForegroundOwner(t *testing.T) {
	configDir, stateDir, endpoint, path := localOwnerFixture(t)
	if err := config.Write(filepath.Join(stateDir, "generated.yaml"), []byte("durable profile")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready, done := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		done <- RunFileAt(ctx, configDir, stateDir, endpoint, path, func() error { ready <- struct{}{}; return nil })
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("stopped before readiness: %v", err)
	case <-time.After(6 * time.Second):
		t.Fatal("not ready")
	}
	files, err := filepath.Glob(filepath.Join(stateDir, "generated-local-*.yaml"))
	if err != nil || len(files) != 1 {
		t.Fatalf("private local config = %v, %v", files, err)
	}
	if info, err := os.Stat(files[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("local config mode = %v, %v", info, err)
	}
	if body, err := os.ReadFile(filepath.Join(stateDir, "generated.yaml")); err != nil || string(body) != "durable profile" {
		t.Fatalf("durable config changed: %q, %v", body, err)
	}
	if err := RunAt(context.Background(), configDir, stateDir, endpoint); !errors.Is(err, errStateInUse) || strings.Contains(err.Error(), "refreshing") {
		t.Fatalf("second owner = %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("stopped without cancellation: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("shutdown blocked")
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatalf("local config survived shutdown: %v", err)
	}
	if release, err := acquireOwnerLock(stateDir); err != nil {
		t.Fatalf("lock survived shutdown: %v", err)
	} else {
		release()
	}
}

func TestLocalProfileReadinessFailure(t *testing.T) {
	configDir, stateDir, endpoint, path := localOwnerFixture(t)
	err := RunFileAt(context.Background(), configDir, stateDir, endpoint, path, func() error { return os.ErrClosed })
	if public, ok := core.PublicActivation(err); !ok || public.Stage != core.ActivationStateCommit || strings.Contains(err.Error(), path) {
		t.Fatalf("readiness write failure = %v", err)
	}
	if files, _ := filepath.Glob(filepath.Join(stateDir, "generated-local-*.yaml")); len(files) != 0 {
		t.Fatalf("failed readiness left local config: %v", files)
	}
}

func TestLocalProfileOrphanCleanup(t *testing.T) {
	configDir, stateDir, endpoint, path := localOwnerFixture(t)
	orphan := filepath.Join(stateDir, "generated-local-old.yaml")
	if err := config.Write(orphan, []byte("private")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready, done := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		done <- RunFileAt(ctx, configDir, stateDir, endpoint, path, func() error { ready <- struct{}{}; return nil })
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("owner: %v", err)
	case <-time.After(6 * time.Second):
		t.Fatal("not ready")
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan survived startup: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLocalProfileNoGroups(t *testing.T) {
	configDir, stateDir, endpoint, path := localOwnerFixture(t)
	if err := os.WriteFile(path, []byte("proxies:\n  - name: node-a\n    type: direct\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(filepath.Dir(path), "fake.py")
	data, err := os.ReadFile(server)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "self.wfile.write(json.dumps(payload).encode()); return", "self.wfile.write(b'{\"proxies\":{}}'); return", 1))
	if err := os.WriteFile(server, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready, done := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		done <- RunFileAt(ctx, configDir, stateDir, endpoint, path, func() error { ready <- struct{}{}; return nil })
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("no-group profile failed: %v", err)
	case <-time.After(6 * time.Second):
		t.Fatal("not ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLocalProfileUnexpectedChildExit(t *testing.T) {
	configDir, stateDir, endpoint, path := localOwnerFixture(t)
	server := filepath.Join(filepath.Dir(path), "fake.py")
	data, err := os.ReadFile(server)
	if err != nil {
		t.Fatal(err)
	}
	original := "http.server.HTTPServer(('127.0.0.1', int(address.rsplit(':',1)[1])), Handler).serve_forever()"
	data = []byte(strings.Replace(string(data), original, "import os, threading\nthreading.Timer(1, lambda: os._exit(1)).start()\n"+original, 1))
	if err := os.WriteFile(server, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	err = RunFileAt(ctx, configDir, stateDir, endpoint, path, func() error { ready <- struct{}{}; return nil })
	select {
	case <-ready:
	default:
		t.Fatalf("child exited before ready: %v", err)
	}
	if public, ok := core.PublicActivation(err); !ok || public.Stage != core.ActivationProcessStart {
		t.Fatalf("unexpected child exit = %v", err)
	}
}

func TestLocalProfileExitsBeforeReadiness(t *testing.T) {
	configDir, stateDir, endpoint, path := localOwnerFixture(t)
	binary := filepath.Join(filepath.Dir(path), "mihomo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ncase \"$1\" in\n -v) echo 'Mihomo Meta v1.19.31 linux amd64';;\n -t) exit 0;;\n -f) exit 1;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	called := false
	ctx, stop := context.WithTimeout(context.Background(), 7*time.Second)
	defer stop()
	err := RunFileAt(ctx, configDir, stateDir, endpoint, path, func() error { called = true; return nil })
	public, ok := core.PublicActivation(err)
	if called || !ok || public.Stage != core.ActivationControllerReadiness && public.Stage != core.ActivationProcessStart {
		t.Fatalf("early exit called readiness or lost safe stage: %v, called=%t", err, called)
	}
}

func TestLocalProfileSurvivesSourceRemoval(t *testing.T) {
	configDir, stateDir, endpoint, path := localOwnerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready, done := make(chan struct{}, 1), make(chan error, 1)
	go func() {
		done <- RunFileAt(ctx, configDir, stateDir, endpoint, path, func() error { ready <- struct{}{}; return nil })
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("initial start: %v", err)
	case <-time.After(6 * time.Second):
		t.Fatal("not ready")
	}
	request, stop := context.WithTimeout(ctx, time.Second)
	client, err := ipc.Dial(request, endpoint)
	stop()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	state := waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool { return len(state.Groups) == 1 })
	selectionRevision := state.Revision
	request, stop = context.WithTimeout(ctx, time.Second)
	_, err = client.Send(request, ipc.Command{Kind: ipc.CommandSelectGroup, GroupID: opaqueID("select-main"), ChoiceID: opaqueID("node-b")})
	stop()
	if err != nil {
		t.Fatal(err)
	}
	state = waitAppSnapshot(t, ctx, client, func(snapshot core.Snapshot) bool {
		return snapshot.Revision > selectionRevision && len(snapshot.Groups) == 1 && snapshot.Groups[0].Selected == opaqueID("node-b")
	})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	previousRevision := state.Revision
	request, stop = context.WithTimeout(ctx, time.Second)
	_, err = client.Send(request, ipc.Command{Kind: ipc.CommandRestart})
	stop()
	if err != nil {
		t.Fatal(err)
	}
	state = waitAppSnapshot(t, ctx, client, func(state core.Snapshot) bool {
		return state.Revision > previousRevision && len(state.Groups) == 1 && state.Groups[0].Selected == opaqueID("node-b") && len(state.Jobs) == 0
	})
	if len(state.Groups) != 1 || state.ActiveSource != "local" {
		t.Fatal("local profile lost after source removal")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
