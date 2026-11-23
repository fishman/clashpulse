package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func TestThemeColorsFollowVariant(t *testing.T) {
	th := newTheme()
	for _, check := range []struct {
		name    fyne.ThemeColorName
		variant fyne.ThemeVariant
		want    color.NRGBA
	}{
		{theme.ColorNameBackground, theme.VariantLight, color.NRGBA{0xF5, 0xF5, 0xF7, 0xFF}},
		{theme.ColorNameBackground, theme.VariantDark, color.NRGBA{0x1C, 0x1C, 0x1E, 0xFF}},
		{colorNameCard, theme.VariantLight, color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}},
		{colorNameCard, theme.VariantDark, color.NRGBA{0x2C, 0x2C, 0x2E, 0xFF}},
		{colorNameSidebar, theme.VariantDark, color.NRGBA{0x23, 0x23, 0x25, 0xFF}},
	} {
		if got := th.Color(check.name, check.variant); got != check.want {
			t.Errorf("Color(%s, %d) = %v, want %v", check.name, check.variant, got, check.want)
		}
	}
	if got, want := th.Color(theme.ColorNameHover, theme.VariantDark), theme.DefaultTheme().Color(theme.ColorNameHover, theme.VariantDark); got != want {
		t.Errorf("unknown color = %v, want default %v", got, want)
	}
}

func TestThemeSizes(t *testing.T) {
	th := newTheme()
	if got := th.Size(sizeNameCardRadius); got != 14 {
		t.Errorf("card radius = %v", got)
	}
	if got := th.Size(theme.SizeNamePadding); got != 6 {
		t.Errorf("padding = %v", got)
	}
	if got, want := th.Size(theme.SizeNameScrollBar), theme.DefaultTheme().Size(theme.SizeNameScrollBar); got != want {
		t.Errorf("unknown size = %v, want default %v", got, want)
	}
}

func TestMeterClampsFraction(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	app.Settings().SetTheme(newTheme())
	m := newMeter()
	m.object.Resize(fyne.NewSize(100+2*theme.InnerPadding(), 8))
	m.set(1.5, theme.ColorNameSuccess)
	if got := m.fill.Size().Width; got != 100 {
		t.Fatalf("over-full meter width = %v", got)
	}
	m.set(-1, theme.ColorNameError)
	if got := m.fill.Size().Width; got != 0 {
		t.Fatalf("negative meter width = %v", got)
	}
	m.set(0.25, theme.ColorNameWarning)
	if got := m.fill.Size().Width; got != 25 {
		t.Fatalf("quarter meter width = %v", got)
	}
}

func TestPillStateColor(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	app.Settings().SetTheme(newTheme())
	p := pill("TUN")
	p.set(pillFailed)
	if p.state != pillFailed || p.dot.circle.FillColor != theme.Color(theme.ColorNameError) {
		t.Fatalf("failed pill = %v %v", p.state, p.dot.circle.FillColor)
	}
	p.set(pillOn)
	if p.dot.circle.FillColor != theme.Color(theme.ColorNameSuccess) {
		t.Fatalf("on pill = %v", p.dot.circle.FillColor)
	}
}

// variantTheme pins a theme to one variant; the test app has no desktop
// appearance to follow.
type variantTheme struct {
	fyne.Theme
	variant fyne.ThemeVariant
}

func (v variantTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return v.Theme.Color(name, v.variant)
}

func TestHelpersFollowLiveVariantSwitch(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	app.Settings().SetTheme(variantTheme{newTheme(), theme.VariantLight})
	card := newCard("Health", widget.NewLabel("body"))
	surface := card.Objects[0].(*canvas.Rectangle)
	p := pill("TUN")
	_, number, _ := newValue("12", "ms")
	window := app.NewWindow("ClashPulse")
	b := newBadge()
	b.set("120 ms", theme.ColorNameDisabled)
	window.SetContent(container.NewVBox(card, p.object, number, b.object))
	window.Resize(fyne.NewSize(300, 300))
	dark := newTheme()
	app.Settings().SetTheme(variantTheme{dark, theme.VariantDark})
	if got, want := surface.FillColor, dark.Color(colorNameCard, theme.VariantDark); got != want {
		t.Errorf("card surface = %v, want %v", got, want)
	}
	if got, want := p.text.Color, dark.Color(theme.ColorNameForeground, theme.VariantDark); got != want {
		t.Errorf("pill text = %v, want %v", got, want)
	}
	if got, want := b.text.Color, dark.Color(theme.ColorNameDisabled, theme.VariantDark); got != want {
		t.Errorf("badge text = %v, want %v", got, want)
	}
}
