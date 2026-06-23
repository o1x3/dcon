package cmd

import (
	"strings"
	"testing"
)

func TestWarmEligible(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want bool
	}{
		{"plain rm+cmd", []string{"--rm", "alpine", "echo", "hi"}, true},
		{"rm+env", []string{"--rm", "-e", "FOO=bar", "alpine", "env"}, true},
		{"rm+workdir+user", []string{"--rm", "-w", "/app", "-u", "root", "alpine", "pwd"}, true},
		{"interactive+tty", []string{"--rm", "-i", "-t", "alpine", "sh"}, true},
		{"no rm", []string{"alpine", "echo", "hi"}, false},
		{"no command", []string{"--rm", "alpine"}, false},
		{"detach", []string{"--rm", "-d", "alpine", "sleep", "1"}, false},
		{"volume disqualifies", []string{"--rm", "-v", "/x:/y", "alpine", "ls"}, false},
		{"publish disqualifies", []string{"--rm", "-p", "80:80", "alpine", "ls"}, false},
		{"memory disqualifies", []string{"--rm", "-m", "512m", "alpine", "ls"}, false},
		{"network disqualifies", []string{"--rm", "--network", "mynet", "alpine", "ls"}, false},
		{"name disqualifies", []string{"--rm", "--name", "x", "alpine", "ls"}, false},
		{"entrypoint disqualifies", []string{"--rm", "--entrypoint", "/b", "alpine", "ls"}, false},
		{"cap-add disqualifies", []string{"--rm", "--cap-add", "NET_ADMIN", "alpine", "ls"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newRunCmd()
			if err := cmd.ParseFlags(tc.argv); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			args := cmd.Flags().Args()
			if got := warmEligible(cmd, args); got != tc.want {
				t.Errorf("warmEligible(%v) = %v, want %v", tc.argv, got, tc.want)
			}
		})
	}
}

func TestWarmExecArgs(t *testing.T) {
	cmd := newRunCmd()
	argv := []string{"--rm", "-i", "-t", "-e", "A=1", "-e", "B=2", "-w", "/app", "-u", "1000:1000", "alpine", "sh", "-c", "echo hi"}
	if err := cmd.ParseFlags(argv); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	pos := cmd.Flags().Args() // [alpine sh -c echo hi]
	command := pos[1:]
	got := warmExecArgs(cmd, "CID", command)
	joined := strings.Join(got, " ")

	for _, want := range []string{
		"exec", "--interactive", "--tty",
		"--workdir /app", "--user 1000:1000",
		"--env A=1", "--env B=2",
		"CID sh -c echo hi",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warmExecArgs missing %q in: %s", want, joined)
		}
	}
	// The container ID must precede the command tokens.
	if iCID, iCmd := indexOf(got, "CID"), indexOf(got, "sh"); iCID < 0 || iCmd < 0 || iCID > iCmd {
		t.Errorf("expected CID before command in %v", got)
	}
}

func TestPullConcurrency(t *testing.T) {
	t.Setenv("DCON_PULL_CONCURRENCY", "")
	cmd := newPullCmd()
	if got := pullConcurrency(cmd); got != 8 {
		t.Errorf("default pullConcurrency = %d, want 8", got)
	}

	t.Setenv("DCON_PULL_CONCURRENCY", "12")
	cmd = newPullCmd()
	if got := pullConcurrency(cmd); got != 12 {
		t.Errorf("env pullConcurrency = %d, want 12", got)
	}

	// Explicit flag wins over env and is clamped.
	t.Setenv("DCON_PULL_CONCURRENCY", "12")
	cmd = newPullCmd()
	_ = cmd.ParseFlags([]string{"--max-concurrent-downloads", "99"})
	if got := pullConcurrency(cmd); got != 32 {
		t.Errorf("flag pullConcurrency = %d, want 32 (clamped)", got)
	}
}

func TestAgeAndShort(t *testing.T) {
	ages := map[int64]string{0: "0s", 5: "5s", 59: "59s", 60: "1m", 3599: "59m", 3600: "1h", 7200: "2h", -3: "0s"}
	for in, want := range ages {
		if got := age(in); got != want {
			t.Errorf("age(%d) = %q, want %q", in, got, want)
		}
	}
	if got := short("0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("short long id = %q, want 12 chars", got)
	}
	if got := short("short"); got != "short" {
		t.Errorf("short of short id = %q, want unchanged", got)
	}
}

// TestTryWarmRunFallthrough covers every path where tryWarmRun must decline to
// handle the run (so run falls back to a normal cold boot) WITHOUT touching the
// container backend: pooling disabled, ineligible flags, and an empty pool.
func TestTryWarmRunFallthrough(t *testing.T) {
	cases := []struct {
		name string
		warm string   // DCON_WARM value
		argv []string // run argv
	}{
		{"disabled forces cold", "off", []string{"--rm", "alpine", "echo", "hi"}},
		{"ineligible: no --rm", "", []string{"alpine", "echo", "hi"}},
		{"ineligible: volume", "", []string{"--rm", "-v", "/a:/b", "alpine", "ls"}},
		{"eligible but empty pool", "", []string{"--rm", "alpine", "echo", "hi"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir()) // empty, private pool state
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("DCON_WARM", tc.warm)
			cmd := newRunCmd()
			if err := cmd.ParseFlags(tc.argv); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			handled, err := tryWarmRun(cmd, cmd.Flags().Args())
			if handled {
				t.Errorf("tryWarmRun handled=true, want false (should fall through to cold)")
			}
			if err != nil {
				t.Errorf("tryWarmRun err=%v, want nil on fall-through", err)
			}
		})
	}
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}
