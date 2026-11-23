package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// paintLayout wraps a layout and re-applies theme colors whenever Fyne lays
// the container out. Fyne lays out every container on a theme change, so the
// canvas objects below follow live light/dark switches without a listener.
type paintLayout struct {
	inner fyne.Layout
	paint func()
}

func (p paintLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	p.paint()
	p.inner.Layout(objects, size)
}

func (p paintLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return p.inner.MinSize(objects)
}

func newMutedText(text string, sizeName fyne.ThemeSizeName) *canvas.Text {
	value := canvas.NewText(text, theme.Color(theme.ColorNamePlaceHolder))
	value.TextSize = theme.Size(sizeName)
	return value
}

func newSurface(colorName fyne.ThemeColorName) *canvas.Rectangle {
	surface := canvas.NewRectangle(theme.Color(colorName))
	surface.StrokeColor = theme.Color(theme.ColorNameSeparator)
	surface.StrokeWidth = 1
	surface.CornerRadius = theme.Size(sizeNameCardRadius)
	return surface
}

// newCard puts a muted uppercase caption and the body on a rounded card
// surface. The caption, values, and meters are inset by the inner padding so
// they line up with label text.
func newCard(caption string, body ...fyne.CanvasObject) *fyne.Container {
	card, _ := newTitledCard(caption, body...)
	return card
}

// newTitledCard is newCard that also returns the caption for later updates
// through setCaption.
func newTitledCard(caption string, body ...fyne.CanvasObject) (*fyne.Container, *canvas.Text) {
	surface := newSurface(colorNameCard)
	title := newMutedText(strings.ToUpper(caption), sizeNameCaption)
	title.TextStyle.Bold = true
	heading := container.New(layout.NewCustomPaddedLayout(0, 2, theme.InnerPadding(), 0), title)
	content := container.New(layout.NewCustomPaddedVBoxLayout(0), append([]fyne.CanvasObject{heading}, body...)...)
	inset := container.New(layout.NewCustomPaddedLayout(10, 10, 4, 4), content)
	return container.New(paintLayout{layout.NewStackLayout(), func() {
		surface.FillColor = theme.Color(colorNameCard)
		surface.StrokeColor = theme.Color(theme.ColorNameSeparator)
		title.Color = theme.Color(theme.ColorNamePlaceHolder)
	}}, surface, inset), title
}

func setCaption(title *canvas.Text, caption string) {
	if caption = strings.ToUpper(caption); title.Text != caption {
		title.Text = caption
		title.Refresh()
	}
}

// newValue shows a large number with a smaller muted unit on its baseline.
func newValue(value, unit string) (*fyne.Container, *canvas.Text, *canvas.Text) {
	number := canvas.NewText(value, theme.Color(theme.ColorNameForeground))
	number.TextSize = theme.Size(sizeNameValue)
	number.TextStyle.Bold = true
	suffix := newMutedText(unit, theme.SizeNameText)
	return container.New(paintLayout{valueLayout{}, func() {
		number.Color = theme.Color(theme.ColorNameForeground)
		suffix.Color = theme.Color(theme.ColorNamePlaceHolder)
	}}, number, suffix), number, suffix
}

// valueLayout places the unit after the number, bottom-aligned.
type valueLayout struct{}

func (valueLayout) Layout(objects []fyne.CanvasObject, _ fyne.Size) {
	number, unit := objects[0].MinSize(), objects[1].MinSize()
	objects[0].Resize(number)
	objects[0].Move(fyne.NewPos(theme.InnerPadding(), 0))
	objects[1].Resize(unit)
	objects[1].Move(fyne.NewPos(theme.InnerPadding()+number.Width+theme.Padding(), number.Height-unit.Height-theme.Padding()))
}

func (valueLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	number, unit := objects[0].MinSize(), objects[1].MinSize()
	return fyne.NewSize(theme.InnerPadding()+number.Width+theme.Padding()+unit.Width, number.Height)
}

// meter is an 8 px rounded bar filled to a fraction.
type meter struct {
	object   *fyne.Container
	track    *canvas.Rectangle
	fill     *canvas.Rectangle
	fraction float64
	color    fyne.ThemeColorName
}

func newMeter() *meter {
	m := &meter{track: canvas.NewRectangle(theme.Color(theme.ColorNameSeparator)), color: theme.ColorNamePrimary}
	m.fill = canvas.NewRectangle(theme.Color(m.color))
	m.track.CornerRadius, m.fill.CornerRadius = 4, 4
	m.object = container.New(paintLayout{meterLayout{m}, func() {
		m.track.FillColor = theme.Color(theme.ColorNameSeparator)
		m.fill.FillColor = theme.Color(m.color)
	}}, m.track, m.fill)
	return m
}

func (m *meter) set(fraction float64, color fyne.ThemeColorName) {
	fraction = min(max(fraction, 0), 1)
	if fraction == m.fraction && color == m.color {
		return
	}
	m.fraction, m.color = fraction, color
	m.fill.FillColor = theme.Color(color)
	meterLayout{m}.Layout(m.object.Objects, m.object.Size())
	m.fill.Refresh()
}

type meterLayout struct{ m *meter }

func (l meterLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	inset := theme.InnerPadding()
	width := max(size.Width-2*inset, 0)
	l.m.track.Move(fyne.NewPos(inset, 0))
	l.m.track.Resize(fyne.NewSize(width, size.Height))
	l.m.fill.Move(fyne.NewPos(inset, 0))
	l.m.fill.Resize(fyne.NewSize(width*float32(l.m.fraction), size.Height))
}

func (meterLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(40, 8)
}

type pillState int

const (
	pillOff pillState = iota
	pillOn
	pillFailed
	// pillArmed is a setting that is on but waits for the service to start.
	pillArmed
)

func (s pillState) color() fyne.ThemeColorName {
	switch s {
	case pillOn:
		return theme.ColorNameSuccess
	case pillFailed:
		return theme.ColorNameError
	case pillArmed:
		return theme.ColorNamePrimary
	}
	return theme.ColorNamePlaceHolder
}

// tapArea makes content clickable with a pointer cursor, without button
// chrome: a whole card, or a status pill that toggles its setting.
type tapArea struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
}

func newTapArea(content fyne.CanvasObject, onTap func()) *tapArea {
	t := &tapArea{content: content, onTap: onTap}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tapArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.content)
}

func (t *tapArea) Tapped(*fyne.PointEvent) { t.onTap() }

func (t *tapArea) Cursor() desktop.Cursor { return desktop.PointerCursor }

// statusPill is a dot and a label on a rounded surface.
type statusPill struct {
	object fyne.CanvasObject
	dot    *dot
	text   *canvas.Text
	state  pillState
}

func pill(label string) *statusPill {
	p := &statusPill{dot: newDot(), text: canvas.NewText(label, theme.Color(theme.ColorNameForeground))}
	p.text.TextSize = theme.Size(sizeNameCaption) + 1
	surface := newSurface(colorNameCard)
	surface.CornerRadius = 12
	row := container.New(layout.NewCustomPaddedLayout(3, 3, 10, 10), container.NewHBox(p.dot.object, p.text))
	p.object = container.New(paintLayout{layout.NewStackLayout(), func() {
		surface.FillColor = theme.Color(colorNameCard)
		surface.StrokeColor = theme.Color(theme.ColorNameSeparator)
		p.text.Color = theme.Color(theme.ColorNameForeground)
	}}, surface, row)
	return p
}

func (p *statusPill) set(state pillState) {
	if state == p.state {
		return
	}
	p.state = state
	p.dot.set(state.color())
}

// tint returns a theme color at the given alpha.
func tint(name fyne.ThemeColorName, alpha uint8) color.Color {
	c := color.NRGBAModel.Convert(theme.Color(name)).(color.NRGBA)
	c.A = alpha
	return c
}

// badge is short colored text on a tinted rounded rectangle.
type badge struct {
	object *fyne.Container
	bg     *canvas.Rectangle
	text   *canvas.Text
	color  fyne.ThemeColorName
}

func newBadge() *badge {
	b := &badge{bg: canvas.NewRectangle(color.Transparent), text: canvas.NewText("", nil), color: theme.ColorNamePlaceHolder}
	b.bg.CornerRadius = 8
	b.text.TextSize = theme.Size(sizeNameCaption) + 1
	b.text.TextStyle.Bold = true
	b.text.Alignment = fyne.TextAlignCenter
	b.paint()
	b.object = container.New(paintLayout{layout.NewStackLayout(), b.paint}, b.bg, container.New(layout.NewCustomPaddedLayout(3, 3, 8, 8), b.text))
	return b
}

func (b *badge) paint() {
	// The muted color is close to the card, so its tint stays fainter to keep
	// the text readable on it.
	alpha := uint8(0x2E)
	if b.color == theme.ColorNamePlaceHolder {
		alpha = 0x1A
	}
	b.bg.FillColor = tint(b.color, alpha)
	b.text.Color = theme.Color(b.color)
}

func (b *badge) set(text string, color fyne.ThemeColorName) {
	if b.text.Text == text && b.color == color {
		return
	}
	b.text.Text, b.color = text, color
	b.paint()
	b.object.Refresh()
}

// dot is an 8 px status circle.
type dot struct {
	object *fyne.Container
	circle *canvas.Circle
	color  fyne.ThemeColorName
}

func newDot() *dot {
	d := &dot{circle: canvas.NewCircle(color.Transparent), color: theme.ColorNamePlaceHolder}
	d.paint()
	d.object = container.New(paintLayout{layout.NewCenterLayout(), d.paint}, container.NewGridWrap(fyne.NewSize(8, 8), d.circle))
	return d
}

func (d *dot) paint() {
	d.circle.FillColor = theme.Color(d.color)
}

func (d *dot) set(color fyne.ThemeColorName) {
	if d.color == color {
		return
	}
	d.color = color
	d.paint()
	d.circle.Refresh()
}

// mutedLine is secondary row text in the muted color.
func mutedLine() (*fyne.Container, *canvas.Text) {
	text := newMutedText("", theme.SizeNameText)
	text.TextSize = theme.Size(sizeNameCaption) + 1
	return container.New(paintLayout{layout.NewCustomPaddedLayout(0, 0, theme.InnerPadding(), 0), func() {
		text.Color = theme.Color(theme.ColorNamePlaceHolder)
	}}, text), text
}
