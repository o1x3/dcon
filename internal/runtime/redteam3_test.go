package runtime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBackend writes an executable shell script and points DCON_CONTAINER_BIN
// at it for the duration of the test.
func fakeBackend(t *testing.T, script string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "container")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DCON_CONTAINER_BIN", p)
}

// TestCaptureSilentPreservesExitCode reproduces the exit-code flattening bug:
// CaptureSilent used to return errors.New(stderr), losing the *exec.ExitError,
// so every caller exited 1 no matter what the backend reported. It also kept
// stderr's trailing newline, giving root's Fprintln a blank line.
func TestCaptureSilentPreservesExitCode(t *testing.T) {
	fakeBackend(t, `echo "boom happened" >&2; exit 3`)

	_, err := CaptureSilent("anything")
	if err == nil {
		t.Fatal("expected an error from a failing backend")
	}
	if got := err.Error(); got != "boom happened" {
		t.Errorf("message = %q, want trimmed %q", got, "boom happened")
	}
	if strings.HasSuffix(err.Error(), "\n") {
		t.Error("message must be TrimSpace'd (no trailing newline)")
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatal("error must unwrap to *exec.ExitError")
	}
	if got := ExitCode(err); got != 3 {
		t.Errorf("ExitCode = %d, want 3 (the backend's real code)", got)
	}
	var be *BackendError
	if !errors.As(err, &be) || be.Msg != "boom happened" {
		t.Errorf("expected *BackendError with trimmed stderr, got %#v", err)
	}
}

// TestCaptureSilentEmptyStderr keeps the fallback: with nothing on stderr the
// message is the exec error text, and the exit code still survives.
func TestCaptureSilentEmptyStderr(t *testing.T) {
	fakeBackend(t, `exit 7`)

	_, err := CaptureSilent("x")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() == "" {
		t.Error("message must not be empty when stderr was silent")
	}
	if got := ExitCode(err); got != 7 {
		t.Errorf("ExitCode = %d, want 7", got)
	}
}
