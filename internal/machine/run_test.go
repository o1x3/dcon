package machine

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildRunArgsDefault(t *testing.T) {
	got, err := BuildRunArgs(CreateOpts{Name: "u", Distro: "ubuntu", Image: "ubuntu:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run", "-d",
		"--name", "dcon-machine-u",
		"--label", "dcon.machine=1",
		"--label", "dcon.machine.name=u",
		"--label", "dcon.machine.distro=ubuntu",
		"--entrypoint", "sleep",
		"ubuntu:latest", "2147483647",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildRunArgs default:\n got %v\nwant %v", got, want)
	}
}

func TestBuildRunArgsResources(t *testing.T) {
	got, err := BuildRunArgs(CreateOpts{
		Name: "u", Distro: "ubuntu", Image: "ubuntu:latest",
		CPUs: 2, Memory: "4G", Arch: "arm64",
		MountHome: true, HomePath: "/Users/x",
		Virtualization: true, Kernel: "/path/to/vmlinux",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"--cpus 2", "--memory 4G", "--arch arm64",
		"--virtualization", "--kernel /path/to/vmlinux",
		"--volume /Users/x:/mnt/mac",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("BuildRunArgs missing %q in %q", want, joined)
		}
	}
	// keepalive must remain the final positional after the image.
	if got[len(got)-1] != "2147483647" || got[len(got)-2] != "ubuntu:latest" {
		t.Errorf("image/keepalive tail wrong: %v", got[len(got)-2:])
	}
}

func TestBuildRunArgsVirtKernelAlone(t *testing.T) {
	// Nested virt without a custom kernel is still a valid Apple flag combo
	// (the default kernel may or may not expose /dev/kvm — that's a runtime
	// concern, not an arg-builder one).
	got, err := BuildRunArgs(CreateOpts{
		Name: "u", Distro: "ubuntu", Image: "ubuntu:latest",
		Virtualization: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsArg(got, "--virtualization") {
		t.Errorf("missing --virtualization: %v", got)
	}
	if containsArg(got, "--kernel") {
		t.Errorf("empty kernel must not emit --kernel: %v", got)
	}

	got2, err := BuildRunArgs(CreateOpts{
		Name: "u", Distro: "ubuntu", Image: "ubuntu:latest",
		Kernel: "/custom/vmlinux",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPairArgs(got2, "--kernel", "/custom/vmlinux") {
		t.Errorf("missing --kernel: %v", got2)
	}
	if containsArg(got2, "--virtualization") {
		t.Errorf("virtualization=false must not emit flag: %v", got2)
	}
}

func TestBuildRunArgsVirtKernelOrder(t *testing.T) {
	// Flag order is part of the pure-builder contract (and keeps golden diffs
	// stable): resources → virt → kernel → volume → image → keepalive.
	got, err := BuildRunArgs(CreateOpts{
		Name: "u", Distro: "ubuntu", Image: "ubuntu:latest",
		CPUs: 1, Memory: "1G", Arch: "arm64",
		Virtualization: true, Kernel: "/k",
		MountHome: true, HomePath: "/Users/x",
	})
	if err != nil {
		t.Fatal(err)
	}
	idx := func(s string) int {
		for i, a := range got {
			if a == s {
				return i
			}
		}
		return -1
	}
	order := []string{"--cpus", "--memory", "--arch", "--virtualization", "--kernel", "--volume"}
	prev := -1
	for _, flag := range order {
		i := idx(flag)
		if i < 0 {
			t.Fatalf("missing %s in %v", flag, got)
		}
		if i < prev {
			t.Errorf("flag order broken: %s at %d after previous at %d in %v", flag, i, prev, got)
		}
		prev = i
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsPairArgs(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

func TestBuildRunArgsDeterministic(t *testing.T) {
	o := CreateOpts{Name: "u", Distro: "ubuntu", Image: "ubuntu:latest", CPUs: 1, Memory: "2G", MountHome: true, HomePath: "/Users/x"}
	a, _ := BuildRunArgs(o)
	b, _ := BuildRunArgs(o)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("BuildRunArgs not deterministic:\n%v\n%v", a, b)
	}
}

func TestBuildRunArgsErrors(t *testing.T) {
	if _, err := BuildRunArgs(CreateOpts{Name: "a:b", Image: "x"}); err == nil {
		t.Error("name with ':' should be rejected")
	}
	if _, err := BuildRunArgs(CreateOpts{Name: "u"}); err == nil {
		t.Error("missing image should be rejected")
	}
	if _, err := BuildRunArgs(CreateOpts{Name: "u", Image: "x", MountHome: true, HomePath: "/a:b"}); err == nil {
		t.Error("home path with ':' should be rejected")
	}
	if _, err := BuildRunArgs(CreateOpts{Name: "u", Image: "x", MountHome: true, HomePath: ""}); err == nil {
		t.Error("mount-home with empty home should be rejected")
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"ubuntu", "my-machine", "dev2", "work_box"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "a/b", "a:b", "a b", "user@host", "dcon-machine-x"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) should have errored", bad)
		}
	}
}

func TestContainerNameAndRecover(t *testing.T) {
	if ContainerName("foo") != "dcon-machine-foo" {
		t.Errorf("ContainerName = %q", ContainerName("foo"))
	}
	if got := NameFromContainer("dcon-machine-foo", "foo"); got != "foo" {
		t.Errorf("NameFromContainer(label) = %q", got)
	}
	if got := NameFromContainer("dcon-machine-bar", ""); got != "bar" {
		t.Errorf("NameFromContainer(strip) = %q", got)
	}
}

func TestShellArgv(t *testing.T) {
	got := ShellArgv()
	if len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-lc" || !strings.Contains(got[2], "bash") || !strings.Contains(got[2], "sh") {
		t.Errorf("ShellArgv = %v", got)
	}
}
