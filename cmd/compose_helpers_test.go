package cmd

import (
	"reflect"
	"testing"

	"dcon/internal/compose"

	"github.com/spf13/cobra"
)

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

func TestDropFlag(t *testing.T) {
	in := []string{"run", "--detach", "--name", "x", "alpine"}
	got := dropFlag(in, "--detach")
	want := []string{"run", "--name", "x", "alpine"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dropFlag = %v, want %v", got, want)
	}
	// Input must not be mutated (dropFlag allocates a fresh backing array).
	if in[1] != "--detach" {
		t.Errorf("dropFlag mutated its input: %v", in)
	}
	// Absent flag -> unchanged contents.
	if got := dropFlag([]string{"a", "b"}, "--x"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("dropFlag(absent) = %v, want [a b]", got)
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
