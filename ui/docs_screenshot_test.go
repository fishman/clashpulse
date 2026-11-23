//go:build screenshots

package ui

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"github.com/fishman/clashpulse/internal/docsfixture"
)

// TestDocsScreenshots builds the real window over the docs fixture and
// captures one PNG per page. DOCS_SCREENSHOTS overrides the output directory;
// a relative value is taken from the repository root. DOCS_SCREENSHOTS_DARK,
// when set, also writes a dark Overview there for a manual check; it is not a
// docs image.
func TestDocsScreenshots(t *testing.T) {
	dir := os.Getenv("DOCS_SCREENSHOTS")
	if dir == "" {
		dir = filepath.Join("docs", "assets", "screenshots")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join("..", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	app := test.NewApp()
	defer app.Quit()
	app.Settings().SetTheme(variantTheme{newTheme(), theme.VariantLight})
	window := app.NewWindow("ClashPulse")
	view := newDesktopUI(t.Context(), "", window)
	window.Resize(fyne.NewSize(1100, 720))
	view.postSnapshot(docsfixture.Snapshot())
	fyne.DoAndWait(func() {})

	// The policy shot keeps only the centered dialog: the software test
	// painter does not clip an Entry's text to an enclosing scroll, so form
	// rows scrolled out of the dialog would otherwise paint over the page.
	var policy image.Rectangle
	for _, shot := range []struct {
		name string
		show func()
		crop *image.Rectangle
	}{
		{"gui-overview.png", func() { view.showView("Overview") }, nil},
		{"gui-proxies.png", func() { view.showView("Proxies") }, nil},
		{"gui-subscriptions.png", func() { view.showView("Subscriptions") }, nil},
		{"gui-filter-lists.png", func() { view.showView("Filter Lists") }, nil},
		{"gui-data-resources.png", func() { view.showView("Data Resources") }, nil},
		{"gui-settings-monitor.png", func() {
			view.showView("Settings")
			view.settingsPage.selectSection("Monitor")
		}, nil},
		{"gui-monitor-policy.png", func() {
			dialog := view.settingsPage.monitorPolicyDialog()
			dialog.Show()
			size, canvas := dialog.MinSize(), window.Canvas().Size()
			x, y := int((canvas.Width-size.Width)/2), int((canvas.Height-size.Height)/2)
			policy = image.Rect(x, y, x+int(size.Width), y+int(size.Height))
		}, &policy},
	} {
		fyne.DoAndWait(shot.show)
		var img image.Image = window.Canvas().Capture()
		if shot.crop != nil {
			cropped := image.NewNRGBA(image.Rectangle{Max: shot.crop.Size()})
			draw.Draw(cropped, cropped.Bounds(), img, shot.crop.Min, draw.Src)
			img = cropped
		}
		savePNG(t, filepath.Join(dir, shot.name), img)
	}

	darkDir := os.Getenv("DOCS_SCREENSHOTS_DARK")
	if darkDir == "" {
		return
	}
	if err := os.MkdirAll(darkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fyne.DoAndWait(func() {
		window.Canvas().Overlays().Remove(window.Canvas().Overlays().Top())
		app.Settings().SetTheme(variantTheme{newTheme(), theme.VariantDark})
		view.showView("Overview")
	})
	savePNG(t, filepath.Join(darkDir, "gui-overview.png"), window.Canvas().Capture())
}

func savePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
