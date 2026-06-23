package cmd

import "os"

// isTerminal reports whether f is attached to a character device (a TTY).
// Used to auto-disable PTY allocation when stdin/stdout is a pipe, matching the
// Docker CLI which only allocates a TTY when one is actually present.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// haveTTY is true only when both stdin and stdout are terminals.
func haveTTY() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}
