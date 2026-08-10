package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"dcon/internal/machine"
)

// TestRunReadOnlyAndMaskedPaths forwards Apple container 1.2.1 path flags
// verbatim (boot-bound extras, like --kernel / --publish-socket).
func TestRunReadOnlyAndMaskedPaths(t *testing.T) {
	c := parse(t, newRunCmd(), []string{
		"--masked-path", "NONE",
		"--masked-path", "/run/secrets",
		"--read-only-path", "/tmp",
		"alpine",
	})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--masked-path", "NONE") || !containsPair(got, "--masked-path", "/run/secrets") {
		t.Errorf("masked-path missing: %v", got)
	}
	if !containsPair(got, "--read-only-path", "/tmp") {
		t.Errorf("read-only-path missing: %v", got)
	}
}

// TestRunKernelArg forwards repeatable --kernel-arg (Apple container 1.2.0+).
func TestRunKernelArg(t *testing.T) {
	c := parse(t, newRunCmd(), []string{
		"--kernel-arg", "console=hvc0",
		"--kernel-arg", "quiet",
		"alpine",
	})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--kernel-arg", "console=hvc0") || !containsPair(got, "--kernel-arg", "quiet") {
		t.Errorf("kernel-arg missing: %v", got)
	}
}

// TestRunSecurityOptSystempathsUnconfined maps Docker's systempaths=unconfined
// onto --masked-path NONE and still warns on other security-opt values.
func TestRunSecurityOptSystempathsUnconfined(t *testing.T) {
	restore := captureStderr(t)
	c := parse(t, newRunCmd(), []string{
		"--security-opt", "systempaths=unconfined",
		"--security-opt", "seccomp=unconfined",
		"alpine",
	})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	out := restore()
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--masked-path", "NONE") {
		t.Errorf("systempaths=unconfined should emit --masked-path NONE; got %v", got)
	}
	if contains(got, "--security-opt") {
		t.Errorf("--security-opt must not leak: %v", got)
	}
	if !strings.Contains(out, "seccomp=unconfined") {
		t.Errorf("expected warning for seccomp; stderr=%q", out)
	}
	if strings.Contains(strings.ToLower(out), "systempaths") {
		t.Errorf("systempaths should not be warned; stderr=%q", out)
	}
}

// TestRunSecurityOptThenMaskedPathOrder ensures NONE from systempaths lands
// before explicit --masked-path so Apple's ordered processing rebuilds the set.
func TestRunSecurityOptThenMaskedPathOrder(t *testing.T) {
	c := parse(t, newRunCmd(), []string{
		"--security-opt", "systempaths=unconfined",
		"--masked-path", "/run/secrets",
		"alpine",
	})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	noneIdx, secretsIdx := -1, -1
	for i := 0; i+1 < len(got); i++ {
		if got[i] == "--masked-path" && got[i+1] == "NONE" {
			noneIdx = i
		}
		if got[i] == "--masked-path" && got[i+1] == "/run/secrets" {
			secretsIdx = i
		}
	}
	if noneIdx < 0 || secretsIdx < 0 || noneIdx >= secretsIdx {
		t.Errorf("want NONE before /run/secrets; got %v", got)
	}
}

// TestBuildSSHDefault wires Docker --ssh default onto Apple's --ssh default.
func TestBuildSSHDefault(t *testing.T) {
	for _, spec := range []string{"default", "id=default"} {
		c := parse(t, newBuildCmd(), []string{"--ssh", spec, "-t", "img", "."})
		got, err := buildBuildArgs(c, c.Flags().Args())
		if err != nil {
			t.Fatal(err)
		}
		if !containsPair(got, "--ssh", "default") {
			t.Errorf("--ssh %s should map to --ssh default; got %v", spec, got)
		}
	}
}

// TestBuildSSHUnsupportedWarnsAndDrops key-file / named-agent forms.
func TestBuildSSHUnsupportedWarnsAndDrops(t *testing.T) {
	restore := captureStderr(t)
	c := parse(t, newBuildCmd(), []string{"--ssh", "id=mykey,src=/tmp/k", "-t", "img", "."})
	got, err := buildBuildArgs(c, c.Flags().Args())
	out := restore()
	if err != nil {
		t.Fatal(err)
	}
	if contains(got, "--ssh") {
		t.Errorf("unsupported --ssh must not reach backend; got %v", got)
	}
	if !strings.Contains(out, "not supported") {
		t.Errorf("expected warning; stderr=%q", out)
	}
}

// TestBuildSSHMixed enables default while warning on the bad sibling.
func TestBuildSSHMixed(t *testing.T) {
	restore := captureStderr(t)
	c := parse(t, newBuildCmd(), []string{
		"--ssh", "id=foo",
		"--ssh", "default",
		"-t", "img", ".",
	})
	got, err := buildBuildArgs(c, c.Flags().Args())
	out := restore()
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--ssh", "default") {
		t.Errorf("default should still enable; got %v", got)
	}
	if !strings.Contains(out, "id=foo") {
		t.Errorf("expected warning for id=foo; stderr=%q", out)
	}
}

// TestWarmIneligibleApple12Flags keeps boot-bound 1.2.x extras off the warm path.
func TestWarmIneligibleApple12Flags(t *testing.T) {
	cases := [][]string{
		{"--rm", "--masked-path", "/proc/kcore", "alpine", "true"},
		{"--rm", "--read-only-path", "/tmp", "alpine", "true"},
		{"--rm", "--kernel-arg", "quiet", "alpine", "true"},
		{"--rm", "--security-opt", "systempaths=unconfined", "alpine", "true"},
	}
	for _, argv := range cases {
		c := parse(t, newRunCmd(), argv)
		if warmEligible(c, c.Flags().Args()) {
			t.Errorf("warmEligible(%v) = true, want false", argv)
		}
	}
}

// TestK8sCommandRegistered exposes Apple's experimental k8s plugin via dcon.
func TestK8sCommandRegistered(t *testing.T) {
	var found bool
	for _, c := range rootCmd.Commands() {
		if c.Name() == "k8s" {
			found = true
			if !c.DisableFlagParsing {
				t.Error("k8s should DisableFlagParsing for passthrough")
			}
			break
		}
	}
	if !found {
		t.Fatal("top-level k8s command not registered")
	}
}

// TestMachineCreateKernelArgFlag wires --kernel-arg onto CreateOpts.
func TestMachineCreateKernelArgFlag(t *testing.T) {
	cmd := machineCreateCmd()
	if f := cmd.Flags().Lookup("kernel-arg"); f == nil {
		t.Fatal("machine create missing --kernel-arg")
	}
	if err := cmd.ParseFlags([]string{
		"--kernel-arg", "console=hvc0",
		"--kernel-arg", "quiet",
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := cmd.Flags().GetStringArray("kernel-arg")
	got, err := machine.BuildRunArgs(machine.CreateOpts{
		Name: "u", Distro: "ubuntu", Image: "ubuntu:latest",
		KernelArgs: args,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--kernel-arg", "console=hvc0") || !containsPair(got, "--kernel-arg", "quiet") {
		t.Errorf("kernel-arg missing from machine args: %v", got)
	}
}

// TestExportHelpMentionsLiveSnapshot documents the 1.2.1 live-export behavior.
func TestExportHelpMentionsLiveSnapshot(t *testing.T) {
	c := newExportCmd()
	if !strings.Contains(c.Long, "1.2.1") && !strings.Contains(c.Long, "snapshot") {
		t.Errorf("export Long should mention live snapshot; got %q", c.Long)
	}
}

func captureStderr(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	return func() string {
		_ = w.Close()
		<-done
		_ = r.Close()
		os.Stderr = old
		return buf.String()
	}
}
