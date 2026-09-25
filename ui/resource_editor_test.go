package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestResourceAddSendsTypedIntentAndClearsPrivateSource(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newResourcePage(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)

	page.add.OnTapped()
	editor := page.editor
	if !editor.source.Password {
		t.Fatal("resource URL is exposed in the edit field")
	}
	editor.id.SetText("nightly")
	sourceURL := "https://example.test/private?token=add-secret"
	editor.source.SetText(sourceURL)
	pin := strings.Repeat("a", 64)
	editor.pin.SetText(pin)
	editor.dialog.Submit()

	if len(sent) != 1 {
		t.Fatalf("sent %d resource intents, want 1", len(sent))
	}
	command := sent[0]
	if command.Kind != ipc.CommandPutResource || command.ResourceID != "nightly" || command.Resource == nil {
		t.Fatal("add did not send a typed resource intent")
	}
	edit := command.Resource
	if edit.Kind == nil || *edit.Kind != "rule-provider" || edit.Format == nil || *edit.Format != "yaml" || edit.RuleType == nil || *edit.RuleType != "domain" || edit.URL == nil || *edit.URL != sourceURL || edit.Enabled == nil || !*edit.Enabled || edit.IntervalSeconds == nil || *edit.IntervalSeconds != 43200 || edit.SHA256 == nil || *edit.SHA256 != pin {
		t.Fatal("add intent did not preserve resource settings")
	}
	if editor.source.Text != "" {
		t.Fatal("submitted private source remains in the editor")
	}

	page.update([]core.ResourceSnapshot{{ID: "nightly", Kind: "rule-provider", Format: "yaml", RuleType: "domain", SourceHost: "example.test", Enabled: true}})
	row := page.list.CreateItem().(*fyne.Container)
	page.list.UpdateItem(0, row)
	item := page.items[row]
	if strings.Contains(item.name.Text+item.source.Text+item.status.Text, sourceURL) {
		t.Fatal("resource row exposed the private source URL")
	}
}

func TestResourceEditOmitsPrivateSourceAndWaitsForSnapshot(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newResourcePage(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)
	page.update([]core.ResourceSnapshot{{ID: "managed", Kind: "rule-provider", Format: "yaml", RuleType: "domain", SourceHost: "feed.example", Enabled: true}})
	row := page.list.CreateItem().(*fyne.Container)
	page.list.UpdateItem(0, row)
	item := page.items[row]
	before := item.source.Text + item.status.Text

	item.edit.OnTapped()
	editor := page.editor
	editor.enabled.SetChecked(false)
	editor.dialog.Submit()

	if len(sent) != 1 {
		t.Fatalf("sent %d resource intents, want 1", len(sent))
	}
	command := sent[0]
	if command.Kind != ipc.CommandPutResource || command.ResourceID != "managed" || command.Resource == nil || command.Resource.Enabled == nil || *command.Resource.Enabled {
		t.Fatal("disable did not send a typed resource intent")
	}
	edit := command.Resource
	if edit.URL != nil || edit.Kind != nil || edit.Format != nil || edit.RuleType != nil || edit.IntervalSeconds != nil || edit.SHA256 != nil || editor.source.Text != "" {
		t.Fatal("edit resent omitted private source or unchanged fields")
	}
	if !page.rows[0].Enabled || item.source.Text+item.status.Text != before || item.refresh.Disabled() {
		t.Fatal("resource row changed before a new snapshot arrived")
	}

	page.update([]core.ResourceSnapshot{{ID: "managed", Kind: "rule-provider", Format: "yaml", RuleType: "domain", SourceHost: "feed.example", Enabled: false}})
	page.list.UpdateItem(0, row)
	if !item.refresh.Disabled() {
		t.Fatal("resource row did not reflect the next snapshot")
	}
}

func TestFilterAddAndDisableSendTypedIntents(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	var sent []ipc.Command
	page := newFilterPage(func(command ipc.Command) { sent = append(sent, command) }, window)
	window.SetContent(page.view)

	page.add.OnTapped()
	add := page.editor
	add.id.SetText("ads")
	add.resource.SetText("managed-rules")
	add.target.SetText("REJECT")
	add.dialog.Submit()
	if len(sent) != 1 || sent[0].Kind != ipc.CommandPutFilter || sent[0].FilterID != "ads" || sent[0].Filter == nil {
		t.Fatal("add did not send a typed filter intent")
	}
	edit := sent[0].Filter
	if edit.ResourceID == nil || *edit.ResourceID != "managed-rules" || edit.Format == nil || *edit.Format != "yaml" || edit.Target == nil || *edit.Target != "REJECT" || edit.Enabled == nil || !*edit.Enabled {
		t.Fatal("add intent did not preserve filter settings")
	}

	page.update([]core.FilterSnapshot{{ID: "ads", ResourceID: "managed-rules", Format: "yaml", Target: "REJECT", Enabled: true}})
	row := page.list.CreateItem().(*fyne.Container)
	page.list.UpdateItem(0, row)
	item := page.items[row]
	item.edit.OnTapped()
	update := page.editor
	if update.format.Selected != "yaml" {
		t.Fatalf("current filter format was hidden: %q", update.format.Selected)
	}
	update.enabled.SetChecked(false)
	update.dialog.Submit()
	if len(sent) != 2 {
		t.Fatalf("sent %d total filter intents, want 2", len(sent))
	}
	command := sent[1]
	if command.Kind != ipc.CommandPutFilter || command.FilterID != "ads" || command.Filter == nil || command.Filter.Enabled == nil || *command.Filter.Enabled || command.Filter.ResourceID != nil || command.Filter.Format != nil || command.Filter.Target != nil {
		t.Fatal("disable did not send only the changed filter field")
	}
	if !page.rows[0].Enabled || item.refresh.Disabled() {
		t.Fatal("filter row changed before a new snapshot arrived")
	}
	page.update([]core.FilterSnapshot{{ID: "ads", ResourceID: "managed-rules", Format: "yaml", Target: "REJECT", Enabled: false}})
	page.list.UpdateItem(0, row)
	if !item.refresh.Disabled() {
		t.Fatal("filter row did not reflect the next snapshot")
	}
}

func TestResourceEditorRejectsMalformedLocalValues(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("ClashPulse")
	page := newResourcePage(func(ipc.Command) {}, window)
	page.add.OnTapped()
	e := page.editor
	e.id.SetText("valid")
	e.source.SetText("https://user:secret@example.test/feed.yaml")
	if _, _, err := e.command(); err == nil {
		t.Fatal("source URL with user information passed desktop editor")
	}
	e.source.SetText("/tmp/feed.yaml")
	e.pin.SetText("not-a-sha256-pin")
	if _, _, err := e.command(); err == nil {
		t.Fatal("malformed SHA-256 pin passed desktop editor")
	}
	e.pin.SetText("")
	e.source.SetText("relative/rules.yaml")
	if _, _, err := e.command(); err == nil {
		t.Fatal("relative resource path passed desktop editor")
	}
}
