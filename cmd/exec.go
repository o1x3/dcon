package cmd

import (
	"os"

	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "exec [OPTIONS] CONTAINER COMMAND [ARG...]",
		Short:                 "Execute a command in a running container",
		Args:                  cobra.MinimumNArgs(2),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			cargs := []string{"exec"}
			if v, _ := f.GetBool("detach"); v {
				cargs = append(cargs, "--detach")
			}
			if v, _ := f.GetBool("interactive"); v {
				cargs = append(cargs, "--interactive")
			}
			if v, _ := f.GetBool("tty"); v {
				cargs = append(cargs, "--tty")
			}
			if u, _ := f.GetString("user"); u != "" {
				cargs = append(cargs, "--user", u)
			}
			if w, _ := f.GetString("workdir"); w != "" {
				cargs = append(cargs, "--workdir", w)
			}
			if g, _ := f.GetString("gid"); g != "" {
				cargs = append(cargs, "--gid", g)
			}
			if u, _ := f.GetString("uid"); u != "" {
				cargs = append(cargs, "--uid", u)
			}
			for _, e := range mustStringSlice(f, "env") {
				cargs = append(cargs, "--env", e)
			}
			for _, e := range mustStringSlice(f, "env-file") {
				cargs = append(cargs, "--env-file", e)
			}
			if v, _ := f.GetBool("privileged"); v {
				os.Stderr.WriteString("dcon: warning: --privileged is not supported by exec and was ignored\n")
			}
			cargs = append(cargs, args...)
			return runtime.Run(cargs...)
		},
	}
	f := cmd.Flags()
	f.SetInterspersed(false)
	f.BoolP("detach", "d", false, "Detached mode: run command in the background")
	f.BoolP("interactive", "i", false, "Keep STDIN open even if not attached")
	f.BoolP("tty", "t", false, "Allocate a pseudo-TTY")
	f.StringP("user", "u", "", "Username or UID (format: <name|uid>[:<group|gid>])")
	f.StringP("workdir", "w", "", "Working directory inside the container")
	f.StringSliceP("env", "e", nil, "Set environment variables")
	f.StringSlice("env-file", nil, "Read in a file of environment variables")
	f.String("gid", "", "Group ID for the process")
	f.String("uid", "", "User ID for the process")
	f.Bool("privileged", false, "Give extended privileges to the command (unsupported)")
	f.String("detach-keys", "", "")
	_ = f.MarkHidden("detach-keys")
	return cmd
}

func mustStringSlice(f interface {
	GetStringSlice(string) ([]string, error)
}, name string) []string {
	v, _ := f.GetStringSlice(name)
	return v
}
