// Command stemma compiles coding-agent context between provider formats.
//
// Stemma is deterministic and local-first: it makes no network calls, uses no
// language model, and never executes anything it finds in repository files.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/alexvinola/stemma/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	env := cli.Env{
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Stdin:      os.Stdin,
		StdinIsTTY: isTerminal(os.Stdin),
		WorkingDir: wd,
	}
	os.Exit(cli.Run(ctx, env, os.Args[1:]))
}

// isTerminal reports whether f is an interactive terminal. It uses only the
// standard library: a character device is treated as interactive.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
