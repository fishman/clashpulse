package tui

import (
	"fmt"
	"testing"
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
