package runtime

import (
	"os"
	"os/exec"
	"testing"
)

func TestBinHonoursEnv(t *testing.T) {
	t.Setenv("DCON_CONTAINER_BIN", "/custom/container")
	if got := Bin(); got != "/custom/container" {
		t.Errorf("Bin() = %q; want /custom/container", got)
	}
}

func TestBinFallback(t *testing.T) {
	os.Unsetenv("DCON_CONTAINER_BIN")
	// Either resolves via PATH or falls back to the well-known location.
	if got := Bin(); got == "" {
		t.Error("Bin() returned empty")
	}
}

func TestExitCode(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Errorf("ExitCode(nil) = %d; want 0", got)
	}
	err := exec.Command("/bin/sh", "-c", "exit 3").Run()
	if got := ExitCode(err); got != 3 {
		t.Errorf("ExitCode(exit 3) = %d; want 3", got)
	}
}

func TestCaptureSilentEcho(t *testing.T) {
	t.Setenv("DCON_CONTAINER_BIN", "/bin/echo")
	out, err := CaptureSilent("hello", "world")
	if err != nil {
		t.Fatalf("CaptureSilent: %v", err)
	}
	if out != "hello world\n" {
		t.Errorf("CaptureSilent = %q; want %q", out, "hello world\n")
	}
}

func TestCaptureJSON(t *testing.T) {
	t.Setenv("DCON_CONTAINER_BIN", "/bin/echo")
	var v map[string]any
	if err := CaptureJSON(&v, `{"a":1,"b":"x"}`); err != nil {
		t.Fatalf("CaptureJSON: %v", err)
	}
	if v["b"] != "x" {
		t.Errorf("CaptureJSON parsed wrong: %v", v)
	}
}

func TestAppRootNonEmpty(t *testing.T) {
	if AppRoot() == "" {
		t.Error("AppRoot() empty")
	}
}
