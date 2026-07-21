package machine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Label keys stamped on a machine's backing container so dcon can distinguish
// machines from ordinary containers and recover a machine's user-facing name
// and distro without a separate state file (the backend is the source of truth
// for which machines exist).
const (
	LabelMachine = "dcon.machine"        // "1" on every machine container
	LabelName    = "dcon.machine.name"   // the user-facing machine name
	LabelDistro  = "dcon.machine.distro" // the distro id it was created from

	// namePrefix namespaces a machine's backend container so `dcon machine rm web`
	// can never resolve to a user's `run --name web` container. The prefix is an
	// implementation detail: users always refer to machines by their bare name.
	namePrefix = "dcon-machine-"

	// keepAlive is PID 1 for a machine: sleep for ~68 years so the container
	// stays up until explicitly stopped/deleted. Same value (and --entrypoint
	// sleep override) the warm pool has used in production; relies on a `sleep`
	// binary, which every mainstream distro base image ships.
	keepAlive = "2147483647"
)

// ContainerName returns the backend container name for a machine.
func ContainerName(name string) string { return namePrefix + name }

// NameFromContainer recovers a machine's user-facing name from its container
// (preferring the label, falling back to stripping the prefix off the id).
func NameFromContainer(id, labelName string) string {
	if labelName != "" {
		return labelName
	}
	return strings.TrimPrefix(id, namePrefix)
}

// validName is docker's container-name allow-list. An allow-list (not the old
// " \t/:@" blocklist) is what keeps control characters and ANSI escapes out of
// the name, which is stored in labels and echoed back through styled TTY
// output by every machine command.
var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ValidateName rejects machine names that would break the prefix scheme or the
// backend's naming rules.
func ValidateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("a machine name is required")
	}
	if strings.HasPrefix(name, namePrefix) {
		return fmt.Errorf("machine name %q must not start with the reserved prefix %q", name, namePrefix)
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid machine name %q: must match [A-Za-z0-9][A-Za-z0-9_.-]*", name)
	}
	return nil
}

// CreateOpts are the resolved inputs to BuildRunArgs. Image and Distro are
// already resolved (see ResolveImage / DistroID); CPUs/Memory/Arch are empty
// (0/"") when unset.
type CreateOpts struct {
	Name           string
	Distro         string
	Image          string
	CPUs           int
	Memory         string
	Arch           string
	MountHome      bool
	HomePath       string // absolute host home dir, mounted at /mnt/mac when MountHome
	Virtualization bool   // expose nested virt (/dev/kvm); needs M3+ and a KVM-enabled kernel
	Kernel         string // custom guest kernel path (e.g. for nested virt)
}

// BuildRunArgs builds the `container run` argument list that boots a machine. It
// is pure (no env, no clock, deterministic ordering) so it can be unit-tested
// without a backend, mirroring cmd.buildContainerArgs.
func BuildRunArgs(o CreateOpts) ([]string, error) {
	if err := ValidateName(o.Name); err != nil {
		return nil, err
	}
	if o.Image == "" {
		return nil, fmt.Errorf("machine %q has no image to boot from", o.Name)
	}
	out := []string{
		"run", "-d",
		"--name", ContainerName(o.Name),
		"--label", LabelMachine + "=1",
		"--label", LabelName + "=" + o.Name,
		"--label", LabelDistro + "=" + o.Distro,
		// Override the image entrypoint so the keep-alive always runs, regardless
		// of what the distro image would launch by default.
		"--entrypoint", "sleep",
	}
	if o.CPUs > 0 {
		out = append(out, "--cpus", strconv.Itoa(o.CPUs))
	}
	if o.Memory != "" {
		out = append(out, "--memory", o.Memory)
	}
	if o.Arch != "" {
		out = append(out, "--arch", o.Arch)
	}
	if o.Virtualization {
		out = append(out, "--virtualization")
	}
	if o.Kernel != "" {
		out = append(out, "--kernel", o.Kernel)
	}
	if o.MountHome {
		if o.HomePath == "" {
			return nil, fmt.Errorf("--mount-home requested but the host home directory is unknown")
		}
		if strings.Contains(o.HomePath, ":") {
			return nil, fmt.Errorf("home path %q contains ':' and cannot be bind-mounted", o.HomePath)
		}
		out = append(out, "--volume", o.HomePath+":/mnt/mac")
	}
	out = append(out, o.Image, keepAlive)
	return out, nil
}

// ShellArgv is the argv exec'd when opening an interactive shell with no
// explicit command: prefer a login bash, fall back to sh. Built as a single
// /bin/sh -lc so the fallback works even when bash is absent.
func ShellArgv() []string {
	return []string{"/bin/sh", "-lc", "exec bash -l 2>/dev/null || exec sh -l"}
}
