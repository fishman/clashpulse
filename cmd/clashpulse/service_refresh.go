package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/ipc"
	"github.com/fishman/clashpulse/localize"
)

// serviceRefresh hands a refresh to an already-running service, which owns the
// private state the offline path would need to lock. It reports handled=false
// when no service answers, so the caller runs the offline path instead.
func serviceRefresh(ctx context.Context, endpoint, kind, id string, showResponse bool) (bool, error) {
	probe, stop := context.WithTimeout(ctx, time.Second)
	client, err := ipc.Dial(probe, endpoint)
	stop()
	if err != nil {
		return false, nil
	}
	defer client.Close()
	if showResponse {
		return true, errors.New(localize.T("clashpulse: --show-response needs the service stopped; HTTP response bodies never leave it"))
	}
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		return true, err
	}
	commands, err := refreshCommands(snapshot, kind, id)
	if err != nil {
		return true, err
	}
	var failures []error
	// One at a time keeps the service's bounded intent queue free for the GUI.
	for _, command := range commands {
		ack, err := client.Send(ctx, command)
		if err != nil {
			return true, err
		}
		state, snapshot, err := waitFinished(ctx, client, ack.JobID)
		if err != nil {
			return true, err
		}
		if state != "succeeded" {
			failures = append(failures, refreshFailure(snapshot, command, state))
		}
	}
	return true, errors.Join(failures...)
}

func refreshCommands(snapshot core.Snapshot, kind, id string) ([]ipc.Command, error) {
	var commands []ipc.Command
	if kind == "" || kind == "subscription" {
		for _, item := range snapshot.Subscriptions {
			if id == "" && item.Enabled || id != "" && item.ID == id {
				commands = append(commands, ipc.Command{Kind: ipc.CommandRefreshSubscription, SubscriptionID: item.ID})
			}
		}
	}
	if kind == "" || kind == "resource" {
		for _, item := range snapshot.Resources {
			if id == "" && item.Enabled || id != "" && item.ID == id {
				commands = append(commands, ipc.Command{Kind: ipc.CommandRefreshResource, ResourceID: item.ID})
			}
		}
	}
	if id != "" && len(commands) == 0 {
		return nil, fmt.Errorf(localize.T("clashpulse: %s %q is not configured"), localize.T(kind), id)
	}
	return commands, nil
}

func waitFinished(ctx context.Context, client *ipc.Client, jobID string) (string, core.Snapshot, error) {
	snapshot, err := client.Snapshot(ctx)
	for {
		if err != nil {
			return "", core.Snapshot{}, err
		}
		for _, job := range snapshot.FinishedJobs {
			if job.ID == jobID {
				return job.State, snapshot, nil
			}
		}
		select {
		case event, ok := <-client.Events():
			if !ok {
				return "", core.Snapshot{}, errors.New(localize.T("clashpulse: the service closed the connection"))
			}
			snapshot = event.Snapshot
		case <-ctx.Done():
			return "", core.Snapshot{}, ctx.Err()
		}
	}
}

func refreshFailure(snapshot core.Snapshot, command ipc.Command, state string) error {
	kind, id, reason, kept := "resource", command.ResourceID, "", ""
	if command.SubscriptionID != "" {
		kind, id = "subscription", command.SubscriptionID
		for _, item := range snapshot.Subscriptions {
			if item.ID == id {
				reason, kept = item.LastFailure, item.HashPrefix
			}
		}
	} else {
		for _, item := range snapshot.Resources {
			if item.ID == id && item.LastResult != "" && item.LastCheck > item.LastSuccess {
				reason, kept = item.LastResult, item.HashPrefix
			}
		}
	}
	if reason == "" {
		reason = "operation " + state
		for _, item := range snapshot.Errors {
			if item.Kind == string(command.Kind) && item.SourceID == id {
				reason = item.Message
			}
		}
	}
	message := fmt.Sprintf("%s %s: %s", localize.T(kind), id, localize.T(reason))
	if kept != "" {
		message += "; " + fmt.Sprintf(localize.T("kept last good version %s"), kept)
	}
	return errors.New(message)
}
