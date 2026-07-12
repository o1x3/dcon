package cmd

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"dcon/internal/dockerfmt"
)

// TestRunHostnameShorthandNotStolenByHelp locks the round-3 fix: Docker's `-h`
// is the hostname shorthand. We pre-register a long-only --help so cobra's
// InitDefaultHelpFlag (which unconditionally grabs -h) leaves -h for hostname.
func TestRunHostnameShorthandNotStolenByHelp(t *testing.T) {
	cmd := newRunCmd()
	cmd.InitDefaultHelpFlag() // cobra runs this at execute time
	if err := cmd.ParseFlags([]string{"-h", "web", "alpine"}); err != nil {
		t.Fatalf("ParseFlags(-h web): %v", err)
	}
	if hn, _ := cmd.Flags().GetString("hostname"); hn != "web" {
		t.Errorf("-h must set hostname=web (not be stolen by --help), got %q", hn)
	}
	if cmd.Flags().Lookup("help") == nil {
		t.Error("--help flag must still exist (long form)")
	}
}

// TestLogsTailShorthand locks the round-4 fix: docker's `-n` is the --tail
// shorthand (`dcon logs -n 100 web`), which previously errored as unknown.
func TestLogsTailShorthand(t *testing.T) {
	cmd := newLogsCmd()
	if err := cmd.ParseFlags([]string{"-n", "100", "web"}); err != nil {
		t.Fatalf("logs -n must parse (docker's --tail shorthand): %v", err)
	}
	if tail, _ := cmd.Flags().GetString("tail"); tail != "100" {
		t.Errorf("-n 100 should set tail=100, got %q", tail)
	}
}

// TestMatchImageFiltersReferenceOR locks the round-3 fix: repeated reference=
// filters OR-combine (union), matching docker — not the old AND/intersection.
func TestMatchImageFiltersReferenceOR(t *testing.T) {
	mk := func(repo, tag string) imageView { return imageView{Repository: repo, Tag: tag} }
	or := []string{"reference=alpine:3.*", "reference=nginx:latest"}
	if !matchImageFilters(mk("alpine", "3.20"), or) || !matchImageFilters(mk("nginx", "latest"), or) {
		t.Error("alpine:3.20 and nginx:latest should each match the reference OR set")
	}
	if matchImageFilters(mk("redis", "7"), or) {
		t.Error("redis:7 should NOT match either reference pattern")
	}
}

// TestLabelFileRejectsReservedMachineLabel locks the round-3 fix: --label-file
// gets the same dcon.machine reserved-namespace guard as direct --label.
func TestLabelFileRejectsReservedMachineLabel(t *testing.T) {
	dir := t.TempDir()
	lf := filepath.Join(dir, "labels.txt")
	if err := os.WriteFile(lf, []byte("dcon.machine=1\nfoo=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := parse(t, newRunCmd(), []string{"--rm", "--label-file", lf, "alpine"})
	if _, err := buildContainerArgs(c, c.Flags().Args(), "run"); err == nil {
		t.Error("label-file containing dcon.machine=1 must be rejected, like direct --label")
	}
}

// TestApplyFiltersSameKeyOR locks the fix where repeated same-key ps filters
// were AND-combined (so `status=running --filter status=exited` matched
// nothing). Docker ORs repeated values of the same key.
func TestApplyFiltersSameKeyOR(t *testing.T) {
	mk := func(id, state string) dockerfmt.Container {
		var c dockerfmt.Container
		c.ID = id
		c.Status.State = state
		return c
	}
	// matchStatusFilter maps "exited" -> backend state "stopped".
	list := []dockerfmt.Container{mk("run", "running"), mk("ex", "stopped"), mk("mid", "stopping")}
	out := applyFilters(list, []string{"status=running", "status=exited"})
	ids := map[string]bool{}
	for _, c := range out {
		ids[c.ID] = true
	}
	if len(out) != 2 || !ids["run"] || !ids["ex"] {
		t.Errorf("same-key OR = %v, want union {run, ex}", ids)
	}
}

// TestApplyFiltersDistinctKeysAND confirms distinct keys still AND, and multiple
// label predicates still AND (Docker semantics preserved alongside the OR fix).
func TestApplyFiltersDistinctKeysAND(t *testing.T) {
	mk := func(id, state string) dockerfmt.Container {
		var c dockerfmt.Container
		c.ID = id
		c.Status.State = state
		return c
	}
	list := []dockerfmt.Container{mk("web1", "running"), mk("db1", "running"), mk("web2", "stopped")}
	out := applyFilters(list, []string{"name=web", "status=running"})
	if len(out) != 1 || out[0].ID != "web1" {
		t.Errorf("distinct-key AND = %v, want [web1]", out)
	}

	mkL := func(id string, labels map[string]string) dockerfmt.Container {
		var c dockerfmt.Container
		c.ID = id
		c.Configuration.Labels = labels
		return c
	}
	ll := []dockerfmt.Container{
		mkL("both", map[string]string{"a": "1", "b": "2"}),
		mkL("one", map[string]string{"a": "1"}),
	}
	got := applyFilters(ll, []string{"label=a=1", "label=b=2"})
	if len(got) != 1 || got[0].ID != "both" {
		t.Errorf("label AND = %v, want [both]", got)
	}
}

// TestRunEmptyEntrypointForwarded locks the fix where `--entrypoint ""` (which
// clears the image ENTRYPOINT in docker) was silently dropped because the
// passthrough gated on a non-empty value.
func TestRunEmptyEntrypointForwarded(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--rm", "--entrypoint", "", "img", "/bin/sh"})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--entrypoint", "") {
		t.Errorf("empty --entrypoint not forwarded: %v", got)
	}

	// Unset entrypoint must NOT emit the flag.
	c2 := parse(t, newRunCmd(), []string{"--rm", "img", "/bin/sh"})
	got2, _ := buildContainerArgs(c2, c2.Flags().Args(), "run")
	if contains(got2, "--entrypoint") {
		t.Errorf("unset entrypoint emitted --entrypoint: %v", got2)
	}

	// Non-empty entrypoint still forwarded.
	c3 := parse(t, newRunCmd(), []string{"--rm", "--entrypoint", "/bin/bash", "img"})
	got3, _ := buildContainerArgs(c3, c3.Flags().Args(), "run")
	if !containsPair(got3, "--entrypoint", "/bin/bash") {
		t.Errorf("entrypoint not forwarded: %v", got3)
	}
}

// TestInfoExit locks the fix where `dcon info` returned exit 0 with the backend
// down; `docker info` exits non-zero so readiness gates work.
func TestInfoExit(t *testing.T) {
	if err := infoExit("running"); err != nil {
		t.Errorf("infoExit(running) = %v, want nil", err)
	}
	if err := infoExit("stopped"); err == nil {
		t.Error("infoExit(stopped) = nil, want non-nil (backend down must exit non-zero)")
	}
}

// TestNetworkCreateSubnetStringArray locks the fix where --subnet was a scalar
// String flag that silently dropped all but the last value, breaking dual-stack.
func TestNetworkCreateSubnetStringArray(t *testing.T) {
	create := subcmd(t, newNetworkGroupCmd(), "create")
	if err := create.ParseFlags([]string{"--subnet", "10.0.0.0/16", "--subnet", "fd00::/64"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got, err := create.Flags().GetStringArray("subnet")
	if err != nil {
		t.Fatalf("subnet is not a StringArray flag: %v", err)
	}
	if len(got) != 2 || got[0] != "10.0.0.0/16" || got[1] != "fd00::/64" {
		t.Errorf("subnet = %v, want both values preserved (dual-stack)", got)
	}
}

// TestBuildImageViewSizePrecision3 locks `docker images` SIZE precision-3.
func TestBuildImageViewSizePrecision3(t *testing.T) {
	var img dockerfmt.Image
	img.Variants = []dockerfmt.ImageVariant{{
		Platform: dockerfmt.Platform{OS: "linux", Architecture: goruntime.GOARCH},
		Size:     13256,
	}}
	if got := buildImageView(img, false).Size; got != "13.3kB" {
		t.Errorf("image SIZE = %q, want %q (precision 3, not 13.26kB)", got, "13.3kB")
	}
}

// TestMatchVolumeFiltersSameKeyOR locks the round-2 fix: repeated same-key
// volume filters OR-combine; distinct keys AND (matching docker and the ps fix).
func TestMatchVolumeFiltersSameKeyOR(t *testing.T) {
	mk := func(name string) dockerfmt.Volume {
		var v dockerfmt.Volume
		v.Configuration.Name = name
		return v
	}
	or := []string{"name=alpha", "name=bravo"}
	if !matchVolumeFilters(mk("alpha-vol"), "local", or) || !matchVolumeFilters(mk("bravo-vol"), "local", or) {
		t.Error("name=alpha OR name=bravo must match both alpha-vol and bravo-vol")
	}
	if matchVolumeFilters(mk("charlie-vol"), "local", or) {
		t.Error("charlie-vol must NOT match name=alpha OR name=bravo")
	}
	// distinct keys AND.
	if !matchVolumeFilters(mk("alpha-vol"), "local", []string{"name=alpha", "driver=local"}) {
		t.Error("name=alpha AND driver=local must match alpha-vol/local")
	}
	if matchVolumeFilters(mk("alpha-vol"), "nfs", []string{"name=alpha", "driver=local"}) {
		t.Error("driver=local must exclude an nfs volume (distinct-key AND)")
	}
}

// TestMatchNetworkFiltersSameKeyOR locks the same OR/AND fix for network ls.
func TestMatchNetworkFiltersSameKeyOR(t *testing.T) {
	mk := func(name string) dockerfmt.Network {
		var n dockerfmt.Network
		n.Configuration.Name = name
		return n
	}
	or := []string{"name=front", "name=back"}
	if !matchNetworkFilters(mk("frontend"), or) || !matchNetworkFilters(mk("backend"), or) {
		t.Error("name=front OR name=back must match frontend and backend")
	}
	if matchNetworkFilters(mk("db"), or) {
		t.Error("db must NOT match name=front OR name=back")
	}
}

// TestWarmReplenishFlagSeparateFromQuiet locks the round-2 regression fix: the
// depth clamp is gated on the hidden --replenish flag, NOT on the public -q, so
// an explicit `dcon warm -q -n N` is never capped to TargetDepth.
func TestWarmReplenishFlagSeparateFromQuiet(t *testing.T) {
	cmd := newWarmCmd()
	if err := cmd.ParseFlags([]string{"-q", "-n", "5", "alpine"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	quiet, _ := cmd.Flags().GetBool("quiet")
	replenish, _ := cmd.Flags().GetBool("replenish")
	if !quiet {
		t.Error("-q must set quiet")
	}
	if replenish {
		t.Error("manual `warm -q` must NOT set --replenish (else the clamp caps -n)")
	}
}

// TestRenderStatsBinaryMemDecimalIO locks: MEM USAGE/LIMIT uses binary IEC units
// (MiB/GiB) while NET/BLOCK I/O uses decimal SI at precision 3, matching docker.
func TestRenderStatsBinaryMemDecimalIO(t *testing.T) {
	cur := []dockerfmt.Stats{{
		ID:               "c1",
		MemoryUsageBytes: 536870912,  // 512 MiB
		MemoryLimitBytes: 8589934592, // 8 GiB
		NetworkRxBytes:   12345678,   // 12.3 MB at precision 3
		BlockReadBytes:   12345678,
	}}
	out := captureOut(t, func() {
		if err := renderStats(cur, nil, 1.0, "", false, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "512MiB") || !strings.Contains(out, "8GiB") {
		t.Errorf("expected binary MEM units (512MiB / 8GiB), got:\n%s", out)
	}
	if !strings.Contains(out, "12.3MB") {
		t.Errorf("expected NET/BLOCK I/O at precision 3 (12.3MB), got:\n%s", out)
	}
	if strings.Contains(out, "12.35MB") {
		t.Errorf("NET/BLOCK I/O used precision 4 (12.35MB):\n%s", out)
	}
}
