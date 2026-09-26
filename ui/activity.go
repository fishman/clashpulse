package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
)

type activityView struct {
	entries []core.DiagnosticSnapshot
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
			label := widget.NewLabel("")
			label.Wrapping = fyne.TextWrapWord
			return label
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			item.(*widget.Label).SetText(activityText(view.entries[id]))
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
		v.list.SetItemHeight(widget.ListItemID(id), activityRowHeight(activityText(entry), width))
	}
}

func activityRowHeight(text string, width float32) float32 {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	label.MinSize()
	label.Resize(fyne.NewSize(width, 1<<15))
	return label.MinSize().Height
}

func activityText(entry core.DiagnosticSnapshot) string {
	return time.Unix(entry.At, 0).UTC().Format(time.RFC3339) + "  " + entry.Severity + "  " + entry.SourceID + "  " + entry.Message
}

func (v *activityView) update(entries []core.DiagnosticSnapshot) {
	v.entries = append(v.entries[:0], entries...)
	v.list.Refresh()
	v.resizeRows(v.list.Size().Width, true)
}

func (d *desktopUI) openActivity() {
	if d.activity.dialog == nil {
		d.activity.dialog = dialog.NewCustom("Activity", "Close", d.activity.content, d.window)
	}
	d.activity.dialog.Show()
}
