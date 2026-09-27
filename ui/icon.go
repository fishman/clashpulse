package ui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed clashpulse.svg
var iconSVG []byte

func appIcon() fyne.Resource {
	return fyne.NewStaticResource("clashpulse.svg", iconSVG)
}
