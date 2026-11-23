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

func TestServiceActionsNeedAnEnabledCheck(t *testing.T) {
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Services: []core.ServiceSnapshot{{ID: "claude", Name: "Claude", IntervalSeconds: 1800, Failures: 2}}}}).selectTab(TabSettings)
	model.Selection[TabSettings] = "service:claude"
	for _, action := range []string{"service_automatic", "service_check"} {
		if next, command, _ := model.HandleKey(tunKey(t, model, action)); command != nil || next.Notice == "" {
			t.Fatalf("%s on a disabled service sent %+v (notice %q)", action, command, next.Notice)
		}
	}
	if _, command, _ := model.HandleKey(tunKey(t, model, "service_toggle")); command == nil || !command.Service.Enabled {
		t.Fatalf("enable on a disabled service sent %+v", command)
	}
}

func TestServiceModalKeepsInvalidInputOpen(t *testing.T) {
	for field, input := range map[int]string{0: "USA", 1: "299", 2: "11"} {
		model, _, _ := serviceModel(t).HandleKey("enter")
		for range field {
			model, _, _ = model.HandleKey("down")
		}
		model, _, _ = model.HandleKey("enter")
		for _, key := range input {
			model, _, _ = model.HandleKey(string(key))
		}
		model, command, _ := model.HandleKey("ctrl+s")
		if command != nil || model.Modal == nil || model.Notice == "" {
			t.Fatalf("field %d input %q sent %+v (modal %v, notice %q)", field, input, command, model.Modal != nil, model.Notice)
		}
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
