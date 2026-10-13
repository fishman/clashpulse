package mihomo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

type StartPlan struct {
	Capability Capability
	ConfigPath string
	Args       []string
}

// stopGrace lets Mihomo remove its TUN routes, ip rules, and nftables state
// after SIGTERM before it is killed; it stays shorter than the 5s cleanup
// deadlines that wrap Stop so the kill always happens inside them.
const stopGrace = 3 * time.Second

type Process struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
	// grace overrides stopGrace when positive.
	grace time.Duration
}

// Exited reports the latest child's completion; nil means no child has started.
func (p *Process) Exited() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

func (p *Process) Start(ctx context.Context, plan StartPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if plan.Capability.Path == "" {
		return fmt.Errorf("start mihomo: empty binary path")
	}
	if plan.ConfigPath == "" {
		return fmt.Errorf("start mihomo: empty config path")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		return fmt.Errorf("start mihomo: already running")
	}

	argv := make([]string, 0, len(plan.Args)+2)
	argv = append(argv, "-f", plan.ConfigPath)
	argv = append(argv, plan.Args...)
	cmd := exec.CommandContext(ctx, plan.Capability.Path, argv...)
	cmd.Cancel = func() error { return terminate(cmd.Process) }
	cmd.WaitDelay = p.gracePeriod()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mihomo: %w", err)
	}

	done := make(chan struct{})
	p.cmd = cmd
	p.done = done
	go p.wait(cmd, done)
	return nil
}

func (p *Process) Stop(ctx context.Context) error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	if cmd == nil {
		return nil
	}
	if err := terminate(cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop mihomo: %w", err)
	}
	grace := time.NewTimer(p.gracePeriod())
	defer grace.Stop()
	var canceled error
	select {
	case <-done:
		return nil
	case <-grace.C:
	case <-ctx.Done():
		canceled = ctx.Err()
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop mihomo: %w", err)
	}
	if canceled != nil {
		return canceled
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Process) gracePeriod() time.Duration {
	if p.grace > 0 {
		return p.grace
	}
	return stopGrace
}

func (p *Process) wait(cmd *exec.Cmd, done chan struct{}) {
	_ = cmd.Wait()

	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd = nil
		close(done)
	}
	p.mu.Unlock()
}
