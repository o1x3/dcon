package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

// addRunFlags registers the Docker `run`/`create` flag surface plus the
// Apple-container-native extras. Flags that container genuinely cannot honour
// are still accepted (so scripts/compose don't break) and reported once via a
// warning when used.
func addRunFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.SetInterspersed(false) // everything after IMAGE is the container command

	// --- Docker flags that map directly to container ---
	f.BoolP("detach", "d", false, "Run container in background and print container ID")
	f.BoolP("interactive", "i", false, "Keep STDIN open even if not attached")
	f.BoolP("tty", "t", false, "Allocate a pseudo-TTY")
	f.Bool("rm", false, "Automatically remove the container when it exits")
	f.Bool("read-only", false, "Mount the container's root filesystem as read only")
	f.Bool("init", false, "Run an init inside the container that forwards signals and reaps processes")
	f.String("name", "", "Assign a name to the container")
	f.StringP("workdir", "w", "", "Working directory inside the container")
	f.StringP("user", "u", "", "Username or UID (format: <name|uid>[:<group|gid>])")
	f.String("entrypoint", "", "Overwrite the default ENTRYPOINT of the image")
	f.StringP("memory", "m", "", "Memory limit")
	f.String("cpus", "", "Number of CPUs")
	f.String("network", "", "Connect a container to a network")
	f.String("net", "", "Alias for --network")
	f.String("cidfile", "", "Write the container ID to the file")
	f.String("shm-size", "", "Size of /dev/shm")
	f.String("platform", "", "Set platform if server is multi-platform capable")
	f.StringSliceP("env", "e", nil, "Set environment variables")
	f.StringSlice("env-file", nil, "Read in a file of environment variables")
	f.StringSliceP("volume", "v", nil, "Bind mount a volume")
	f.StringSlice("mount", nil, "Attach a filesystem mount to the container")
	f.StringSliceP("publish", "p", nil, "Publish a container's port(s) to the host")
	f.StringSliceP("label", "l", nil, "Set metadata on a container")
	f.StringSlice("label-file", nil, "Read in a line-delimited file of labels")
	f.StringSlice("cap-add", nil, "Add Linux capabilities")
	f.StringSlice("cap-drop", nil, "Drop Linux capabilities")
	f.StringSlice("dns", nil, "Set custom DNS servers")
	f.StringSlice("dns-search", nil, "Set custom DNS search domains")
	f.StringSlice("dns-option", nil, "Set DNS options")
	f.StringSlice("dns-opt", nil, "Set DNS options (Docker alias of --dns-option)")
	f.StringSlice("tmpfs", nil, "Mount a tmpfs directory")
	f.StringSlice("ulimit", nil, "Ulimit options (format: <type>=<soft>[:<hard>])")

	// --privileged is approximated as cap-add ALL (container has no single
	// privileged switch); flagged when used.
	f.Bool("privileged", false, "Give extended privileges (approximated as --cap-add ALL)")

	// --- Apple-container-native extras, exposed so power users reach them ---
	f.Bool("rosetta", false, "Enable Rosetta x86_64 emulation in the container")
	f.Bool("ssh", false, "Forward SSH agent socket to the container")
	f.Bool("virtualization", false, "Expose nested virtualization to the container")
	f.Bool("no-dns", false, "Do not configure DNS in the container")
	f.StringP("arch", "a", "", "Target architecture for a multi-arch image (e.g. arm64, amd64)")
	f.String("os", "", "Target OS for a multi-OS image")
	f.StringP("kernel", "k", "", "Custom kernel path")
	f.String("init-image", "", "Custom init image")
	f.String("dns-domain", "", "Default DNS domain")
	f.String("runtime", "", "Runtime handler for the container")
	f.String("gid", "", "Primary group ID for the process")
	f.String("uid", "", "User ID for the process")
	f.StringSlice("publish-socket", nil, "Publish a unix socket from container to host (host_path:container_path)")
	f.String("scheme", "", "Registry scheme: http|https|auto")

	// --- Accepted-but-unsupported Docker flags (warned once when used) ---
	f.BoolP("publish-all", "P", false, "Publish all exposed ports (unsupported by backend)")
	f.String("hostname", "", "Container host name (unsupported by backend)")
	f.String("restart", "", "Restart policy (unsupported by backend)")
	f.String("pull", "", "Pull image before running: always|missing|never (backend pulls on demand)")
	f.String("stop-signal", "", "Signal to stop the container (unsupported by backend)")
	f.StringSlice("add-host", nil, "Add a custom host-to-IP mapping (unsupported by backend)")
	f.StringSlice("device", nil, "Add a host device (unsupported by backend)")
	f.StringSlice("group-add", nil, "Add additional groups (unsupported by backend)")
	f.StringSlice("sysctl", nil, "Sysctl options (unsupported by backend)")
	f.StringSlice("expose", nil, "Expose a port or range (informational; no-op)")
	f.String("gpus", "", "GPU devices (unsupported by backend)")
	f.String("memory-swap", "", "Swap limit (unsupported by backend)")
	f.String("cpu-shares", "", "CPU shares (unsupported by backend)")
	f.Bool("detach-keys", false, "")
	_ = f.MarkHidden("net")
	_ = f.MarkHidden("dns-opt")
	_ = f.MarkHidden("detach-keys")
}

// buildContainerArgs translates the parsed Docker flags on cmd into a
// `container <subcmd> ...` argument list. subcmd is "run" or "create".
func buildContainerArgs(cmd *cobra.Command, posArgs []string, subcmd string) ([]string, error) {
	f := cmd.Flags()
	out := []string{subcmd}
	var warnings []string

	// passthrough bool flag -> container flag
	boolMap := []struct{ name, flag string }{
		{"detach", "--detach"}, {"interactive", "--interactive"}, {"tty", "--tty"},
		{"rm", "--rm"}, {"read-only", "--read-only"}, {"init", "--init"},
		{"rosetta", "--rosetta"}, {"ssh", "--ssh"}, {"virtualization", "--virtualization"},
		{"no-dns", "--no-dns"},
	}
	for _, b := range boolMap {
		if v, _ := f.GetBool(b.name); v {
			out = append(out, b.flag)
		}
	}

	// passthrough string flag -> container flag
	strMap := []struct{ name, flag string }{
		{"name", "--name"}, {"workdir", "--workdir"}, {"user", "--user"},
		{"entrypoint", "--entrypoint"}, {"memory", "--memory"}, {"cpus", "--cpus"},
		{"cidfile", "--cidfile"}, {"shm-size", "--shm-size"}, {"platform", "--platform"},
		{"arch", "--arch"}, {"os", "--os"}, {"kernel", "--kernel"},
		{"init-image", "--init-image"}, {"dns-domain", "--dns-domain"},
		{"runtime", "--runtime"}, {"gid", "--gid"}, {"uid", "--uid"}, {"scheme", "--scheme"},
	}
	for _, s := range strMap {
		if v, _ := f.GetString(s.name); v != "" {
			out = append(out, s.flag, v)
		}
	}

	// --network / --net (alias)
	net, _ := f.GetString("network")
	if net == "" {
		net, _ = f.GetString("net")
	}
	if net != "" && net != "default" && net != "bridge" {
		out = append(out, "--network", net)
	}

	// repeatable string flags -> repeated container flags
	sliceMap := []struct{ name, flag string }{
		{"env", "--env"}, {"env-file", "--env-file"}, {"volume", "--volume"},
		{"mount", "--mount"}, {"publish", "--publish"}, {"label", "--label"},
		{"cap-add", "--cap-add"}, {"cap-drop", "--cap-drop"},
		{"dns", "--dns"}, {"dns-search", "--dns-search"},
		{"publish-socket", "--publish-socket"}, {"ulimit", "--ulimit"},
	}
	for _, s := range sliceMap {
		vals, _ := f.GetStringSlice(s.name)
		for _, v := range vals {
			out = append(out, s.flag, v)
		}
	}

	// --dns-option and its Docker alias --dns-opt
	for _, name := range []string{"dns-option", "dns-opt"} {
		vals, _ := f.GetStringSlice(name)
		for _, v := range vals {
			out = append(out, "--dns-option", v)
		}
	}

	// tmpfs: Docker allows path[:options]; container takes just the path.
	tmpfs, _ := f.GetStringSlice("tmpfs")
	for _, t := range tmpfs {
		path := t
		if i := strings.Index(t, ":"); i >= 0 {
			path = t[:i]
		}
		out = append(out, "--tmpfs", path)
	}

	// label-file: expand into individual --label flags.
	lfiles, _ := f.GetStringSlice("label-file")
	for _, lf := range lfiles {
		labels, err := readKVFile(lf)
		if err != nil {
			return nil, err
		}
		for _, l := range labels {
			out = append(out, "--label", l)
		}
	}

	// --privileged approximated as cap-add ALL.
	if v, _ := f.GetBool("privileged"); v {
		out = append(out, "--cap-add", "ALL")
		warnings = append(warnings, "--privileged approximated as --cap-add ALL (device passthrough not supported)")
	}

	// Collect unsupported flags actually set, warn once.
	unsupported := map[string]string{
		"publish-all": "-P/--publish-all", "hostname": "--hostname", "restart": "--restart",
		"stop-signal": "--stop-signal", "add-host": "--add-host", "device": "--device",
		"group-add": "--group-add", "sysctl": "--sysctl", "gpus": "--gpus",
		"memory-swap": "--memory-swap", "cpu-shares": "--cpu-shares",
	}
	for name, label := range unsupported {
		if f.Changed(name) {
			warnings = append(warnings, label+" is not supported by the container backend and was ignored")
		}
	}

	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "dcon: warning: "+w)
	}

	out = append(out, posArgs...)
	return out, nil
}

func readKVFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []string
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "run [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short:                 "Create and run a new container from an image",
		Args:                  cobra.MinimumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cArgs, err := buildContainerArgs(cmd, args, "run")
			if err != nil {
				return err
			}
			return runtime.Run(cArgs...)
		},
	}
	addRunFlags(cmd)
	return cmd
}

func newCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "create [OPTIONS] IMAGE [COMMAND] [ARG...]",
		Short:                 "Create a new container",
		Args:                  cobra.MinimumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cArgs, err := buildContainerArgs(cmd, args, "create")
			if err != nil {
				return err
			}
			return runtime.Run(cArgs...)
		},
	}
	addRunFlags(cmd)
	return cmd
}
