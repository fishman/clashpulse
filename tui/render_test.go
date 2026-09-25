package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
)

func TestViewportKeepsSelectedRowVisible(t *testing.T) {
	rows := make([]Row, 30)
	for i := range rows {
		rows[i] = Row{ID: fmt.Sprintf("proxy-%d", i), Selected: i == 25}
	}
	visible := visibleRows(rows, 10)
	if len(visible) != 10 || visible[0].ID == "proxy-0" {
		t.Fatalf("viewport did not scroll: %#v", visible)
	}
	found := false
	for _, row := range visible {
		if row.ID == "proxy-25" && row.Selected {
			found = true
		}
	}
	if !found {
		t.Fatal("selected proxy was invisible")
	}
	first := visibleRows(rows, 0)
	if len(first) != 0 {
		t.Fatalf("zero-height viewport rendered rows: %#v", first)
	}
}

func mockRender(t *testing.T, model Model, width, height int) []string {
	t.Helper()
	terminal := vt.NewMockTerm(vt.MockOptSize{X: vt.Col(width), Y: vt.Row(height)})
	screen, err := tcell.NewTerminfoScreenFromTty(terminal, tcell.OptNegotiation(false), tcell.OptAltScreen(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	render(screen, model, &renderCache{})
	rows := make([]string, height)
	for y := range rows {
		var line strings.Builder
		for x := 0; x < width; x++ {
			line.WriteString(terminal.GetCell(vt.Coord{X: vt.Col(x), Y: vt.Row(y)}).C)
		}
		rows[y] = line.String()
	}
	return rows
}

func TestRenderKeepsActiveTabVisible(t *testing.T) {
	model := NewModel()
	model.Tab = TabSettings
	rows := mockRender(t, model, 24, 9)
	if !strings.Contains(rows[0], "Settings") {
		t.Fatalf("active tab clipped: %q", rows[0])
	}
}

func TestRenderAlignsSettingsDetails(t *testing.T) {
	model := NewModel()
	model.Tab = TabSettings
	rows := mockRender(t, model, 80, 10)
	a, b := strings.Index(rows[2], "desired"), strings.Index(rows[3], "requested")
	if a < 0 || b < 0 || a != b || !strings.Contains(rows[1], "Value") {
		t.Fatalf("settings detail columns drift: header=%q first=%q second=%q", rows[1], rows[2], rows[3])
	}
}

func TestRenderAlignsPendingStatusRight(t *testing.T) {
	model := NewModel()
	model.Pending = 2
	rows := mockRender(t, model, 60, 9)
	if !strings.HasSuffix(rows[8], " sending 2 ") {
		t.Fatalf("pending status is not right aligned: %q", rows[8])
	}
}

func TestRenderStatusIsBottommost(t *testing.T) {
	model := NewModel()
	model.Notice = "Configuration updated"
	rows := mockRender(t, model, 80, 12)
	if !strings.Contains(rows[11], "IPC connected") || !strings.Contains(rows[10], "quit") || !strings.Contains(rows[9], "Configuration updated") {
		t.Fatalf("footer order: notice=%q hotkeys=%q status=%q", rows[9], rows[10], rows[11])
	}
}

func TestRenderTabsOccupyFirstRow(t *testing.T) {
	model := NewModel()
	model.Tab = TabResources
	rows := mockRender(t, model, 80, 12)
	if !strings.Contains(rows[0], "Resources") || strings.Contains(rows[0], "ClashPulse") {
		t.Fatalf("top tab row = %q", rows[0])
	}
	if !strings.Contains(rows[1], "Name") {
		t.Fatalf("table header = %q", rows[1])
	}
}

func TestRenderStatusIdentifiesConnectionAndActiveProfile(t *testing.T) {
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{
		Subscriptions: []core.SubscriptionSnapshot{{ID: "primary", Name: "Primary", Active: true}},
	}})
	model.Pending = 2
	rows := mockRender(t, model, 80, 12)
	if !strings.Contains(rows[11], "IPC connected") || !strings.Contains(rows[11], "Primary") || !strings.Contains(rows[11], "sending 2") {
		t.Fatalf("status segments = %q", rows[11])
	}
	if row := mockRender(t, NewModel(), 80, 12)[11]; !strings.Contains(row, "profile not reported") {
		t.Fatalf("unknown profile was invented: %q", row)
	}
}

func TestRenderCatppuccinMochaPalette(t *testing.T) {
	if styles["normal"].Bg != "#1e1e2e" || styles["normal"].Fg != "#cdd6f4" || styles["tabbar.active"].Bg != "#89b4fa" {
		t.Fatalf("theme = %#v", styles)
	}
}

func TestRenderResourceTableAlignsAndUsesSourceHost(t *testing.T) {
	model := NewModel()
	model.Tab = TabResources
	model = model.Apply(ipc.Event{Snapshot: core.Snapshot{Resources: []core.ResourceSnapshot{
		{ID: "geo", Kind: "geosite.dat", Format: "dat", SourceHost: "mirror.example", Enabled: true, Validated: true},
	}}})
	wide := mockRender(t, model, 120, 12)
	for _, label := range []string{"Name", "Type", "Format", "Source", "Enabled", "Validated"} {
		if !strings.Contains(wide[1], label) {
			t.Fatalf("missing %s: %q", label, wide[1])
		}
	}
	if !strings.Contains(wide[2], "mirror.example") || !strings.Contains(wide[2], "geosite.dat") {
		t.Fatalf("resource cells = %q", wide[2])
	}
	narrow := mockRender(t, model, 30, 12)
	if !strings.Contains(narrow[1], "Name") || !strings.Contains(narrow[1], "Enabled") || strings.Contains(narrow[1], "Source") || !strings.Contains(narrow[2], "enabled") {
		t.Fatalf("narrow resource columns = %q, row = %q", narrow[1], narrow[2])
	}
}

func TestRenderTabTables(t *testing.T) {
	cases := []struct {
		tab      Tab
		snapshot core.Snapshot
		header   string
		row      string
	}{
		{TabSubscriptions, core.Snapshot{Subscriptions: []core.SubscriptionSnapshot{{ID: "primary", Name: "Primary", SourceHost: "provider.example", Enabled: true}}}, "Source", "provider.example"},
		{TabFilters, core.Snapshot{Filters: []core.FilterSnapshot{{ID: "ads", Format: "yaml", Target: "REJECT", SourceHost: "rules.example", Enabled: true}}}, "Target", "REJECT"},
		{TabProxies, core.Snapshot{Groups: []core.GroupSnapshot{{ID: "main", Label: "Main", Selected: "alpha", Proxies: []string{"alpha"}}}, Proxies: []core.ProxySnapshot{{GroupID: "main", ID: "alpha", Outcome: "success", LatencyMillis: 45}}}, "Latency", "alpha"},
		{TabSettings, core.Snapshot{Binary: core.BinarySnapshot{Desired: "system"}}, "Value", "system"},
	}
	for _, tc := range cases {
		t.Run(string(tc.tab), func(t *testing.T) {
			model := NewModel()
			model.Tab = tc.tab
			model = model.Apply(ipc.Event{Snapshot: tc.snapshot})
			lines := mockRender(t, model, 120, 12)
			if !strings.Contains(lines[1], tc.header) || !strings.Contains(strings.Join(lines[2:8], " "), tc.row) {
				t.Fatalf("tab %s: header %q, rows %q", tc.tab, lines[1], lines[2:8])
			}
		})
	}
}

func TestRenderNarrowTable(t *testing.T) {
	model := NewModel()
	model.Tab = TabResources
	model = model.Apply(ipc.Event{Snapshot: core.Snapshot{Resources: []core.ResourceSnapshot{
		{ID: "geo-active", Kind: "geosite.dat", SourceHost: "mirror.example", Enabled: true},
	}}})
	rows := mockRender(t, model, 20, 12)
	if !strings.Contains(rows[1], "Name") || strings.Contains(rows[1], "Source") || !strings.Contains(rows[2], "geo-active") || !strings.Contains(rows[8], "geosite.dat") {
		t.Fatalf("narrow table lost identity or selected detail: header %q, row %q, detail %q", rows[1], rows[2], rows[8])
	}
}
