package core

type GroupSnapshot struct {
	ID       string
	Selected string
}

type Snapshot struct {
	Revision uint64
	Groups   []GroupSnapshot
}

func NewSnapshot(groups []GroupSnapshot) Snapshot {
	return Snapshot{Groups: append([]GroupSnapshot(nil), groups...)}
}
