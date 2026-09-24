package tui

import (
	"fmt"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
)

func TestUnrelatedJobEventDoesNotRebuildProxyRows(t *testing.T) {
	names := make([]string, 300)
	proxies := make([]core.ProxySnapshot, len(names))
	for i := range names {
		names[i] = fmt.Sprintf("node-%d", i)
		proxies[i] = core.ProxySnapshot{GroupID: "group", ID: names[i]}
	}
	base := core.Snapshot{Groups: []core.GroupSnapshot{{ID: "group", Type: "Selector", Proxies: names, Selected: names[0]}}, Proxies: proxies}
	model := NewModel().Apply(ipc.Event{Snapshot: base}).selectTab(TabProxies)
	changed := ipc.Event{Snapshot: core.CloneSnapshot(base)}
	changed.Snapshot.Jobs = []core.JobSnapshot{{ID: "job", Kind: "refresh", State: "running"}}
	allocations := testing.AllocsPerRun(5, func() { _ = model.Apply(changed) })
	if allocations > 100 {
		t.Fatalf("unrelated job allocated %.0f objects while proxy rows were unchanged", allocations)
	}
}
