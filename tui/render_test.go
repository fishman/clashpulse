package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/notmutt/lib/tui/form"
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

func TestSettingsTableAlignedWithoutPipes(t *testing.T) {
	model := NewModel()
	model.Tab = TabSettings
	model = model.Apply(ipc.Event{Snapshot: core.Snapshot{
		Binary:      core.BinarySnapshot{Desired: "system", ObservedVersion: "v1"},
		SystemProxy: core.SystemProxySnapshot{Enabled: true, Active: true},
	}})
	wide := mockRender(t, model, 100, 12)
	heading := wide[1]
	a, b, c := strings.Index(heading, "Setting"), strings.Index(heading, "Value"), strings.Index(heading, "Action")
	if a < 0 || b <= a || c <= b || !strings.Contains(wide[2], "system") || !strings.Contains(wide[3], "requested") || !strings.Contains(wide[3], "active") || strings.Contains(strings.Join(wide[1:9], ""), " | ") {
		t.Fatalf("unaligned Settings: %q", wide[1:9])
	}
	narrow := mockRender(t, model, 30, 12)
	if !strings.Contains(narrow[1], "Setting") || !strings.Contains(narrow[1], "Value") || strings.Contains(narrow[1], "Action") {
		t.Fatalf("narrow Settings lost its value column: %q", narrow[1])
	}
}

func TestSettingsActionUsesKeymap(t *testing.T) {
	keys, err := NewKeymap([]byte("[schemes.default.settings]\n\"v\" = { fun = \"edit_binary\", desc = \"choose Mihomo\", show = true }\n"))
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(keys)
	model.Tab = TabSettings
	model = model.Apply(ipc.Event{Snapshot: core.Snapshot{Binary: core.BinarySnapshot{Desired: "system"}}})
	for _, row := range model.Rows() {
		if row.ID == "setting:binary" {
			if len(row.Cells) < 3 || row.Cells[2] != "v" {
				t.Fatalf("binary action did not follow keymap: %#v", row.Cells)
			}
			return
		}
	}
	t.Fatal("binary setting missing")
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
	if !strings.Contains(rows[11], "IPC connected") || strings.TrimSpace(rows[10]) == "" || !strings.Contains(rows[9], "Configuration updated") {
		t.Fatalf("footer order: notice=%q hotkeys=%q status=%q", rows[9], rows[10], rows[11])
	}
}

func TestRenderMonitorPolicyTitle(t *testing.T) {
	model := NewModel().selectTab(TabSettings)
	model, _, _ = model.HandleKey("i")
	rows := strings.Join(mockRender(t, model, 80, 20), "\n")
	if !strings.Contains(rows, "Monitor policy") || strings.Contains(rows, "setting settings") {
		t.Fatalf("monitor form title is unclear: %q", rows)
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

func TestRenderResourceDoesNotRepeatTableDetails(t *testing.T) {
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Resources: []core.ResourceSnapshot{{
		ID: "cn", Kind: "rule-set", Format: "mrs", RuleType: "domain", SourceHost: "raw.githubusercontent.com",
		Enabled: true, Validated: false, LastResult: "needs attention",
	}}}}).selectTab(TabResources)
	rows := mockRender(t, model, 140, 12)
	if !strings.Contains(rows[2], "rule-set") || !strings.Contains(rows[2], "mrs") || !strings.Contains(rows[2], "raw.github") {
		t.Fatalf("resource row missing table values: %q", rows[2])
	}
	if strings.Contains(rows[8], "rule-set") || strings.Contains(rows[8], "raw.githubusercontent.com") {
		t.Fatalf("resource details repeated below the table: %q", rows[8])
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

func TestRenderLogKeepsBottomStatus(t *testing.T) {
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Diagnostics: []core.DiagnosticSnapshot{
		{At: 100, Severity: "error", Kind: "subscription", SourceID: "feed", Message: "HTTP 406"},
		{At: 101, Severity: "info", Kind: "subscription", SourceID: "feed", Message: "recovered"},
	}}})
	model, _, _ = model.HandleKey("~")
	for _, size := range []struct{ width, height int }{{80, 12}, {32, 9}} {
		rows := mockRender(t, model, size.width, size.height)
		text := strings.Join(rows[:size.height-1], "\n")
		if !strings.Contains(text, "HTTP 406") || !strings.Contains(text, "recovered") || !strings.Contains(text, "feed") {
			t.Fatalf("%d-column activity lost event details: %q", size.width, text)
		}
		if !strings.Contains(rows[size.height-1], "IPC connected") {
			t.Fatalf("%d-column activity covered connection status: %q", size.width, rows[size.height-1])
		}
	}
}

func TestRenderShortNarrowLogShowsLatestMessage(t *testing.T) {
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Diagnostics: []core.DiagnosticSnapshot{{At: 100, Severity: "error", Kind: "subscription", SourceID: "feed", Message: "HTTP 406"}}}})
	model, _, _ = model.HandleKey("~")
	rows := mockRender(t, model, 30, 6)
	if !strings.Contains(rows[2], "HTTP 406") || strings.Contains(rows[2], "No session diagnostics") || !strings.Contains(rows[5], "IPC connected") {
		t.Fatalf("short log lost the reported error: %q", rows)
	}
}

func TestRenderFiveRowLogKeepsLatestDiagnostic(t *testing.T) {
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Diagnostics: []core.DiagnosticSnapshot{{At: 100, Severity: "error", Kind: "subscription", SourceID: "feed", Message: "HTTP 406"}}}})
	model, _, _ = model.HandleKey("~")
	rows := mockRender(t, model, 30, 5)
	if !strings.Contains(rows[2], "HTTP 406") {
		t.Fatalf("five-row log lost the latest message: %q", rows)
	}
}

func TestRenderHelpOverlayKeepsBottomStatus(t *testing.T) {
	model := NewModel().selectTab(TabSettings)
	model, _, _ = model.HandleKey("?")
	for _, size := range []struct{ width, height int }{{80, 14}, {32, 9}} {
		rows := mockRender(t, model, size.width, size.height)
		text := strings.Join(rows[:size.height-1], "\n")
		if !strings.Contains(text, "Keyboard help") || !strings.Contains(text, "close help") {
			t.Fatalf("%d-column help is missing: %q", size.width, text)
		}
		if !strings.Contains(rows[size.height-1], "IPC connected") {
			t.Fatalf("help covered bottom status: %q", rows[size.height-1])
		}
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
	if !strings.Contains(rows[1], "Name") || strings.Contains(rows[1], "Source") || !strings.Contains(rows[2], "geo-active") || strings.TrimSpace(rows[8]) != "" {
		t.Fatalf("narrow table repeated resource details: header %q, row %q, footer %q", rows[1], rows[2], rows[8])
	}
}

func TestSubscriptionFormMasksInputsAndKeepsFooter(t *testing.T) {
	fields, err := form.New([]form.Field{
		{ID: "url", Label: "Source URL", Kind: form.Text, Sensitive: true},
		{ID: "enabled", Label: "Enabled", Kind: form.Toggle, Value: "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fields.SetText("https://private.invalid/?token=secret")
	model := NewModel()
	model.Focus = FocusModal
	model.Modal = &Modal{Kind: ModalSubscription, Form: fields}
	wide := mockRender(t, model, 80, 16)
	frame := strings.Join(wide, "\n")
	if !strings.Contains(frame, "\u256d") || !strings.Contains(frame, "\u2570") ||
		!strings.Contains(frame, "[hidden]") || strings.Contains(frame, "private.invalid") ||
		!strings.Contains(wide[14], "space toggle field") || !strings.Contains(wide[15], "IPC connected") {
		t.Fatal("private form, border, or footer missing")
	}
	small := strings.Join(mockRender(t, model, 20, 5), "\n")
	if strings.Contains(small, "\u256d") || model.Modal == nil || model.Focus != FocusModal {
		t.Fatal("too-small modal overwrote footer or lost focus")
	}
}

func TestOverviewRendersIssueSourceIDs(t *testing.T) {
	model := NewModel().Apply(ipc.Event{Snapshot: core.Snapshot{Errors: []core.ErrorSnapshot{
		{Kind: "refresh_subscription", Key: "subscription", SourceID: "feed-a", Message: "HTTP 406"},
		{Kind: "refresh_subscription", Key: "subscription", SourceID: "feed-b", Message: "HTTP 429"},
	}}})
	rows := mockRender(t, model, 120, 12)
	text := strings.Join(rows, "\n")
	for _, detail := range []string{"feed-a", "HTTP 406", "feed-b", "HTTP 429"} {
		if !strings.Contains(text, detail) {
			t.Fatalf("terminal issue detail %q absent from %q", detail, rows)
		}
	}
}
