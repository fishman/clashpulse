package ui

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// newChoiceSelect is a select over a fixed set of setting values. A pick that
// differs from the snapshot value is sent; the select keeps showing the
// snapshot value until a new snapshot confirms the pick, because a rejected
// pick publishes no change that would correct it. show displays a snapshot
// value without sending it; a value outside the set shows as itself.
func newChoiceSelect(values []string, label func(string) string, pick func(string)) (sel *widget.Select, show func(string)) {
	labels := make([]string, len(values))
	for i, value := range values {
		labels[i] = label(value)
	}
	current := ""
	show = func(value string) {
		current = value
		text := value
		if i := slices.Index(values, value); i >= 0 {
			text = labels[i]
		}
		// Assigning Selected, unlike SetSelected, does not call OnChanged.
		if sel.Selected != text {
			sel.Selected = text
			sel.Refresh()
		}
	}
	sel = widget.NewSelect(labels, func(text string) {
		if i := slices.Index(labels, text); i >= 0 && values[i] != current {
			pick(values[i])
		}
		show(current)
	})
	return sel, show
}

// cardSelect paints a select with its card's background, so it reads as the
// card's value and shows as a dropdown on hover. Its text stays regular:
// Select sizes its text with the regular style, so bold would be clipped.
func cardSelect(sel *widget.Select) fyne.CanvasObject {
	return container.NewThemeOverride(sel, cardFieldTheme{})
}

type cardFieldTheme struct{}

func (cardFieldTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	app := fyne.CurrentApp().Settings().Theme()
	switch name {
	case theme.ColorNameInputBackground, theme.ColorNameDisabledButton:
		return app.Color(colorNameCard, variant)
	}
	return app.Color(name, variant)
}

func (cardFieldTheme) Font(style fyne.TextStyle) fyne.Resource {
	return fyne.CurrentApp().Settings().Theme().Font(style)
}

func (cardFieldTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return fyne.CurrentApp().Settings().Theme().Icon(name)
}

func (cardFieldTheme) Size(name fyne.ThemeSizeName) float32 {
	return fyne.CurrentApp().Settings().Theme().Size(name)
}
