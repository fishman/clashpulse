package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/fishman/clashpulse/core"
)

// TestSourceRowActionsAlignRight checks that the row actions on the three
// source pages sit at the row's right edge beside the text, and that long
// text truncates instead of widening the row.
func TestSourceRowActionsAlignRight(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	a.Settings().SetTheme(variantTheme{newTheme(), theme.VariantLight})
	long := strings.Repeat("very-long-source-name-", 12)

	subscriptions := newSubscriptionPage(nil, a.NewWindow("subscriptions"))
	subscriptions.update([]core.SubscriptionSnapshot{{ID: "feed", Name: long, SourceHost: long, Enabled: true}})
	row := subscriptions.list.CreateItem()
	subscriptions.list.UpdateItem(0, row)
	item := listItem[subscriptionItem](row)
	checkSourceRow(t, a, "subscriptions", row, item.name, item.refresh, item.details)

	resources := newResourcePage(nil, a.NewWindow("resources"))
	resources.update([]core.ResourceSnapshot{{ID: long, SourceHost: long, Enabled: true}})
	row = resources.list.CreateItem()
	resources.list.UpdateItem(0, row)
	resource := listItem[resourceItem](row)
	checkSourceRow(t, a, "resources", row, resource.name, resource.refresh, resource.details)

	filters := newFilterPage(nil, a.NewWindow("filters"))
	filters.update([]core.FilterSnapshot{{ID: long, ResourceID: long, SourceHost: long, Enabled: true}})
	row = filters.list.CreateItem()
	filters.list.UpdateItem(0, row)
	filter := listItem[filterItem](row)
	checkSourceRow(t, a, "filters", row, filter.name, filter.refresh, filter.details)
}

func checkSourceRow(t *testing.T, a fyne.App, page string, row fyne.CanvasObject, name *widget.Label, first, last *widget.Button) {
	t.Helper()
	const width = 900
	if got := row.MinSize().Width; got >= width {
		t.Errorf("%s: long text widens the row to %v", page, got)
	}
	window := a.NewWindow(page)
	defer window.Close()
	window.SetPadded(false)
	window.SetContent(row)
	window.Resize(fyne.NewSize(width, row.MinSize().Height))
	driver := a.Driver()
	rowRight := driver.AbsolutePositionForObject(row).X + row.Size().Width
	lastRight := driver.AbsolutePositionForObject(last).X + last.Size().Width
	if gap := rowRight - lastRight; gap < theme.Padding() || gap > 4*theme.Padding() {
		t.Errorf("%s: last action ends %v from the row edge, want right-aligned with padding", page, gap)
	}
	nameRight := driver.AbsolutePositionForObject(name).X + name.Size().Width
	if firstLeft := driver.AbsolutePositionForObject(first).X; firstLeft < nameRight {
		t.Errorf("%s: actions start at %v, inside the text column ending at %v", page, firstLeft, nameRight)
	}
	nameTop := driver.AbsolutePositionForObject(name).Y
	if firstTop := driver.AbsolutePositionForObject(first).Y; firstTop > nameTop+row.Size().Height/2 {
		t.Errorf("%s: actions sit under the text (y %v, name y %v)", page, firstTop, nameTop)
	}
}
