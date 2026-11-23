package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/localize"
)

// sidebarEntry is one tappable, focusable navigation row: an optional icon and
// a label over an accent tint shown while selected, ringed while focused.
type sidebarEntry struct {
	widget.BaseWidget
	id       string
	label    *widget.Label
	tint     *canvas.Rectangle
	content  fyne.CanvasObject
	selected bool
	focused  bool
	onTap    func(id string)
}

func newSidebarEntry(id, label string, icon fyne.Resource, onTap func(string)) *sidebarEntry {
	e := &sidebarEntry{id: id, label: widget.NewLabel(label), tint: canvas.NewRectangle(color.Transparent), onTap: onTap}
	e.tint.CornerRadius = theme.Size(theme.SizeNameSelectionRadius)
	e.ExtendBaseWidget(e)
	e.content = e.label
	if icon != nil {
		e.content = container.NewHBox(widget.NewIcon(icon), e.label)
	}
	return e
}

func (e *sidebarEntry) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.New(paintLayout{layout.NewStackLayout(), e.paint}, e.tint, e.content))
}

func (e *sidebarEntry) paint() {
	e.tint.FillColor = color.Transparent
	if e.selected {
		accent := color.NRGBAModel.Convert(theme.Color(theme.ColorNamePrimary)).(color.NRGBA)
		accent.A = 0x2E
		e.tint.FillColor = accent
	}
	e.tint.StrokeWidth = 0
	if e.focused {
		e.tint.StrokeWidth = 2
		e.tint.StrokeColor = theme.Color(theme.ColorNameFocus)
	}
}

func (e *sidebarEntry) Tapped(*fyne.PointEvent) {
	e.onTap(e.id)
}

func (e *sidebarEntry) FocusGained() { e.setFocused(true) }

func (e *sidebarEntry) FocusLost() { e.setFocused(false) }

func (e *sidebarEntry) setFocused(focused bool) {
	e.focused = focused
	e.paint()
	e.tint.Refresh()
}

func (e *sidebarEntry) TypedRune(rune) {}

// TypedKey selects the entry on Space or Return, like a tap.
func (e *sidebarEntry) TypedKey(event *fyne.KeyEvent) {
	switch event.Name {
	case fyne.KeySpace, fyne.KeyReturn, fyne.KeyEnter:
		e.onTap(e.id)
	}
}

func (e *sidebarEntry) setSelected(selected bool) {
	if e.selected == selected {
		return
	}
	e.selected = selected
	e.label.TextStyle.Bold = selected
	e.paint()
	e.tint.Refresh()
	e.label.Refresh()
}

// sidebar is the fixed navigation column. Six fixed rows need no
// virtualization, so it is plain containers.
type sidebar struct {
	object  fyne.CanvasObject
	entries []*sidebarEntry
	current string
}

func newSidebar(onSelect func(id string)) *sidebar {
	s := &sidebar{}
	entry := func(id string, icon fyne.Resource) *sidebarEntry {
		e := newSidebarEntry(id, localize.T(id), icon, onSelect)
		s.entries = append(s.entries, e)
		return e
	}
	sources := newMutedText(strings.ToUpper(localize.T("Sources")), sizeNameCaption)
	sources.TextStyle.Bold = true
	top := container.NewVBox(
		entry("Overview", theme.HomeIcon()),
		entry("Proxies", theme.ComputerIcon()),
		container.New(layout.NewCustomPaddedLayout(10, 2, 8, 0), sources),
		entry("Subscriptions", theme.DownloadIcon()),
		entry("Filter Lists", theme.ListIcon()),
		entry("Data Resources", theme.StorageIcon()),
	)
	bottom := entry("Settings", theme.SettingsIcon())
	surface := canvas.NewRectangle(theme.Color(colorNameSidebar))
	width := canvas.NewRectangle(color.Transparent)
	width.SetMinSize(fyne.NewSize(190, 0))
	column := container.New(layout.NewCustomPaddedLayout(10, 10, 8, 8), container.NewBorder(top, bottom, nil, nil))
	s.object = container.New(paintLayout{layout.NewStackLayout(), func() {
		surface.FillColor = theme.Color(colorNameSidebar)
		sources.Color = theme.Color(theme.ColorNamePlaceHolder)
	}}, surface, width, column)
	return s
}

// labels returns the localized entry labels in display order.
func (s *sidebar) labels() []string {
	labels := make([]string, len(s.entries))
	for i, e := range s.entries {
		labels[i] = e.label.Text
	}
	return labels
}

func (s *sidebar) selected() string {
	return s.current
}

func (s *sidebar) setSelected(id string) {
	s.current = id
	for _, e := range s.entries {
		e.setSelected(e.id == id)
	}
}

// powerIcon is Material Design's power_settings_new (Apache 2.0), the icon set
// Fyne's own theme icons come from.
var powerIcon = fyne.NewStaticResource("power.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M13 3h-2v10h2V3zm4.83 2.17l-1.42 1.42C17.99 7.86 19 9.81 19 12c0 3.87-3.13 7-7 7s-7-3.13-7-7c0-2.19 1.01-4.14 2.58-5.42L6.17 5.17C4.23 6.82 3 9.26 3 12c0 4.97 4.03 9 9 9s9-4.03 9-9c0-2.74-1.23-5.18-3.17-6.83z" fill="#000000"/></svg>`))

var (
	powerOnIcon  = theme.NewSuccessThemedResource(powerIcon)
	powerOffIcon = theme.NewColoredResource(powerIcon, theme.ColorNamePlaceHolder)
)

// topBar shows the page title, the connection text, the service status
// pills, the power toggle, and the service menu.
type topBar struct {
	object      fyne.CanvasObject
	title       *canvas.Text
	mihomo      *statusPill
	systemProxy *statusPill
	tun         *statusPill
	connection  *widget.Label
	power       *widget.Button
	running     bool
	menu        *widget.Button
}

// newTopBar builds the bar; setRunning sends the start or stop the power
// button asks for.
func newTopBar(w fyne.Window, connection *widget.Label, setRunning func(bool), items ...*fyne.MenuItem) *topBar {
	t := &topBar{
		title:       canvas.NewText("", theme.Color(theme.ColorNameForeground)),
		mihomo:      pill("Mihomo"),
		systemProxy: pill(localize.T("System proxy")),
		tun:         pill(localize.T("TUN")),
		connection:  connection,
	}
	t.title.TextSize = 26
	t.title.TextStyle.Bold = true
	t.power = widget.NewButtonWithIcon("", powerOffIcon, func() { setRunning(!t.running) })
	t.power.Importance = widget.LowImportance
	t.power.Disable()
	menu := fyne.NewMenu("", items...)
	t.menu = widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), nil)
	t.menu.Importance = widget.LowImportance
	t.menu.OnTapped = func() {
		position := fyne.CurrentApp().Driver().AbsolutePositionForObject(t.menu)
		widget.NewPopUpMenu(menu, w.Canvas()).ShowAtPosition(position.AddXY(0, t.menu.Size().Height))
	}
	// The connection text takes whatever width is left and truncates, so a
	// long status never widens the window.
	connection.Truncation = fyne.TextTruncateEllipsis
	connection.Alignment = fyne.TextAlignTrailing
	status := container.NewHBox(center(t.mihomo.object), center(t.systemProxy.object), center(t.tun.object), t.power, t.menu)
	title := container.New(paintLayout{layout.NewVBoxLayout(), func() {
		t.title.Color = theme.Color(theme.ColorNameForeground)
	}}, t.title)
	row := container.NewBorder(nil, nil, title, status, connection)
	t.object = container.New(layout.NewCustomPaddedLayout(12, 6, 16, 12), row)
	return t
}

// setRunning colors the power icon green while the service runs, so a tap
// stops it; otherwise the icon is muted and a tap starts it.
func (t *topBar) setRunning(running bool) {
	if t.running == running {
		return
	}
	t.running = running
	if running {
		t.power.SetIcon(powerOnIcon)
	} else {
		t.power.SetIcon(powerOffIcon)
	}
}

func center(object fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCenterLayout(), object)
}

func (t *topBar) setTitle(title string) {
	if t.title.Text != title {
		t.title.Text = title
		t.title.Refresh()
	}
}

func (t *topBar) setConnection(text string) {
	if t.connection.Text != text {
		t.connection.SetText(text)
	}
}

func (t *topBar) update(s core.Snapshot) {
	mihomo, systemProxy, tun := pillStates(s)
	t.mihomo.set(mihomo)
	t.systemProxy.set(systemProxy)
	t.tun.set(tun)
	t.setConnection(proxyStatus(s))
	t.setRunning(s.ServiceRunning)
	t.power.Enable()
}

func (t *topBar) disconnect() {
	t.mihomo.set(pillOff)
	t.systemProxy.set(pillOff)
	t.tun.set(pillOff)
	t.setConnection(localize.T("Disconnected"))
	t.setRunning(false)
	t.power.Disable()
}

// pillStates maps a snapshot to the Mihomo, system proxy, and TUN pills.
func pillStates(s core.Snapshot) (mihomo, systemProxy, tun pillState) {
	switch {
	case s.Binary.LastCompatibilityFailure != "":
		mihomo = pillFailed
	case s.ServiceRunning:
		mihomo = pillOn
	}
	switch {
	case s.SystemProxy.Active:
		systemProxy = pillOn
	case s.SystemProxy.Enabled && s.ServiceRunning:
		systemProxy = pillFailed
	}
	switch {
	case s.Tun.LastFailure != "":
		tun = pillFailed
	case s.Tun.Active:
		tun = pillOn
	}
	return mihomo, systemProxy, tun
}
