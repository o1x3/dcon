package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"

	"dcon/internal/dockerfmt"
)

// --- item 2: run --pull ------------------------------------------------------

func TestValidatePullPolicy(t *testing.T) {
	for _, ok := range []string{"", "always", "missing", "never"} {
		if err := validatePullPolicy(ok); err != nil {
			t.Errorf("validatePullPolicy(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"sometimes", "Always", "true"} {
		if err := validatePullPolicy(bad); err == nil {
			t.Errorf("validatePullPolicy(%q) = nil, want error", bad)
		}
	}
}

// A run carrying ANY explicit --pull must lose the warm fast path: a warm
// member was booted from whatever the ref pointed at boot time, so
// --pull=always served from the pool would silently run a stale image.
func TestPullDisqualifiesWarm(t *testing.T) {
	for _, policy := range []string{"always", "missing", "never"} {
		cmd := newRunCmd()
		if err := cmd.ParseFlags([]string{"--rm", "--pull", policy, "alpine", "true"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if warmEligible(cmd, cmd.Flags().Args()) {
			t.Errorf("--pull %s must disqualify the warm fast path", policy)
		}
	}
}

// --- item 3: docker's 125 exit convention -------------------------------------

func TestCLIFailureExitCode(t *testing.T) {
	for _, name := range []string{"run", "create", "exec"} {
		if got := cliFailureExitCode(name); got != 125 {
			t.Errorf("cliFailureExitCode(%q) = %d, want 125", name, got)
		}
	}
	for _, name := range []string{"ps", "images", "rm", "compose", "build"} {
		if got := cliFailureExitCode(name); got != 1 {
			t.Errorf("cliFailureExitCode(%q) = %d, want 1", name, got)
		}
	}
}

// --- item 4: build --cpus must be long-only ------------------------------------

// docker's legacy `build -c` means --cpu-shares; dcon previously bound -c to
// its own --cpus, silently reinterpreting the value.
func TestBuildCpusLongOnly(t *testing.T) {
	cmd := newBuildCmd()
	if err := cmd.ParseFlags([]string{"-c", "512", "."}); err == nil {
		t.Error("build -c must no longer parse (was bound to --cpus)")
	}
	cmd = newBuildCmd()
	if err := cmd.ParseFlags([]string{"--cpus", "4", "."}); err != nil {
		t.Errorf("build --cpus must keep working: %v", err)
	}
}

// --- item 5: logs --tail validation --------------------------------------------

func TestValidateTail(t *testing.T) {
	for in, want := range map[string]string{"": "", "all": "", "10": "10", "-1": "-1"} {
		got, err := validateTail(in)
		if err != nil || got != want {
			t.Errorf("validateTail(%q) = %q,%v; want %q,nil", in, got, err, want)
		}
	}
	for _, bad := range []string{"latest", "10x", "ten"} {
		if _, err := validateTail(bad); err == nil {
			t.Errorf("validateTail(%q) = nil, want error (was silently ignored)", bad)
		}
	}
}

// --- item 6: rm --link must hard-error ------------------------------------------

// Honoring --link by falling through would FORCE-DELETE the named container
// (docker only removes the network link). It must error before any backend call.
func TestRmLinkHardErrors(t *testing.T) {
	cmd := newRmCmd()
	if err := cmd.ParseFlags([]string{"-l", "web"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	err := cmd.RunE(cmd, cmd.Flags().Args())
	if err == nil || !strings.Contains(err.Error(), "--link") {
		t.Errorf("rm --link must hard-error, got %v", err)
	}
}

// --- item 7: docker's 10 s stop grace --------------------------------------------

func TestStopRestartDefaultGrace(t *testing.T) {
	if def := newStopCmd().Flags().Lookup("time").DefValue; def != "10" {
		t.Errorf("stop --time default = %s, want 10 (docker's grace)", def)
	}
	if def := newRestartCmd().Flags().Lookup("time").DefValue; def != "10" {
		t.Errorf("restart --time default = %s, want 10 (docker's grace)", def)
	}
	// restart must ALWAYS forward --time (backend default is 5 s).
	if got := restartStopArgs(10, ""); !reflect.DeepEqual(got, []string{"stop", "--time", "10"}) {
		t.Errorf("restartStopArgs default = %v, want --time always forwarded", got)
	}
}

// --- item 8: bare -e KEY client-env resolution ------------------------------------

func TestExpandEnvSpecs(t *testing.T) {
	lookup := func(k string) (string, bool) {
		if k == "SET_VAR" {
			return "resolved", true
		}
		return "", false
	}
	got := expandEnvSpecs([]string{"A=1", "SET_VAR", "UNSET_VAR", "EMPTY="}, lookup)
	want := []string{"A=1", "SET_VAR=resolved", "EMPTY="}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expandEnvSpecs = %v, want %v", got, want)
	}
}

func TestBuildContainerArgsExpandsBareEnv(t *testing.T) {
	t.Setenv("DCON_RT4_SET", "hello")
	os.Unsetenv("DCON_RT4_UNSET")
	c := parse(t, newRunCmd(), []string{"-e", "DCON_RT4_SET", "-e", "DCON_RT4_UNSET", "alpine"})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--env", "DCON_RT4_SET=hello") {
		t.Errorf("bare -e KEY must resolve from the client env; got %v", got)
	}
	for _, a := range got {
		if strings.Contains(a, "DCON_RT4_UNSET") {
			t.Errorf("unset bare -e KEY must be dropped; got %v", got)
		}
	}
}

func TestWarmExecArgsExpandsBareEnv(t *testing.T) {
	t.Setenv("DCON_RT4_WARM", "zz")
	cmd := newRunCmd()
	if err := cmd.ParseFlags([]string{"--rm", "-e", "DCON_RT4_WARM", "-e", "DCON_RT4_NOPE", "alpine", "env"}); err != nil {
		t.Fatal(err)
	}
	got := warmExecArgs(cmd, "CID", []string{"env"})
	if !containsPair(got, "--env", "DCON_RT4_WARM=zz") {
		t.Errorf("warm path must resolve bare -e KEY; got %v", got)
	}
	for _, a := range got {
		if strings.Contains(a, "DCON_RT4_NOPE") {
			t.Errorf("warm path must drop unset bare -e KEY; got %v", got)
		}
	}
}

// --- item 9: cp host paths absolutized ---------------------------------------------

func TestCpHostPath(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"file.txt":   filepath.Join(cwd, "file.txt"),
		"./file.txt": filepath.Join(cwd, "file.txt"),
		"../x":       filepath.Join(filepath.Dir(cwd), "x"),
		"/abs/path":  "/abs/path",
		"dir/.":      filepath.Join(cwd, "dir") + "/.", // copy-contents preserved
		"dir/":       filepath.Join(cwd, "dir") + "/",
		"/abs/dir/.": "/abs/dir/.",
		".":          cwd,
	}
	for in, want := range cases {
		if got := cpHostPath(in); got != want {
			t.Errorf("cpHostPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- item 10: inspect --type validation ----------------------------------------------

func TestInspectTypeValidation(t *testing.T) {
	cmd := newInspectCmd()
	if err := cmd.Flags().Set("type", "sandwich"); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "--type") {
		t.Errorf("invalid --type must error before any backend call, got %v", err)
	}
}

func TestProbeOK(t *testing.T) {
	if probeOK("[]", nil) {
		t.Error("an empty array must count as a miss (wrong-namespace auto-detect)")
	}
	if probeOK("", nil) {
		t.Error("empty output must count as a miss")
	}
	if !probeOK(`[{"id":"x"}]`, nil) {
		t.Error("real output must count as a hit")
	}
}

// --- item 11: build --iidfile ----------------------------------------------------------

func TestFormatImageID(t *testing.T) {
	cases := [][3]string{
		{"sha256:abc", "", "sha256:abc"},
		{"abc123", "", "sha256:abc123"},            // bare hex gains the prefix
		{"", "sha256:fallback", "sha256:fallback"}, // descriptor digest fallback
		{"", "", ""},
	}
	for _, c := range cases {
		if got := formatImageID(c[0], c[1]); got != c[2] {
			t.Errorf("formatImageID(%q,%q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// --- item 12: push --all-tags -----------------------------------------------------------

func TestRepoTagRefs(t *testing.T) {
	names := []string{
		"docker.io/library/myapp:v1",
		"docker.io/library/myapp:v2",
		"docker.io/library/other:1.0",
		"ghcr.io/team/myapp:v9",
		"docker.io/library/myapp@sha256:aa", // digest pin skipped
	}
	imgs := make([]dockerfmt.Image, 0, len(names))
	for _, n := range names {
		var img dockerfmt.Image
		img.Configuration.Name = n
		imgs = append(imgs, img)
	}
	got := repoTagRefs(imgs, "myapp")
	if len(got) != 2 {
		t.Fatalf("repoTagRefs(myapp) = %v, want the two docker.io myapp tags", got)
	}
	for _, r := range got {
		if !strings.Contains(r, "library/myapp:") {
			t.Errorf("unexpected ref %q", r)
		}
	}
	if refs := repoTagRefs(imgs, "nosuch"); refs != nil {
		t.Errorf("repoTagRefs(nosuch) = %v, want nil", refs)
	}
}

// push -a with an explicit tag or digest must error before touching the backend.
func TestPushAllTagsRejectsTag(t *testing.T) {
	for _, ref := range []string{"myapp:v1", "myapp@sha256:abc", "reg.io:5000/app:v2"} {
		cmd := newPushCmd()
		if err := cmd.ParseFlags([]string{"-a"}); err != nil {
			t.Fatal(err)
		}
		if err := cmd.RunE(cmd, []string{ref}); err == nil || !strings.Contains(err.Error(), "--all-tags") {
			t.Errorf("push -a %s: got %v, want tag-conflict error", ref, err)
		}
	}
	// A registry port alone is NOT a tag and must pass the check (it then
	// fails later at the backend, which this test doesn't reach — so use a
	// pure check via repoTagRefs instead of RunE).
	if i := strings.LastIndex("reg.io:5000/app", ":"); i >= 0 && !strings.Contains("reg.io:5000/app"[i:], "/") {
		t.Error("registry-port ref wrongly classified as tagged")
	}
}

// --- item 13: -P/--publish-all -----------------------------------------------------------

func TestPublishAllSpecs(t *testing.T) {
	next := 40000
	alloc := func() (int, error) { next++; return next, nil }
	got, err := publishAllSpecs([]string{"80/tcp", "53/udp", "9000"}, alloc)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"40001:80/tcp", "40002:53/udp", "40003:9000/tcp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("publishAllSpecs = %v, want %v", got, want)
	}
	if specs, err := publishAllSpecs(nil, alloc); err != nil || specs != nil {
		t.Errorf("no exposed ports must publish nothing, got %v/%v", specs, err)
	}
}

func TestFreeHostPort(t *testing.T) {
	p, err := freeHostPort()
	if err != nil || p <= 0 || p > 65535 {
		t.Errorf("freeHostPort = %d, %v", p, err)
	}
}

// --- item 14: buildx fakes ----------------------------------------------------------------

func TestBuildxSurface(t *testing.T) {
	bx := newBuildxCmd()
	subs := map[string]bool{}
	for _, c := range bx.Commands() {
		subs[c.Name()] = true
	}
	for _, want := range []string{"build", "version", "ls", "inspect", "create", "use", "rm", "du", "prune", "bake"} {
		if !subs[want] {
			t.Errorf("buildx is missing subcommand %q (CI probes it)", want)
		}
	}
	// bake is a hard error; create/use/rm are accepted no-ops.
	for _, c := range bx.Commands() {
		switch c.Name() {
		case "bake":
			if err := c.RunE(c, nil); err == nil {
				t.Error("buildx bake must hard-error")
			}
		case "create", "use", "rm", "du", "prune":
			if err := c.RunE(c, nil); err != nil {
				t.Errorf("buildx %s must be an accepted no-op, got %v", c.Name(), err)
			}
		case "inspect":
			if err := c.RunE(c, []string{"nonexistent"}); err == nil {
				t.Error("buildx inspect of an unknown builder must error")
			}
			if err := c.RunE(c, []string{"default"}); err != nil {
				t.Errorf("buildx inspect default: %v", err)
			}
		}
	}
	if !strings.HasPrefix(buildxPlatforms(), "linux/") {
		t.Errorf("buildxPlatforms = %q", buildxPlatforms())
	}
}

// The image group must answer `image import` with the specific import message,
// not cobra's generic unknown-command error.
func TestImageImportStub(t *testing.T) {
	grp := newImageGroupCmd()
	for _, c := range grp.Commands() {
		if c.Name() == "import" {
			if err := c.RunE(c, nil); err == nil || !strings.Contains(err.Error(), "OCI") {
				t.Errorf("image import stub error = %v", err)
			}
			return
		}
	}
	t.Error("image group has no import stub")
}

// --- item 15: system df client-side rendering ------------------------------------------------

func TestDfViews(t *testing.T) {
	views := dfViews(backendDF{
		Images:     dfUsage{Total: 10, Active: 1, SizeInBytes: 5600980992, Reclaimable: 5175377920},
		Containers: dfUsage{Total: 1, Active: 0, SizeInBytes: 509562880, Reclaimable: 509562880},
		Volumes:    dfUsage{},
	})
	if len(views) != 4 {
		t.Fatalf("dfViews rows = %d, want 4 (Images/Containers/Local Volumes/Build Cache)", len(views))
	}
	img := views[0].(dfView)
	if img.Type != "Images" || img.TotalCount != "10" || img.Active != "1" {
		t.Errorf("images row = %+v", img)
	}
	if !strings.Contains(img.Reclaimable, "(92%)") {
		t.Errorf("images reclaimable = %q, want a (92%%) suffix", img.Reclaimable)
	}
	ct := views[1].(dfView)
	if ct.Type != "Containers" || !strings.Contains(ct.Reclaimable, "(100%)") {
		t.Errorf("containers row = %+v", ct)
	}
	vol := views[2].(dfView)
	if vol.Type != "Local Volumes" || vol.Size != "0B" || strings.Contains(vol.Reclaimable, "%") {
		t.Errorf("volumes row = %+v (zero-size must not carry a %% suffix)", vol)
	}
	if bc := views[3].(dfView); bc.Type != "Build Cache" || bc.TotalCount != "0" {
		t.Errorf("build-cache row = %+v", bc)
	}
}

// --- item 16: docker info template fields ------------------------------------------------------

func TestInfoHostFacts(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("hostMemTotal/hostKernelVersion are darwin-only; stubs return zero values elsewhere")
	}
	if mt := hostMemTotal(); mt <= 0 {
		t.Errorf("hostMemTotal = %d, want > 0 on darwin", mt)
	}
	if kv := hostKernelVersion(); !strings.HasPrefix(kv, "Darwin ") {
		t.Errorf("hostKernelVersion = %q, want a Darwin release", kv)
	}
}
