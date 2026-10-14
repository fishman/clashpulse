package tui

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
)

func mixedPortModel(t *testing.T) (Model, string) {
	t.Helper()
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{MixedPort: 7899}}).selectTab(TabSettings)
	return model, tunKey(t, model, "edit_mixed_port")
}

func typeMixedPort(model Model, input string) (Model, *ipc.Command) {
	for range []rune(model.Modal.Input) {
		model, _, _ = model.HandleKey("backspace")
	}
	for _, key := range input {
		model, _, _ = model.HandleKey(string(key))
	}
	model, command, _ := model.HandleKey("enter")
	return model, command
}

func TestMixedPortRowShowsSnapshotValueAndKey(t *testing.T) {
	model, key := mixedPortModel(t)
	rows := model.Rows()
	index := rowIndex(rows, "setting:mixed-port")
	if index < 0 {
		t.Fatal("mixed port settings row missing")
	}
	if row := rows[index]; row.Cells[0] != "Mixed port" || row.Cells[1] != "7899" || row.Cells[2] != key {
		t.Fatalf("mixed port row = %+v", row.Cells)
	}
}

func TestMixedPortModalSendsOnlyInRangePort(t *testing.T) {
	model, key := mixedPortModel(t)
	model, command, _ := model.HandleKey(key)
	if command != nil || model.Modal == nil || model.Modal.Kind != ModalMixedPort || model.Modal.Input != "7899" {
		t.Fatalf("mixed port key opened %+v and sent %+v", model.Modal, command)
	}
	notice := fmt.Sprintf("Mixed port must be between %d and %d.", core.MixedPortMin, core.MixedPortMax)
	for _, input := range []string{"80", "70000", ""} {
		next, command := typeMixedPort(model, input)
		if command != nil || next.Modal == nil || next.Notice != notice {
			t.Fatalf("mixed port %q sent %+v with notice %q", input, command, next.Notice)
		}
	}
	model, command = typeMixedPort(model, "8080")
	port := uint32(8080)
	if want := configCommand(&ipc.ConfigPatch{MixedPort: &port}); !reflect.DeepEqual(command, want) || model.Modal != nil {
		t.Fatalf("mixed port intent = %+v, modal %+v", command, model.Modal)
	}
	if model.snapshot.Snapshot.MixedPort != 7899 {
		t.Fatal("pending mixed port edit changed the immutable snapshot")
	}
}

func TestMixedPortRowActivationAndMainlandText(t *testing.T) {
	model, _ := mixedPortModel(t)
	model.Selection[TabSettings] = "setting:mixed-port"
	model, _, _ = model.HandleKey("enter")
	if model.Modal == nil || model.Modal.Kind != ModalMixedPort {
		t.Fatalf("activating the mixed port row opened %+v", model.Modal)
	}
	keymap, err := DefaultKeymap()
	if err != nil {
		t.Fatal(err)
	}
	english := keymap.Help(TabSettings)
	localize.SetLanguage("zh-CN")
	t.Cleanup(func() { localize.SetLanguage("en") })
	mainland := keymap.Help(TabSettings)
	key := keymap.KeyFor(TabSettings, "edit_mixed_port")
	for i, entry := range english {
		if len(entry) > len(key) && entry[:len(key)+1] == key+" " && mainland[i] == entry {
			t.Errorf("mixed port help has no Mainland translation: %q", entry)
		}
	}
	if got := mixedPortRangeNotice(); got == fmt.Sprintf("Mixed port must be between %d and %d.", core.MixedPortMin, core.MixedPortMax) {
		t.Fatalf("mixed port range notice is untranslated: %q", got)
	}
}
