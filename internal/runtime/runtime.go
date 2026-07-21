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
	"strings"
)

// wellKnownBins are install locations checked after PATH when resolving the
// Apple container binary (signed .pkg and Homebrew cask).
var wellKnownBins = []string{"/usr/local/bin/container", "/opt/homebrew/bin/container"}

// Bin returns the path to the Apple `container` binary. It honours the
// DCON_CONTAINER_BIN environment variable, then falls back to PATH, then to
// well-known install locations, then to /usr/local/bin/container (for error text).
func Bin() string {
	if v := os.Getenv("DCON_CONTAINER_BIN"); v != "" {
		return v
	}
	if p, err := exec.LookPath("container"); err == nil {
		return p
	}
	for _, p := range wellKnownBins {
		if isExecutable(p) {
			return p
		}
	}
	return wellKnownBins[0]
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}

// binMissingError is the user-facing message when the backend binary cannot be
// executed. Shared by Run/Capture and surfaced by `dcon doctor`.
func binMissingError(path string) error {
	return fmt.Errorf("Apple container CLI not found at %s. Install from https://github.com/apple/container/releases (or `brew install --cask container`)", path)
}

// ensureBin verifies the resolved backend binary exists and is executable.
// Without this, a missing install surfaces as the opaque Go exec error
// "fork/exec …: no such file or directory".
func ensureBin() error {
	p := Bin()
	if !isExecutable(p) {
		return binMissingError(p)
	}
	return nil
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
	if err := ensureBin(); err != nil {
		return err
	}
	trace(args)
	cmd := exec.Command(Bin(), args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Capture runs `container <args...>` and returns its stdout. stderr is still
// streamed to the user so progress/errors remain visible.
func Capture(args ...string) (string, error) {
	if err := ensureBin(); err != nil {
		return "", err
	}
	trace(args)
	var out bytes.Buffer
	cmd := exec.Command(Bin(), args...)
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return out.String(), err
}

// BackendError is returned by CaptureSilent when the backend command fails: it
// carries the backend's stderr text as the message while wrapping the original
// error (usually an *exec.ExitError), so errors.As — and therefore ExitCode —
// still see the real process exit status instead of a flattened generic 1.
type BackendError struct {
	Msg string // trimmed stderr text (or the exec error text when stderr was empty)
	Err error  // the underlying exec error
}

func (e *BackendError) Error() string { return e.Msg }
func (e *BackendError) Unwrap() error { return e.Err }

// CaptureSilent runs `container <args...>` capturing both stdout and stderr,
// returning stdout and the combined error (with stderr text folded in). Used
// when dcon needs to inspect output without leaking container's native
// formatting to the user. A non-zero exit returns a *BackendError whose
// message is the trimmed stderr text and which unwraps to the *exec.ExitError.
func CaptureSilent(args ...string) (string, error) {
	if err := ensureBin(); err != nil {
		return "", err
	}
	trace(args)
	var out, errb bytes.Buffer
	cmd := exec.Command(Bin(), args...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), &BackendError{Msg: msg, Err: err}
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

// IsExitError reports whether err is a proxied child-process non-zero exit (a
// bare *exec.ExitError from Run) rather than a dcon-level error. The child has
// already written its own stderr via the inherited streams, so callers should
// propagate the exit code WITHOUT printing Go's "exit status N" artifact —
// matching docker, which prints nothing when a workload merely exits non-zero.
func IsExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
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
