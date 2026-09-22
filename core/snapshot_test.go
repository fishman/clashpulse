package core

import "testing"

func TestSnapshotCopiesSlices(t *testing.T) {
	groups := []GroupSnapshot{{ID: "auto", Selected: "alpha"}}
	snapshot := NewSnapshot(groups)
	groups[0].Selected = "beta"
	if snapshot.Groups[0].Selected != "alpha" {
		t.Fatal("snapshot retained caller-owned memory")
	}
}
