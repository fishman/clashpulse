package tui

import (
	"slices"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func serviceModel(t *testing.T) Model {
	t.Helper()
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Services: []core.ServiceSnapshot{{ID: "netflix", Name: "Netflix", Enabled: true,
		IntervalSeconds: 1800, Failures: 2, Regions: []string{"US"}, Outcome: "usable", Region: "US", Selected: "Proxy 3"}}}}).selectTab(TabSettings)
	model.Selection[TabSettings] = "service:netflix"
	return model
}

func TestServiceRowsSendCommands(t *testing.T) {
	model := serviceModel(t)
	rows := model.Rows()
	if index := rowIndex(rows, "service:spotify"); index < 0 {
		t.Fatal("built-in services without a snapshot entry are not listed")
	}
	model, command, _ := model.HandleKey(tunKey(t, model, "service_automatic"))
	if command == nil || command.Kind != ipc.CommandSetService || !command.Service.Automatic || !command.Service.Enabled ||
		command.Service.IntervalSeconds != 1800 || command.Service.Failures != 2 || !slices.Equal(command.Service.Regions, []string{"US"}) {
		t.Fatalf("automatic action = %+v", command)
	}
	_, command, _ = model.HandleKey(tunKey(t, model, "service_toggle"))
	if command == nil || command.Kind != ipc.CommandSetService || command.Service.Enabled {
		t.Fatalf("toggle action = %+v", command)
	}
	_, command, _ = model.HandleKey(tunKey(t, model, "service_check"))
	if command == nil || command.Kind != ipc.CommandCheckService || command.ServiceID != "netflix" {
		t.Fatalf("check action = %+v", command)
	}
	model.Selection[TabSettings] = "service:spotify"
	if _, command, _ = model.HandleKey(tunKey(t, model, "service_check")); command != nil {
		t.Fatalf("check on a service without published settings sent %+v", command)
	}
}

func TestServiceModalSavesOneEdit(t *testing.T) {
	model, command, _ := serviceModel(t).HandleKey("enter")
	if command != nil || model.Modal == nil || model.Modal.Kind != ModalService {
		t.Fatalf("enter opened %+v and sent %+v", model.Modal, command)
	}
	// Regions is the first field; append a second code.
	model, _, _ = model.HandleKey("enter")
	for _, key := range "jp, de" {
		model, _, _ = model.HandleKey(string(key))
	}
	model, command, _ = model.HandleKey("ctrl+s")
	if command == nil || command.Kind != ipc.CommandSetService || command.Service.ID != "netflix" ||
		!slices.Equal(command.Service.Regions, []string{"JP", "DE"}) || command.Service.IntervalSeconds != 1800 || model.Modal != nil {
		t.Fatalf("save sent %+v (modal %+v, notice %q)", command, model.Modal, model.Notice)
	}
}
