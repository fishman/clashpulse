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
	if !strings.Contains(page.binaryValues[3].Text, "bundled Mihomo is not installed") {
		t.Fatalf("bundled compatibility issue not surfaced: %q", page.binaryValues[3].Text)
	}
	if page.binaryValues[3].Wrapping != fyne.TextWrapWord {
		t.Fatal("binary capability and compatibility fields clip instead of wrapping")
	}
}

func TestSettingsNavigationCollapsesOnlyWhenSectionsCannotFit(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("ClashPulse")
	page := newSettingsPage(func(ipc.Command) {}, w)
	w.SetContent(page.view)
	w.Resize(fyne.NewSize(1200, 640))
	if !page.sidebar.Visible() || page.sectionSelect.Visible() {
		t.Fatal("wide settings page did not show its sidebar")
	}
	page.sidebar.SetSelected("DNS")
	if page.sectionSelect.Selected != "DNS" || page.sectionContent.Objects[0] != page.dns.view {
		t.Fatal("sidebar did not select the DNS section")
	}
	w.Resize(fyne.NewSize(480, 640))
	if page.sidebar.Visible() || !page.sectionSelect.Visible() || page.sectionSelect.Selected != "DNS" || page.sectionContent.Objects[0] != page.dns.view {
		t.Fatal("collapsed navigation lost the DNS section")
	}
	if page.sectionViews["Mihomo binary"].MinSize().Width > page.sectionContent.Size().Width {
		t.Fatal("binary actions overflow the compact settings viewport")
	}
	page.sectionSelect.SetSelected("Mihomo binary")
	if !page.sidebar.Visible() || page.sectionSelect.Visible() {
		t.Fatal("settings switched to dropdown despite enough room for the sidebar")
	}
	page.sectionSelect.SetSelected("DNS")
	if page.sidebar.Visible() || page.sectionContent.Size().Width < page.dns.view.MinSize().Width {
		t.Fatal("DNS controls are clipped instead of collapsing the sidebar")
	}
	page.sectionSelect.SetSelected("Monitor")
	if page.sidebar.Selected != "Monitor" || page.sectionContent.Objects[0] == page.dns.view {
		t.Fatal("dropdown did not change sections")
	}
	w.Resize(fyne.NewSize(1200, 640))
	if !page.sidebar.Visible() || page.sectionSelect.Visible() || page.sidebar.Selected != "Monitor" {
		t.Fatal("expanding settings did not restore the selected sidebar section")
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
