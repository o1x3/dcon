package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"dcon/internal/dockerfmt"
	"dcon/internal/machine"
	"dcon/internal/pool"
	"dcon/internal/runtime"
	"dcon/internal/ui"

	"github.com/spf13/cobra"
)

// newMachineCmd builds `dcon machine ...`: OrbStack-style persistent Linux
// machines backed by long-lived Apple-container microVMs. This group shadows
// the backend's OWN `container machine` group (Apple container 1.0+ ships one,
// with nested-virtualization machines as of 1.1.0); `dcon machine native ...`
// is the escape hatch that passes through to the backend's implementation.
func newMachineCmd() *cobra.Command {
	group := &cobra.Command{
		Use:     "machine",
		Short:   "Manage persistent Linux machines (OrbStack-style)",
		Aliases: []string{"m"},
		Long: `Create and manage persistent Linux machines.

A machine is a long-lived microVM you boot from a distro image and open a shell
into — like OrbStack machines, but backed by Apple's container runtime. The
machine's filesystem persists across stop/start; ` + "`dcon machine rm`" + ` deletes it.

  dcon machine create ubuntu           # create a machine named "ubuntu"
  dcon machine shell ubuntu            # open a shell inside it
  dcon machine ls                      # list machines
  dcon machine stop ubuntu             # stop (filesystem is preserved)
  dcon machine rm ubuntu               # delete it

Supported distros: ` + strings.Join(machine.Distros(), ", "),
	}
	group.AddCommand(
		machineCreateCmd(), machineLsCmd(), machineShellCmd(), machineExecCmd(),
		machineStartCmd(), machineStopCmd(), machineRmCmd(), machineDefaultCmd(),
		machineInfoCmd(), machineLogsCmd(), machineRenameCmd(),
		// Escape hatch: dcon's machine group shadows the backend's native
		// `container machine` commands, so keep them reachable here.
		newPassthrough("native [SUBCOMMAND]", "Pass through to the backend's native `container machine` commands", []string{"machine"}),
	)
	return group
}

// machineView exposes a machine as Docker-style template fields for `ls`.
type machineView struct {
	Name    string
	Distro  string
	State   string
	CPUs    string
	Memory  string
	Created string
	Default bool
}

// listMachines enumerates machine containers from the backend (the source of
// truth) and decorates them with the persisted default pointer.
func listMachines() ([]machineView, error) {
	all, err := getContainers(true)
	if err != nil {
		return nil, err
	}
	def := machine.Default()
	var out []machineView
	for _, c := range all {
		if c.Configuration.Labels[machine.LabelMachine] != "1" {
			continue
		}
		name := machine.NameFromContainer(c.ID, c.Configuration.Labels[machine.LabelName])
		cpus := "-"
		if c.Configuration.Resources.CPUs > 0 {
			cpus = fmt.Sprint(c.Configuration.Resources.CPUs)
		}
		mem := "-"
		if c.Configuration.Resources.MemoryInBytes > 0 {
			mem = dockerfmt.HumanSizeBytes(c.Configuration.Resources.MemoryInBytes)
		}
		out = append(out, machineView{
			Name:    name,
			Distro:  c.Configuration.Labels[machine.LabelDistro],
			State:   machineStateName(c.Status.State),
			CPUs:    cpus,
			Memory:  mem,
			Created: dockerfmt.RelativeAgo(c.Configuration.CreationDate),
			Default: name == def && def != "",
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func machineStateName(state string) string {
	switch state {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "stopping":
		return "stopping"
	case "", "unknown":
		return "created"
	default:
		return state
	}
}

// matchMachine finds the machine container for a user-facing name among all
// backend containers. Resolution is by the prefixed backend ID *and* the
// verified dcon.machine label only — never by the free-form dcon.machine.name
// label, which is attacker-controllable (any `dcon run --label` can forge it).
// Matching the label would let a non-prefixed user container masquerade as a
// machine, defeating the prefix namespace and turning `machine rm/stop/shell`
// into a confused deputy against arbitrary containers. A genuine machine always
// has c.ID == dcon-machine-<name>, so the prefix match loses no real capability.
func matchMachine(all []dockerfmt.Container, name string) (dockerfmt.Container, bool) {
	cn := machine.ContainerName(name)
	for _, c := range all {
		if c.Configuration.Labels[machine.LabelMachine] != "1" {
			continue
		}
		if c.ID == cn {
			return c, true
		}
	}
	return dockerfmt.Container{}, false
}

// resolveMachine looks up a machine by its user-facing name, re-verifying the
// dcon.machine label so a mutating command can never act on a same-named
// ordinary container. Returns the backing container (whose ID is the backend
// container name).
func resolveMachine(name string) (dockerfmt.Container, error) {
	all, err := getContainers(true)
	if err != nil {
		return dockerfmt.Container{}, err
	}
	if c, ok := matchMachine(all, name); ok {
		return c, nil
	}
	return dockerfmt.Container{}, fmt.Errorf("no such machine: %s", name)
}

// machineArg resolves a machine name to its backend container ID, defaulting to
// the configured default machine when name is empty.
func machineArg(name string) (id, resolvedName string, err error) {
	if name == "" {
		name = machine.Default()
		if name == "" {
			return "", "", fmt.Errorf("no machine specified and no default set (use `dcon machine default NAME`)")
		}
	}
	c, err := resolveMachine(name)
	if err != nil {
		return "", "", err
	}
	return c.ID, name, nil
}

func machineCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [OPTIONS] DISTRO [NAME]",
		Short: "Create a Linux machine from a distro",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec := args[0]
			name := machine.DistroID(spec)
			if len(args) == 2 {
				name = args[1]
			}
			if err := machine.ValidateName(name); err != nil {
				return err
			}
			image, err := machine.ResolveImage(spec)
			if err != nil {
				return err
			}
			// Refuse if a machine — or any container by the same backend name —
			// already exists, so we never collide with a user container.
			if _, err := resolveMachine(name); err == nil {
				return fmt.Errorf("machine %q already exists", name)
			}
			if existsContainer(machine.ContainerName(name)) {
				return fmt.Errorf("a container named %q already exists; choose another machine name", machine.ContainerName(name))
			}

			opts := machine.CreateOpts{
				Name:   name,
				Distro: machine.DistroID(spec),
				Image:  image,
			}
			opts.CPUs, opts.Memory, opts.Arch, err = machineResourceFlags(cmd)
			if err != nil {
				return err
			}
			if mh, _ := cmd.Flags().GetBool("mount-home"); mh {
				home, herr := os.UserHomeDir()
				if herr != nil {
					return fmt.Errorf("--mount-home: cannot determine home directory: %w", herr)
				}
				opts.MountHome = true
				opts.HomePath = home
			}
			for _, f := range []string{"set-password", "disk", "user"} {
				if cmd.Flags().Changed(f) {
					fmt.Fprintf(os.Stderr, "dcon: warning: --%s is not supported by the container backend and was ignored\n", f)
				}
			}

			rargs, err := machine.BuildRunArgs(opts)
			if err != nil {
				return err
			}
			fmt.Println(ui.Dim("Creating machine ") + ui.Accent(name) + ui.Dim(" ("+image+")..."))
			// Capture stdout (the echoed id) but stream stderr so image-pull
			// progress stays visible on first use.
			if _, err := runtime.Capture(rargs...); err != nil {
				return fmt.Errorf("create machine %s: %w", name, err)
			}
			// A distro image that lacks a `sleep` binary boots then exits; don't
			// leave a dead machine lying around.
			if !pool.IsRunning(machine.ContainerName(name)) {
				_ = runtime.Run("delete", "--force", machine.ContainerName(name))
				return fmt.Errorf("machine %s (%s) did not stay up — the image may lack a 'sleep' binary", name, image)
			}
			_ = machine.SetDefaultIfUnset(name)
			fmt.Printf("%s machine %s created — open a shell with: dcon machine shell %s\n",
				ui.Success("✓"), ui.Accent(name), name)
			return nil
		},
	}
	f := cmd.Flags()
	f.String("cpus", "", "Number of CPUs (fractional values round up)")
	f.StringP("memory", "m", "", "Memory limit (e.g. 4G)")
	f.StringP("arch", "a", "", "Target architecture for the distro image (e.g. arm64, amd64)")
	f.Bool("mount-home", false, "Bind-mount your macOS home directory at /mnt/mac inside the machine")
	// Accepted-but-unsupported OrbStack flags (warned, not errored, for parity).
	f.Bool("set-password", false, "Set a password for the default user (unsupported by backend)")
	f.String("disk", "", "Disk size, e.g. 64G (unsupported; the backend sizes storage automatically)")
	f.StringP("user", "u", "", "Default user (unsupported; the backend uses the image default)")
	return cmd
}

// machineResourceFlags resolves the shared --cpus/--memory/--arch flags.
func machineResourceFlags(cmd *cobra.Command) (cpus int, memory, arch string, err error) {
	if cv, _ := cmd.Flags().GetString("cpus"); cv != "" {
		n, warn, perr := parseCPUs(cv)
		if perr != nil {
			return 0, "", "", perr
		}
		if warn != "" {
			fmt.Fprintln(os.Stderr, "dcon: warning: "+warn)
		}
		cpus = n
	}
	memory, _ = cmd.Flags().GetString("memory")
	arch, _ = cmd.Flags().GetString("arch")
	return cpus, memory, arch, nil
}

func existsContainer(id string) bool {
	all, err := getContainers(true)
	if err != nil {
		return false
	}
	for _, c := range all {
		if c.ID == id {
			return true
		}
	}
	return false
}

func machineLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls [OPTIONS]",
		Aliases: []string{"list"},
		Short:   "List machines",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			quiet, _ := cmd.Flags().GetBool("quiet")
			format, _ := cmd.Flags().GetString("format")
			machines, err := listMachines()
			if err != nil {
				return err
			}
			views := make([]any, 0, len(machines))
			for _, m := range machines {
				views = append(views, m)
			}
			def := dockerfmt.TableDef{
				Headers: []string{"NAME", "DISTRO", "STATE", "CPUS", "MEMORY", "CREATED"},
				Row: func(v any) []string {
					m := v.(machineView)
					name := m.Name
					if m.Default {
						name += " *"
					}
					return []string{name, m.Distro, m.State, m.CPUs, m.Memory, m.Created}
				},
				ID: func(v any) string { return v.(machineView).Name },
			}
			return dockerfmt.Render(format, quiet, views, def)
		},
	}
	cmd.Flags().BoolP("quiet", "q", false, "Only show machine names")
	cmd.Flags().String("format", "", "Format output using a Go template or 'json'")
	return cmd
}

func machineShellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell [NAME] [-- COMMAND [ARG...]]",
		Short: "Open a shell (or run a command) in a machine",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, command := splitNameAndCommand(cmd, args)
			id, _, err := machineArg(name)
			if err != nil {
				return err
			}
			exec := []string{"exec", "--interactive"}
			if machineWantsPTY(cmd) {
				exec = append(exec, "--tty")
			}
			exec = append(exec, id)
			if len(command) > 0 {
				exec = append(exec, command...)
			} else {
				exec = append(exec, machine.ShellArgv()...)
			}
			return runtime.Run(exec...)
		},
	}
	machinePTYFlags(cmd)
	// Stop parsing flags after the machine name so a command's own flags
	// (`shell ubuntu ls -la`) reach the machine instead of erroring here —
	// -t/-T must therefore come before the machine name.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// machinePTYFlags adds the docker-compose-exec-style PTY overrides shared by
// `machine shell` and `machine exec`.
func machinePTYFlags(cmd *cobra.Command) {
	cmd.Flags().BoolP("tty", "t", false, "Force PTY allocation")
	cmd.Flags().BoolP("no-TTY", "T", false, "Disable PTY allocation")
}

// machineWantsPTY resolves the -t/-T overrides against the terminal state.
func machineWantsPTY(cmd *cobra.Command) bool {
	force, _ := cmd.Flags().GetBool("tty")
	disable, _ := cmd.Flags().GetBool("no-TTY")
	return machinePTY(force, disable, haveTTY())
}

// machinePTY decides whether machine shell/exec allocate a PTY: -T always
// wins, -t forces one, otherwise auto-detect — and auto requires stdin AND
// stdout to both be terminals. Keying off stdin alone made piped output
// (`machine exec m cat file | sort`) arrive CRLF-mangled through the PTY.
func machinePTY(force, disable, autoTTY bool) bool {
	if disable {
		return false
	}
	if force {
		return true
	}
	return autoTTY
}

// splitNameAndCommand separates an optional machine name from a command, using
// the `--` position when present (so `shell -- ls` targets the default machine).
func splitNameAndCommand(cmd *cobra.Command, args []string) (name string, command []string) {
	dash := cmd.ArgsLenAtDash()
	if dash == -1 {
		// No `--` terminator was consumed (it was given after the machine name,
		// where SetInterspersed(false) leaves it as a literal token). Take the
		// first arg as the name and drop a leading literal "--" from the command.
		if len(args) > 0 {
			name, command = args[0], args[1:]
		}
		if len(command) > 0 && command[0] == "--" {
			command = command[1:]
		}
		return name, command
	}
	if dash >= 1 {
		name = args[0]
	}
	return name, args[dash:]
}

func machineExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec NAME COMMAND [ARG...]",
		Short: "Run a command in a machine",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _, err := machineArg(args[0])
			if err != nil {
				return err
			}
			exec := []string{"exec", "--interactive"}
			if machineWantsPTY(cmd) {
				exec = append(exec, "--tty")
			}
			exec = append(exec, id)
			// Accept an optional `--` separator before the command, mirroring shell.
			command := args[1:]
			if len(command) > 0 && command[0] == "--" {
				command = command[1:]
			}
			exec = append(exec, command...)
			return runtime.Run(exec...)
		},
	}
	machinePTYFlags(cmd)
	// Treat everything after the machine name as the command, flags included
	// (-t/-T must come before the machine name).
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func machineStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start NAME [NAME...]",
		Short: "Start one or more stopped machines",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return forEachMachine(args, func(id, name string) error {
				// Capture (don't stream) so the backend's raw id echo doesn't leak
				// the internal dcon-machine- prefix; errors still surface.
				if _, err := runtime.CaptureSilent("start", id); err != nil {
					return err
				}
				fmt.Printf("%s machine %s started\n", ui.Success("✓"), ui.Accent(name))
				return nil
			})
		},
	}
}

func machineStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop NAME [NAME...]",
		Short: "Stop one or more running machines (filesystem is preserved)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return forEachMachine(args, func(id, name string) error {
				if _, err := runtime.CaptureSilent("stop", id); err != nil {
					return err
				}
				fmt.Printf("%s machine %s stopped\n", ui.Success("✓"), ui.Accent(name))
				return nil
			})
		},
	}
}

func machineRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm NAME [NAME...]",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete one or more machines",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			cargs := machineDeleteArgs(force)
			return forEachMachine(args, func(id, name string) error {
				if _, err := runtime.CaptureSilent(append(append([]string{}, cargs...), id)...); err != nil {
					return err
				}
				_ = machine.ClearDefaultIf(name)
				fmt.Printf("%s machine %s deleted\n", ui.Success("✓"), ui.Accent(name))
				return nil
			})
		},
	}
	// Default false (not true): a bare `rm` of a running machine should fail and
	// tell the user to pass -f, mirroring `docker rm`. Defaulting to force would
	// make -f a no-op and silently destroy a running machine's filesystem.
	cmd.Flags().BoolP("force", "f", false, "Force removal of a running machine (stops it first)")
	return cmd
}

// machineDeleteArgs builds the backend delete argv, adding --force only when the
// user asked for it.
func machineDeleteArgs(force bool) []string {
	if force {
		return []string{"delete", "--force"}
	}
	return []string{"delete"}
}

// forEachMachine resolves and applies fn to each named machine, attempting all
// of them. With a single name the error is returned as-is (root prints it
// once). With several, every failure — resolve or fn — is printed here exactly
// once and a terse aggregate error is returned, so root's final print doesn't
// duplicate the first failure while the rest vanish.
func forEachMachine(names []string, fn func(id, name string) error) error {
	if len(names) == 1 {
		id, resolved, err := machineArg(names[0])
		if err != nil {
			return err
		}
		return fn(id, resolved)
	}
	failed := 0
	for _, name := range names {
		id, resolved, err := machineArg(name)
		if err == nil {
			err = fn(id, resolved)
		}
		if err != nil {
			failed++
			fmt.Fprintln(os.Stderr, "dcon: "+err.Error())
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d machines failed", failed, len(names))
	}
	return nil
}

func machineDefaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "default [NAME]",
		Short: "Show or set the default machine",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				def := machine.Default()
				if def == "" {
					fmt.Println("(no default machine set)")
					return nil
				}
				fmt.Println(def)
				return nil
			}
			if _, err := resolveMachine(args[0]); err != nil {
				return err
			}
			if err := machine.SetDefault(args[0]); err != nil {
				return err
			}
			fmt.Printf("%s default machine set to %s\n", ui.Success("✓"), ui.Accent(args[0]))
			return nil
		},
	}
}

func machineInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info NAME",
		Short: "Display low-level information about a machine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _, err := machineArg(args[0])
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("format")
			raw, _, err := inspectRaw("container", []string{id})
			if err != nil {
				return err
			}
			return renderInspect(raw, format)
		},
	}
	cmd.Flags().StringP("format", "f", "", "Format output using a Go template or 'json'")
	return cmd
}

func machineLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs NAME",
		Short: "Fetch the boot/console logs of a machine",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, _, err := machineArg(args[0])
			if err != nil {
				return err
			}
			cargs := []string{"logs"}
			if f, _ := cmd.Flags().GetBool("follow"); f {
				cargs = append(cargs, "--follow")
			}
			if b, _ := cmd.Flags().GetBool("boot"); b {
				cargs = append(cargs, "--boot")
			}
			return runtime.Run(append(cargs, id)...)
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "Follow log output")
	cmd.Flags().Bool("boot", false, "Show the VM boot log instead of console output")
	return cmd
}

func machineRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename NAME NEW_NAME",
		Short: "Rename a machine",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("renaming a machine is not supported: its backend container name is immutable — recreate it under the new name instead")
		},
	}
}
