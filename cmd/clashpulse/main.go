package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/fishman/clashpulse/app"
)

const version = "clashpulse dev"

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr, app.Run))
}

func runMain(args []string, stdout, stderr io.Writer, runner func(context.Context) error) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if err := runner(context.Background()); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
