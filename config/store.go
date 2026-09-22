package config

import (
	"reflect"
	"sync"
)

type Store struct {
	mu     sync.RWMutex
	snap   Snapshot
	subs   map[string]map[uint64]func(Change)
	nextID uint64
}

func NewStore(snapshot Snapshot) *Store {
	return &Store{snap: cloneSnapshot(snapshot), subs: map[string]map[uint64]func(Change){}}
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSnapshot(s.snap)
}

func (s *Store) Subscribe(section string, fn func(Change)) func() {
	if fn == nil {
		return func() {}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs == nil {
		s.subs = map[string]map[uint64]func(Change){}
	}
	id := s.nextID
	s.nextID++
	bucket := s.subs[section]
	if bucket == nil {
		bucket = map[uint64]func(Change){}
		s.subs[section] = bucket
	}
	bucket[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if bucket := s.subs[section]; bucket != nil {
			delete(bucket, id)
			if len(bucket) == 0 {
				delete(s.subs, section)
			}
		}
	}
}

func (s *Store) Replace(next Snapshot) Change {
	candidate := cloneSnapshot(next)

	s.mu.Lock()
	old := s.snap
	changed := diffSections(old, candidate)
	if len(changed) == 0 {
		s.mu.Unlock()
		return Change{}
	}
	callbackBuckets := make(map[string][]func(Change), len(changed))
	for _, section := range changed {
		if bucket := s.subs[section]; len(bucket) > 0 {
			for _, fn := range bucket {
				callbackBuckets[section] = append(callbackBuckets[section], fn)
			}
		}
	}
	s.snap = candidate
	s.mu.Unlock()

	change := Change{
		Section:  changed[0],
		Sections: append([]string(nil), changed...),
		Before:   cloneSnapshot(old),
		After:    cloneSnapshot(candidate),
	}
	for _, section := range changed {
		event := change
		event.Section = section
		for _, fn := range callbackBuckets[section] {
			fn(event)
		}
	}
	return change
}

func diffSections(old, next Snapshot) []string {
	var changed []string
	if !reflect.DeepEqual(old.App, next.App) {
		changed = append(changed, "app")
	}
	if !reflect.DeepEqual(old.Mihomo, next.Mihomo) {
		changed = append(changed, "mihomo")
	}
	if !reflect.DeepEqual(old.Monitor, next.Monitor) {
		changed = append(changed, "monitor")
	}
	if !reflect.DeepEqual(old.DNS, next.DNS) {
		changed = append(changed, "dns")
	}
	if !reflect.DeepEqual(old.Subscriptions, next.Subscriptions) {
		changed = append(changed, "subscriptions")
	}
	if !reflect.DeepEqual(old.Resources, next.Resources) {
		changed = append(changed, "resources")
	}
	if !reflect.DeepEqual(old.Filters, next.Filters) {
		changed = append(changed, "filters")
	}
	return changed
}
