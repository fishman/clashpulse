package tui

import (
	"fmt"
	"strings"
	"testing"

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
	if !strings.Contains(rows[1], "Settings") {
		t.Fatalf("active tab clipped: %q", rows[1])
	}
}

func TestRenderAlignsSettingsDetails(t *testing.T) {
	model := NewModel()
	model.Tab = TabSettings
	rows := mockRender(t, model, 80, 10)
	a, b := strings.Index(rows[3], "desired"), strings.Index(rows[4], "requested")
	if a < 0 || b < 0 || a != b || !strings.Contains(rows[2], "Details") {
		t.Fatalf("settings detail columns drift: header=%q first=%q second=%q", rows[2], rows[3], rows[4])
	}
}

func TestRenderAlignsPendingStatusRight(t *testing.T) {
	model := NewModel()
	model.Pending = 2
	rows := mockRender(t, model, 60, 9)
	if !strings.HasSuffix(rows[6], " sending 2 ") {
		t.Fatalf("pending status is not right aligned: %q", rows[6])
	}
}
