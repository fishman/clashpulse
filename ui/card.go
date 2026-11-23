package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
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
// surface.
func newCard(caption string, body ...fyne.CanvasObject) *fyne.Container {
	surface := newSurface(colorNameCard)
	title := newMutedText(strings.ToUpper(caption), sizeNameCaption)
	title.TextStyle.Bold = true
	content := container.NewVBox(append([]fyne.CanvasObject{title}, body...)...)
	inset := container.New(layout.NewCustomPaddedLayout(12, 12, 14, 14), content)
	return container.New(paintLayout{layout.NewStackLayout(), func() {
		surface.FillColor = theme.Color(colorNameCard)
		surface.StrokeColor = theme.Color(theme.ColorNameSeparator)
		title.Color = theme.Color(theme.ColorNamePlaceHolder)
	}}, surface, inset)
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
	objects[0].Move(fyne.NewPos(0, 0))
	objects[1].Resize(unit)
	objects[1].Move(fyne.NewPos(number.Width+theme.Padding(), number.Height-unit.Height-theme.Padding()))
}

func (valueLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	number, unit := objects[0].MinSize(), objects[1].MinSize()
	return fyne.NewSize(number.Width+theme.Padding()+unit.Width, number.Height)
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
	l.m.track.Resize(size)
	l.m.fill.Resize(fyne.NewSize(size.Width*float32(l.m.fraction), size.Height))
}

func (meterLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(40, 8)
}

type pillState int

const (
	pillOff pillState = iota
	pillOn
	pillFailed
)

func (s pillState) color() fyne.ThemeColorName {
	switch s {
	case pillOn:
		return theme.ColorNameSuccess
	case pillFailed:
		return theme.ColorNameError
	}
	return theme.ColorNamePlaceHolder
}

// statusPill is a dot and a label on a rounded surface.
type statusPill struct {
	object fyne.CanvasObject
	dot    *canvas.Circle
	text   *canvas.Text
	state  pillState
}

func pill(label string) *statusPill {
	p := &statusPill{dot: canvas.NewCircle(theme.Color(pillOff.color())), text: canvas.NewText(label, theme.Color(theme.ColorNameForeground))}
	p.text.TextSize = theme.Size(sizeNameCaption) + 1
	surface := newSurface(colorNameCard)
	surface.CornerRadius = 12
	dot := container.New(layout.NewCenterLayout(), container.NewGridWrap(fyne.NewSize(8, 8), p.dot))
	row := container.New(layout.NewCustomPaddedLayout(3, 3, 10, 10), container.NewHBox(dot, p.text))
	p.object = container.New(paintLayout{layout.NewStackLayout(), func() {
		surface.FillColor = theme.Color(colorNameCard)
		surface.StrokeColor = theme.Color(theme.ColorNameSeparator)
		p.dot.FillColor = theme.Color(p.state.color())
		p.text.Color = theme.Color(theme.ColorNameForeground)
	}}, surface, row)
	return p
}

func (p *statusPill) set(state pillState) {
	if state == p.state {
		return
	}
	p.state = state
	p.dot.FillColor = theme.Color(state.color())
	p.dot.Refresh()
}
