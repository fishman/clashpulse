package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestSubscriptionAddSendsTypedIntentAndClearsSourceAfterSubmit(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newSubscriptionPage(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)

	page.add.OnTapped()
	editor := page.editor
	if !editor.source.Password {
		t.Fatal("subscription URL is exposed in the edit field")
	}
	editor.id.SetText("nightly")
	editor.name.SetText("Nightly")
	sourceURL := "https://example.test/private?token=add-secret"
	editor.source.SetText(sourceURL)
	editor.dialog.Submit()

	if len(sent) != 1 {
		t.Fatalf("sent %d subscription intents, want 1", len(sent))
	}
	command := sent[0]
	if command.Kind != ipc.CommandPutSubscription || command.SubscriptionID != "nightly" || command.Subscription == nil {
		t.Fatal("add did not send a typed subscription intent")
	}
	edit := command.Subscription
	if edit.Name == nil || *edit.Name != "Nightly" || edit.URL == nil || *edit.URL != sourceURL || edit.Enabled == nil || !*edit.Enabled || edit.RefreshIntervalSeconds == nil || *edit.RefreshIntervalSeconds != 43200 || edit.TimeoutSeconds == nil || *edit.TimeoutSeconds != 30 || edit.Route == nil || *edit.Route != "direct" || edit.AllowHTTP == nil || *edit.AllowHTTP || edit.AllowInvalidTLS == nil || *edit.AllowInvalidTLS {
		t.Fatal("add intent did not preserve the configured subscription fields")
	}
	if editor.source.Text != "" {
		t.Fatal("submitted source URL remains in the editor")
	}
	page.update([]core.SubscriptionSnapshot{{ID: "nightly", Name: "Nightly", SourceHost: "example.test", Enabled: true}})
	row := page.list.CreateItem().(*fyne.Container)
	page.list.UpdateItem(0, row)
	item := page.items[row]
	if item.name.Text != "Nightly" || item.detail.Text == "" || strings.Contains(item.name.Text+item.detail.Text, sourceURL) {
		t.Fatal("snapshot-rendered subscription controls are unsafe")
	}
}

func TestSubscriptionRenameRetainsPrivateSourceAndWaitsForSnapshot(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newSubscriptionPage(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)
	page.update([]core.SubscriptionSnapshot{{ID: "managed-feed", Name: "Before", SourceHost: "feed.example", Enabled: true}})
	row := page.list.CreateItem().(*fyne.Container)
	page.list.UpdateItem(0, row)
	item := page.items[row]

	item.edit.OnTapped()
	editor := page.editor
	editor.name.SetText("After")
	editor.dialog.Submit()

	if len(sent) != 1 {
		t.Fatalf("sent %d subscription intents, want 1", len(sent))
	}
	command := sent[0]
	if command.Kind != ipc.CommandPutSubscription || command.SubscriptionID != "managed-feed" || command.Subscription == nil || command.Subscription.Name == nil || *command.Subscription.Name != "After" {
		t.Fatal("rename did not send a typed subscription intent")
	}
	edit := command.Subscription
	if edit.URL != nil || edit.Enabled != nil || edit.RefreshIntervalSeconds != nil || edit.TimeoutSeconds != nil || edit.Route != nil || edit.AllowHTTP != nil || edit.AllowInvalidTLS != nil || editor.source.Text != "" {
		t.Fatal("rename overwrote omitted private source or settings")
	}
	if item.name.Text != "Before" {
		t.Fatal("subscription row changed before an immutable snapshot arrived")
	}
	page.update([]core.SubscriptionSnapshot{{ID: "managed-feed", Name: "After", SourceHost: "feed.example", Enabled: true}})
	page.list.UpdateItem(0, row)
	if item.name.Text != "After" || item.detail.Text == "" {
		t.Fatal("subscription row did not update from the next snapshot")
	}
}

func TestSubscriptionEditorRejectsValuesIPCWouldReject(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	page := newSubscriptionPage(func(ipc.Command) {}, window)
	page.add.OnTapped()
	e := page.editor
	e.id.SetText(strings.Repeat("a", 65))
	e.name.SetText("Daily")
	e.source.SetText("https://provider.invalid/profile")
	if _, _, err := e.command(); err == nil {
		t.Fatal("65-byte stable ID passed desktop editor")
	}
	e.id.SetText("daily")
	e.timeout.SetText("301")
	if _, _, err := e.command(); err == nil {
		t.Fatal("timeout above IPC limit passed desktop editor")
	}
}

func TestSubscriptionDeleteRequiresConfirmation(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newSubscriptionPage(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)
	page.update([]core.SubscriptionSnapshot{{ID: "daily", Name: "Daily"}})
	row := page.list.CreateItem().(*fyne.Container)
	page.list.UpdateItem(0, row)
	page.items[row].delete.OnTapped()
	if len(sent) != 0 {
		t.Fatal("delete intent was sent before confirmation")
	}
}
