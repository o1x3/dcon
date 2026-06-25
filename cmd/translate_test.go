package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func parse(t *testing.T, c *cobra.Command, cli []string) *cobra.Command {
	t.Helper()
	if err := c.ParseFlags(cli); err != nil {
		t.Fatalf("ParseFlags(%v): %v", cli, err)
	}
	return c
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

func TestRunTranslationExact(t *testing.T) {
	c := parse(t, newRunCmd(), []string{
		"-it", "--rm", "--name", "web", "-e", "A=1", "-e", "B=2",
		"-p", "8080:80", "-v", "/d:/d", "-m", "512m", "--cpus", "2",
		"nginx", "echo", "hi",
	})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run", "--interactive", "--tty", "--rm",
		"--name", "web", "--memory", "512m", "--cpus", "2",
		"--volume", "/d:/d",
		"--env", "A=1", "--env", "B=2",
		"--publish", "8080:80",
		"nginx", "echo", "hi",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("run translation:\n got=%v\nwant=%v", got, want)
	}
}

func TestRunPrivilegedMapsToCapAddAll(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--privileged", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	if !containsPair(got, "--cap-add", "ALL") {
		t.Errorf("--privileged should map to --cap-add ALL; got %v", got)
	}
}

func TestRunNetAliasAndDNSOpt(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--net", "mynet", "--dns-opt", "ndots:2", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	if !containsPair(got, "--network", "mynet") {
		t.Errorf("--net should map to --network; got %v", got)
	}
	if !containsPair(got, "--dns-option", "ndots:2") {
		t.Errorf("--dns-opt should map to --dns-option; got %v", got)
	}
}

func TestRunUnsupportedFlagsDropped(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--restart", "always", "--gpus", "all", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	if contains(got, "--restart") || contains(got, "--gpus") {
		t.Errorf("unsupported flags must not reach the backend; got %v", got)
	}
}

func TestCreateSubcommand(t *testing.T) {
	c := parse(t, newCreateCmd(), []string{"--name", "x", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "create")
	if len(got) == 0 || got[0] != "create" {
		t.Errorf("create should start with 'create'; got %v", got)
	}
}

func TestRunTmpfsPathOnly(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--tmpfs", "/tmp", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	if !containsPair(got, "--tmpfs", "/tmp") {
		t.Errorf("path-only --tmpfs should stay --tmpfs; got %v", got)
	}
}

func TestRunTmpfsSizeBecomesMount(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--tmpfs", "/run:rw,size=64m,mode=1777", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	if !containsPair(got, "--mount", "type=tmpfs,destination=/run,size=64m,mode=1777") {
		t.Errorf("--tmpfs with size/mode should become a tmpfs --mount; got %v", got)
	}
}

func TestRunCpusFloatRoundsUp(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--cpus", "1.5", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	if !containsPair(got, "--cpus", "2") {
		t.Errorf("--cpus 1.5 should round up to 2; got %v", got)
	}
}

func TestRunCpusInvalid(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--cpus", "abc", "alpine"})
	if _, err := buildContainerArgs(c, c.Flags().Args(), "run"); err == nil {
		t.Error("--cpus abc should error")
	}
	c2 := parse(t, newRunCmd(), []string{"--cpus", "0", "alpine"})
	if _, err := buildContainerArgs(c2, c2.Flags().Args(), "run"); err == nil {
		t.Error("--cpus 0 should error")
	}
}

func TestRunNetworkHostRejected(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--network", "host", "alpine"})
	if _, err := buildContainerArgs(c, c.Flags().Args(), "run"); err == nil {
		t.Error("--network host should error")
	}
}

func TestRunVolumeStripsSELinux(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"-v", "/h:/c:ro,z", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	if !containsPair(got, "--volume", "/h:/c:ro") {
		t.Errorf("SELinux :z should be stripped, ro kept; got %v", got)
	}
}

func TestRunMountTmpfsKeysRewritten(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--mount", "type=tmpfs,destination=/run,tmpfs-size=64m", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	if !containsPair(got, "--mount", "type=tmpfs,destination=/run,size=64m") {
		t.Errorf("tmpfs-size should be rewritten to size; got %v", got)
	}
}

// TestRunMountValuelessTmpfsKeyNoPanic reproduces a slice-out-of-range panic:
// a --mount field of a bare "tmpfs-size"/"tmpfs-mode" (no =value) matched the
// rewrite case and sliced past the string end, crashing the process. It must
// now pass the field through untouched instead.
func TestRunMountValuelessTmpfsKeyNoPanic(t *testing.T) {
	for _, spec := range []string{
		"type=tmpfs,destination=/x,tmpfs-size",
		"type=tmpfs,destination=/x,tmpfs-mode",
	} {
		c := parse(t, newRunCmd(), []string{"--mount", spec, "alpine"})
		got, err := buildContainerArgs(c, c.Flags().Args(), "run") // must not panic
		if err != nil {
			t.Fatalf("%q: unexpected error %v", spec, err)
		}
		if !containsPair(got, "--mount", spec) {
			t.Errorf("%q: expected the --mount payload passed through untouched; got %v", spec, got)
		}
	}
}

// TestRunRejectsMachineNamespace verifies `dcon run` cannot forge a container
// that `dcon machine` would resolve: the reserved `dcon-machine-` name prefix
// and the `dcon.machine*` labels are rejected, closing the confused-deputy hole
// where `machine stop foo` could act on a user container (see cmd/run.go).
func TestRunRejectsMachineNamespace(t *testing.T) {
	cases := [][]string{
		{"--name", "dcon-machine-foo", "alpine"},
		{"--label", "dcon.machine=1", "alpine"},
		{"--label", "dcon.machine.name=foo", "alpine"},
	}
	for _, cli := range cases {
		c := parse(t, newRunCmd(), cli)
		if _, err := buildContainerArgs(c, c.Flags().Args(), "run"); err == nil {
			t.Errorf("args %v: expected rejection of the reserved machine namespace", cli)
		}
	}
	// A normal name/label must still work.
	c := parse(t, newRunCmd(), []string{"--name", "web", "--label", "env=prod", "alpine"})
	if _, err := buildContainerArgs(c, c.Flags().Args(), "run"); err != nil {
		t.Errorf("ordinary name/label must be accepted; got %v", err)
	}
}

func TestBuildTranslation(t *testing.T) {
	c := newBuildCmd()
	parse(t, c, []string{
		"-t", "img:1", "-f", "Dockerfile", "--build-arg", "X=1",
		"--no-cache", "--target", "prod", "--platform", "linux/arm64",
		"--cache-from", "type=registry,ref=foo", "--progress", "rawjson", ".",
	})
	got, err := buildBuildArgs(c, c.Flags().Args())
	if err != nil {
		t.Fatal(err)
	}
	checks := [][2]string{
		{"--tag", "img:1"}, {"--file", "Dockerfile"}, {"--build-arg", "X=1"},
		{"--target", "prod"}, {"--platform", "linux/arm64"},
		{"--cache-in", "type=registry,ref=foo"}, {"--progress", "plain"},
	}
	for _, ck := range checks {
		if !containsPair(got, ck[0], ck[1]) {
			t.Errorf("build missing %s %s; got %v", ck[0], ck[1], got)
		}
	}
	if !contains(got, "--no-cache") {
		t.Errorf("build missing --no-cache; got %v", got)
	}
	if got[len(got)-1] != "." {
		t.Errorf("build context should be last arg; got %v", got)
	}
}

func TestTranslateOutput(t *testing.T) {
	cases := []struct {
		in, wantOut, wantTag string
		err                  bool
	}{
		{in: "type=oci", wantOut: "type=oci"},
		{in: "type=local,dest=out", wantOut: "type=local,dest=out"},
		{in: "type=docker,dest=x.tar", wantOut: "type=oci,dest=x.tar"},
		// no dest: omit --output (backend default is local-store load), carry name as tag
		{in: "type=image,name=foo", wantOut: "", wantTag: "foo"},
		{in: "type=docker", wantOut: "", wantTag: ""},
		{in: "type=registry,ref=foo", err: true},
	}
	for _, c := range cases {
		out, tag, err := translateOutput(c.in)
		if c.err {
			if err == nil {
				t.Errorf("translateOutput(%q) expected error", c.in)
			}
			continue
		}
		if err != nil || out != c.wantOut || tag != c.wantTag {
			t.Errorf("translateOutput(%q) = (out=%q,tag=%q,err=%v); want (out=%q,tag=%q)",
				c.in, out, tag, err, c.wantOut, c.wantTag)
		}
	}
}

// TestBuildOutputNamePreservedAsTag reproduces the bug where '--output
// type=docker,name=X' (no -t) silently dropped the name, producing an untagged
// image. The name must become a --tag, and no bogus --output should be emitted
// for the dest-less local-store load.
func TestBuildOutputNamePreservedAsTag(t *testing.T) {
	c := parse(t, newBuildCmd(), []string{"--output", "type=docker,name=myrepo:1", "."})
	got, err := buildBuildArgs(c, c.Flags().Args())
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--tag", "myrepo:1") {
		t.Errorf("name= should become --tag myrepo:1; got %v", got)
	}
	if contains(got, "--output") {
		t.Errorf("dest-less type=docker should omit --output (backend default load); got %v", got)
	}
	// With an explicit -t, the name= must not add a second tag.
	c2 := parse(t, newBuildCmd(), []string{"-t", "explicit:9", "--output", "type=image,name=other", "."})
	got2, _ := buildBuildArgs(c2, c2.Flags().Args())
	if !containsPair(got2, "--tag", "explicit:9") || containsPair(got2, "--tag", "other") {
		t.Errorf("explicit -t should win; name= must not add a tag; got %v", got2)
	}
}

func TestExecTranslation(t *testing.T) {
	c := newExecCmd()
	parse(t, c, []string{"-it", "-u", "root", "-w", "/app", "-e", "K=V", "ct", "sh", "-c", "ls"})
	got := buildExecArgs(c, c.Flags().Args())
	want := []string{"exec", "--interactive", "--tty", "--user", "root", "--workdir", "/app", "--env", "K=V", "ct", "sh", "-c", "ls"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("exec translation:\n got=%v\nwant=%v", got, want)
	}
}

func TestExecStopsFlagParseAtContainer(t *testing.T) {
	// flags after the container id/cmd must be passed through, not parsed
	c := newExecCmd()
	parse(t, c, []string{"-i", "ct", "ls", "-la"})
	got := buildExecArgs(c, c.Flags().Args())
	if !contains(got, "-la") {
		t.Errorf("flags after the command must pass through; got %v", got)
	}
}

func TestRunCommandArgsPassThrough(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"alpine", "sh", "-c", "echo hi"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	joined := strings.Join(got, " ")
	if !strings.HasSuffix(joined, "alpine sh -c echo hi") {
		t.Errorf("image+command should pass through verbatim; got %q", joined)
	}
}
