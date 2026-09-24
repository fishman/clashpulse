package ui

import "fyne.io/fyne/v2"

func appIcon() fyne.Resource {
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64"><circle cx="32" cy="32" r="30" fill="#165d78"/><path d="M7 34h13l7-14 9 28 8-15h13" fill="none" stroke="#ffffff" stroke-width="5" stroke-linecap="round" stroke-linejoin="round"/></svg>`
	return fyne.NewStaticResource("clashpulse.svg", []byte(svg))
}
