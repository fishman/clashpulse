package config

import (
	"context"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const watchDebounce = 50 * time.Millisecond

func Watch(ctx context.Context, dir string, store *Store) error {
	if store == nil {
		return nil
	}
	loaded, err := Load(dir)
	if err != nil {
		return err
	}
	store.Replace(loaded)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := watcher.Add(dir); err != nil {
		return err
	}

	var timer *time.Timer
	var timerC <-chan time.Time
	schedule := func() {
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
	reload := func() {
		next, err := Load(dir)
		if err != nil {
			return
		}
		store.Replace(next)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-watcher.Errors:
			if err != nil {
				return err
			}
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if watchedConfigFile(filepath.Base(event.Name)) {
				schedule()
			}
		case <-timerC:
			if timer != nil {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer = nil
				timerC = nil
			}
			reload()
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
