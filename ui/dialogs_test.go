package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"github.com/fishman/clashpulse/core"
)

func pressEscape(view *desktopUI) {
	view.window.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})
}

func TestControlQQuitsTheWindow(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(t.Context(), "", app.NewWindow("ClashPulse"))
	quit := 0
	view.quit = func() { quit++ }
	dispatcher, ok := view.window.Canvas().(interface{ TypedShortcut(fyne.Shortcut) })
	if !ok {
		t.Skip("this canvas does not dispatch shortcuts")
	}
	dispatcher.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyQ, Modifier: fyne.KeyModifierControl})
	if quit != 1 {
		t.Fatalf("Control+Q quit the application %d times, want 1", quit)
	}
	dispatcher.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyQ, Modifier: fyne.KeyModifierAlt})
	if quit != 1 {
		t.Fatalf("an unrelated shortcut quit the application: %d", quit)
	}
}

func TestEscapeDismissesEditorThroughItsCancelPath(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(t.Context(), "", app.NewWindow("ClashPulse"))
	view.subPage.openSubscriptionEditor(nil)
	editor := view.subPage.editor
	editor.source.SetText("https://example.test/private?token=escape-secret")

	pressEscape(view)
	fyne.DoAndWait(func() {})

	if view.subPage.editor != nil {
		t.Fatal("dismissed editor still owns the page slot, so it cannot be reopened")
	}
	if editor.source.Text != "" {
		t.Fatalf("dismissed editor kept the private URL: %q", editor.source.Text)
	}
	if view.window.Canvas().Overlays().Top() != nil {
		t.Fatal("editor overlay survived Escape")
	}
	view.subPage.openSubscriptionEditor(nil)
	if view.subPage.editor == nil {
		t.Fatal("the editor could not be opened again after Escape")
	}
}

func TestEscapeHidesReusableDialogAndKeepsItShowable(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(t.Context(), "", app.NewWindow("ClashPulse"))
	view.postSnapshot(core.Snapshot{})
	fyne.DoAndWait(func() {})

	view.openActivity()
	fyne.DoAndWait(func() {})
	pressEscape(view)
	fyne.DoAndWait(func() {})
	if view.window.Canvas().Overlays().Top() != nil {
		t.Fatal("activity overlay survived Escape")
	}
	view.openActivity()
	fyne.DoAndWait(func() {})
	if view.window.Canvas().Overlays().Top() == nil {
		t.Fatal("the activity log panel could not be shown again after Escape")
	}
	pressEscape(view)
	fyne.DoAndWait(func() {})
}

func TestEscapeClosesUntrackedInformationDialog(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newDesktopUI(t.Context(), "", app.NewWindow("ClashPulse"))
	dialog.ShowInformation("Detail", "body", view.window)
	fyne.DoAndWait(func() {})
	if view.window.Canvas().Overlays().Top() == nil {
		t.Fatal("information dialog did not open")
	}
	pressEscape(view)
	fyne.DoAndWait(func() {})
	if view.window.Canvas().Overlays().Top() != nil {
		t.Fatal("information dialog survived Escape")
	}
}
