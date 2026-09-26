package ui

import (
	"context"
	"reflect"
	"strconv"
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
)

const applicationID = "io.github.fishman.clashpulse"

var viewNames = []string{"Overview", "Proxies", "Subscriptions", "Filter Lists", "Data Resources", "Settings"}

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

	// Fyne's default theme follows the operating system, including live desktop
	// appearance changes. Do not set a fixed light or dark variant here.
	a := app.NewWithID(applicationID)
	w := a.NewWindow("ClashPulse")
	w.Resize(fyne.NewSize(920, 640))

	icon := appIcon()
	a.SetIcon(icon)
	desktopUI := newDesktopUI(runCtx, endpoint, w)
	desktopUI.quit = a.Quit
	tray, hasTray := a.(desktop.App)
	if hasTray {
		desktopUI.tray = tray
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
			fyne.Do(a.Quit)
		case <-done:
		}
	}()

	w.Show()
	a.Run()
	desktopUI.stopped.Store(true)
	cancel()
	<-ipcDone
	close(done)
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
	viewSelect *widget.Select
	viewStack  *fyne.Container
	views      map[string]fyne.CanvasObject

	overviewCounts      *fyne.Container
	overviewCountValues []*widget.Label
	binarySummary       *widget.Form
	binaryValues        [4]*widget.Label
	overridesRows       *fyne.Container
	overridesSection    *fyne.Container
	overrideLabels      []*widget.Label
	switchSummary       *widget.Label
	errorSummary        *widget.Label
	proxyPage           *proxyPage
	subPage             *subscriptionPage
	resourcePage        *resourcePage
	filterPage          *filterPage
	settingsPage        *settingsPage
	activity            *activityView
}

func newDesktopUI(ctx context.Context, endpoint string, w fyne.Window) *desktopUI {
	d := &desktopUI{
		window:     w,
		ctx:        ctx,
		endpoint:   endpoint,
		actions:    make(chan ipc.Command, 32),
		views:      make(map[string]fyne.CanvasObject),
		connection: widget.NewLabel("Connecting to local service..."),
	}
	d.overviewCountValues = make([]*widget.Label, 0, 6)
	countCells := make([]fyne.CanvasObject, 0, 6)
	for _, label := range []string{"Proxy groups", "Subscriptions", "Data resources", "Filter lists", "Jobs", "Reported issues"} {
		value := widget.NewLabel("0")
		d.overviewCountValues = append(d.overviewCountValues, value)
		countCells = append(countCells, container.NewVBox(widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), value))
	}
	d.overviewCounts = container.NewGridWithColumns(3, countCells...)
	d.binarySummary, d.binaryValues = newBinaryForm()
	d.overrideLabels = make([]*widget.Label, 14)
	rows := make([]fyne.CanvasObject, len(d.overrideLabels))
	for i := range d.overrideLabels {
		d.overrideLabels[i] = widget.NewLabel("")
		d.overrideLabels[i].Wrapping = fyne.TextWrapWord
		d.overrideLabels[i].Hide()
		rows[i] = d.overrideLabels[i]
	}
	d.overrideLabels[0].SetText("No managed overrides")
	d.overrideLabels[0].Show()
	d.overridesRows = container.NewVBox(rows...)
	d.overridesSection = container.NewVBox(widget.NewLabelWithStyle("Generated config changes", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), d.overridesRows)
	d.switchSummary = widget.NewLabel("No automatic switches recorded")
	d.switchSummary.Wrapping = fyne.TextWrapWord
	d.errorSummary = widget.NewLabel("No service errors")
	d.errorSummary.Wrapping = fyne.TextWrapWord
	d.proxyPage = newProxyPage(d.enqueue)
	d.subPage = newSubscriptionPage(d.enqueue, w)
	d.resourcePage = newResourcePage(d.enqueue, w)
	d.filterPage = newFilterPage(d.enqueue, w)
	d.settingsPage = newSettingsPage(d.enqueue, w)
	d.activity = newActivityView()

	d.views["Overview"] = d.overviewView()
	d.views["Proxies"] = d.proxyPage.view
	d.views["Subscriptions"] = d.subPage.view
	d.views["Filter Lists"] = d.filterPage.view
	d.views["Data Resources"] = d.resourcePage.view
	d.views["Settings"] = d.settingsPage.view

	d.viewSelect = widget.NewSelect(viewNames, d.showView)
	d.viewSelect.Selected = viewNames[0]
	d.viewSelect.PlaceHolder = "Choose a view"
	d.viewSelect.Refresh()
	d.viewStack = container.NewStack(d.views[viewNames[0]])
	header := container.NewBorder(nil, nil, nil, d.connection, d.viewSelect)
	w.SetContent(container.NewBorder(header, nil, nil, nil, d.viewStack))
	d.installKeys()
	return d
}

// installKeys binds the window keys Fyne leaves free: Control+Q quits and Escape
// dismisses the top dialog.
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

func (d *desktopUI) overviewView() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Service overview", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	controls := container.NewGridWithColumns(2,
		widget.NewButton("Start service", func() { d.enqueue(ipc.Command{Kind: ipc.CommandStart}) }),
		widget.NewButton("Stop service", func() { d.enqueue(ipc.Command{Kind: ipc.CommandStop}) }),
		widget.NewButton("Restart service", func() { d.enqueue(ipc.Command{Kind: ipc.CommandRestart}) }),
		widget.NewButton("Reload configuration", func() { d.enqueue(ipc.Command{Kind: ipc.CommandReloadConfiguration}) }),
		widget.NewButton("View activity", d.openActivity),
	)
	return container.NewVScroll(container.NewVBox(title, d.overviewCounts, widget.NewLabelWithStyle("Mihomo binary", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), d.binarySummary, d.switchSummary, d.errorSummary, controls))
}

func (d *desktopUI) showView(name string) {
	view, ok := d.views[name]
	if !ok {
		return
	}
	if d.viewSelect.Selected != name {
		d.viewSelect.Selected = name
		d.viewSelect.Refresh()
	}
	d.viewStack.Objects = []fyne.CanvasObject{view}
	d.viewStack.Refresh()
}

func (d *desktopUI) enqueue(command ipc.Command) {
	if !d.connected {
		d.connection.SetText("Disconnected")
		return
	}
	select {
	case d.actions <- command:
	default:
		d.connection.SetText("Action queue is busy")
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
				d.postStatus("Action could not be queued")
				continue
			}
			d.postStatus("Action queued")
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
		d.overrideLabels[0].SetText("No managed overrides")
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
		status := "Connected"
		if immutable.ActiveSource == "local" {
			status = "Connected - local profile"
		}
		if d.connection.Text != status {
			d.connection.SetText(status)
		}
		counts := []string{count(immutable.Groups), count(immutable.Subscriptions), count(immutable.Resources), count(immutable.Filters), count(immutable.Jobs), count(immutable.Errors)}
		for i, value := range counts {
			if d.overviewCountValues[i].Text != value {
				d.overviewCountValues[i].SetText(value)
			}
		}
		binaryChanged := first || !reflect.DeepEqual(previous.Binary, immutable.Binary)
		if binaryChanged {
			for i, value := range binaryFieldValues(immutable.Binary) {
				if d.binaryValues[i].Text != value {
					d.binaryValues[i].SetText(value)
				}
			}
		}
		if first || !reflect.DeepEqual(previous.ConfigOverrides, immutable.ConfigOverrides) {
			d.updateOverrides(immutable.ConfigOverrides)
			if d.activity.dialog != nil {
				d.activity.dialog.Refresh()
			}
		}
		if first || !reflect.DeepEqual(previous.Switches, immutable.Switches) {
			d.switchSummary.SetText(lastSwitchSummary(immutable))
		}
		if first || !reflect.DeepEqual(previous.Errors, immutable.Errors) {
			d.errorSummary.SetText(serviceErrors(immutable.Errors))
		}
		if first || !reflect.DeepEqual(previous.Groups, immutable.Groups) || !reflect.DeepEqual(previous.Proxies, immutable.Proxies) {
			d.proxyPage.update(immutable)
		}
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
		if !reflect.DeepEqual(previous.Diagnostics, immutable.Diagnostics) {
			d.activity.update(immutable.Diagnostics)
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
		d.connection.SetText("Disconnected")
		d.updateTray(core.Snapshot{})
	})
}

func (d *desktopUI) postStatus(status string) {
	fyne.Do(func() {
		if d.stopped.Load() {
			return
		}
		d.connection.SetText(status)
	})
}

func serviceErrors(issues []core.ErrorSnapshot) string {
	if len(issues) == 0 {
		return "No service errors"
	}
	lines := make([]string, 0, len(issues))
	for _, issue := range issues {
		location := strings.TrimSpace(strings.Join([]string{issue.File, issue.Key}, " "))
		if location == "" {
			location = "Service"
		}
		if issue.SourceID != "" {
			scope := issue.SourceID
			if issue.Kind != "" {
				scope = issue.Kind + "/" + scope
			}
			location += " [" + scope + "]"
		}
		lines = append(lines, location+": "+issue.Message)
	}
	return strings.Join(lines, "\n")
}

func binaryFieldValues(binary core.BinarySnapshot) [4]string {
	desired := binary.Desired
	if desired == "" {
		desired = "unspecified"
	}
	observed := binary.ObservedVersion
	if observed == "" {
		observed = "unknown"
	}
	capabilities := "none verified"
	if len(binary.Capabilities) > 0 {
		capabilities = strings.Join(binary.Capabilities, ", ")
	}
	return [4]string{desired, observed, capabilities, compatibilityLabel(binary.LastCompatibilityFailure)}
}

func newBinaryForm() (*widget.Form, [4]*widget.Label) {
	var values [4]*widget.Label
	initial := binaryFieldValues(core.BinarySnapshot{})
	items := make([]*widget.FormItem, 0, len(values))
	for i, label := range []string{"Desired", "Observed", "Capabilities", "Compatibility"} {
		value := widget.NewLabel(initial[i])
		value.Wrapping = fyne.TextWrapWord
		values[i] = value
		items = append(items, widget.NewFormItem(label, value))
	}
	return widget.NewForm(items...), values
}

func count[T any](items []T) string {
	return strconv.Itoa(len(items))
}
