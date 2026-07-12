package cmd

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"dcon/internal/dockerfmt"

	"github.com/spf13/cobra"
)

// TestTopPassesDashedPsOptions reproduces the bug where `top web -ef` aborted
// with "unknown shorthand flag: 'e'" because cobra parsed the ps options as
// flags of the top command. With SetInterspersed(false) they pass through.
func TestTopPassesDashedPsOptions(t *testing.T) {
	cmd := newTopCmd()
	var got []string
	cmd.RunE = func(c *cobra.Command, args []string) error { got = args; return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"web", "-ef"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("`top web -ef` must not error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"web", "-ef"}) {
		t.Errorf("args reaching RunE = %v, want [web -ef]", got)
	}
}

// TestFormatCreatedByRuneSafe guards the docker-exact CREATED BY rendering:
// truncation counts display columns via Ellipsis (never splitting a rune) and
// docker's /bin/sh -c wrappers are preserved, not stripped.
func TestFormatCreatedByRuneSafe(t *testing.T) {
	// Leading ASCII + wide (2-column) runes: 45 columns = "x" + 21 runes (43
	// cols = 44 total) + "…"; a byte- or rune-count cut would differ.
	in := "x" + strings.Repeat("あ", 50)
	out := formatCreatedBy(in, false)
	if !utf8.ValidString(out) {
		t.Errorf("truncation produced invalid UTF-8: %q", out)
	}
	if want := "x" + strings.Repeat("あ", 21) + "…"; out != want {
		t.Errorf("Ellipsis(45) wrong: got %q want %q", out, want)
	}
	// Docker does NOT strip the shell wrapper prefixes.
	if got := formatCreatedBy("/bin/sh -c #(nop) CMD [\"x\"]", false); got != `/bin/sh -c #(nop) CMD ["x"]` {
		t.Errorf("docker keeps the #(nop) wrapper: %q", got)
	}
	if got := formatCreatedBy("/bin/sh -c apk add curl", false); got != "/bin/sh -c apk add curl" {
		t.Errorf("docker keeps the shell wrapper: %q", got)
	}
}

// TestCpIsContainerRef reproduces the bug where a local path containing a colon
// (./my:file.txt) was misclassified as a CONTAINER:PATH reference.
func TestCpIsContainerRef(t *testing.T) {
	for _, p := range []string{"./my:file.txt", "../x:y", "/abs:path", "/abs/path", "plainfile", ":leading"} {
		if cpIsContainerRef(p) {
			t.Errorf("%q should be treated as a local path", p)
		}
	}
	for _, p := range []string{"web:/tmp", "mycontainer:/var/log", "abc123:/x"} {
		if !cpIsContainerRef(p) {
			t.Errorf("%q should be treated as a container ref", p)
		}
	}
}

// TestPortMappingLinesExpandsRange reproduces the bug where `port` ignored a
// published range (PublishPort.Count>1), printing only the base port. A range
// must expand to one line per port, and per-port filtering must resolve into it.
func TestPortMappingLinesExpandsRange(t *testing.T) {
	ports := []dockerfmt.PublishPort{
		{HostAddress: "0.0.0.0", HostPort: 8000, ContainerPort: 80, Proto: "tcp", Count: 3},
	}
	all := portMappingLines(ports, "", "")
	want := []string{
		"80/tcp -> 0.0.0.0:8000",
		"81/tcp -> 0.0.0.0:8001",
		"82/tcp -> 0.0.0.0:8002",
	}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("range expansion = %v, want %v", all, want)
	}
	// Per-port filter resolves a non-base port within the range.
	if got := portMappingLines(ports, "81", ""); !reflect.DeepEqual(got, []string{"0.0.0.0:8001"}) {
		t.Errorf("filter 81 = %v, want [0.0.0.0:8001]", got)
	}
	// Count 0 is treated as a single port.
	single := portMappingLines([]dockerfmt.PublishPort{{HostPort: 9000, ContainerPort: 90, Proto: "tcp"}}, "", "")
	if len(single) != 1 {
		t.Errorf("count 0 should yield one line, got %v", single)
	}
}

// TestRestartStopArgsForwardsSignal reproduces the bug where restart's
// --signal flag was defined but never used. It must be forwarded to the stop
// phase (the backend stop accepts --signal). --time is now ALWAYS forwarded
// (docker's default grace is 10 s vs the backend's 5 s), so every argv
// carries it.
func TestRestartStopArgsForwardsSignal(t *testing.T) {
	if got := restartStopArgs(10, ""); !reflect.DeepEqual(got, []string{"stop", "--time", "10"}) {
		t.Errorf("default: got %v, want [stop --time 10]", got)
	}
	if got := restartStopArgs(10, "SIGTERM"); !reflect.DeepEqual(got, []string{"stop", "--time", "10", "--signal", "SIGTERM"}) {
		t.Errorf("--signal must be forwarded: got %v", got)
	}
	if got := restartStopArgs(3, "SIGKILL"); !reflect.DeepEqual(got, []string{"stop", "--time", "3", "--signal", "SIGKILL"}) {
		t.Errorf("--time + --signal: got %v", got)
	}
}

// TestMergeInspectArrays guards the mixed container+image inspect merge.
func TestMergeInspectArrays(t *testing.T) {
	got, err := mergeInspectArrays([]string{`[{"id":"a"}]`, "", `[{"id":"b"}]`})
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if jerr := json.Unmarshal([]byte(got), &items); jerr != nil {
		t.Fatalf("merged output is not valid JSON: %v (%q)", jerr, got)
	}
	if len(items) != 2 || items[0]["id"] != "a" || items[1]["id"] != "b" {
		t.Errorf("merged = %v, want two elements a,b", items)
	}
	if out, _ := mergeInspectArrays(nil); out != "" {
		t.Errorf("empty input should yield empty string, got %q", out)
	}
	if _, err := mergeInspectArrays([]string{"not json"}); err == nil {
		t.Error("invalid JSON input should error")
	}
}

// TestResolveVolumeName reproduces the bug where `volume create --name X` was
// ignored (a random-named volume was created). --name (and the positional) must
// be honored, with both-supplied a conflict.
func TestResolveVolumeName(t *testing.T) {
	if n, err := resolveVolumeName("myvol", true, nil); err != nil || n != "myvol" {
		t.Errorf("--name myvol -> (%q,%v), want myvol", n, err)
	}
	if n, err := resolveVolumeName("", false, []string{"posvol"}); err != nil || n != "posvol" {
		t.Errorf("positional -> (%q,%v), want posvol", n, err)
	}
	if _, err := resolveVolumeName("myvol", true, []string{"posvol"}); err == nil {
		t.Error("supplying both --name and a positional must error")
	}
	if n, err := resolveVolumeName("", false, nil); err != nil || len(n) != 64 {
		t.Errorf("no name -> random 64-hex id; got len %d err %v", len(n), err)
	}
	// An explicit but empty --name is invalid usage, not a request for an
	// anonymous volume — it must error rather than silently generate a name.
	if _, err := resolveVolumeName("", true, nil); err == nil {
		t.Error("explicit empty --name must error, not generate an anonymous volume")
	}
}

// TestComposeStopAndKillArgs reproduces the bug where compose kill --signal and
// compose stop/restart --timeout were dropped: the backend stop accepts --time
// and kill accepts --signal, so they must be forwarded.
func TestComposeStopAndKillArgs(t *testing.T) {
	if got := composeStopArgs(false, 10, "c1"); !reflect.DeepEqual(got, []string{"stop", "c1"}) {
		t.Errorf("timeout unset: %v, want [stop c1]", got)
	}
	if got := composeStopArgs(true, 30, "c1"); !reflect.DeepEqual(got, []string{"stop", "--time", "30", "c1"}) {
		t.Errorf("timeout 30: %v, want [stop --time 30 c1]", got)
	}
	if got := composeKillArgs("SIGTERM", "c1"); !reflect.DeepEqual(got, []string{"kill", "--signal", "SIGTERM", "c1"}) {
		t.Errorf("kill -s SIGTERM: %v", got)
	}
	if got := composeKillArgs("", "c1"); !reflect.DeepEqual(got, []string{"kill", "c1"}) {
		t.Errorf("kill no signal: %v", got)
	}
}

// TestBuildProgressQuiet reproduces the bug where `build --progress quiet` was
// forwarded verbatim (the backend only accepts auto|plain|tty); quiet (like
// rawjson) must be remapped to plain.
func TestBuildProgressQuiet(t *testing.T) {
	for _, v := range []string{"quiet", "rawjson"} {
		c := parse(t, newBuildCmd(), []string{"--progress", v, "."})
		got, err := buildBuildArgs(c, c.Flags().Args())
		if err != nil {
			t.Fatal(err)
		}
		if !containsPair(got, "--progress", "plain") {
			t.Errorf("--progress %s should map to plain; got %v", v, got)
		}
		if containsPair(got, "--progress", v) {
			t.Errorf("--progress %s should not be forwarded verbatim; got %v", v, got)
		}
	}
}

// TestBuilderRunning reproduces the doctor bug where the substring "running"
// also matched "not running".
func TestBuilderRunning(t *testing.T) {
	for _, out := range []string{"running", "Builder is running", "status: running"} {
		if !builderRunning(out) {
			t.Errorf("builderRunning(%q) = false, want true", out)
		}
	}
	for _, out := range []string{"not running", "Builder is not running", "stopped", ""} {
		if builderRunning(out) {
			t.Errorf("builderRunning(%q) = true, want false", out)
		}
	}
}

// TestSystemPrunePlan covers the prune step plan that the error-propagating
// loop runs (the bug fixed alongside it was that every step's error was
// discarded and the command always exited 0).
func TestSystemPrunePlan(t *testing.T) {
	base := systemPrunePlan(false, false)
	if len(base) != 3 {
		t.Fatalf("base plan has %d steps, want 3", len(base))
	}
	if reflect.DeepEqual(base[1].args, []string{"image", "prune", "--all"}) {
		t.Error("default prune must not pass --all to image prune")
	}
	full := systemPrunePlan(true, true)
	if len(full) != 4 {
		t.Fatalf("all+volumes plan has %d steps, want 4", len(full))
	}
	if !reflect.DeepEqual(full[1].args, []string{"image", "prune", "--all"}) {
		t.Errorf("--all should add --all to image prune; got %v", full[1].args)
	}
	if !reflect.DeepEqual(full[3].args, []string{"volume", "prune"}) {
		t.Errorf("--volumes should append a volume prune; got %v", full[3].args)
	}
}
