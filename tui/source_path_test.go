package tui

import "testing"

func TestTerminalResourceEditorRejectsRelativeSource(t *testing.T) {
	model := NewModel().selectTab(TabResources)
	model, _, _ = model.HandleKey("n")
	if model.Modal == nil {
		t.Fatal("resource editor did not open")
	}
	for _, field := range []string{"cn", "rule-set", "yaml", "domain"} {
		model, _ = fillModalField(t, model, field)
	}
	model, command := fillModalField(t, model, "relative/rules.yaml")
	if command != nil || model.Modal == nil || model.Modal.managed.step != resourceFieldURL || model.Notice == "" {
		t.Fatalf("relative source progressed through resource editor: command=%#v modal=%#v notice=%q", command, model.Modal, model.Notice)
	}
}
