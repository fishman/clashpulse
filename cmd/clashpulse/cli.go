package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
	"github.com/urfave/cli/v3"
)

type cliActions struct {
	run          func(context.Context) error
	desktop      func(context.Context, func(context.Context) error) error
	tui          func(context.Context) error
	refresh      func(context.Context, string, string, bool) error
	download     func(context.Context, string, bool) error
	activateFile func(context.Context, string, func() error) error
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer, actions cliActions) int {
	command := newCLICommand(stdout, stderr, actions)
	if err := command.Run(ctx, args); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newCLICommand(stdout, stderr io.Writer, actions cliActions) *cli.Command {
	refresh := &cli.Command{
		Name: "refresh", Usage: "refresh subscriptions and resources, then validate generated config",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "show-response", Usage: "print bounded HTTP error response text"}},
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Len() != 0 {
				return errors.New("unexpected refresh arguments")
			}
			return runRefreshAction(ctx, command.Writer, actions.refresh, "", "", command.Bool("show-response"))
		},
		Commands: []*cli.Command{
			refreshKindCommand("subscription", actions.refresh),
			refreshKindCommand("resource", actions.refresh),
		},
	}
	download := &cli.Command{
		Name: "download", Usage: "download and parse subscription profiles without running Mihomo",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "show-response", Usage: "print bounded HTTP error response text"}},
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Len() != 0 {
				return errors.New("download accepts subscription profiles only")
			}
			return runDownloadAction(ctx, command.Writer, actions.download, "", command.Bool("show-response"))
		},
		Commands: []*cli.Command{downloadSubscriptionCommand(actions.download)},
	}
	return &cli.Command{
		Name:        "clashpulse",
		Usage:       "desktop Mihomo proxy manager",
		Version:     version,
		HideVersion: true,
		Writer:      stdout,
		ErrWriter:   stderr,
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Len() != 0 {
				return runApplicationAction(ctx, actions)
			}
			return runDesktopAction(ctx, actions)
		},
		Commands: []*cli.Command{
			{Name: "version", Usage: "print version", Action: func(ctx context.Context, command *cli.Command) error {
				if command.Args().Len() != 0 {
					return runApplicationAction(ctx, actions)
				}
				_, err := fmt.Fprintln(command.Writer, version)
				return err
			}},
			{Name: "gui", Usage: "run the desktop GUI", Action: func(ctx context.Context, command *cli.Command) error {
				if command.Args().Len() != 0 {
					return runApplicationAction(ctx, actions)
				}
				return runDesktopAction(ctx, actions)
			}},
			{Name: "tui", Usage: "connect the terminal UI to the desktop service", Action: func(ctx context.Context, command *cli.Command) error {
				if command.Args().Len() != 0 {
					return runApplicationAction(ctx, actions)
				}
				if actions.tui == nil {
					return errors.New("clashpulse: TUI is unavailable")
				}
				return actions.tui(ctx)
			}},
			{Name: "activate", Usage: "run a local profile until interrupted", ArgsUsage: "<profile.yaml>", SkipFlagParsing: true, Arguments: []cli.Argument{&cli.StringArgs{Name: "path", Min: 1, Max: 1}}, Action: func(ctx context.Context, command *cli.Command) error {
				paths := command.StringArgs("path")
				if command.Args().Len() != 0 || len(paths) != 1 {
					return errors.New("activate requires one file")
				}
				if actions.activateFile == nil {
					return errors.New("clashpulse: activation is unavailable")
				}
				err := actions.activateFile(ctx, paths[0], func() error {
					_, writeErr := fmt.Fprintln(command.Root().Writer, "local profile active; press Ctrl-C to stop")
					return writeErr
				})
				if err != nil {
					if public, ok := core.PublicActivation(err); ok {
						return public
					}
					return core.WrapActivation(core.ActivationStateCommit, err)
				}
				return nil
			}},
			refresh,
			download,
		},
	}
}

func runDesktopAction(ctx context.Context, actions cliActions) error {
	if actions.desktop != nil {
		return actions.desktop(ctx, actions.run)
	}
	return runApplicationAction(ctx, actions)
}

func runApplicationAction(ctx context.Context, actions cliActions) error {
	if actions.run == nil {
		return errors.New("clashpulse: application service is unavailable")
	}
	return actions.run(ctx)
}

func refreshKindCommand(kind string, refresh func(context.Context, string, string, bool) error) *cli.Command {
	return &cli.Command{
		Name:      kind,
		Usage:     "refresh all enabled " + kind + " sources, or one source by ID",
		ArgsUsage: "[id]",
		Arguments: []cli.Argument{&cli.StringArgs{Name: "id", Min: 0, Max: 1}},
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Len() != 0 {
				return errors.New("too many refresh arguments")
			}
			args := command.StringArgs("id")
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			return runRefreshAction(ctx, command.Root().Writer, refresh, kind, id, command.Bool("show-response"))
		},
	}
}

func downloadSubscriptionCommand(download func(context.Context, string, bool) error) *cli.Command {
	return &cli.Command{
		Name:      "subscription",
		Usage:     "download all enabled subscription profiles, or one profile by ID",
		ArgsUsage: "[id]",
		Arguments: []cli.Argument{&cli.StringArgs{Name: "id", Min: 0, Max: 1}},
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Len() != 0 {
				return errors.New("too many download arguments")
			}
			args := command.StringArgs("id")
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			return runDownloadAction(ctx, command.Root().Writer, download, id, command.Bool("show-response"))
		},
	}
}

func runRefreshAction(ctx context.Context, stdout io.Writer, refresh func(context.Context, string, string, bool) error, kind, id string, showResponse bool) error {
	if refresh == nil {
		return errors.New("clashpulse: refresh is unavailable")
	}
	if err := refresh(ctx, kind, id, showResponse); err != nil {
		if showResponse {
			if response, ok := download.HTTPResponseFrom(err); ok {
				return fmt.Errorf("%w\n%s response body (bounded):\n%s", err, responseStatus(response), responseBody(response.ResponseBody))
			}
		}
		return err
	}
	result := "all enabled sources"
	if kind != "" && id == "" {
		result = "all enabled " + kind + "s"
	} else if id != "" {
		result = kind + " " + id
	}
	_, err := fmt.Fprintf(stdout, "refreshed %s\n", result)
	return err
}

func runDownloadAction(ctx context.Context, stdout io.Writer, downloadProfile func(context.Context, string, bool) error, id string, showResponse bool) error {
	if downloadProfile == nil {
		return errors.New("clashpulse: subscription download is unavailable")
	}
	if err := downloadProfile(ctx, id, showResponse); err != nil {
		if showResponse {
			if response, ok := download.HTTPResponseFrom(err); ok {
				return fmt.Errorf("%w\n%s response body (bounded):\n%s", err, responseStatus(response), responseBody(response.ResponseBody))
			}
		}
		return err
	}
	result := "all enabled subscriptions"
	if id != "" {
		result = "subscription " + id
	}
	_, err := fmt.Fprintf(stdout, "downloaded %s\n", result)
	return err
}

func responseStatus(response download.StatusError) string {
	if reason := http.StatusText(response.Code); reason != "" {
		return fmt.Sprintf("HTTP %d %s", response.Code, reason)
	}
	return fmt.Sprintf("HTTP %d", response.Code)
}

func responseBody(body string) string {
	if body == "" {
		return "<empty>"
	}
	return body
}
