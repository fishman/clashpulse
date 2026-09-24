package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestSettingsCanSelectBundledBinaryAndShowCompatibilityIssue(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	var sent []ipc.Command
	page := newSettingsPage(func(command ipc.Command) { sent = append(sent, command) }, app.NewWindow("ClashPulse"))
	bundled := findSettingsButton(page.view, "Use bundled Mihomo")
	if bundled == nil {
		t.Fatal("bundled binary choice is missing")
	}
	bundled.OnTapped()
	if len(sent) != 1 || sent[0].Kind != ipc.CommandUpdateConfiguration || sent[0].Config == nil || sent[0].Config.Binary == nil || *sent[0].Config.Binary != "bundled" {
		t.Fatalf("bundled choice sent wrong command: %+v", sent)
	}

	page.update(core.BinarySnapshot{Desired: "bundled", LastCompatibilityFailure: "bundled Mihomo is not installed"}, core.MonitorSnapshot{}, core.SystemProxySnapshot{}, core.DNSSnapshot{})
	if !strings.Contains(page.binary.Text, "bundled Mihomo is not installed") {
		t.Fatalf("bundled compatibility issue not surfaced: %q", page.binary.Text)
	}
	if page.binary.Wrapping != fyne.TextWrapWord {
		t.Fatal("binary capability and compatibility summary clips instead of wrapping")
	}
}

func findSettingsButton(object fyne.CanvasObject, text string) *widget.Button {
	if button, ok := object.(*widget.Button); ok && button.Text == text {
		return button
	}
	switch object := object.(type) {
	case *fyne.Container:
		for _, child := range object.Objects {
			if button := findSettingsButton(child, text); button != nil {
				return button
			}
		}
	case *container.Scroll:
		return findSettingsButton(object.Content, text)
	}
	return nil
}
