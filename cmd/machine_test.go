package cmd

import (
	"io"
	"reflect"
	"testing"

	"dcon/internal/dockerfmt"
	"dcon/internal/machine"

	"github.com/spf13/cobra"
)

// mkContainer is a tiny helper for building backend container fixtures.
func mkContainer(id string, labels map[string]string) dockerfmt.Container {
	var c dockerfmt.Container
	c.ID = id
	c.Configuration.Labels = labels
	return c
}

// TestMatchMachineRejectsLabelSpoof guards the prefix-namespace invariant: a
// machine must resolve only by its prefixed backend ID + the dcon.machine
// label, never by the attacker-controllable dcon.machine.name label. A container
// that carries forged machine labels but is NOT named dcon-machine-* (which any
// `dcon run --label dcon.machine=1 --label dcon.machine.name=web` can create)
// must never be resolved as machine "web" — otherwise machine rm/stop/shell
// becomes a confused deputy against an arbitrary user container.
func TestMatchMachineRejectsLabelSpoof(t *testing.T) {
	realMachine := mkContainer(machine.ContainerName("web"), map[string]string{
		machine.LabelMachine: "1", machine.LabelName: "web",
	})
	spoof := mkContainer("evil", map[string]string{
		machine.LabelMachine: "1", machine.LabelName: "web",
	})
	prefixedNonMachine := mkContainer(machine.ContainerName("web"), map[string]string{
		// user ran `dcon run --name dcon-machine-web` with no machine label
	})

	// 1. A lone spoof must NOT resolve as "web".
	if c, ok := matchMachine([]dockerfmt.Container{spoof}, "web"); ok {
		t.Errorf("label spoof resolved as machine web: got id %q (confused deputy)", c.ID)
	}
	// 2. With both present, only the genuinely prefixed machine resolves.
	if c, ok := matchMachine([]dockerfmt.Container{spoof, realMachine}, "web"); !ok || c.ID != machine.ContainerName("web") {
		t.Errorf("expected real machine, got id=%q ok=%v", c.ID, ok)
	}
	// 3. A prefixed container without the machine label is not a machine.
	if _, ok := matchMachine([]dockerfmt.Container{prefixedNonMachine}, "web"); ok {
		t.Error("prefixed but unlabeled container resolved as a machine")
	}
	// 4. A genuine machine resolves.
	if _, ok := matchMachine([]dockerfmt.Container{realMachine}, "web"); !ok {
		t.Error("genuine machine failed to resolve")
	}
}

// runSplit drives splitNameAndCommand through a real cobra parse so the
// ArgsLenAtDash (`--`) handling is exercised exactly as in production.
func runSplit(t *testing.T, argv []string) (name string, command []string) {
	t.Helper()
	cmd := &cobra.Command{
		Use:  "shell",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			name, command = splitNameAndCommand(c, args)
			return nil
		},
	}
	cmd.Flags().SetInterspersed(false) // mirror machineShellCmd
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(argv)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", argv, err)
	}
	return name, command
}

func TestSplitNameAndCommand(t *testing.T) {
	cases := []struct {
		argv        []string
		wantName    string
		wantCommand []string
	}{
		{[]string{}, "", nil},
		{[]string{"foo"}, "foo", nil},
		{[]string{"foo", "ls", "-la"}, "foo", []string{"ls", "-la"}},
		{[]string{"foo", "--", "ls", "-la"}, "foo", []string{"ls", "-la"}},
		{[]string{"--", "ls"}, "", []string{"ls"}},
	}
	for _, c := range cases {
		name, command := runSplit(t, c.argv)
		if name != c.wantName {
			t.Errorf("%v: name = %q, want %q", c.argv, name, c.wantName)
		}
		if len(command) == 0 && len(c.wantCommand) == 0 {
			continue
		}
		if !reflect.DeepEqual(command, c.wantCommand) {
			t.Errorf("%v: command = %v, want %v", c.argv, command, c.wantCommand)
		}
	}
}

// TestMachineRmForceDefault locks in that `machine rm` does NOT force by
// default — a bare rm of a running machine must fail (no --force) so its
// filesystem isn't silently destroyed, matching `docker rm`. -f opts in.
func TestMachineRmForceDefault(t *testing.T) {
	cmd := machineRmCmd()
	if def, _ := cmd.Flags().GetBool("force"); def {
		t.Error("machine rm --force defaults to true; should be false (data-loss footgun)")
	}
	if got := machineDeleteArgs(false); !reflect.DeepEqual(got, []string{"delete"}) {
		t.Errorf("machineDeleteArgs(false) = %v, want [delete]", got)
	}
	if got := machineDeleteArgs(true); !reflect.DeepEqual(got, []string{"delete", "--force"}) {
		t.Errorf("machineDeleteArgs(true) = %v, want [delete --force]", got)
	}
}

func TestMachineStateName(t *testing.T) {
	for in, want := range map[string]string{
		"running":  "running",
		"stopped":  "stopped",
		"stopping": "stopping",
		"":         "created",
		"unknown":  "created",
		"weird":    "weird",
	} {
		if got := machineStateName(in); got != want {
			t.Errorf("machineStateName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMachineRenameUnsupported(t *testing.T) {
	cmd := machineRenameCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"a", "b"})
	if err := cmd.Execute(); err == nil {
		t.Error("machine rename should return an unsupported error")
	}
}

func TestParseCPUs(t *testing.T) {
	if n, warn, err := parseCPUs("2"); err != nil || n != 2 || warn != "" {
		t.Errorf("parseCPUs(2) = %d,%q,%v", n, warn, err)
	}
	if n, warn, err := parseCPUs("1.5"); err != nil || n != 2 || warn == "" {
		t.Errorf("parseCPUs(1.5) should round up to 2 with a warning; got %d,%q,%v", n, warn, err)
	}
	if _, _, err := parseCPUs("0"); err == nil {
		t.Error("parseCPUs(0) should error")
	}
	if _, _, err := parseCPUs("abc"); err == nil {
		t.Error("parseCPUs(abc) should error")
	}
	// Non-finite floats parse via ParseFloat but must be rejected, not turned
	// into --cpus 0 (NaN) or --cpus 9223372036854775807 (Inf).
	for _, v := range []string{"NaN", "nan", "inf", "Inf", "+Inf", "infinity"} {
		if n, _, err := parseCPUs(v); err == nil {
			t.Errorf("parseCPUs(%q) should error; got n=%d", v, n)
		}
	}
}
