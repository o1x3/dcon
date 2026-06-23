package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dcon/internal/pool"
	rt "dcon/internal/runtime"

	"github.com/spf13/cobra"
)

// checkLevel is the severity/outcome of a single doctor check.
type checkLevel int

const (
	levelOK checkLevel = iota
	levelWarn
	levelFail
)

func (l checkLevel) symbol() string {
	switch l {
	case levelOK:
		return "✓"
	case levelWarn:
		return "!"
	default:
		return "✗"
	}
}

// check is one diagnostic line: what was probed, the outcome, a human detail,
// and (for non-OK results) a remediation hint.
type check struct {
	name   string
	level  checkLevel
	detail string
	hint   string
}

// parseSystemStatus turns `container system status` key/value output into a
// map. Each non-empty line is "<key>  <value...>"; the header row is harmless.
func parseSystemStatus(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		m[strings.ToLower(fields[0])] = strings.Join(fields[1:], " ")
	}
	return m
}

// renderChecks formats checks into an aligned report and reports whether any
// check hard-failed (used for the process exit code). Pure — unit-tested.
func renderChecks(checks []check) (string, bool) {
	width := 0
	for _, c := range checks {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	var b strings.Builder
	anyFail := false
	for _, c := range checks {
		if c.level == levelFail {
			anyFail = true
		}
		fmt.Fprintf(&b, "  %s  %-*s  %s\n", c.level.symbol(), width, c.name, c.detail)
		if c.level != levelOK && c.hint != "" {
			fmt.Fprintf(&b, "      %*s  ↳ %s\n", width, "", c.hint)
		}
	}
	return b.String(), anyFail
}

// gatherChecks probes the backend and environment. It is the integration half
// of doctor (renderChecks is the testable half).
func gatherChecks() []check {
	var checks []check

	// 1. Apple container CLI present.
	ver, verErr := rt.CaptureSilent("--version")
	ver = strings.TrimSpace(ver)
	if verErr != nil || ver == "" {
		checks = append(checks, check{
			name: "Apple container CLI", level: levelFail,
			detail: "not found at " + rt.Bin(),
			hint:   "install from https://github.com/apple/container/releases (or `brew install --cask container`)",
		})
		// Without the backend binary, the remaining probes are meaningless.
		return checks
	}
	checks = append(checks, check{name: "Apple container CLI", level: levelOK, detail: ver})

	// 2. Backend services running.
	statusOut, _ := rt.CaptureSilent("system", "status")
	if parseSystemStatus(statusOut)["status"] == "running" {
		checks = append(checks, check{name: "Backend services", level: levelOK, detail: "running"})
	} else {
		checks = append(checks, check{
			name: "Backend services", level: levelFail, detail: "not running",
			hint: "start them with `dcon system start`",
		})
	}

	// 3. Guest kernel installed (required to boot containers; read-only
	//    commands work without it, so this is a warning, not a failure).
	if kernelInstalled() {
		checks = append(checks, check{name: "Guest kernel", level: levelOK, detail: "installed"})
	} else {
		checks = append(checks, check{
			name: "Guest kernel", level: levelWarn, detail: "none installed",
			hint: "install one with `dcon system kernel set --recommended` (needed to run containers)",
		})
	}

	// 4. Image builder (only needed for `build`; warn if absent).
	builderOut, _ := rt.CaptureSilent("builder", "status")
	if strings.Contains(builderOut, "running") {
		checks = append(checks, check{name: "Image builder", level: levelOK, detail: "running"})
	} else {
		checks = append(checks, check{
			name: "Image builder", level: levelWarn, detail: "not running",
			hint: "starts on first `dcon build`, or run `dcon builder start`",
		})
	}

	// 5. docker drop-in symlink (informational).
	checks = append(checks, dockerLinkCheck())

	// 6. Warm pool status (informational).
	members := pool.List()
	mode := "manual (seed with `dcon warm`)"
	switch {
	case pool.Disabled():
		mode = "off (always cold)"
	case pool.AutoEnabled():
		mode = "auto (self-priming)"
	}
	checks = append(checks, check{
		name: "Warm pool", level: levelOK,
		detail: fmt.Sprintf("%d ready · mode: %s", len(members), mode),
	})

	return checks
}

// kernelInstalled reports whether a guest kernel is present, by checking the
// backend's kernels directory under the application-support root.
func kernelInstalled() bool {
	entries, err := os.ReadDir(filepath.Join(rt.AppRoot(), "kernels"))
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// dockerLinkCheck reports whether `docker` on PATH resolves to this dcon binary.
func dockerLinkCheck() check {
	self, _ := os.Executable()
	dpath, err := lookDocker()
	if err != nil {
		return check{
			name: "docker drop-in", level: levelWarn, detail: "`docker` not on PATH",
			hint: "alias docker=dcon, or `make link-docker`, to reuse existing scripts",
		}
	}
	if self != "" && sameFile(dpath, self) {
		return check{name: "docker drop-in", level: levelOK, detail: "docker → dcon"}
	}
	return check{
		name: "docker drop-in", level: levelWarn,
		detail: "`docker` points to " + dpath,
		hint:   "alias docker=dcon to route Docker commands through dcon",
	}
}

func lookDocker() (string, error) { return exec.LookPath("docker") }

// sameFile reports whether two paths resolve to the same on-disk file (so a
// `docker` symlink pointing at the dcon binary counts as linked).
func sameFile(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "doctor",
		Short:         "Diagnose the dcon/Apple-container setup and suggest fixes",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, anyFail := renderChecks(gatherChecks())
			fmt.Fprintf(os.Stdout, "dcon doctor — environment check\n\n")
			fmt.Fprint(os.Stdout, report)
			if anyFail {
				fmt.Fprintln(os.Stdout, "\nSome checks failed — see hints above.")
				return fmt.Errorf("doctor: one or more checks failed")
			}
			fmt.Fprintln(os.Stdout, "\nAll required checks passed.")
			return nil
		},
	}
}
