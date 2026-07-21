package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"dcon/internal/machine"

	"github.com/spf13/cobra"
)

// TestRunMacAddressWhitespaceOnly is a no-op: trimming turns a blank
// --mac-address into "unset", so no --network is emitted (matching a plain run).
func TestRunMacAddressWhitespaceOnly(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--mac-address", "   ", "alpine"})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if contains(got, "--network") {
		t.Errorf("whitespace-only --mac-address must not emit --network; got %v", got)
	}
}

// TestRunMacAddressPreservesOtherNetworkOpts ensures mtu=/other Apple network
// options survive when --mac-address is merged in.
func TestRunMacAddressPreservesOtherNetworkOpts(t *testing.T) {
	c := parse(t, newRunCmd(), []string{
		"--network", "mynet,mtu=1500",
		"--mac-address", "02:42:ac:11:00:02",
		"alpine",
	})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(got, "--network", "mynet,mtu=1500,mac=02:42:ac:11:00:02") {
		t.Errorf("expected mtu preserved ahead of mac=; got %v", got)
	}
}

// TestRunMacAddressSingleNetworkFlag locks that we emit exactly one --network
// (never a bare network plus a second mac-bearing one).
func TestRunMacAddressSingleNetworkFlag(t *testing.T) {
	c := parse(t, newRunCmd(), []string{"--network", "mynet", "--mac-address", "02:42:ac:11:00:02", "alpine", "true"})
	got, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i := 0; i+1 < len(got); i++ {
		if got[i] == "--network" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want exactly one --network, got %d in %v", n, got)
	}
}

// TestRunMacAddressErrorMessages pin the user-facing conflict/invalid text so
// scripts can match on them.
func TestRunMacAddressErrorMessages(t *testing.T) {
	c := parse(t, newRunCmd(), []string{
		"--network", "default,mac=aa:bb:cc:dd:ee:ff",
		"--mac-address", "02:42:ac:11:00:02",
		"alpine",
	})
	_, err := buildContainerArgs(c, c.Flags().Args(), "run")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("conflict error = %v", err)
	}

	c2 := parse(t, newRunCmd(), []string{"--mac-address", "02:42:ac:11:00:0g", "alpine"})
	_, err = buildContainerArgs(c2, c2.Flags().Args(), "run")
	if err == nil || !strings.Contains(err.Error(), "invalid MAC") {
		t.Errorf("invalid MAC error = %v", err)
	}
}

// TestRunNetworkContainerWithMacStillRejected: container: namespace networking
// is unsupported regardless of --mac-address.
func TestRunNetworkContainerWithMacStillRejected(t *testing.T) {
	c := parse(t, newRunCmd(), []string{
		"--network", "container:other",
		"--mac-address", "02:42:ac:11:00:02",
		"alpine",
	})
	if _, err := buildContainerArgs(c, c.Flags().Args(), "run"); err == nil {
		t.Error("--network container:… must still error with --mac-address")
	}
}

// TestRunMacAddressDoesNotWarnAsUnsupported: regression guard — mac-address
// left the unsupported map when Apple's network,mac= form was wired up.
func TestRunMacAddressDoesNotWarnAsUnsupported(t *testing.T) {
	var buf bytes.Buffer
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	c := parse(t, newRunCmd(), []string{
		"--mac-address", "02:42:ac:11:00:02",
		"--restart", "always", // still unsupported → should warn
		"alpine",
	})
	got, berr := buildContainerArgs(c, c.Flags().Args(), "run")
	_ = w.Close()
	os.Stderr = old
	<-done
	_ = r.Close()
	if berr != nil {
		t.Fatal(berr)
	}
	if !containsPair(got, "--network", "default,mac=02:42:ac:11:00:02") {
		t.Errorf("mac translation missing: %v", got)
	}
	errOut := buf.String()
	if strings.Contains(errOut, "--mac-address") {
		t.Errorf("mac-address must not appear in warnings; stderr=%q", errOut)
	}
	if !strings.Contains(errOut, "--restart") {
		t.Errorf("control: --restart should still warn; stderr=%q", errOut)
	}
}

// TestWarmIneligibleWithVirtualizationAndKernel: boot-bound Apple extras must
// keep a run off the warm exec path (same rule as --mac-address / -v / -p).
func TestWarmIneligibleWithVirtualizationAndKernel(t *testing.T) {
	for _, flags := range [][]string{
		{"--rm", "--virtualization", "alpine", "true"},
		{"--rm", "--kernel", "/tmp/vmlinux", "alpine", "true"},
		{"--rm", "--publish-socket", "/tmp/a.sock:/tmp/a.sock", "alpine", "true"},
	} {
		c := parse(t, newRunCmd(), flags)
		if warmEligible(c, c.Flags().Args()) {
			t.Errorf("flags %v must be warm-ineligible", flags)
		}
	}
}

// TestSystemPropertyGetSetIntercepted exercises the cobra command path (not
// just the pure helper) so get/set never reach the backend.
func TestSystemPropertyGetSetIntercepted(t *testing.T) {
	for _, verb := range []string{"get", "set"} {
		cmd := newSystemPropertyCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{verb, "dns.domain"})
		err := cmd.Execute()
		if err == nil {
			t.Errorf("property %s must error", verb)
			continue
		}
		if !strings.Contains(err.Error(), "config.toml") {
			t.Errorf("property %s error = %q; want config.toml hint", verb, err)
		}
	}
}

// TestSystemPropertyRegisteredOnSystemGroup ensures the intercepting command
// (not a blind passthrough) is what `dcon system property` resolves to.
func TestSystemPropertyRegisteredOnSystemGroup(t *testing.T) {
	var prop *cobra.Command
	for _, c := range newSystemGroupCmd().Commands() {
		if c.Name() == "property" {
			prop = c
			break
		}
	}
	if prop == nil {
		t.Fatal("system group missing property subcommand")
	}
	if !prop.DisableFlagParsing {
		t.Error("property must DisableFlagParsing so list flags reach the backend")
	}
	// Short help must mention the TOML migration.
	if !strings.Contains(prop.Short, "config.toml") {
		t.Errorf("property Short = %q; want config.toml mention", prop.Short)
	}
}

// TestMachineCreateVirtFlagsWireToBuildRunArgs parses the create command flags
// the way a user would set them and checks they reach CreateOpts → BuildRunArgs.
func TestMachineCreateVirtFlagsWireToBuildRunArgs(t *testing.T) {
	cmd := machineCreateCmd()
	if err := cmd.ParseFlags([]string{
		"--virtualization",
		"--kernel", "/opt/kvm/vmlinux",
		"--cpus", "2",
		"--memory", "4G",
	}); err != nil {
		t.Fatal(err)
	}
	virt, _ := cmd.Flags().GetBool("virtualization")
	kernel, _ := cmd.Flags().GetString("kernel")
	if !virt || kernel != "/opt/kvm/vmlinux" {
		t.Fatalf("flags not parsed: virt=%v kernel=%q", virt, kernel)
	}
	cpus, memory, arch, err := machineResourceFlags(cmd)
	if err != nil {
		t.Fatal(err)
	}
	got, err := machine.BuildRunArgs(machine.CreateOpts{
		Name: "dev", Distro: "ubuntu", Image: "ubuntu:latest",
		CPUs: cpus, Memory: memory, Arch: arch,
		Virtualization: virt, Kernel: kernel,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"--virtualization", "--kernel /opt/kvm/vmlinux", "--cpus 2", "--memory 4G"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}
