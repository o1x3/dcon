package cmd

import (
	"reflect"
	"testing"

	"dcon/internal/compose"

	"github.com/spf13/cobra"
)

// TestComposeExecArgsInteractiveWithoutTTY reproduces the bug where `compose
// exec -T svc cmd < file` (no TTY) dropped --interactive, so redirected stdin
// never reached the process. --interactive must follow the flag, not the TTY.
func TestComposeExecArgsInteractiveWithoutTTY(t *testing.T) {
	// -T form with no terminal: interactive=true, tty=true, noTTY=true, hasTTY=false.
	got := composeExecArgs(false, true, true, true, false, "", "", nil, "cid", []string{"psql"})
	if !contains(got, "--interactive") {
		t.Errorf("piped `exec -T` must keep --interactive (for `< file`): %v", got)
	}
	if contains(got, "--tty") {
		t.Errorf("--tty must not be set when -T/no TTY: %v", got)
	}
	want := []string{"exec", "--interactive", "cid", "psql"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exec args = %v, want %v", got, want)
	}
}

// TestComposeExecArgsTTYGating confirms --tty is added only when requested, not
// suppressed by -T, and a real terminal is present.
func TestComposeExecArgsTTYGating(t *testing.T) {
	if got := composeExecArgs(false, true, true, false, true, "", "", nil, "cid", []string{"sh"}); !contains(got, "--tty") {
		t.Errorf("tty && !noTTY && hasTTY should set --tty: %v", got)
	}
	if got := composeExecArgs(false, true, true, false, false, "", "", nil, "cid", []string{"sh"}); contains(got, "--tty") {
		t.Errorf("no real TTY: --tty must be absent: %v", got)
	}
}

// TestComposeRunPtyFlags reproduces the bug where `compose run` never forwarded
// --tty/--interactive: a plain run keeps stdin open and allocates a TTY (unless
// -T or no terminal), matching docker compose run defaults.
func TestComposeRunPtyFlags(t *testing.T) {
	// interactive=true, not -T, real terminal -> both.
	if got := composeRunPtyFlags(true, false, true); !reflect.DeepEqual(got, []string{"--interactive", "--tty"}) {
		t.Errorf("run pty flags = %v, want [--interactive --tty]", got)
	}
	// -T suppresses the PTY but keeps interactive.
	if got := composeRunPtyFlags(true, true, true); !reflect.DeepEqual(got, []string{"--interactive"}) {
		t.Errorf("-T should drop --tty: %v", got)
	}
	// No real terminal -> no PTY (so `compose run web cmd | cat` works).
	if got := composeRunPtyFlags(true, false, false); !reflect.DeepEqual(got, []string{"--interactive"}) {
		t.Errorf("no TTY: %v", got)
	}
}

// TestEffectiveReplicas reproduces the bug where `up --scale web=0` ran 1
// replica: an explicit scale (including 0) must win over the unset default.
func TestEffectiveReplicas(t *testing.T) {
	svc := &compose.Service{}
	if n := effectiveReplicas(svc, map[string]int{"web": 0}, "web"); n != 0 {
		t.Errorf("--scale web=0 should yield 0 replicas, got %d", n)
	}
	if n := effectiveReplicas(svc, map[string]int{"web": 3}, "web"); n != 3 {
		t.Errorf("--scale web=3 should yield 3, got %d", n)
	}
	if n := effectiveReplicas(svc, nil, "web"); n != 1 {
		t.Errorf("no scale, no service scale -> 1, got %d", n)
	}
	if n := effectiveReplicas(&compose.Service{Scale: 2}, nil, "web"); n != 2 {
		t.Errorf("service-level scale 2 -> 2, got %d", n)
	}
	if n := effectiveReplicas(svc, map[string]int{"web": -5}, "web"); n != 0 {
		t.Errorf("negative scale floored to 0, got %d", n)
	}
}

func TestServiceSet(t *testing.T) {
	if got := serviceSet(nil); got != nil {
		t.Errorf("serviceSet(nil) = %v, want nil", got)
	}
	got := serviceSet([]string{"web", "db", "web"})
	want := map[string]bool{"web": true, "db": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serviceSet = %v, want %v", got, want)
	}
}

func TestParallelLimit(t *testing.T) {
	cases := map[string]int{"": 8, "4": 4, "1": 1, "0": 0, "-3": 0, "bad": 8}
	for in, want := range cases {
		t.Setenv("DCON_COMPOSE_PARALLEL", in)
		if got := parallelLimit(); got != want {
			t.Errorf("parallelLimit() with DCON_COMPOSE_PARALLEL=%q = %d, want %d", in, got, want)
		}
	}
}

func TestEnabledProfiles(t *testing.T) {
	t.Setenv("COMPOSE_PROFILES", "prod,test")
	cmd := &cobra.Command{}
	cmd.Flags().StringArray("profile", nil, "")
	_ = cmd.ParseFlags([]string{"--profile", "dev"})
	got := enabledProfiles(cmd)
	for _, want := range []string{"dev", "prod", "test"} {
		if !got[want] {
			t.Errorf("enabledProfiles missing %q in %v", want, got)
		}
	}
	if got["unset"] {
		t.Errorf("enabledProfiles has spurious profile: %v", got)
	}
}

func TestComposeLogsTail(t *testing.T) {
	cases := map[string]string{
		"all": "", // Docker default -> no -n
		"":    "",
		"50":  "50",
		"1":   "1",
		"abc": "", // non-numeric -> ignored
	}
	for in, want := range cases {
		cmd := composeLogs()
		_ = cmd.ParseFlags([]string{"--tail", in})
		if got := composeLogsTail(cmd); got != want {
			t.Errorf("composeLogsTail(--tail %q) = %q, want %q", in, got, want)
		}
	}
}

func TestSkipService(t *testing.T) {
	p := &compose.Project{Services: map[string]*compose.Service{
		"web": {},
		"db":  {},
	}}
	selected := serviceSet([]string{"web"})

	// With an explicit selection, only selected services run.
	if skipService(p, "web", selected, nil) {
		t.Error("selected service web should not be skipped")
	}
	if !skipService(p, "db", selected, nil) {
		t.Error("unselected service db should be skipped")
	}
	// With no selection, a service with no profiles is enabled by default.
	if skipService(p, "web", nil, map[string]bool{}) {
		t.Error("default (no-profile) service should not be skipped")
	}
}
