package app

import (
	"context"
	"fmt"

	"github.com/fishman/clashpulse/ipc"
)

func (s *runtimeService) enqueueIntent(cmd ipc.Command) error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	select {
	case s.intents <- cmd:
		if cmd.Kind == ipc.CommandStop {
			s.stopRequested = true
			if s.operationCancel != nil && s.operationKind != ipc.CommandStop {
				s.operationCancel()
			}
		}
		return nil
	default:
		return fmt.Errorf("clashpulse: intent queue is full")
	}
}

func (s *runtimeService) runOperation(ctx context.Context, kind ipc.CommandKind, operation func(context.Context) error) error {
	operationCtx, cancel := context.WithCancel(ctx)
	s.operationMu.Lock()
	s.operationCancel, s.operationKind = cancel, kind
	if s.stopRequested && kind != ipc.CommandStop {
		cancel()
	}
	s.operationMu.Unlock()
	defer func() {
		s.operationMu.Lock()
		s.operationCancel, s.operationKind = nil, ""
		if kind == ipc.CommandStop {
			s.stopRequested = false
		}
		s.operationMu.Unlock()
		cancel()
	}()
	return operation(operationCtx)
}
