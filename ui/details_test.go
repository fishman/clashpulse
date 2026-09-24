package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestDetailDialogsShowOnSoftwareDriver(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	send := func(ipc.Command) { t.Fatal("disabled action dispatched") }

	resource := newResourcePage(send, window)
	window.SetContent(resource.view)
	resource.update([]core.ResourceSnapshot{{ID: "cn", SourceHost: "provider.example", Enabled: false}})
	resourceRow := resource.list.CreateItem().(*fyne.Container)
	resource.list.UpdateItem(0, resourceRow)
	resourceItem := resource.items[resourceRow]
	if !resourceItem.refresh.Disabled() {
		t.Fatal("disabled resource has live refresh action")
	}
	resourceItem.details.OnTapped()
	if window.Canvas().Overlays().Top() == nil {
		t.Fatal("resource details did not open")
	}
	window.Canvas().Overlays().Remove(window.Canvas().Overlays().Top())

	subscription := newSubscriptionPage(send, window)
	window.SetContent(subscription.view)
	subscription.update([]core.SubscriptionSnapshot{{ID: "active", Enabled: true, Active: true}})
	subscriptionRow := subscription.list.CreateItem().(*fyne.Container)
	subscription.list.UpdateItem(0, subscriptionRow)
	subItem := subscription.items[subscriptionRow]
	if !subItem.delete.Disabled() {
		t.Fatal("active subscription can be deleted")
	}
	subItem.details.OnTapped()
	if window.Canvas().Overlays().Top() == nil {
		t.Fatal("subscription details did not open")
	}
	window.Canvas().Overlays().Remove(window.Canvas().Overlays().Top())

	filter := newFilterPage(send, window)
	window.SetContent(filter.view)
	filter.update([]core.FilterSnapshot{{ID: "ads", Enabled: false}})
	filterRow := filter.list.CreateItem().(*fyne.Container)
	filter.list.UpdateItem(0, filterRow)
	filterItem := filter.items[filterRow]
	if !filterItem.refresh.Disabled() {
		t.Fatal("disabled filter has live refresh action")
	}
	filterItem.details.OnTapped()
	if window.Canvas().Overlays().Top() == nil {
		t.Fatal("filter details did not open")
	}
}
