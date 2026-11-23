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

// sidebarEntry is one tappable navigation row: icon and label over an accent
// tint shown while selected.
type sidebarEntry struct {
	widget.BaseWidget
	id       string
	label    *widget.Label
	tint     *canvas.Rectangle
	content  fyne.CanvasObject
	selected bool
	onTap    func(id string)
}

func newSidebarEntry(id string, icon fyne.Resource, onTap func(string)) *sidebarEntry {
	e := &sidebarEntry{id: id, label: widget.NewLabel(localize.T(id)), tint: canvas.NewRectangle(color.Transparent), onTap: onTap}
	e.tint.CornerRadius = theme.Size(theme.SizeNameSelectionRadius)
	e.ExtendBaseWidget(e)
	e.content = container.NewHBox(widget.NewIcon(icon), e.label)
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
}

func (e *sidebarEntry) Tapped(*fyne.PointEvent) {
	e.onTap(e.id)
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
		e := newSidebarEntry(id, icon, onSelect)
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

// topBar shows the page title, the connection text, the service status
// pills, and the service menu.
type topBar struct {
	object      fyne.CanvasObject
	title       *canvas.Text
	mihomo      *statusPill
	systemProxy *statusPill
	tun         *statusPill
	connection  *widget.Label
	menu        *widget.Button
}

func newTopBar(w fyne.Window, connection *widget.Label, items ...*fyne.MenuItem) *topBar {
	t := &topBar{
		title:       canvas.NewText("", theme.Color(theme.ColorNameForeground)),
		mihomo:      pill("Mihomo"),
		systemProxy: pill(localize.T("System proxy")),
		tun:         pill(localize.T("TUN")),
		connection:  connection,
	}
	t.title.TextSize = 26
	t.title.TextStyle.Bold = true
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
	status := container.NewHBox(center(t.mihomo.object), center(t.systemProxy.object), center(t.tun.object), t.menu)
	title := container.New(paintLayout{layout.NewVBoxLayout(), func() {
		t.title.Color = theme.Color(theme.ColorNameForeground)
	}}, t.title)
	row := container.NewBorder(nil, nil, title, status, connection)
	t.object = container.New(layout.NewCustomPaddedLayout(12, 6, 16, 12), row)
	return t
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
}

func (t *topBar) disconnect() {
	t.mihomo.set(pillOff)
	t.systemProxy.set(pillOff)
	t.tun.set(pillOff)
	t.setConnection(localize.T("Disconnected"))
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
