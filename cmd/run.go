package cmd

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strconv"
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
	// StringArray (not StringSlice): these values legitimately contain commas
	// (mount/env/label specs), so they must NOT be comma-split.
	f.StringArrayP("env", "e", nil, "Set environment variables")
	f.StringArray("env-file", nil, "Read in a file of environment variables")
	f.StringArrayP("volume", "v", nil, "Bind mount a volume")
	f.StringArray("mount", nil, "Attach a filesystem mount to the container")
	f.StringArrayP("publish", "p", nil, "Publish a container's port(s) to the host")
	f.StringArrayP("label", "l", nil, "Set metadata on a container")
	f.StringArray("label-file", nil, "Read in a line-delimited file of labels")
	f.StringArray("cap-add", nil, "Add Linux capabilities")
	f.StringArray("cap-drop", nil, "Drop Linux capabilities")
	f.StringArray("dns", nil, "Set custom DNS servers")
	f.StringArray("dns-search", nil, "Set custom DNS search domains")
	f.StringArray("dns-option", nil, "Set DNS options")
	f.StringArray("dns-opt", nil, "Set DNS options (Docker alias of --dns-option)")
	f.StringArray("tmpfs", nil, "Mount a tmpfs directory")
	f.StringArray("ulimit", nil, "Ulimit options (format: <type>=<soft>[:<hard>])")

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
	f.StringArray("publish-socket", nil, "Publish a unix socket from container to host (host_path:container_path)")
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
		{"entrypoint", "--entrypoint"}, {"memory", "--memory"},
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

	// --cpus: Docker accepts a fractional CPU quota (e.g. 1.5); the backend
	// accepts only whole CPUs. Round up (so 0<f<1 never yields 0) and warn on
	// any lossy rounding.
	if cv, _ := f.GetString("cpus"); cv != "" {
		fv, err := strconv.ParseFloat(cv, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid --cpus value %q: must be a number", cv)
		}
		if fv <= 0 {
			return nil, fmt.Errorf("invalid --cpus value %q: must be greater than 0", cv)
		}
		n := int(math.Ceil(fv))
		if float64(n) != fv {
			warnings = append(warnings, fmt.Sprintf("--cpus %s rounded up to %d (backend accepts whole CPUs only)", cv, n))
		}
		out = append(out, "--cpus", strconv.Itoa(n))
	}

	// --network / --net (alias)
	net, _ := f.GetString("network")
	if net == "" {
		net, _ = f.GetString("net")
	}
	if net == "host" || strings.HasPrefix(net, "container:") {
		return nil, fmt.Errorf("--network %s is not supported by the container backend (no host/container-namespace networking on macOS VMs)", net)
	}
	if net != "" && net != "default" && net != "bridge" {
		out = append(out, "--network", net)
	}

	// --volume: strip macOS-irrelevant Docker mount options (SELinux :z/:Z,
	// :cached/:delegated/:consistent) which the backend rejects.
	for _, v := range mustStringArray(f, "volume") {
		out = append(out, "--volume", normalizeVolume(v, &warnings))
	}
	// --mount: rewrite Docker tmpfs-size/tmpfs-mode keys and drop options the
	// backend cannot honor.
	for _, m := range mustStringArray(f, "mount") {
		out = append(out, "--mount", normalizeMount(m, &warnings))
	}

	// repeatable string flags -> repeated container flags
	sliceMap := []struct{ name, flag string }{
		{"env", "--env"}, {"env-file", "--env-file"},
		{"publish", "--publish"}, {"label", "--label"},
		{"cap-add", "--cap-add"}, {"cap-drop", "--cap-drop"},
		{"dns", "--dns"}, {"dns-search", "--dns-search"},
		{"publish-socket", "--publish-socket"}, {"ulimit", "--ulimit"},
	}
	for _, s := range sliceMap {
		vals, _ := f.GetStringArray(s.name)
		for _, v := range vals {
			out = append(out, s.flag, v)
		}
	}

	// --dns-option and its Docker alias --dns-opt
	for _, name := range []string{"dns-option", "dns-opt"} {
		vals, _ := f.GetStringArray(name)
		for _, v := range vals {
			out = append(out, "--dns-option", v)
		}
	}

	// tmpfs: Docker allows path[:options]. Map size=/mode= onto a tmpfs --mount;
	// pass path-only specs through as --tmpfs; warn on other dropped options.
	for _, t := range mustStringArray(f, "tmpfs") {
		out = append(out, tmpfsArgs(t, &warnings)...)
	}

	// label-file: expand into individual --label flags.
	lfiles, _ := f.GetStringArray("label-file")
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

// macOS-irrelevant bind-mount options that the container backend rejects.
var droppedVolumeOpts = map[string]bool{"z": true, "Z": true, "cached": true, "delegated": true, "consistent": true}

// normalizeVolume strips SELinux/consistency options from a Docker -v spec's
// third (options) field, preserving ro/rw and other tokens.
func normalizeVolume(spec string, warnings *[]string) string {
	parts := strings.Split(spec, ":")
	if len(parts) < 3 {
		return spec // src:dst or named:dst — nothing to strip
	}
	opts := strings.Split(parts[len(parts)-1], ",")
	var kept []string
	for _, o := range opts {
		if droppedVolumeOpts[o] {
			*warnings = append(*warnings, fmt.Sprintf("volume option ':%s' is ignored on macOS (no SELinux/virtiofs equivalent)", o))
			continue
		}
		kept = append(kept, o)
	}
	base := strings.Join(parts[:len(parts)-1], ":")
	if len(kept) == 0 {
		return base
	}
	return base + ":" + strings.Join(kept, ",")
}

// normalizeMount rewrites Docker's tmpfs-size/tmpfs-mode keys to the backend's
// size/mode and drops mount options the backend cannot honor.
func normalizeMount(spec string, warnings *[]string) string {
	fields := strings.Split(spec, ",")
	var out []string
	for _, fld := range fields {
		key := fld
		if i := strings.Index(fld, "="); i >= 0 {
			key = fld[:i]
		}
		switch key {
		case "tmpfs-size":
			out = append(out, "size="+fld[len("tmpfs-size="):])
		case "tmpfs-mode":
			out = append(out, "mode="+fld[len("tmpfs-mode="):])
		case "volume-driver", "volume-opt", "bind-propagation", "consistency":
			*warnings = append(*warnings, fmt.Sprintf("--mount option %q is not supported by the container backend and was ignored", key))
		default:
			out = append(out, fld)
		}
	}
	return strings.Join(out, ",")
}

// tmpfsArgs converts a Docker --tmpfs path[:options] spec into either a plain
// --tmpfs (path only) or a tmpfs --mount (when size=/mode= are present).
func tmpfsArgs(spec string, warnings *[]string) []string {
	path := spec
	var optStr string
	if i := strings.Index(spec, ":"); i >= 0 {
		path, optStr = spec[:i], spec[i+1:]
	}
	if optStr == "" {
		return []string{"--tmpfs", path}
	}
	var size, mode string
	var dropped []string
	for _, o := range strings.Split(optStr, ",") {
		kv := strings.SplitN(o, "=", 2)
		k := strings.ToLower(kv[0])
		switch {
		case k == "size" && len(kv) == 2:
			size = kv[1]
		case k == "mode" && len(kv) == 2:
			mode = kv[1]
		default:
			dropped = append(dropped, o)
		}
	}
	if len(dropped) > 0 {
		*warnings = append(*warnings, fmt.Sprintf("--tmpfs options %s are not supported by the backend and were dropped", strings.Join(dropped, ",")))
	}
	if size == "" && mode == "" {
		return []string{"--tmpfs", path}
	}
	mnt := "type=tmpfs,destination=" + path
	if size != "" {
		mnt += ",size=" + size
	}
	if mode != "" {
		mnt += ",mode=" + mode
	}
	return []string{"--mount", mnt}
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
