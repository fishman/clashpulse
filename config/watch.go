package config

import (
	"context"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const watchDebounce = 50 * time.Millisecond

type dirWatcher interface {
	Add(string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

type fsWatcher struct {
	*fsnotify.Watcher
}

func (w *fsWatcher) Events() <-chan fsnotify.Event { return w.Watcher.Events }
func (w *fsWatcher) Errors() <-chan error          { return w.Watcher.Errors }

var newWatcher = func() (dirWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &fsWatcher{Watcher: watcher}, nil
}

var loadSnapshotFn = Load

func Watch(ctx context.Context, dir string, store *Store) error {
	if store == nil {
		return nil
	}
	watcher, err := newWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := watcher.Add(dir); err != nil {
		return err
	}

	snap, err := loadSnapshotFn(dir)
	if err != nil {
		return err
	}
	store.Replace(snap)

	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	reloadRequests := make(chan struct{}, 1)
	go watchReloadWorker(workerCtx, dir, store, reloadRequests)

	var timer *time.Timer
	var timerC <-chan time.Time
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timerC = nil
	}
	scheduleReload := func() {
		if timer == nil {
			timer = time.NewTimer(watchDebounce)
			timerC = timer.C
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(watchDebounce)
	}
	triggerReload := func() {
		select {
		case reloadRequests <- struct{}{}:
		default:
		}
	}

	for {
		select {
		case <-ctx.Done():
			stopTimer()
			return nil
		case err, ok := <-watcher.Errors():
			if !ok {
				stopTimer()
				return nil
			}
			if err != nil {
				stopTimer()
				return err
			}
		case event, ok := <-watcher.Events():
			if !ok {
				stopTimer()
				return nil
			}
			if watchedConfigFile(filepath.Base(event.Name)) {
				scheduleReload()
			}
		case <-timerC:
			stopTimer()
			triggerReload()
		}
	}
}

func watchReloadWorker(ctx context.Context, dir string, store *Store, reloadRequests <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-reloadRequests:
			snap, err := loadSnapshotFn(dir)
			if err != nil {
				continue
			}
			store.Replace(snap)
		}
	}
}

func watchedConfigFile(name string) bool {
	switch name {
	case "config.toml", "subscriptions.toml", "resources.toml", "filters.toml":
		return true
	default:
		return false
	}
}
