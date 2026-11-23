package ui

import (
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/localize"
)

type activityView struct {
	entries []core.DiagnosticSnapshot
	proxies []core.ProxySnapshot
	list    *widget.List
	content fyne.CanvasObject
	dialog  *dialog.CustomDialog
	width   float32
}

func newActivityView() *activityView {
	view := &activityView{}
	view.list = widget.NewList(
		func() int { return len(view.entries) },
		func() fyne.CanvasObject {
			return activityRow(nil)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			row := item.(*widget.RichText)
			row.Segments = activitySegments(view.entries[id], view.proxies)
			row.Refresh()
		},
	)
	view.content = container.New(activityLayout{view: view}, view.list)
	return view
}

type activityLayout struct{ view *activityView }

func (l activityLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.Size{}
	}
	return objects[0].MinSize()
}

func (l activityLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	objects[0].Resize(size)
	l.view.resizeRows(size.Width, false)
}

func (v *activityView) resizeRows(width float32, force bool) {
	if width <= 0 || (!force && width == v.width) {
		return
	}
	v.width = width
	for id, entry := range v.entries {
		v.list.SetItemHeight(widget.ListItemID(id), activityRowHeight(activitySegments(entry, v.proxies), width))
	}
}

func activityRow(segments []widget.RichTextSegment) *widget.RichText {
	row := widget.NewRichText(segments...)
	row.Wrapping = fyne.TextWrapWord
	return row
}

func activityRowHeight(segments []widget.RichTextSegment, width float32) float32 {
	row := activityRow(segments)
	row.MinSize()
	row.Resize(fyne.NewSize(width, 1<<15))
	return row.MinSize().Height
}

// activitySegments renders an entry as a header line (local time, severity,
// operation, source) above its message, so the message starts at the left edge
// and the severity color marks errors at a glance.
func activitySegments(entry core.DiagnosticSnapshot, proxies []core.ProxySnapshot) []widget.RichTextSegment {
	severity := theme.ColorNamePrimary
	if entry.Severity == "error" {
		severity = theme.ColorNameError
	}
	inline := func(text string, color fyne.ThemeColorName, bold bool) *widget.TextSegment {
		return &widget.TextSegment{Text: text, Style: widget.RichTextStyle{Inline: true, ColorName: color, SizeName: theme.SizeNameCaptionText, TextStyle: fyne.TextStyle{Bold: bold}}}
	}
	header := []widget.RichTextSegment{
		inline(time.Unix(entry.At, 0).Local().Format(time.DateTime)+"   ", theme.ColorNamePlaceHolder, false),
		inline(localize.Code("diagnostic.severity", entry.Severity), severity, true),
	}
	if entry.Kind != "" {
		// English has no operation labels, so the code itself is shown, spaced.
		header = append(header, inline("   "+strings.ReplaceAll(localize.Code("operation", entry.Kind), "_", " "), theme.ColorNameForeground, true))
	}
	if source := core.DiagnosticSource(proxies, entry); source != "" {
		header = append(header, inline("   "+source, theme.ColorNamePlaceHolder, false))
	}
	header[len(header)-1].(*widget.TextSegment).Style.Inline = false
	return append(header, &widget.TextSegment{Text: localize.T(entry.Message), Style: widget.RichTextStyleParagraph})
}

// update shows entries; proxies name the nodes stream probe entries refer to.
func (v *activityView) update(entries []core.DiagnosticSnapshot, proxies []core.ProxySnapshot) {
	v.entries, v.proxies = append(v.entries[:0], entries...), proxies
	v.list.Refresh()
	v.resizeRows(v.list.Size().Width, true)
}

// openActivity shows the log panel: what this service changed in the generated
// configuration, above the diagnostics it recorded.
func (d *desktopUI) openActivity() {
	if d.activity.dialog == nil {
		d.activity.dialog = dialog.NewCustom(localize.T("Activity"), localize.T("Close"), container.NewBorder(d.overridesSection, nil, nil, nil, d.activity.content), d.window)
	}
	showSized(d.activity.dialog, d.window, logDialogWidth, true)
}
