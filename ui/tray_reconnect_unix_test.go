//go:build unix

package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

type recordingTray struct{ menus chan *fyne.Menu }

func (r *recordingTray) SetSystemTrayMenu(menu *fyne.Menu) { r.menus <- menu }
func (*recordingTray) SetSystemTrayIcon(fyne.Resource)     {}
func (*recordingTray) SetSystemTrayWindow(fyne.Window)     {}

func TestTrayReconnectsAndSelectsManagedProxy(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := filepath.Join(t.TempDir(), "socket", "ipc.sock")
	d := newDesktopUI(ctx, endpoint, a.NewWindow("ClashPulse"))
	tray := &recordingTray{menus: make(chan *fyne.Menu, 3)}
	d.tray = tray
	clientDone := make(chan struct{})
	go func() { d.runIPC(); close(clientDone) }()
	defer func() { cancel(); <-clientDone }()

	select {
	case menu := <-tray.menus:
		if len(menu.Items) == 0 || menu.Items[0].Label != "Disconnected - state unavailable" {
			t.Fatalf("initial tray state = %+v", menu)
		}
	case <-time.After(time.Second):
		t.Fatal("desktop did not show the initial IPC outage in the tray")
	}

	commands := make(chan ipc.Command, 1)
	server, err := ipc.NewServer(ipc.ServerOptions{
		Endpoint: endpoint,
		Handler:  func(_ context.Context, command ipc.Command) error { commands <- command; return nil },
		InitialSnapshot: core.Snapshot{
			Groups:  []core.GroupSnapshot{{ID: "managed", Label: "Managed", Type: "Selector", Selected: "alpha", Proxies: []string{"alpha", "beta"}}},
			Proxies: []core.ProxySnapshot{{GroupID: "managed", ID: "alpha", Label: "Alpha"}, {GroupID: "managed", ID: "beta", Label: "Beta"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, stopServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(serverCtx) }()
	defer func() { stopServer(); server.Close(); <-serverDone }()

	var menu *fyne.Menu
	select {
	case menu = <-tray.menus:
	case <-time.After(3 * time.Second):
		t.Fatal("tray remained disconnected after IPC service became available")
	}
	if len(menu.Items) < 3 || menu.Items[2].ChildMenu == nil || len(menu.Items[2].ChildMenu.Items) != 1 {
		t.Fatalf("managed groups missing after reconnect: %+v", menu)
	}
	group := menu.Items[2].ChildMenu.Items[0]
	if group.Label != "Managed" || len(group.ChildMenu.Items) != 2 || !group.ChildMenu.Items[0].Checked {
		t.Fatalf("managed proxy choices missing from tray: %+v", group)
	}
	fyne.DoAndWait(group.ChildMenu.Items[1].Action)
	select {
	case command := <-commands:
		if command.Kind != ipc.CommandSelectGroup || command.GroupID != "managed" || command.ChoiceID != "beta" {
			t.Fatalf("tray selection reached wrong IPC intent: %+v", command)
		}
	case <-time.After(time.Second):
		t.Fatal("tray selection was not sent over IPC")
	}
}
