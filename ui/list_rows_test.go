package ui

import (
	"runtime"
	"sync/atomic"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// TestListRowsAreNotRetained guards against a page keeping every row it ever
// built: Fyne builds a throwaway row to measure the list on every refresh, so
// a page that holds rows grows by one per snapshot.
func TestListRowsAreNotRetained(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	window := a.NewWindow("ClashPulse")
	expectCollected[proxyItem](t, "proxies", newProxyPage(nil).list)
	expectCollected[subscriptionItem](t, "subscriptions", newSubscriptionPage(nil, window).list)
	expectCollected[resourceItem](t, "resources", newResourcePage(nil, window).list)
	expectCollected[filterItem](t, "filters", newFilterPage(nil, window).list)
}

// expectCollected builds rows the way a list refresh does, drops them, and
// checks that the garbage collector can reclaim most of them.
func expectCollected[T any](t *testing.T, name string, list *widget.List) {
	t.Helper()
	const built = 100
	var collected atomic.Int32
	for range built {
		row := list.CreateItem().(*listRow[T])
		runtime.AddCleanup(row, func(count *atomic.Int32) { count.Add(1) }, &collected)
	}
	for range 20 {
		runtime.GC()
		if collected.Load() >= built/2 {
			break
		}
		runtime.Gosched()
	}
	if got := collected.Load(); got < built/2 {
		t.Errorf("%s: %d of %d discarded rows were collected", name, got, built)
	}
}

// listItem returns the widgets a list row carries.
func listItem[T any](object fyne.CanvasObject) *T {
	return object.(*listRow[T]).item
}
