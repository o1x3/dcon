package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"dcon/internal/dockerfmt"

	"github.com/spf13/cobra"
)

// dcon is daemonless: it shells out to the Apple `container` CLI rather than
// dialing a Docker daemon over a socket. There is therefore exactly one,
// always-current context ("default"). The context command exists so tools and
// scripts that probe `docker context ls`/`inspect`/`show` keep working.

const defaultContextName = "default"

// dockerHost reports the conventional Docker endpoint, honoring DOCKER_HOST.
func dockerHost() string {
	if v := os.Getenv("DOCKER_HOST"); v != "" {
		return v
	}
	return "unix:///var/run/docker.sock"
}

type contextView struct {
	Name           string
	Current        bool
	Description    string
	DockerEndpoint string
	Error          string
}

func newContextGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "context",
		Short: "Manage contexts",
	}

	ls := &cobra.Command{
		Use:     "ls [OPTIONS]",
		Aliases: []string{"list"},
		Short:   "List contexts",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			quiet, _ := cmd.Flags().GetBool("quiet")
			format, _ := cmd.Flags().GetString("format")
			views := []any{contextView{
				Name:           defaultContextName,
				Current:        true,
				Description:    "Current DOCKER_HOST based configuration",
				DockerEndpoint: dockerHost(),
			}}
			def := dockerfmt.TableDef{
				Headers: []string{"NAME", "DESCRIPTION", "DOCKER ENDPOINT", "ERROR"},
				Row: func(v any) []string {
					c := v.(contextView)
					name := c.Name
					if c.Current {
						name += " *"
					}
					return []string{name, c.Description, c.DockerEndpoint, c.Error}
				},
				ID: func(v any) string { return v.(contextView).Name },
			}
			return dockerfmt.Render(format, quiet, views, def)
		},
	}
	ls.Flags().BoolP("quiet", "q", false, "Only show context names")
	ls.Flags().String("format", "", "Format output using a Go template or 'json'")

	show := &cobra.Command{
		Use:   "show",
		Short: "Print the name of the current context",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(currentContextName())
			return nil
		},
	}

	use := &cobra.Command{
		Use:   "use CONTEXT",
		Short: "Set the current docker context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != defaultContextName {
				return fmt.Errorf("context %q does not exist (dcon is daemonless and provides only the built-in %q context)", args[0], defaultContextName)
			}
			fmt.Println(args[0])
			return nil
		},
	}

	inspect := &cobra.Command{
		Use:   "inspect [OPTIONS] [CONTEXT...]",
		Short: "Display detailed information on one or more contexts",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, a := range args {
				if a != defaultContextName {
					return fmt.Errorf("context %q does not exist", a)
				}
			}
			raw, err := json.MarshalIndent([]map[string]any{contextInspect()}, "", "    ")
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("format")
			return renderInspect(string(raw), format)
		},
	}
	inspect.Flags().StringP("format", "f", "", "Format output using a Go template or 'json'")

	create := &cobra.Command{
		Use:   "create CONTEXT",
		Short: "Create a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("creating contexts is not supported: dcon is daemonless and exposes only the built-in %q context", defaultContextName)
		},
	}

	group.AddCommand(
		ls, show, use, inspect, create,
		stub("rm CONTEXT [CONTEXT...]", "Remove one or more contexts", "the built-in \"default\" context cannot be removed"),
		stub("update CONTEXT", "Update a context", "updating contexts is not supported (dcon is daemonless)"),
		stub("export CONTEXT [FILE]", "Export a context to a tar archive", "exporting contexts is not supported (dcon is daemonless)"),
		stub("import CONTEXT FILE", "Import a context from a tar archive", "importing contexts is not supported (dcon is daemonless)"),
	)
	return group
}

// currentContextName honors DOCKER_CONTEXT but only the built-in context exists.
func currentContextName() string {
	if v := os.Getenv("DOCKER_CONTEXT"); v != "" {
		return v
	}
	return defaultContextName
}

// contextInspect builds the docker-context-inspect JSON for the built-in context.
func contextInspect() map[string]any {
	return map[string]any{
		"Name":     defaultContextName,
		"Metadata": map[string]any{},
		"Endpoints": map[string]any{
			"docker": map[string]any{
				"Host":          dockerHost(),
				"SkipTLSVerify": false,
			},
		},
		"TLSMaterial": map[string]any{},
		"Storage": map[string]any{
			"MetadataPath": "<IN MEMORY>",
			"TLSPath":      "<IN MEMORY>",
		},
	}
}
