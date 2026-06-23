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
		"--env", "A=1", "--env", "B=2",
		"--volume", "/d:/d", "--publish", "8080:80",
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

func TestRunTmpfsStripsOptions(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--tmpfs", "/tmp:rw,size=64m", "alpine"})
	got, _ := buildContainerArgs(c, c.Flags().Args(), "run")
	if !containsPair(got, "--tmpfs", "/tmp") {
		t.Errorf("--tmpfs path should be stripped of options; got %v", got)
	}
}

func TestBuildTranslation(t *testing.T) {
	c := newBuildCmd()
	parse(t, c, []string{
		"-t", "img:1", "-f", "Dockerfile", "--build-arg", "X=1",
		"--no-cache", "--target", "prod", "--platform", "linux/arm64",
		"--cache-from", "type=registry,ref=foo", "--progress", "rawjson", ".",
	})
	got := buildBuildArgs(c, c.Flags().Args())
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
