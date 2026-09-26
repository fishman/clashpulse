package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fishman/clashpulse/ipc"
	"github.com/gdamore/tcell/v3"
)

const (
	commandQueueSize = 8
	commandTimeout   = 8 * time.Second
)

type commandWork struct {
	command ipc.Command
}

type commandResult struct {
	queued  bool
	err     error
	private bool
}

// Run connects to an already-running application service and drives the
// terminal view until the user exits or ctx is canceled. An empty endpoint
// selects ipc.DefaultEndpoint.
func Run(ctx context.Context, endpoint string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if endpoint == "" {
		endpoint = ipc.DefaultEndpoint()
	}
	client, err := ipc.Dial(ctx, endpoint)
	if err != nil {
		return fmt.Errorf("cannot connect to the ClashPulse IPC service at %q: %w (start the application service first)", endpoint, err)
	}
	defer client.Close()

	keymap, err := DefaultKeymap()
	if err != nil {
		return fmt.Errorf("load TUI keybindings: %w", err)
	}
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("cannot read initial snapshot from the ClashPulse IPC service: %w", err)
	}

	screen, err := tcell.NewScreen()
	if err != nil {
		return fmt.Errorf("create terminal screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return fmt.Errorf("initialize terminal screen: %w", err)
	}
	defer screen.Fini()

	model := NewModel(keymap).Apply(ipc.Event{Snapshot: snapshot})
	var cache renderCache
	work := make(chan commandWork, commandQueueSize)
	results := make(chan commandResult, commandQueueSize)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	defer func() { cancelWorker(); <-workerDone }()
	go func() { defer close(workerDone); runCommandWorker(workerCtx, client, work, results) }()

	clientEvents := client.Events()
	render(screen, model, &cache)
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-clientEvents:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return errors.New("ClashPulse IPC connection closed while the TUI was running")
			}
			model = model.Apply(event)
			render(screen, model, &cache)
		case result := <-results:
			model = model.CommandResult(result.queued, result.err, result.private)
			render(screen, model, &cache)
		case event, ok := <-screen.EventQ():
			if !ok {
				return errors.New("terminal event stream closed")
			}
			switch event := event.(type) {
			case *tcell.EventResize:
				screen.Sync()
				cache.width = 0
				render(screen, model, &cache)
			case *tcell.EventKey:
				key := keyForModelEvent(model, event)
				var outgoing *ipc.Command
				var quit bool
				model, outgoing, quit = model.HandleKey(key)
				if quit {
					return nil
				}
				if outgoing != nil {
					model = enqueueCommand(model, work, *outgoing)
				}
				render(screen, model, &cache)
			}
		}
	}
}

func keyForModelEvent(model Model, event *tcell.EventKey) string {
	key := eventKeyName(event)
	if event != nil && model.Modal != nil && event.Key() == tcell.KeyRune && event.Modifiers()&tcell.ModCtrl == 0 && (model.Modal.Form == nil || key != "space") {
		return event.Str()
	}
	return key
}

func enqueueCommand(model Model, work chan commandWork, command ipc.Command) Model {
	select {
	case work <- commandWork{command: command}:
		return model.CommandQueued()
	default:
		return model.QueueFull()
	}
}

func runCommandWorker(ctx context.Context, client *ipc.Client, work <-chan commandWork, results chan<- commandResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-work:
			commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
			ack, err := client.Send(commandCtx, item.command)
			cancel()
			result := commandResult{queued: ack.Queued, err: err, private: commandHasPrivateSource(item.command)}
			select {
			case results <- result:
			case <-ctx.Done():
				return
			}
		}
	}
}

func commandHasPrivateSource(command ipc.Command) bool {
	if command.DNSRouting != nil || command.Config != nil && command.Config.MonitorTestURL != nil {
		return true
	}
	return command.SubscriptionID != "" || command.ResourceID != "" || command.FilterID != "" || command.Subscription != nil || command.Resource != nil || command.Filter != nil
}

func viewTitle(tab Tab) string {
	switch tab {
	case TabOverview:
		return "Overview"
	case TabProxies:
		return "Proxies"
	case TabSubscriptions:
		return "Subscriptions"
	case TabFilters:
		return "Filter Lists"
	case TabResources:
		return "Data Resources"
	case TabSettings:
		return "Settings"
	default:
		return string(tab)
	}
}
