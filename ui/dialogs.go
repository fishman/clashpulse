package ui

import (
	"reflect"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// dialogOwner is a view that holds an editor dialog while it is open, so that
// view can be asked to hand it back for dismissal.
type dialogOwner interface {
	openDialog() dialog.Dialog
}

// dismissTopDialog closes the dialog the user is looking at. An editor form is
// dismissed through the Cancel path its view already relies on; every other dialog
// is hidden at the overlay, which runs no callback because none of them carry state.
func (d *desktopUI) dismissTopDialog() {
	if d.window.Canvas().Overlays().Top() == nil {
		return
	}
	for _, owner := range []dialogOwner{d.subPage, d.resourcePage, d.filterPage} {
		if open := owner.openDialog(); open != nil {
			open.Dismiss()
			return
		}
	}
	hideTopOverlay(d.window.Canvas())
}

// hideTopOverlay hides the topmost dialog. Fyne exposes no handle on the dialog
// behind an overlay, and hiding the overlay container itself would leave its popup
// unable to show again, so the popup that owns the container is hidden instead.
func hideTopOverlay(canvas fyne.Canvas) {
	top := canvas.Overlays().Top()
	if top == nil {
		return
	}
	value := reflect.ValueOf(top)
	if value.Kind() == reflect.Ptr && !value.IsNil() {
		if content := value.Elem().FieldByName("Content"); content.IsValid() && content.CanInterface() {
			if popup, ok := content.Interface().(*widget.PopUp); ok {
				popup.Hide()
				return
			}
		}
	}
	top.Hide()
}
