// Package runtime locates and drives Apple's `container` CLI, which is the
// backend that actually runs Linux containers in lightweight VMs on macOS.
//
// dcon is a thin Docker-compatible translation layer: every dcon command
// ultimately shells out to `container` (or, occasionally, several `container`
// invocations) here.
package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Bin returns the path to the Apple `container` binary. It honours the
// DCON_CONTAINER_BIN environment variable, then falls back to PATH, then to the
// well-known install location.
func Bin() string {
	if v := os.Getenv("DCON_CONTAINER_BIN"); v != "" {
		return v
	}
	if p, err := exec.LookPath("container"); err == nil {
		return p
	}
	return "/usr/local/bin/container"
}

// debug reports whether dcon should echo the underlying container commands.
func debug() bool {
	v := os.Getenv("DCON_DEBUG")
	return v == "1" || v == "true"
}

func trace(args []string) {
	if debug() {
		fmt.Fprintf(os.Stderr, "+ container %v\n", args)
	}
}

// Run executes `container <args...>` with stdio inherited from the current
// process. It is used for interactive / streaming commands (run, exec, logs,
// build, ...). The returned error is an *exec.ExitError when the child exits
// non-zero, so callers can surface the underlying exit code.
func Run(args ...string) error {
	trace(args)
	cmd := exec.Command(Bin(), args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunWith is like Run but lets the caller redirect the child's standard
// streams (used for `cp` to stdout, `export -o -`, etc.).
func RunWith(stdin *os.File, stdout *os.File, stderr *os.File, args ...string) error {
	trace(args)
	cmd := exec.Command(Bin(), args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// Capture runs `container <args...>` and returns its stdout. stderr is still
// streamed to the user so progress/errors remain visible.
func Capture(args ...string) (string, error) {
	trace(args)
	var out bytes.Buffer
	cmd := exec.Command(Bin(), args...)
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return out.String(), err
}

// CaptureSilent runs `container <args...>` capturing both stdout and stderr,
// returning stdout and the combined error (with stderr text folded in). Used
// when dcon needs to inspect output without leaking container's native
// formatting to the user.
func CaptureSilent(args ...string) (string, error) {
	trace(args)
	var out, errb bytes.Buffer
	cmd := exec.Command(Bin(), args...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		msg := errb.String()
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}

// CaptureJSON runs the command and unmarshals stdout into v.
func CaptureJSON(v any, args ...string) error {
	out, err := CaptureSilent(args...)
	if err != nil {
		return err
	}
	if out == "" {
		return nil
	}
	return json.Unmarshal([]byte(out), v)
}

// ExitCode extracts the process exit code from an error returned by Run.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

// AppRoot returns the Apple container application-support directory, used by a
// few commands that need to read persisted state directly.
func AppRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "com.apple.container")
}
