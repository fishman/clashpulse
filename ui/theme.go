package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

const (
	colorNameCard    fyne.ThemeColorName = "clashpulseCard"
	colorNameSidebar fyne.ThemeColorName = "clashpulseSidebar"

	sizeNameCardRadius fyne.ThemeSizeName = "clashpulseCardRadius"
	sizeNameCaption    fyne.ThemeSizeName = "clashpulseCaption"
	sizeNameValue      fyne.ThemeSizeName = "clashpulseValue"
)

// clashTheme overrides a few colors and sizes of the default theme. Colors
// follow the variant Fyne passes in, so the system light/dark setting still
// applies live.
type clashTheme struct{ fyne.Theme }

func newTheme() fyne.Theme {
	return clashTheme{theme.DefaultTheme()}
}

func (t clashTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	dark := variant == theme.VariantDark
	pick := func(light, darkColor color.NRGBA) color.Color {
		if dark {
			return darkColor
		}
		return light
	}
	switch name {
	case theme.ColorNameBackground:
		return pick(color.NRGBA{0xF5, 0xF5, 0xF7, 0xFF}, color.NRGBA{0x1C, 0x1C, 0x1E, 0xFF})
	case colorNameCard:
		return pick(color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}, color.NRGBA{0x2C, 0x2C, 0x2E, 0xFF})
	case colorNameSidebar:
		return pick(color.NRGBA{0xEC, 0xEC, 0xEF, 0xFF}, color.NRGBA{0x23, 0x23, 0x25, 0xFF})
	case theme.ColorNameSeparator, theme.ColorNameInputBorder:
		return pick(color.NRGBA{0xE0, 0xE0, 0xE4, 0xFF}, color.NRGBA{0x3A, 0x3A, 0x3C, 0xFF})
	case theme.ColorNamePrimary:
		return color.NRGBA{0x0A, 0x84, 0xFF, 0xFF}
	case theme.ColorNameSuccess:
		return color.NRGBA{0x34, 0xC7, 0x59, 0xFF}
	case theme.ColorNameWarning:
		return color.NRGBA{0xFF, 0x9F, 0x0A, 0xFF}
	case theme.ColorNameError:
		return color.NRGBA{0xFF, 0x45, 0x3A, 0xFF}
	case theme.ColorNameForeground:
		return pick(color.NRGBA{0x1D, 0x1D, 0x1F, 0xFF}, color.NRGBA{0xF5, 0xF5, 0xF7, 0xFF})
	case theme.ColorNamePlaceHolder:
		return pick(color.NRGBA{0x86, 0x86, 0x8B, 0xFF}, color.NRGBA{0x98, 0x98, 0x9D, 0xFF})
	}
	return t.Theme.Color(name, variant)
}

func (t clashTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 8
	case theme.SizeNameText:
		return 14
	case sizeNameCardRadius:
		return 14
	case sizeNameCaption:
		return 11
	case sizeNameValue:
		return 32
	}
	return t.Theme.Size(name)
}
