package cmd

import (
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
		"--mac-address", "02:42:ac:11:00:02",
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
