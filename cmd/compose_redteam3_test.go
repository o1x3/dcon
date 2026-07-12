package cmd

// Round-3 red-team regression tests for cmd/compose.go: depends_on closure,
// compose run defaults, ps filtering, lifecycle one-off exclusion,
// abort-on-container-exit polling predicate, and compose config parity.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"dcon/internal/compose"
	"dcon/internal/dockerfmt"
)

// --- item 3: up/create/run SERVICE must include the depends_on closure ---

func TestWithDepsClosure(t *testing.T) {
	p := &compose.Project{Services: map[string]*compose.Service{
		"web":   {DependsOn: []string{"api"}},
		"api":   {DependsOn: []string{"db", "cache"}},
		"db":    {},
		"cache": {},
		"other": {},
	}}
	got := withDeps(p, map[string]bool{"web": true})
	want := map[string]bool{"web": true, "api": true, "db": true, "cache": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("withDeps(web) = %v; want %v", got, want)
	}
	// nil selection (all services) passes through.
	if withDeps(p, nil) != nil {
		t.Error("withDeps(nil) must stay nil (= all services)")
	}
	// Unknown names are kept (existing typo behavior: silently no-op).
	got = withDeps(p, map[string]bool{"typo": true})
	if !got["typo"] {
		t.Errorf("unknown selection must be preserved: %v", got)
	}
}

func TestWithDepsCycleTerminates(t *testing.T) {
	p := &compose.Project{Services: map[string]*compose.Service{
		"a": {DependsOn: []string{"b"}},
		"b": {DependsOn: []string{"a"}},
	}}
	got := withDeps(p, map[string]bool{"a": true})
	if !got["a"] || !got["b"] {
		t.Errorf("cycle closure = %v; want both members", got)
	}
}

// --- item 11: compose run keeps one-off containers unless --rm ---

func TestComposeRunRmDefaultsFalse(t *testing.T) {
	f := composeRun().Flags().Lookup("rm")
	if f == nil {
		t.Fatal("no --rm flag")
	}
	if f.DefValue != "false" {
		t.Errorf("compose run --rm default = %s; docker keeps one-off containers (want false)", f.DefValue)
	}
}

// --- item 14: compose ps filtering (services args, -a, --status) ---

func psContainer(service, state, oneoff string) dockerfmt.Container {
	var c dockerfmt.Container
	c.ID = "proj-" + service + "-1"
	c.Status.State = state
	c.Configuration.Labels = map[string]string{
		compose.LabelService: service,
		compose.LabelOneoff:  oneoff,
	}
	return c
}

func TestComposePsMatch(t *testing.T) {
	running := psContainer("web", "running", "False")
	stopped := psContainer("db", "stopped", "False")

	// Default: running only.
	if !composePsMatch(running, nil, false, "") || composePsMatch(stopped, nil, false, "") {
		t.Error("default must show running containers only")
	}
	// -a includes stopped.
	if !composePsMatch(stopped, nil, true, "") {
		t.Error("-a must include stopped containers")
	}
	// SERVICE selection applies.
	sel := map[string]bool{"db": true}
	if composePsMatch(running, sel, true, "") || !composePsMatch(stopped, sel, true, "") {
		t.Error("service selection must filter non-selected services")
	}
	// --status filters client-side on the backend state (exited -> stopped).
	if !composePsMatch(stopped, nil, false, "exited") {
		t.Error("--status exited must match a stopped container even without -a")
	}
	if composePsMatch(running, nil, false, "exited") {
		t.Error("--status exited must exclude running containers")
	}
	if !composePsMatch(running, nil, false, "running") {
		t.Error("--status running must match running containers")
	}
}

// --- item 10: lifecycle verbs exclude one-off run containers from stop/kill ---

func TestLifecycleSkipsOneOffs(t *testing.T) {
	oneoff := psContainer("web", "running", "True")
	replica := psContainer("web", "running", "False")
	for _, verb := range []string{"stop", "kill"} {
		if !lifecycleSkips(verb, oneoff, nil) {
			t.Errorf("%s must skip one-off `compose run` containers", verb)
		}
		if lifecycleSkips(verb, replica, nil) {
			t.Errorf("%s must not skip service replicas", verb)
		}
	}
	// rm/start still cover one-offs (docker removes them with rm).
	for _, verb := range []string{"rm", "start", "restart"} {
		if lifecycleSkips(verb, oneoff, nil) {
			t.Errorf("%s must not skip one-off containers", verb)
		}
	}
	// Selection still applies.
	if !lifecycleSkips("stop", replica, map[string]bool{"db": true}) {
		t.Error("unselected service must be skipped")
	}
}

// --- item 17: abort-on-container-exit predicate ---

func TestAnyExited(t *testing.T) {
	names := []string{"p-web-1", "p-db-1"}
	if anyExited(names, map[string]string{"p-web-1": "running", "p-db-1": "running"}) {
		t.Error("all running: no abort")
	}
	if !anyExited(names, map[string]string{"p-web-1": "running", "p-db-1": "stopped"}) {
		t.Error("a stopped container must trigger the abort")
	}
	if !anyExited(names, map[string]string{"p-web-1": "running"}) {
		t.Error("a container missing from the snapshot counts as exited")
	}
}

// unsetEnvVar unsets a variable for the test's duration (t.Setenv registers
// the restore; plain os.Unsetenv would leak into other tests).
func unsetEnvVar(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	os.Unsetenv(key)
}

// --- item 18: compose config --format/--profiles/--images ---

func TestComposeConfigParityFlags(t *testing.T) {
	unsetEnvVar(t, "COMPOSE_FILE")
	dir := t.TempDir()
	content := `
services:
  web:
    image: nginx:1
    profiles: ["frontend"]
  worker:
    image: nginx:1
    profiles: ["backend", "frontend"]
  db:
    image: postgres:16
`
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	run := func(args ...string) string {
		cmd := composeConfig()
		cmd.SetArgs(args)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		return captureOut(t, func() {
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute %v: %v", args, err)
			}
		})
	}

	profiles := strings.Fields(run("--profiles"))
	sort.Strings(profiles)
	if !reflect.DeepEqual(profiles, []string{"backend", "frontend"}) {
		t.Errorf("--profiles = %v; want [backend frontend]", profiles)
	}

	images := strings.Fields(run("--images"))
	sort.Strings(images)
	if !reflect.DeepEqual(images, []string{"nginx:1", "postgres:16"}) {
		t.Errorf("--images = %v; want deduped [nginx:1 postgres:16]", images)
	}

	out := run("--format", "json")
	var tree map[string]any
	if err := json.Unmarshal([]byte(out), &tree); err != nil {
		t.Fatalf("--format json produced invalid JSON: %v\n%s", err, out)
	}
	svcs, _ := tree["services"].(map[string]any)
	if _, ok := svcs["web"]; !ok {
		t.Errorf("json output missing services.web: %v", tree)
	}

	// Invalid format must error.
	cmd := composeConfig()
	cmd.SetArgs([]string{"--format", "toml"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil {
		t.Error("--format toml must error")
	}
}

// --- item 16 (cmd side): loadProject auto-merges compose.override.yaml ---

func TestComposeConfigMergesOverride(t *testing.T) {
	unsetEnvVar(t, "COMPOSE_FILE")
	dir := t.TempDir()
	base := "services:\n  web:\n    image: nginx:1\n"
	override := "services:\n  web:\n    image: nginx:override\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.override.yaml"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cmd := composeConfig()
	cmd.SetArgs(nil)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	out := captureOut(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(out, "nginx:override") {
		t.Errorf("compose.override.yaml not merged; config = %q", out)
	}
	if strings.Contains(out, "nginx:1") {
		t.Errorf("override must replace the base image; config = %q", out)
	}
}
