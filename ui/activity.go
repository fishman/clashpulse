package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
)

type activityView struct {
	entries []core.DiagnosticSnapshot
	list    *widget.List
	dialog  *dialog.CustomDialog
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
			entry := view.entries[id]
			text := time.Unix(entry.At, 0).UTC().Format(time.RFC3339) + "  " + entry.Severity + "  " + entry.SourceID + "  " + entry.Message
			item.(*widget.Label).SetText(text)
		},
	)
	return view
}

func (v *activityView) update(entries []core.DiagnosticSnapshot) {
	v.entries = append(v.entries[:0], entries...)
	v.list.Refresh()
}

func (d *desktopUI) openActivity() {
	if d.activity.dialog == nil {
		d.activity.dialog = dialog.NewCustom("Activity", "Close", d.activity.list, d.window)
	}
	d.activity.dialog.Show()
}
