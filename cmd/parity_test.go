package cmd

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

// TestRunExtendedFlagsAcceptedNotLeaked is the core run/create parity guarantee:
// the full Docker flag surface parses without error, and flags the backend
// cannot honor are dropped rather than forwarded to `container`.
func TestRunExtendedFlagsAcceptedNotLeaked(t *testing.T) {
	args := []string{
		"--security-opt", "seccomp=unconfined",
		"--pids-limit", "100",
		"--volumes-from", "other",
		"--health-cmd", "curl localhost",
		"--no-healthcheck",
		"--pid", "host",
		"--ipc", "host",
		"--uts", "host",
		"--userns", "host",
		"--cgroupns", "host",
		"--log-driver", "json-file",
		"--log-opt", "max-size=10m",
		"--memory-swappiness", "60",
		"--cgroup-parent", "/x",
		"--isolation", "default",
		"--ip", "10.0.0.5",
		"--storage-opt", "size=10G",
		"--stop-timeout", "5",
		"--annotation", "k=v",
		"--blkio-weight", "500",
		"--device-read-bps", "/dev/sda:1mb",
		"--link", "db:db",
		"--network-alias", "web",
		"--sysctl", "net.core.somaxconn=1024",
		"alpine", "true",
	}
	c := parse(t, newRunCmd(), args)
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatalf("extended Docker flags must be accepted, got error: %v", err)
	}
	leaked := []string{
		"--security-opt", "--pids-limit", "--volumes-from", "--health-cmd",
		"--no-healthcheck", "--pid", "--ipc", "--uts", "--userns", "--cgroupns",
		"--log-driver", "--log-opt", "--memory-swappiness", "--cgroup-parent",
		"--isolation", "--mac-address", "--ip", "--storage-opt", "--stop-timeout",
		"--annotation", "--blkio-weight", "--device-read-bps", "--link",
		"--network-alias", "--sysctl",
	}
	for _, l := range leaked {
		if contains(got, l) {
			t.Errorf("%s must not reach the backend; got %v", l, got)
		}
	}
	if !contains(got, "alpine") || !contains(got, "true") {
		t.Errorf("image/command must survive translation; got %v", got)
	}
}

// TestRunMacAddressInExtendedSurfaceAccepted positively asserts that
// --mac-address is accepted alongside other Docker flags and reaches the
// backend as --network …,mac=… (not dropped, not leaked as --mac-address).
func TestRunMacAddressInExtendedSurfaceAccepted(t *testing.T) {
	c := parse(t, newRunCmd(), []string{
		"--mac-address", "02:42:ac:11:00:02",
		"--security-opt", "seccomp=unconfined",
		"--pids-limit", "100",
		"alpine",
	})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--network", "default,mac=02:42:ac:11:00:02") {
		t.Errorf("mac-address should translate; got %v", got)
	}
	if contains(got, "--mac-address") || contains(got, "--security-opt") || contains(got, "--pids-limit") {
		t.Errorf("docker-only flags leaked: %v", got)
	}
}

// TestCreateExtendedFlagsAccepted ensures `create` shares the same surface.
func TestCreateExtendedFlagsAccepted(t *testing.T) {
	c := parse(t, newCreateCmd(), []string{"--cpuset-cpus", "0,1", "--oom-kill-disable", "alpine"})
	if _, err := buildContainerArgs(c, c.Flags().Args(), "create"); err != nil {
		t.Fatalf("create must accept extended flags: %v", err)
	}
}

// parseOnly asserts a command parses a flag set without an unknown-flag error.
func parseOnly(t *testing.T, c *cobra.Command, cli []string) {
	t.Helper()
	if err := c.ParseFlags(cli); err != nil {
		t.Fatalf("ParseFlags(%v): %v", cli, err)
	}
}

// TestComposeFlagSurfaceParses guards against regressions where a common
// Compose flag is missing and the whole command hard-fails as "unknown flag".
func TestComposeFlagSurfaceParses(t *testing.T) {
	cases := []struct {
		name string
		cmd  func() *cobra.Command
		cli  []string
	}{
		{"up", composeUp, []string{"-d", "--no-deps", "--quiet-pull", "--remove-orphans", "-V", "--abort-on-container-exit", "--wait"}},
		{"down", composeDown, []string{"-v", "--rmi", "all", "--remove-orphans", "-t", "5"}},
		{"build", composeBuild, []string{"--no-cache", "--pull", "-q", "--build-arg", "K=V"}},
		{"run", composeRun, []string{"--rm", "-d", "-T", "--no-deps", "--service-ports", "--build"}},
		{"exec", composeExec, []string{"-T", "-u", "root", "--index", "2"}},
		{"ps", composePs, []string{"-a", "-q", "--filter", "status=running", "--no-trunc"}},
		{"pull", composePull, []string{"-q", "--ignore-pull-failures", "--include-deps"}},
		{"create", composeCreate, []string{"--force-recreate", "--pull", "missing", "-y"}},
		{"logs", composeLogs, []string{"-f", "--tail", "10", "--no-color", "--no-log-prefix"}},
		{"push", composePush, []string{"-q", "--ignore-push-failures"}},
		{"port", composePort, []string{"--index", "1", "--protocol", "tcp"}},
		{"attach", composeAttach, []string{"--index", "1", "--no-stdin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parseOnly(t, tc.cmd(), tc.cli)
		})
	}
}

// TestComposeGlobalFlagsParse covers Docker Compose's global flags on the
// compose group (e.g. `docker compose --progress plain up`).
func TestComposeGlobalFlagsParse(t *testing.T) {
	parseOnly(t, newComposeCmd(), []string{
		"--progress", "plain", "--ansi", "never", "--parallel", "4",
		"--compatibility", "--dry-run", "--env-file", ".env",
		"--file", "compose.yaml", "--project-name", "proj", "--profile", "dev",
	})
}

func TestRewriteComposeGlobalShorthands(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		// -f before the subcommand becomes --file
		{[]string{"compose", "-f", "x.yml", "up"}, []string{"compose", "--file", "x.yml", "up"}},
		{[]string{"compose", "-p", "proj", "down"}, []string{"compose", "--project-name", "proj", "down"}},
		// attached value forms
		{[]string{"compose", "-fx.yml", "up"}, []string{"compose", "--file", "x.yml", "up"}},
		{[]string{"compose", "-f=x.yml", "up"}, []string{"compose", "--file", "x.yml", "up"}},
		// multiple files + project
		{[]string{"compose", "-f", "a.yml", "-f", "b.yml", "-p", "x", "up", "-d"},
			[]string{"compose", "--file", "a.yml", "--file", "b.yml", "--project-name", "x", "up", "-d"}},
		// the subcommand's own -f (logs --follow) must NOT be rewritten
		{[]string{"compose", "logs", "-f"}, []string{"compose", "logs", "-f"}},
		{[]string{"compose", "-f", "x.yml", "logs", "-f", "web"}, []string{"compose", "--file", "x.yml", "logs", "-f", "web"}},
		// rm -f (force) after subcommand untouched
		{[]string{"compose", "rm", "-f"}, []string{"compose", "rm", "-f"}},
		// run -p (publish) after subcommand untouched
		{[]string{"compose", "run", "-p", "8080:80", "web"}, []string{"compose", "run", "-p", "8080:80", "web"}},
		// long forms pass through; boolean global doesn't swallow the subcommand
		{[]string{"compose", "--dry-run", "up"}, []string{"compose", "--dry-run", "up"}},
		{[]string{"compose", "--profile", "dev", "-f", "x.yml", "up"}, []string{"compose", "--profile", "dev", "--file", "x.yml", "up"}},
		// not a compose invocation: leave untouched (e.g. running an image named compose)
		{[]string{"run", "-f", "compose"}, []string{"run", "-f", "compose"}},
		{[]string{"ps", "-a"}, []string{"ps", "-a"}},
		// root flags BEFORE `compose` must not block the -f/-p rewrite
		{[]string{"-D", "compose", "-f", "x.yml", "up"}, []string{"-D", "compose", "--file", "x.yml", "up"}},
		{[]string{"--host", "tcp://x", "compose", "-f", "x.yml", "up"},
			[]string{"--host", "tcp://x", "compose", "--file", "x.yml", "up"}},
		{[]string{"--host=x", "compose", "-p", "proj", "down"},
			[]string{"--host=x", "compose", "--project-name", "proj", "down"}},
		{[]string{"-D", "--context", "c", "compose", "-fx.yml", "up"},
			[]string{"-D", "--context", "c", "compose", "--file", "x.yml", "up"}},
		// root flag before a non-compose subcommand: untouched
		{[]string{"-D", "ps", "-a"}, []string{"-D", "ps", "-a"}},
	}
	for _, tc := range cases {
		got := rewriteComposeGlobalShorthands(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("rewrite(%v) = %v; want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("rewrite(%v) = %v; want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestKernelAlreadyInstalled(t *testing.T) {
	// The real backend EEXIST failure (NSCocoa 516 wrapping POSIX 17).
	cocoa := errors.New(`Error Domain=NSCocoaErrorDomain Code=516 "vmlinux-6.18.15-186 couldn't be copied to "kernels" because an item with the same name already exists." UserInfo={NSUnderlyingError=0x0 {Error Domain=NSPOSIXErrorDomain Code=17 "File exists"}}`)
	if !kernelAlreadyInstalled(cocoa) {
		t.Error("the backend's already-exists EEXIST error should be detected as already-installed")
	}
	if !kernelAlreadyInstalled(errors.New("copy failed: File exists")) {
		t.Error("a POSIX 'File exists' error should be detected")
	}
	// Genuine failures must NOT be swallowed.
	for _, e := range []error{
		nil,
		errors.New("network error: could not download kernel"),
		errors.New("no space left on device"),
		errors.New("Error Domain=NSURLErrorDomain Code=-1009 offline"),
	} {
		if kernelAlreadyInstalled(e) {
			t.Errorf("a genuine error must not be treated as already-installed: %v", e)
		}
	}
}

func TestCurrentContextHonorsEnv(t *testing.T) {
	t.Setenv("DOCKER_CONTEXT", "")
	if got := currentContextName(); got != defaultContextName {
		t.Errorf("default context = %q; want %q", got, defaultContextName)
	}
	t.Setenv("DOCKER_CONTEXT", "prod")
	if got := currentContextName(); got != "prod" {
		t.Errorf("DOCKER_CONTEXT should be honored; got %q", got)
	}
}

func TestDockerHostHonorsEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	if got := dockerHost(); got != "unix:///var/run/docker.sock" {
		t.Errorf("default docker host = %q", got)
	}
	t.Setenv("DOCKER_HOST", "tcp://1.2.3.4:2375")
	if got := dockerHost(); got != "tcp://1.2.3.4:2375" {
		t.Errorf("DOCKER_HOST should be honored; got %q", got)
	}
}

func TestContextInspectShape(t *testing.T) {
	m := contextInspect()
	if m["Name"] != defaultContextName {
		t.Errorf("context Name = %v; want %q", m["Name"], defaultContextName)
	}
	eps, ok := m["Endpoints"].(map[string]any)
	if !ok {
		t.Fatalf("Endpoints missing or wrong type: %v", m["Endpoints"])
	}
	d, ok := eps["docker"].(map[string]any)
	if !ok {
		t.Fatalf("docker endpoint missing: %v", eps)
	}
	if d["Host"] == "" || d["Host"] == nil {
		t.Errorf("docker endpoint Host must be set; got %v", d["Host"])
	}
}

// TestContextUseRejectsUnknown verifies `context use other` fails but the
// built-in context is accepted.
func TestContextUseRejectsUnknown(t *testing.T) {
	find := func(name string) *cobra.Command {
		for _, c := range newContextGroupCmd().Commands() {
			if c.Name() == name {
				return c
			}
		}
		t.Fatalf("context subcommand %q not registered", name)
		return nil
	}
	use := find("use")
	if err := use.RunE(use, []string{"default"}); err != nil {
		t.Errorf("use default should succeed; got %v", err)
	}
	if err := use.RunE(use, []string{"nonexistent"}); err == nil {
		t.Error("use of an unknown context should error")
	}
}
