package mihomo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
)

type StartPlan struct {
	Capability Capability
	ConfigPath string
	Args       []string
}

type Process struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
}

// Exited reports the current child's completion; nil means no child is running.
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
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop mihomo: %w", err)
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Process) wait(cmd *exec.Cmd, done chan struct{}) {
	_ = cmd.Wait()

	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd = nil
		p.done = nil
		close(done)
	}
	p.mu.Unlock()
}
