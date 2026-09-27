package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/download"
	"github.com/fishman/clashpulse/localize"
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
	helpMode := len(args) > 1 && args[1] == "help"
	if len(args) > 2 && (args[1] == "refresh" || args[1] == "download") && args[2] == "help" {
		helpMode = true
	}
	if len(args) > 1 && args[1] != "activate" {
		for _, value := range args[1:] {
			if value == "--help" || value == "-h" {
				helpMode = true
				break
			}
		}
	}
	var help bytes.Buffer
	writer := stdout
	if helpMode {
		writer = &help
	}
	command := newCLICommand(writer, stderr, actions)
	err := command.Run(ctx, args)
	if helpMode {
		_, _ = io.WriteString(stdout, localizeHelpOutput(help.String()))
	}
	if err != nil {
		if public, ok := core.PublicActivation(err); ok {
			message := localize.T(public.Stage.Message())
			if public.ResourceID != "" {
				message += fmt.Sprintf(localize.T(" (resource %s)"), public.ResourceID)
			}
			_, _ = fmt.Fprintln(stderr, message)
		} else {
			_, _ = fmt.Fprintln(stderr, err)
		}
		return 1
	}
	return 0
}

func newCLICommand(stdout, stderr io.Writer, actions cliActions) *cli.Command {
	refresh := &cli.Command{
		Name: "refresh", Usage: localize.T("refresh subscriptions and resources, then validate generated config"),
		Flags: []cli.Flag{&cli.BoolFlag{Name: "show-response", Usage: localize.T("print bounded HTTP error response text")}},
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Len() != 0 {
				return errors.New(localize.T("unexpected refresh arguments"))
			}
			return runRefreshAction(ctx, command.Writer, actions.refresh, "", "", command.Bool("show-response"))
		},
		Commands: []*cli.Command{
			refreshKindCommand("subscription", actions.refresh),
			refreshKindCommand("resource", actions.refresh),
		},
	}
	download := &cli.Command{
		Name: "download", Usage: localize.T("download and parse subscription profiles without running Mihomo"),
		Flags: []cli.Flag{&cli.BoolFlag{Name: "show-response", Usage: localize.T("print bounded HTTP error response text")}},
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Len() != 0 {
				return errors.New(localize.T("download accepts subscription profiles only"))
			}
			return runDownloadAction(ctx, command.Writer, actions.download, "", command.Bool("show-response"))
		},
		Commands: []*cli.Command{downloadSubscriptionCommand(actions.download)},
	}
	root := &cli.Command{
		Name:        "clashpulse",
		Usage:       localize.T("desktop Mihomo proxy manager"),
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
			{Name: "version", Usage: localize.T("print version"), Action: func(ctx context.Context, command *cli.Command) error {
				if command.Args().Len() != 0 {
					return runApplicationAction(ctx, actions)
				}
				_, err := fmt.Fprintln(command.Writer, version)
				return err
			}},
			{Name: "gui", Usage: localize.T("run the desktop GUI"), Action: func(ctx context.Context, command *cli.Command) error {
				if command.Args().Len() != 0 {
					return runApplicationAction(ctx, actions)
				}
				return runDesktopAction(ctx, actions)
			}},
			{Name: "tui", Usage: localize.T("connect the terminal UI to the desktop service"), Action: func(ctx context.Context, command *cli.Command) error {
				if command.Args().Len() != 0 {
					return runApplicationAction(ctx, actions)
				}
				if actions.tui == nil {
					return errors.New(localize.T("clashpulse: TUI is unavailable"))
				}
				return actions.tui(ctx)
			}},
			{Name: "activate", Usage: localize.T("run a local profile until interrupted"), ArgsUsage: "<profile.yaml>", SkipFlagParsing: true, Arguments: []cli.Argument{&cli.StringArgs{Name: "path", Min: 1, Max: 1}}, Action: func(ctx context.Context, command *cli.Command) error {
				paths := command.StringArgs("path")
				if command.Args().Len() != 0 || len(paths) != 1 {
					return errors.New(localize.T("activate requires one file"))
				}
				if actions.activateFile == nil {
					return errors.New(localize.T("clashpulse: activation is unavailable"))
				}
				err := actions.activateFile(ctx, paths[0], func() error {
					_, writeErr := fmt.Fprintln(command.Root().Writer, localize.T("local profile active; press Ctrl-C to stop"))
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
	return root
}

func localizeHelpOutput(text string) string {
	for _, message := range [...]string{"Shows a list of commands or help for one command", "show help", "GLOBAL OPTIONS:", "COMMANDS:", "DESCRIPTION:", "COPYRIGHT:", "VERSION:", "CATEGORY:", "OPTIONS:", "USAGE:", "NAME:", "[global options]", "[command [command options]]", "[arguments...]", "[options]"} {
		text = strings.ReplaceAll(text, message, localize.T(message))
	}
	return text
}

func runDesktopAction(ctx context.Context, actions cliActions) error {
	if actions.desktop != nil {
		return actions.desktop(ctx, actions.run)
	}
	return runApplicationAction(ctx, actions)
}

func runApplicationAction(ctx context.Context, actions cliActions) error {
	if actions.run == nil {
		return errors.New(localize.T("clashpulse: application service is unavailable"))
	}
	return actions.run(ctx)
}

func refreshKindCommand(kind string, refresh func(context.Context, string, string, bool) error) *cli.Command {
	return &cli.Command{
		Name:      kind,
		Usage:     fmt.Sprintf(localize.T("refresh all enabled %s sources, or one source by ID"), localize.T(kind)),
		ArgsUsage: "[id]",
		Arguments: []cli.Argument{&cli.StringArgs{Name: "id", Min: 0, Max: 1}},
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Len() != 0 {
				return errors.New(localize.T("too many refresh arguments"))
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
		Usage:     localize.T("download all enabled subscription profiles, or one profile by ID"),
		ArgsUsage: "[id]",
		Arguments: []cli.Argument{&cli.StringArgs{Name: "id", Min: 0, Max: 1}},
		Action: func(ctx context.Context, command *cli.Command) error {
			if command.Args().Len() != 0 {
				return errors.New(localize.T("too many download arguments"))
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
		return errors.New(localize.T("clashpulse: refresh is unavailable"))
	}
	if err := refresh(ctx, kind, id, showResponse); err != nil {
		if showResponse {
			if response, ok := download.HTTPResponseFrom(err); ok {
				return fmt.Errorf("%w\n"+localize.T("%s response body (bounded):")+"\n%s", err, responseStatus(response), responseBody(response.ResponseBody))
			}
		}
		return err
	}
	result := localize.T("all enabled sources")
	if kind != "" && id == "" {
		result = fmt.Sprintf(localize.T("all enabled %s sources"), localize.T(kind))
	} else if id != "" {
		result = localize.T(kind) + " " + id
	}
	_, err := fmt.Fprintf(stdout, localize.T("refreshed %s")+"\n", result)
	return err
}

func runDownloadAction(ctx context.Context, stdout io.Writer, downloadProfile func(context.Context, string, bool) error, id string, showResponse bool) error {
	if downloadProfile == nil {
		return errors.New(localize.T("clashpulse: subscription download is unavailable"))
	}
	if err := downloadProfile(ctx, id, showResponse); err != nil {
		if showResponse {
			if response, ok := download.HTTPResponseFrom(err); ok {
				return fmt.Errorf("%w\n"+localize.T("%s response body (bounded):")+"\n%s", err, responseStatus(response), responseBody(response.ResponseBody))
			}
		}
		return err
	}
	result := localize.T("all enabled subscriptions")
	if id != "" {
		result = localize.T("subscription") + " " + id
	}
	_, err := fmt.Fprintf(stdout, localize.T("downloaded %s")+"\n", result)
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
		return localize.T("<empty>")
	}
	return body
}
