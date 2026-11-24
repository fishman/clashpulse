package ui

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// choice maps a fixed set of setting values to labels. A pick that differs
// from the snapshot value is sent; controls keep showing the snapshot value
// until a new snapshot confirms the pick, because a rejected pick publishes
// no change that would correct it.
type choice struct {
	values, labels []string
	current        string
	pick           func(string)
}

func newChoice(values []string, label func(string) string, pick func(string)) choice {
	labels := make([]string, len(values))
	for i, value := range values {
		labels[i] = label(value)
	}
	return choice{values: values, labels: labels, pick: pick}
}

// label is the current value's label; a value outside the set shows as
// itself, and no value shows as empty.
func (c *choice) label() string {
	if i := slices.Index(c.values, c.current); i >= 0 {
		return c.labels[i]
	}
	return c.current
}

func (c *choice) choose(i int) {
	if i >= 0 && c.values[i] != c.current {
		c.pick(c.values[i])
	}
}

// newChoiceSelect is a select over a choice. show displays a snapshot value
// without sending it.
func newChoiceSelect(values []string, label func(string) string, pick func(string)) (sel *widget.Select, show func(string)) {
	c := newChoice(values, label, pick)
	show = func(value string) {
		c.current = value
		// Assigning Selected, unlike SetSelected, does not call OnChanged.
		if text := c.label(); sel.Selected != text {
			sel.Selected = text
			sel.Refresh()
		}
	}
	sel = widget.NewSelect(c.labels, func(text string) {
		c.choose(slices.Index(c.labels, text))
		show(c.current)
	})
	return sel, show
}

// cardChoice is a card value that opens a menu of its choices when tapped.
// It reads like the bold values on the other cards and shows as a dropdown
// on hover. Unlike a Select it takes no focus, so a menu dismissed with Esc
// or an outside click leaves no highlight behind.
type cardChoice struct {
	widget.DisableableWidget
	choice
	text    *widget.Label
	tint    *canvas.Rectangle
	hovered bool
}

func newCardChoice(values []string, label func(string) string, pick func(string)) *cardChoice {
	c := &cardChoice{choice: newChoice(values, label, pick), text: widget.NewLabelWithStyle("-", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), tint: canvas.NewRectangle(color.Transparent)}
	c.text.Truncation = fyne.TextTruncateEllipsis
	c.tint.CornerRadius = theme.Size(theme.SizeNameInputRadius)
	c.ExtendBaseWidget(c)
	return c
}

func (c *cardChoice) CreateRenderer() fyne.WidgetRenderer {
	arrow := widget.NewIcon(theme.MenuDropDownIcon())
	row := container.NewBorder(nil, nil, nil, arrow, c.text)
	return widget.NewSimpleRenderer(container.New(paintLayout{layout.NewStackLayout(), c.paint}, c.tint, row))
}

func (c *cardChoice) paint() {
	c.tint.FillColor = color.Transparent
	if c.hovered && !c.Disabled() {
		c.tint.FillColor = theme.Color(theme.ColorNameHover)
	}
}

// show displays a snapshot value without sending it.
func (c *cardChoice) show(value string) {
	c.current = value
	text := c.label()
	if text == "" {
		text = "-"
	}
	if c.text.Text != text {
		c.text.SetText(text)
	}
}

func (c *cardChoice) Tapped(*fyne.PointEvent) {
	window := fyne.CurrentApp().Driver().CanvasForObject(c)
	if c.Disabled() || window == nil {
		return
	}
	items := make([]*fyne.MenuItem, len(c.labels))
	for i, text := range c.labels {
		items[i] = fyne.NewMenuItem(text, func() { c.choose(i) })
		items[i].Checked = c.values[i] == c.current
	}
	menu := widget.NewPopUpMenu(fyne.NewMenu("", items...), window)
	menu.ShowAtPosition(fyne.CurrentApp().Driver().AbsolutePositionForObject(c).AddXY(0, c.Size().Height))
	menu.Resize(fyne.NewSize(max(c.Size().Width, menu.MinSize().Width), menu.MinSize().Height))
}

func (c *cardChoice) Cursor() desktop.Cursor {
	if c.Disabled() {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}

func (c *cardChoice) MouseIn(*desktop.MouseEvent) { c.setHovered(true) }

func (c *cardChoice) MouseMoved(*desktop.MouseEvent) {}

func (c *cardChoice) MouseOut() { c.setHovered(false) }

func (c *cardChoice) setHovered(hovered bool) {
	if c.hovered != hovered {
		c.hovered = hovered
		c.paint()
		c.tint.Refresh()
	}
}
