package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"dcon/internal/dockerfmt"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Start one or more stopped containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			attach, _ := cmd.Flags().GetBool("attach")
			interactive, _ := cmd.Flags().GetBool("interactive")
			var firstErr error
			for _, id := range args {
				cargs := []string{"start"}
				if attach {
					cargs = append(cargs, "--attach")
				}
				if interactive {
					cargs = append(cargs, "--interactive")
				}
				cargs = append(cargs, id)
				// The backend echoes the id on success, matching docker.
				if err := runtime.Run(cargs...); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
			}
			return firstErr
		},
	}
	cmd.Flags().BoolP("attach", "a", false, "Attach STDOUT/STDERR and forward signals")
	cmd.Flags().BoolP("interactive", "i", false, "Attach container's STDIN")
	return cmd
}

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Stop one or more running containers",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			cargs := []string{"stop"}
			if all {
				cargs = append(cargs, "--all")
			}
			if t, _ := cmd.Flags().GetInt("time"); cmd.Flags().Changed("time") {
				cargs = append(cargs, "--time", fmt.Sprint(t))
			}
			if s, _ := cmd.Flags().GetString("signal"); s != "" {
				cargs = append(cargs, "--signal", s)
			}
			cargs = append(cargs, args...)
			return runtime.Run(cargs...) // backend echoes stopped ids
		},
	}
	cmd.Flags().BoolP("all", "a", false, "Stop all running containers")
	cmd.Flags().IntP("time", "t", 5, "Seconds to wait before killing the container")
	cmd.Flags().StringP("signal", "s", "", "Signal to send to the container")
	return cmd
}

func newRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Restart one or more containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tv, _ := cmd.Flags().GetInt("time")
			sig, _ := cmd.Flags().GetString("signal")
			stopFlags := restartStopArgs(cmd.Flags().Changed("time"), tv, sig)
			var firstErr error
			for _, id := range args {
				stopArgs := append(append([]string{}, stopFlags...), id)
				// Best-effort stop (suppress its id echo), then start (which
				// echoes the id once, matching docker restart).
				_, _ = runtime.CaptureSilent(stopArgs...)
				if err := runtime.Run("start", id); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
			}
			return firstErr
		},
	}
	cmd.Flags().IntP("time", "t", 5, "Seconds to wait before killing the container")
	cmd.Flags().StringP("signal", "s", "", "Signal to send to the container")
	return cmd
}

// restartStopArgs builds the backend stop argv for `restart` (restart = stop +
// start). The --signal flag was previously defined but ignored; it is forwarded
// to the stop phase here (the backend stop accepts --signal), matching docker.
func restartStopArgs(timeChanged bool, timeVal int, signal string) []string {
	args := []string{"stop"}
	if timeChanged {
		args = append(args, "--time", fmt.Sprint(timeVal))
	}
	if signal != "" {
		args = append(args, "--signal", signal)
	}
	return args
}

func newKillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kill [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Kill one or more running containers",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"kill"}
			if all, _ := cmd.Flags().GetBool("all"); all {
				cargs = append(cargs, "--all")
			}
			if s, _ := cmd.Flags().GetString("signal"); s != "" {
				cargs = append(cargs, "--signal", s)
			}
			cargs = append(cargs, args...)
			return runtime.Run(cargs...) // backend echoes killed ids
		},
	}
	cmd.Flags().BoolP("all", "a", false, "Kill all running containers")
	cmd.Flags().StringP("signal", "s", "KILL", "Signal to send to the container")
	return cmd
}

func newRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [OPTIONS] CONTAINER [CONTAINER...]",
		Short: "Remove one or more containers",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"delete"}
			if force, _ := cmd.Flags().GetBool("force"); force {
				cargs = append(cargs, "--force")
			}
			if all, _ := cmd.Flags().GetBool("all"); all {
				cargs = append(cargs, "--all")
			}
			if v, _ := cmd.Flags().GetBool("volumes"); v {
				fmt.Fprintln(os.Stderr, "dcon: warning: -v/--volumes has no backend equivalent and was ignored")
			}
			cargs = append(cargs, args...)
			return runtime.Run(cargs...) // backend echoes removed ids
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Force the removal of a running container")
	cmd.Flags().BoolP("volumes", "v", false, "Remove anonymous volumes (no-op)")
	cmd.Flags().BoolP("all", "a", false, "Remove all containers")
	cmd.Flags().BoolP("link", "l", false, "")
	_ = cmd.Flags().MarkHidden("link")
	return cmd
}

func newPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause CONTAINER [CONTAINER...]",
		Short: "Pause all processes within one or more containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("pause is not supported by the container backend")
		},
	}
}

func newUnpauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unpause CONTAINER [CONTAINER...]",
		Short: "Unpause all processes within one or more containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("unpause is not supported by the container backend")
		},
	}
}

func newWaitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wait CONTAINER [CONTAINER...]",
		Short: "Block until one or more containers stop, then print their exit codes",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range args {
				for {
					var list []dockerfmt.Container
					if err := runtime.CaptureJSON(&list, "inspect", id); err != nil {
						return err
					}
					state := ""
					if len(list) > 0 {
						state = list[0].Status.State
					}
					if state != "running" && state != "stopping" {
						// Backend does not expose the process exit code; report 0.
						fmt.Println("0")
						break
					}
					time.Sleep(500 * time.Millisecond)
				}
			}
			return nil
		},
	}
}

func newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename CONTAINER NEW_NAME",
		Short: "Rename a container",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("rename is not supported: a container's name is its immutable ID in the backend")
		},
	}
}

func newTopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "top CONTAINER [ps OPTIONS]",
		Short: "Display the running processes of a container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			psArgs := args[1:]
			if len(psArgs) == 0 {
				psArgs = []string{"-ef"}
			}
			cargs := append([]string{"exec", id, "ps"}, psArgs...)
			return runtime.Run(cargs...)
		},
	}
	// `top` is a pure pass-through of [ps OPTIONS] to ps inside the container.
	// Stop flag parsing after the container name so dashed ps options
	// (`top web -ef`, `top web -eo pid,comm`) reach ps instead of erroring as
	// unknown flags on the top command itself. Mirrors exec/machine exec.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func newPortCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "port CONTAINER [PRIVATE_PORT[/PROTO]]",
		Short: "List port mappings or a specific mapping for the container",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var list []dockerfmt.Container
			if err := runtime.CaptureJSON(&list, "inspect", args[0]); err != nil {
				return err
			}
			if len(list) == 0 {
				return fmt.Errorf("no such container: %s", args[0])
			}
			var filterPort, filterProto string
			if len(args) == 2 {
				parts := strings.SplitN(args[1], "/", 2)
				filterPort = parts[0]
				if len(parts) == 2 {
					filterProto = parts[1]
				} else {
					filterProto = "tcp" // docker defaults a proto-less PORT query to tcp
				}
			}
			lines := portMappingLines(list[0].Configuration.Ports, filterPort, filterProto)
			for _, line := range lines {
				fmt.Println(line)
			}
			// docker `port CONTAINER PORT` errors (exit 1) when that port is not
			// published; the bare-listing form (no PORT) exits 0 even when empty.
			if filterPort != "" && len(lines) == 0 {
				return fmt.Errorf("No public port '%s/%s' published for %s", filterPort, filterProto, args[0])
			}
			return nil
		},
	}
}

// portMappingLines renders a container's published ports the way `docker port`
// does. A published range arrives from the backend as one PublishPort with
// Count>1; it is expanded to one line per port so every port of the range is
// listed and per-port filtering (`dcon port web 81`) resolves into the range.
func portMappingLines(ports []dockerfmt.PublishPort, filterPort, filterProto string) []string {
	var out []string
	for _, p := range ports {
		proto := p.Proto
		if proto == "" {
			proto = "tcp"
		}
		host := p.HostAddress
		if host == "" {
			host = "0.0.0.0"
		}
		cnt := p.Count
		if cnt < 1 {
			cnt = 1
		}
		for k := 0; k < cnt; k++ {
			cport := p.ContainerPort + k
			hport := p.HostPort + k
			if filterPort != "" && fmt.Sprint(cport) != filterPort {
				continue
			}
			if filterProto != "" && proto != filterProto {
				continue
			}
			if filterPort != "" {
				out = append(out, fmt.Sprintf("%s:%d", host, hport))
			} else {
				out = append(out, fmt.Sprintf("%d/%s -> %s:%d", cport, proto, host, hport))
			}
		}
	}
	return out
}

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach CONTAINER",
		Short: "Attach local standard output and error streams to a running container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stderr, "dcon: note: backend supports stdout/stderr streaming only; STDIN is not forwarded on attach")
			return runtime.Run("logs", "--follow", args[0])
		},
	}
}
