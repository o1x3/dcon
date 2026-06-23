package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/template"

	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [OPTIONS] CONTAINER",
		Short: "Fetch the logs of a container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			cargs := []string{"logs"}
			if v, _ := f.GetBool("follow"); v {
				cargs = append(cargs, "--follow")
			}
			if v, _ := f.GetBool("boot"); v {
				cargs = append(cargs, "--boot")
			}
			if tail, _ := f.GetString("tail"); tail != "" && tail != "all" {
				if _, err := strconv.Atoi(tail); err == nil {
					cargs = append(cargs, "-n", tail)
				}
			}
			for _, flag := range []string{"since", "until", "timestamps"} {
				if f.Changed(flag) {
					fmt.Fprintf(os.Stderr, "dcon: warning: --%s is not supported by the backend and was ignored\n", flag)
				}
			}
			cargs = append(cargs, args[0])
			return runtime.Run(cargs...)
		},
	}
	f := cmd.Flags()
	f.BoolP("follow", "f", false, "Follow log output")
	f.String("tail", "all", "Number of lines to show from the end of the logs")
	f.Bool("boot", false, "Show the VM boot log instead of container stdio (backend extra)")
	f.BoolP("timestamps", "t", false, "Show timestamps (unsupported)")
	f.String("since", "", "Show logs since timestamp (unsupported)")
	f.String("until", "", "Show logs before timestamp (unsupported)")
	f.Bool("details", false, "")
	_ = f.MarkHidden("details")
	return cmd
}

func newInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect [OPTIONS] NAME|ID [NAME|ID...]",
		Short: "Return low-level information on dcon objects",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			typ, _ := cmd.Flags().GetString("type")
			format, _ := cmd.Flags().GetString("format")

			raw, err := inspectRaw(typ, args)
			if err != nil {
				return err
			}
			if format == "" {
				fmt.Println(strings.TrimRight(raw, "\n"))
				return nil
			}
			// Apply a Go template over each element, like docker inspect --format.
			// NOTE: field names follow the backend (container) JSON schema.
			var items []json.RawMessage
			if err := json.Unmarshal([]byte(raw), &items); err != nil {
				return err
			}
			if format == "json" {
				fmt.Println(raw)
				return nil
			}
			tmpl, err := template.New("inspect").Parse(format + "\n")
			if err != nil {
				return err
			}
			for _, it := range items {
				var v any
				_ = json.Unmarshal(it, &v)
				if err := tmpl.Execute(os.Stdout, v); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringP("format", "f", "", "Format output using a Go template (backend JSON schema)")
	cmd.Flags().String("type", "", "Return JSON for specified type: container|image")
	cmd.Flags().BoolP("size", "s", false, "Display total file sizes (no-op)")
	return cmd
}

// inspectRaw returns the pretty JSON array for the given ids, auto-detecting
// container vs image when type is unset.
func inspectRaw(typ string, ids []string) (string, error) {
	switch typ {
	case "image":
		return runtime.CaptureSilent(append([]string{"image", "inspect"}, ids...)...)
	case "container":
		return runtime.CaptureSilent(append([]string{"inspect"}, ids...)...)
	default:
		out, err := runtime.CaptureSilent(append([]string{"inspect"}, ids...)...)
		if err == nil {
			return out, nil
		}
		// Fall back to image inspect.
		out2, err2 := runtime.CaptureSilent(append([]string{"image", "inspect"}, ids...)...)
		if err2 == nil {
			return out2, nil
		}
		return "", err
	}
}

func newCpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cp [OPTIONS] SRC_PATH|CONTAINER:SRC_PATH DEST_PATH|CONTAINER:DEST_PATH",
		Short: "Copy files/folders between a container and the local filesystem",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, flag := range []string{"archive", "follow-link"} {
				if cmd.Flags().Changed(flag) {
					fmt.Fprintf(os.Stderr, "dcon: warning: --%s is not supported by the backend and was ignored\n", flag)
				}
			}
			return runtime.Run("copy", args[0], args[1])
		},
	}
	cmd.Flags().BoolP("archive", "a", false, "Archive mode (unsupported)")
	cmd.Flags().BoolP("follow-link", "L", false, "Always follow symbol link (unsupported)")
	return cmd
}

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export [OPTIONS] CONTAINER",
		Short: "Export a container's filesystem as a tar archive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"export"}
			if o, _ := cmd.Flags().GetString("output"); o != "" {
				cargs = append(cargs, "--output", o)
			}
			cargs = append(cargs, args[0])
			return runtime.Run(cargs...)
		},
	}
	cmd.Flags().StringP("output", "o", "", "Write to a file, instead of STDOUT")
	return cmd
}
