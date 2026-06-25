package cmd

import (
	"io"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

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
}
