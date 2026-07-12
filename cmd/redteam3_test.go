package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestWarmAllowedTLSCompatFlags: root accepts-and-ignores the docker TLS
// flags (see cmd/root.go), so their presence must not knock a run off the
// warm fast path — `docker --tlsverify run --rm ...` used to silently cold-run.
func TestWarmAllowedTLSCompatFlags(t *testing.T) {
	for _, name := range []string{"tls", "tlsverify", "tlscacert", "tlscert", "tlskey"} {
		if !warmAllowed[name] {
			t.Errorf("warmAllowed[%q] = false; ignored TLS compat flag must stay warm-eligible", name)
		}
	}
}

// TestWarmSignalCleanupStop is a lifecycle smoke test: installing the handler
// and stopping it without a signal must not fire the cleanup and must not
// panic or wedge. (The signal-fire path shells out and re-raises a fatal
// signal, so it is validated by code reading, not in-process.)
func TestWarmSignalCleanupStop(t *testing.T) {
	stop := warmSignalCleanup("member-id")
	stop()
}

// TestMachinePTY pins the shell/exec PTY decision: -T always wins, -t forces,
// otherwise auto — which requires stdin AND stdout to both be terminals (a
// stdin-only check CRLF-mangled `machine exec m cat file | sort`).
func TestMachinePTY(t *testing.T) {
	cases := []struct {
		force, disable, auto bool
		want                 bool
	}{
		{false, false, true, true},   // plain interactive terminal
		{false, false, false, false}, // piped stdout (or stdin): no PTY
		{true, false, false, true},   // -t forces
		{false, true, true, false},   // -T disables even on a terminal
		{true, true, true, false},    // -T beats -t (docker compose exec semantics)
	}
	for _, tc := range cases {
		if got := machinePTY(tc.force, tc.disable, tc.auto); got != tc.want {
			t.Errorf("machinePTY(force=%v, disable=%v, auto=%v) = %v, want %v",
				tc.force, tc.disable, tc.auto, got, tc.want)
		}
	}
}

// TestMachinePTYFlagsRegistered ensures shell and exec expose the -t/-T
// overrides (long names matching compose exec's tty/no-TTY).
func TestMachinePTYFlagsRegistered(t *testing.T) {
	for label, c := range map[string]*cobra.Command{
		"shell": machineShellCmd(),
		"exec":  machineExecCmd(),
	} {
		tty := c.Flags().Lookup("tty")
		if tty == nil || tty.Shorthand != "t" {
			t.Errorf("%s: missing -t/--tty flag", label)
		}
		noTTY := c.Flags().Lookup("no-TTY")
		if noTTY == nil || noTTY.Shorthand != "T" {
			t.Errorf("%s: missing -T/--no-TTY flag", label)
		}
	}
}

// TestForEachMachineErrors locks the error contract: a single machine returns
// the underlying error untouched (root prints it exactly once), while multiple
// machines print each failure in the loop and return only a terse aggregate —
// previously the first error was printed twice and the rest were swallowed.
func TestForEachMachineErrors(t *testing.T) {
	// Any backend call fails, so every machineArg resolution errors.
	t.Setenv("DCON_CONTAINER_BIN", "/usr/bin/false")

	err := forEachMachine([]string{"a"}, func(id, name string) error { return nil })
	if err == nil {
		t.Fatal("single machine: expected the resolve error")
	}
	if strings.Contains(err.Error(), "machines failed") {
		t.Errorf("single machine: got aggregate %q, want the underlying error", err)
	}

	err = forEachMachine([]string{"a", "b", "c"}, func(id, name string) error { return nil })
	if err == nil {
		t.Fatal("multiple machines: expected an aggregate error")
	}
	if got := err.Error(); got != "3 of 3 machines failed" {
		t.Errorf("aggregate = %q, want %q", got, "3 of 3 machines failed")
	}
}

// TestMachineNativePassthroughRegistered guards the escape hatch to the
// backend's native `container machine` group, which dcon's group shadows.
func TestMachineNativePassthroughRegistered(t *testing.T) {
	for _, c := range newMachineCmd().Commands() {
		if c.Name() == "native" {
			if !c.DisableFlagParsing {
				t.Error("machine native must pass flags through verbatim")
			}
			return
		}
	}
	t.Error("machine group has no `native` passthrough subcommand")
}
