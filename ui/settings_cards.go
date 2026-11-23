package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// settingsPane stacks a Settings pane's cards, with a margin that keeps the
// card edges clear of the scroll bar and window edge.
func settingsPane(cards ...fyne.CanvasObject) fyne.CanvasObject {
	pad := theme.Padding()
	return container.New(layout.NewCustomPaddedLayout(0, 2*pad, 0, 3*pad), container.New(layout.NewCustomPaddedVBoxLayout(3*pad), cards...))
}

// settingsCard groups rows under an uppercase muted caption shown above a
// rounded card surface. action, when set, sits at the right of the caption.
func settingsCard(caption string, action fyne.CanvasObject, body *fyne.Container) fyne.CanvasObject {
	title := newMutedText(strings.ToUpper(caption), sizeNameCaption)
	title.TextStyle.Bold = true
	heading := container.NewBorder(nil, nil, container.New(layout.NewCustomPaddedLayout(0, 0, theme.InnerPadding(), 0), title), action)
	surface := newSurface(colorNameCard)
	card := container.New(paintLayout{layout.NewStackLayout(), func() {
		surface.FillColor = theme.Color(colorNameCard)
		surface.StrokeColor = theme.Color(theme.ColorNameSeparator)
		title.Color = theme.Color(theme.ColorNamePlaceHolder)
	}}, surface, container.New(layout.NewCustomPaddedLayout(theme.Padding(), theme.Padding(), theme.Padding(), theme.Padding()), body))
	return container.New(layout.NewCustomPaddedVBoxLayout(theme.Padding()), heading, card)
}

// cardBody holds a card's rows; setCardRows fills it.
func cardBody(rows ...fyne.CanvasObject) *fyne.Container {
	body := container.New(layout.NewCustomPaddedVBoxLayout(0))
	setCardRows(body, rows...)
	return body
}

// setCardRows replaces a card's rows, with a separator line between rows.
func setCardRows(body *fyne.Container, rows ...fyne.CanvasObject) {
	objects := make([]fyne.CanvasObject, 0, 2*len(rows))
	for i, row := range rows {
		if i > 0 {
			objects = append(objects, widget.NewSeparator())
		}
		objects = append(objects, row)
	}
	body.Objects = objects
	body.Refresh()
}

// settingRow puts a setting's name on the left and its value and controls on
// the right. The value wraps right-aligned in the remaining width. Any extra
// objects (help text, conditional actions) stack under the row.
func settingRow(name string, value *widget.Label, controls []fyne.CanvasObject, below ...fyne.CanvasObject) fyne.CanvasObject {
	var center fyne.CanvasObject
	if value != nil {
		value.Alignment = fyne.TextAlignTrailing
		value.Wrapping = fyne.TextWrapWord
		center = value
	}
	var right fyne.CanvasObject
	if len(controls) > 0 {
		right = container.NewCenter(container.NewHBox(controls...))
	}
	var content fyne.CanvasObject = container.NewBorder(nil, nil, widget.NewLabel(name), right, center)
	if len(below) > 0 {
		content = container.New(layout.NewCustomPaddedVBoxLayout(0), append([]fyne.CanvasObject{content}, below...)...)
	}
	// Inset every row, text-only or not, so controls clear the separators
	// and the card's right edge and the card rhythm stays even.
	pad := theme.Padding()
	return container.New(layout.NewCustomPaddedLayout(pad, pad, 0, pad), content)
}

// newHelpText is wrapped, muted, caption-size text that explains the row
// above it. Update it with setDetailText. It uses Fyne's own caption size: a
// theme without the custom size name (tests) would measure zero-width runes,
// which word wrapping cannot handle.
func newHelpText(text string) *widget.RichText {
	help := widget.NewRichText(&widget.TextSegment{Text: text, Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder, SizeName: theme.SizeNameCaptionText, Inline: true}})
	help.Wrapping = fyne.TextWrapWord
	return help
}
