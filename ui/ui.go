package ui

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
	"github.com/fishman/clashpulse/monitor"
)

const applicationID = "io.github.fishman.clashpulse"

var viewIDs = []string{"Overview", "Proxies", "Subscriptions", "Filter Lists", "Data Resources", "Settings"}

// Run launches the Fyne desktop interface and connects it to the local service.
// IPC requests and event processing run in background goroutines; the window
// only receives immutable snapshot copies on Fyne's event loop.
func Run(ctx context.Context, endpoint string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	a := app.NewWithID(applicationID)
	a.Settings().SetTheme(newTheme())
	metadata := a.Metadata()
	if metadata.Migrations == nil {
		metadata.Migrations = make(map[string]bool)
	}
	metadata.Migrations["fyneDo"] = true
	app.SetMetadata(metadata)
	w := a.NewWindow("ClashPulse")
	w.Resize(fyne.NewSize(980, 700))

	icon := appIcon()
	a.SetIcon(icon)
	desktopUI := newDesktopUI(runCtx, endpoint, w)
	desktopUI.quit = a.Quit
	tray, hasTray := a.(desktop.App)
	if hasTray {
		desktopUI.tray = tray
		primeTrayTitle(a)
		tray.SetSystemTrayMenu(desktopUI.trayMenu(core.Snapshot{}))
		desktopUI.traySignature = trayStateSignature(core.Snapshot{}, false)
		tray.SetSystemTrayIcon(icon)
		tray.SetSystemTrayWindow(w)
		w.SetCloseIntercept(w.Hide)
	}

	done := make(chan struct{})
	ipcDone := make(chan struct{})
	go func() { defer close(ipcDone); desktopUI.runIPC() }()
	go func() {
		select {
		case <-runCtx.Done():
			select {
			case <-done:
			default:
				<-ipcDone
				fyne.Do(a.Quit)
			}
		case <-done:
		}
	}()

	w.Show()
	a.Run()
	desktopUI.stopped.Store(true)
	close(done)
	cancel()
	<-ipcDone
	return nil
}

type desktopUI struct {
	ctx      context.Context
	endpoint string

	actions       chan ipc.Command
	connected     bool
	current       core.Snapshot
	hasSnapshot   bool
	stopped       atomic.Bool
	tray          desktop.App
	traySignature string
	window        fyne.Window
	quit          func()

	connection *widget.Label
	sidebar    *sidebar
	topBar     *topBar
	viewStack  *fyne.Container
	views      map[string]fyne.CanvasObject

	overview         *overviewView
	overridesRows    *fyne.Container
	overridesSection *fyne.Container
	// overridesFold starts collapsed so the activity log keeps the space.
	overridesFold  *widget.Accordion
	overrideLabels []*widget.Label
	proxyPage      *proxyPage
	subPage        *subscriptionPage
	resourcePage   *resourcePage
	filterPage     *filterPage
	settingsPage   *settingsPage
	activity       *activityView
}

func newDesktopUI(ctx context.Context, endpoint string, w fyne.Window) *desktopUI {
	d := &desktopUI{
		window:     w,
		ctx:        ctx,
		endpoint:   endpoint,
		actions:    make(chan ipc.Command, 32),
		views:      make(map[string]fyne.CanvasObject),
		connection: widget.NewLabel(localize.T("Connecting to local service...")),
	}
	d.overrideLabels = make([]*widget.Label, 14)
	rows := make([]fyne.CanvasObject, len(d.overrideLabels))
	for i := range d.overrideLabels {
		d.overrideLabels[i] = widget.NewLabel("")
		d.overrideLabels[i].Wrapping = fyne.TextWrapWord
		d.overrideLabels[i].Hide()
		rows[i] = d.overrideLabels[i]
	}
	d.overrideLabels[0].SetText(localize.T("No managed overrides"))
	d.overrideLabels[0].Show()
	d.overridesRows = container.NewVBox(rows...)
	d.overridesFold = widget.NewAccordion(widget.NewAccordionItem(localize.T("Generated config changes"), d.overridesRows))
	d.overridesSection = container.NewVBox(d.overridesFold)
	d.proxyPage = newProxyPage(d.enqueue)
	d.subPage = newSubscriptionPage(d.enqueue, w)
	d.resourcePage = newResourcePage(d.enqueue, w)
	d.filterPage = newFilterPage(d.enqueue, w)
	d.settingsPage = newSettingsPage(d.enqueue, w)
	d.activity = newActivityView()

	d.overview = newOverviewView(d.openActivity)
	d.views["Overview"] = d.overview.object
	d.views["Proxies"] = d.proxyPage.view
	d.views["Subscriptions"] = d.subPage.view
	d.views["Filter Lists"] = d.filterPage.view
	d.views["Data Resources"] = d.resourcePage.view
	d.views["Settings"] = d.settingsPage.view
	d.sidebar = newSidebar(d.showView)
	d.topBar = newTopBar(w, d.connection, topBarActions{
		setRunning: func(running bool) {
			kind := ipc.CommandStop
			if running {
				kind = ipc.CommandStart
			}
			d.enqueue(ipc.Command{Kind: kind})
		},
		setSystemProxy: func(enabled bool) {
			d.enqueue(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{SystemProxyEnabled: &enabled}})
		},
		setTun: func(enabled bool) {
			d.enqueue(ipc.Command{Kind: ipc.CommandUpdateConfiguration, Config: &ipc.ConfigPatch{TunEnabled: &enabled}})
		},
	},
		fyne.NewMenuItem(localize.T("Restart service"), func() { d.enqueue(ipc.Command{Kind: ipc.CommandRestart}) }),
		fyne.NewMenuItem(localize.T("Reload configuration"), func() { d.enqueue(ipc.Command{Kind: ipc.CommandReloadConfiguration}) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(localize.T("View activity"), d.openActivity),
	)
	// Every page stays in the window tree so a theme change reaches the hidden
	// ones too; showView only toggles visibility.
	d.viewStack = container.NewStack()
	for _, name := range viewIDs {
		d.viewStack.Add(d.views[name])
	}
	d.showView(viewIDs[0])
	page := container.NewBorder(d.topBar.object, nil, nil, nil, d.viewStack)
	w.SetContent(container.NewBorder(nil, nil, d.sidebar.object, nil, page))
	d.installKeys()
	return d
}

func (d *desktopUI) installKeys() {
	d.window.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyQ, Modifier: fyne.KeyModifierControl}, func(fyne.Shortcut) {
		if d.quit != nil {
			d.quit()
		}
	})
	d.window.Canvas().SetOnTypedKey(func(event *fyne.KeyEvent) {
		if event.Name == fyne.KeyEscape {
			d.dismissTopDialog()
		}
	})
}

func (d *desktopUI) showView(name string) {
	view, ok := d.views[name]
	if !ok {
		return
	}
	d.sidebar.setSelected(name)
	d.topBar.setTitle(localize.T(name))
	for _, other := range d.viewStack.Objects {
		if other != view {
			other.Hide()
		}
	}
	view.Show()
	view.Refresh()
}

func (d *desktopUI) enqueue(command ipc.Command) {
	if !d.connected {
		d.connection.SetText(localize.T("Disconnected"))
		return
	}
	select {
	case d.actions <- command:
	default:
		d.connection.SetText(localize.T("Action queue is busy"))
	}
}

func (d *desktopUI) runIPC() {
	for d.ctx.Err() == nil {
		d.runIPCSession()
		if d.ctx.Err() != nil {
			return
		}
		d.postDisconnected()
		for draining := true; draining; {
			select {
			case <-d.actions:
			default:
				draining = false
			}
		}
		select {
		case <-d.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (d *desktopUI) runIPCSession() {
	client, err := ipc.Dial(d.ctx, d.endpoint)
	if err != nil {
		return
	}
	defer client.Close()
	snapshot, err := client.Snapshot(d.ctx)
	if err != nil {
		return
	}
	d.postSnapshot(snapshot)
	var timer *time.Timer
	var flush <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	var latest core.Snapshot
	for {
		select {
		case <-d.ctx.Done():
			return
		case event, ok := <-client.Events():
			if !ok {
				return
			}
			latest = event.Snapshot
			if flush == nil {
				if timer == nil {
					timer = time.NewTimer(35 * time.Millisecond)
				} else {
					timer.Reset(35 * time.Millisecond)
				}
				flush = timer.C
			}
		case <-flush:
			flush = nil
			d.postSnapshot(latest)
		case command := <-d.actions:
			if _, err := client.Send(d.ctx, command); err != nil {
				if d.ctx.Err() != nil {
					return
				}
				d.postStatus(localize.T("Action could not be queued"))
				continue
			}
			d.postStatus(localize.T("Action queued"))
		}
	}
}

func (d *desktopUI) updateOverrides(changes []core.ConfigOverrideSnapshot) {
	count := 0
	for _, item := range changes {
		if !item.Valid() || count == len(d.overrideLabels) {
			continue
		}
		label := d.overrideLabels[count]
		label.SetText(item.Key + " (" + item.Change + "): " + item.Description())
		label.Show()
		count++
	}
	if count == 0 {
		d.overrideLabels[0].SetText(localize.T("No managed overrides"))
		d.overrideLabels[0].Show()
		count = 1
	}
	for _, label := range d.overrideLabels[count:] {
		label.Hide()
	}
	d.overridesRows.Refresh()
}

func (d *desktopUI) postSnapshot(snapshot core.Snapshot) {
	// IPC clones snapshots before returning or publishing them. Keep that
	// immutable value captured by the Fyne callback rather than cloning again.
	immutable := snapshot
	fyne.Do(func() {
		if d.stopped.Load() {
			return
		}
		previous := d.current
		first := !d.hasSnapshot
		d.connected = true
		d.topBar.update(immutable)
		d.overview.update(buildOverview(immutable))
		binaryChanged := first || !reflect.DeepEqual(previous.Binary, immutable.Binary)
		if first || !reflect.DeepEqual(previous.ConfigOverrides, immutable.ConfigOverrides) {
			d.updateOverrides(immutable.ConfigOverrides)
			if d.activity.dialog != nil {
				d.activity.dialog.Refresh()
			}
		}
		if first || !reflect.DeepEqual(previous.Groups, immutable.Groups) || !reflect.DeepEqual(previous.Proxies, immutable.Proxies) || previous.Monitor != immutable.Monitor {
			d.proxyPage.update(immutable)
		}
		d.proxyPage.setStreamProbe(immutable.Monitor.SwitchPolicy == monitor.SwitchSteadyStream && immutable.AnyGroupAutomated())
		if first || !reflect.DeepEqual(previous.Subscriptions, immutable.Subscriptions) {
			d.subPage.update(immutable.Subscriptions)
		}
		if first || !reflect.DeepEqual(previous.Resources, immutable.Resources) {
			d.resourcePage.update(immutable.Resources)
		}
		if first || !reflect.DeepEqual(previous.Filters, immutable.Filters) {
			d.filterPage.update(immutable.Filters)
		}
		if binaryChanged || previous.Monitor != immutable.Monitor || previous.SystemProxy != immutable.SystemProxy || !reflect.DeepEqual(previous.DNS, immutable.DNS) {
			d.settingsPage.update(immutable.Binary, immutable.Monitor, immutable.SystemProxy, immutable.DNS)
		}
		d.settingsPage.updateTun(immutable.Tun)
		d.settingsPage.updateMixedPort(immutable.MixedPort)
		d.settingsPage.updateProxyMode(immutable.ProxyMode)
		if !reflect.DeepEqual(previous.Diagnostics, immutable.Diagnostics) {
			d.activity.update(immutable.Diagnostics, immutable.Proxies)
		}
		d.current, d.hasSnapshot = immutable, true
		d.updateTray(immutable)
	})
}

func (d *desktopUI) postDisconnected() {
	fyne.Do(func() {
		if d.stopped.Load() {
			return
		}
		d.connected = false
		d.proxyPage.setStreamProbe(false)
		d.topBar.disconnect()
		d.overview.disconnect()
		// The last snapshot keeps the tray sections populated; trayMenu disables
		// what a disconnected UI cannot serve.
		d.updateTray(d.current)
	})
}

func (d *desktopUI) postStatus(status string) {
	fyne.Do(func() {
		if d.stopped.Load() {
			return
		}
		d.topBar.setConnection(status)
	})
}

// serviceError names where a reported issue comes from, then its message.
func serviceError(issue core.ErrorSnapshot) string {
	location := strings.TrimSpace(strings.Join([]string{issue.File, issue.Key}, " "))
	if location == "" {
		location = localize.T("Service")
	}
	if issue.SourceID != "" {
		scope := issue.SourceID
		if issue.Kind != "" {
			scope = issue.Kind + "/" + scope
		}
		location += " [" + scope + "]"
	}
	return location + ": " + issue.Message
}

func binaryFieldValues(binary core.BinarySnapshot) [4]string {
	desired := binary.Desired
	if desired == "" {
		desired = localize.T("unspecified")
	}
	observed := binary.ObservedVersion
	if observed == "" {
		observed = localize.T("unknown")
	}
	capabilities := localize.T("none verified")
	if len(binary.Capabilities) > 0 {
		capabilities = strings.Join(binary.Capabilities, ", ")
	}
	return [4]string{desired, observed, capabilities, compatibilityLabel(binary.LastCompatibilityFailure)}
}
