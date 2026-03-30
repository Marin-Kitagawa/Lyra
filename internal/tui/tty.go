package tui

import (
	"os"

	"golang.org/x/term"
)

// isTTY reports whether stdout is an interactive terminal.
// When false (pipe, file redirect, CI, test script capture) all tui
// functions fall back to plain fmt output so they never block.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
