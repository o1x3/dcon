package cmd

import (
	"os"
	"os/exec"
	"strings"

	"dcon/internal/dockerfmt"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

const defaultRegistry = "docker.io"

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login [OPTIONS] [SERVER]",
		Short: "Log in to a registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			server := defaultRegistry
			if len(args) == 1 {
				server = args[0]
			}
			user, _ := cmd.Flags().GetString("username")
			pass, _ := cmd.Flags().GetString("password")
			passStdin, _ := cmd.Flags().GetBool("password-stdin")

			cargs := []string{"registry", "login"}
			if user != "" {
				cargs = append(cargs, "--username", user)
			}

			// container only accepts the password via --password-stdin. If the
			// user passed -p, feed it on stdin; otherwise pass through stdio.
			if pass != "" {
				cargs = append(cargs, "--password-stdin", server)
				c := exec.Command(runtime.Bin(), cargs...)
				c.Stdin = strings.NewReader(pass)
				c.Stdout = os.Stdout
				c.Stderr = os.Stderr
				return c.Run()
			}
			if passStdin {
				cargs = append(cargs, "--password-stdin")
			}
			cargs = append(cargs, server)
			return runtime.Run(cargs...)
		},
	}
	cmd.Flags().StringP("username", "u", "", "Username")
	cmd.Flags().StringP("password", "p", "", "Password")
	cmd.Flags().Bool("password-stdin", false, "Take the password from stdin")
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [SERVER]",
		Short: "Log out from a registry",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			server := defaultRegistry
			if len(args) == 1 {
				server = args[0]
			}
			return runtime.Run("registry", "logout", server)
		},
	}
}

// registryView exposes `dcon registry ls` rows.
type registryView struct {
	Hostname string
	Username string
	Modified string
	Created  string
}

func newRegistryGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:     "registry",
		Short:   "Manage registry logins (backend-native)",
		Aliases: []string{"r"},
	}
	login := newLoginCmd()
	login.Use = "login [OPTIONS] SERVER"
	logout := newLogoutCmd()
	logout.Use = "logout SERVER"
	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List registry logins",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			quiet, _ := cmd.Flags().GetBool("quiet")
			format, _ := cmd.Flags().GetString("format")
			var rows []struct {
				Hostname string `json:"hostname"`
				Username string `json:"username"`
				Modified string `json:"modified"`
				Created  string `json:"created"`
			}
			if err := runtime.CaptureJSON(&rows, "registry", "list", "--format", "json"); err != nil {
				return err
			}
			views := make([]any, 0, len(rows))
			for _, r := range rows {
				views = append(views, registryView{
					Hostname: r.Hostname, Username: r.Username,
					Modified: dockerfmt.RelativeAgo(r.Modified), Created: dockerfmt.RelativeAgo(r.Created),
				})
			}
			def := dockerfmt.TableDef{
				Headers: []string{"HOSTNAME", "USERNAME", "MODIFIED", "CREATED"},
				Row: func(v any) []string {
					r := v.(registryView)
					return []string{r.Hostname, r.Username, r.Modified, r.Created}
				},
				ID: func(v any) string { return v.(registryView).Hostname },
			}
			return dockerfmt.Render(format, quiet, views, def)
		},
	}
	ls.Flags().BoolP("quiet", "q", false, "Only display hostnames")
	ls.Flags().String("format", "", "Format output using a Go template or 'json'")
	group.AddCommand(login, logout, ls)
	return group
}
