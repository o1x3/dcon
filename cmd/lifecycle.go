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
			tflag := []string{}
			if cmd.Flags().Changed("time") {
				t, _ := cmd.Flags().GetInt("time")
				tflag = []string{"--time", fmt.Sprint(t)}
			}
			var firstErr error
			for _, id := range args {
				stopArgs := append([]string{"stop"}, tflag...)
				stopArgs = append(stopArgs, id)
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
	return &cobra.Command{
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
				}
			}
			for _, p := range list[0].Configuration.Ports {
				proto := p.Proto
				if proto == "" {
					proto = "tcp"
				}
				if filterPort != "" && fmt.Sprint(p.ContainerPort) != filterPort {
					continue
				}
				if filterProto != "" && proto != filterProto {
					continue
				}
				host := p.HostAddress
				if host == "" {
					host = "0.0.0.0"
				}
				if filterPort != "" {
					fmt.Printf("%s:%d\n", host, p.HostPort)
				} else {
					fmt.Printf("%d/%s -> %s:%d\n", p.ContainerPort, proto, host, p.HostPort)
				}
			}
			return nil
		},
	}
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
