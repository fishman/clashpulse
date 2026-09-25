package tui

import "testing"

func TestTerminalResourceEditorRejectsRelativeSource(t *testing.T) {
	model := NewModel().selectTab(TabResources)
	model, _, _ = model.HandleKey("n")
	if model.Modal == nil || model.Modal.Form == nil {
		t.Fatal("resource form did not open")
	}
	for _, field := range []struct{ id, value string }{{"id", "cn"}, {"kind", "rule-set"}, {"format", "yaml"}, {"rule_type", "domain"}, {"url", "relative/rules.yaml"}} {
		model = moveToFormField(t, model, field.id)
		model = typeFormText(t, model, field.value)
	}
	model, command, _ := model.HandleKey("ctrl+s")
	if command != nil || model.Modal == nil || model.Notice == "" {
		t.Fatalf("relative source accepted by resource form: command=%#v notice=%q", command, model.Notice)
	}
}
