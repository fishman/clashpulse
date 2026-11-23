package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
)

func TestSettingsPanesExposeRowsAndActions(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	page := newSettingsPage(func(ipc.Command) {}, a.NewWindow("ClashPulse"))
	for section, objects := range map[string][]fyne.CanvasObject{
		"Mihomo binary": {page.binaryValues[0], page.binaryValues[1], page.binaryValues[2], page.binaryValues[3], page.binarySource},
		"System Proxy":  {page.systemProxy, page.proxyStatus, page.mixedPort, page.proxyMode},
		"TUN":           {page.tunEnabled, page.tunStatus, page.tunAuthorize, page.tunManual, page.tunLeftover, page.tunStack, page.tunDNSMode},
		"Monitor":       {page.monitor, page.interval, page.threshold, page.switchPolicy, page.switchPolicyHelp, page.stability, page.urlTestDelay},
		"DNS":           {page.dns.listener, page.dns.editListener, page.dns.addResolver, page.dns.addRoute},
	} {
		for i, object := range objects {
			if !containsObject(page.sectionViews[section], object) {
				t.Errorf("%s pane lacks object %d", section, i)
			}
		}
	}
	for section, buttons := range map[string][]string{
		"Mihomo binary": {"Choose path..."},
		"System Proxy":  {"Change"},
		"Monitor":       {"Change", "Edit...", "Send test"},
		"Services":      {"Check now", "Edit..."},
		"DNS":           {"Change", "Add"},
	} {
		for _, text := range buttons {
			if findSettingsButton(page.sectionViews[section], localize.T(text)) == nil {
				t.Errorf("%s pane lacks the %q action", section, text)
			}
		}
	}
	// The section list names the pane; the card panes repeat no bold heading.
	for _, section := range []string{"Mihomo binary", "System Proxy", "TUN", "Monitor", "Services", "DNS"} {
		if heading := boldLabels(page.sectionViews[section]); len(heading) > 0 {
			t.Errorf("%s pane has a bold heading: %q", section, heading)
		}
	}
}

func boldLabels(object fyne.CanvasObject) []string {
	switch object := object.(type) {
	case *widget.Label:
		if object.TextStyle.Bold {
			return []string{object.Text}
		}
	case *container.Scroll:
		return boldLabels(object.Content)
	case *fyne.Container:
		var labels []string
		for _, child := range object.Objects {
			labels = append(labels, boldLabels(child)...)
		}
		return labels
	}
	return nil
}

func TestSettingRowInsetsControls(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	control := widget.NewSelect([]string{"60 seconds"}, nil)
	row := settingRow("Monitor interval", nil, []fyne.CanvasObject{control})
	w := a.NewWindow("ClashPulse")
	w.SetContent(row)
	w.Resize(fyne.NewSize(600, row.MinSize().Height+2*theme.Padding()))
	top := fyne.CurrentApp().Driver().AbsolutePositionForObject(control).Y - fyne.CurrentApp().Driver().AbsolutePositionForObject(row).Y
	pad := theme.Padding()
	if top < pad || top+control.Size().Height > row.Size().Height-pad {
		t.Fatalf("select spans %v-%v in a %v-high row; want %v inset", top, top+control.Size().Height, row.Size().Height, pad)
	}
	right := fyne.CurrentApp().Driver().AbsolutePositionForObject(control).X - fyne.CurrentApp().Driver().AbsolutePositionForObject(row).X + control.Size().Width
	if right > row.Size().Width-pad {
		t.Fatalf("select ends at %v in a %v-wide row", right, row.Size().Width)
	}
}
