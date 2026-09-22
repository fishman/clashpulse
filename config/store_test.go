package config

import (
	"testing"
	"time"
)

func defaultSnapshot() Snapshot {
	return Snapshot{
		Monitor: Monitor{Interval: 5 * time.Minute},
	}
}

func TestReplaceNotifiesOnlyChangedSection(t *testing.T) {
	store := NewStore(defaultSnapshot())
	changed := make(chan Change, 1)
	store.Subscribe("monitor", func(change Change) { changed <- change })

	next := store.Snapshot()
	next.Monitor.Interval = 10 * time.Minute
	store.Replace(next)

	if change := <-changed; change.Section != "monitor" {
		t.Fatalf("section = %q", change.Section)
	}
}
