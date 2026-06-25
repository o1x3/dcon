package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/template"

	"dcon/internal/dockerfmt"
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
			return renderInspect(raw, format)
		},
	}
	cmd.Flags().StringP("format", "f", "", "Format output using a Go template (backend JSON schema)")
	cmd.Flags().String("type", "", "Return JSON for specified type: container|image")
	cmd.Flags().BoolP("size", "s", false, "Display total file sizes (no-op)")
	return cmd
}

// renderInspect prints inspect JSON honouring docker's --format conventions.
// With no format it passes the pretty JSON through; "json" prints it raw;
// otherwise it executes a Go template per element. Template field names follow
// the backend (container) JSON schema, not docker's PascalCase inspect schema.
func renderInspect(raw, format string) error {
	if format == "" || format == "json" {
		fmt.Println(strings.TrimRight(raw, "\n"))
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return err
	}
	tmpl, err := template.New("inspect").Funcs(dockerfmt.TemplateFuncs()).Parse(format + "\n")
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
		// Fast path: all ids resolve as containers, or all as images — a single
		// batch call preserves the backend's native formatting.
		if out, err := runtime.CaptureSilent(append([]string{"inspect"}, ids...)...); err == nil {
			return out, nil
		}
		if out, err := runtime.CaptureSilent(append([]string{"image", "inspect"}, ids...)...); err == nil {
			return out, nil
		}
		// Mixed (some containers, some images) or partly missing: resolve each id
		// independently and merge, so `inspect <container> <image>` works like
		// docker instead of failing because no single namespace holds them all.
		var results []string
		var missing []string
		for _, id := range ids {
			out, err := runtime.CaptureSilent("inspect", id)
			if err != nil {
				out, err = runtime.CaptureSilent("image", "inspect", id)
			}
			if err != nil {
				missing = append(missing, id)
				continue
			}
			results = append(results, out)
		}
		merged, err := mergeInspectArrays(results)
		if err != nil {
			return "", err
		}
		if merged == "" {
			return "", fmt.Errorf("no such object: %s", strings.Join(missing, " "))
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "dcon: warning: no such object: %s\n", strings.Join(missing, " "))
		}
		return merged, nil
	}
}

// mergeInspectArrays concatenates several `inspect` JSON-array outputs into a
// single pretty-printed JSON array, so a mixed container+image inspect prints
// one combined array (as docker does). Empty/blank outputs are skipped; an
// all-empty input yields "".
func mergeInspectArrays(outs []string) (string, error) {
	var merged []json.RawMessage
	for _, o := range outs {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal([]byte(o), &items); err != nil {
			return "", err
		}
		merged = append(merged, items...)
	}
	if len(merged) == 0 {
		return "", nil
	}
	b, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// cpIsContainerRef reports whether a `cp` argument is a CONTAINER:PATH
// reference rather than a local path, mirroring docker's splitCpArg: an
// absolute path (/...) or one whose part before the first colon starts with
// "." (./x:y, ../x:y) is a local path, even though it contains a colon. The old
// `strings.IndexByte(p,':')>0` test misclassified local paths like
// ./my:file.txt as a container ref and copied to the wrong place.
func cpIsContainerRef(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, ".") {
		return false // absolute or explicitly-relative local path
	}
	return strings.IndexByte(p, ':') > 0
}

func newCpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cp [OPTIONS] SRC_PATH|CONTAINER:SRC_PATH DEST_PATH|CONTAINER:DEST_PATH",
		Short: "Copy files/folders between a container and the local filesystem",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, dst := args[0], args[1]
			if src == "-" || dst == "-" {
				return fmt.Errorf("streaming copy (-) is not supported by the backend; copy to/from a file path instead")
			}
			if !cpIsContainerRef(src) && !cpIsContainerRef(dst) {
				return fmt.Errorf("copying between two local paths is not supported; one of SRC or DEST must be CONTAINER:PATH")
			}
			for _, flag := range []string{"archive", "follow-link"} {
				if cmd.Flags().Changed(flag) {
					fmt.Fprintf(os.Stderr, "dcon: warning: --%s is not supported by the backend and was ignored\n", flag)
				}
			}
			return runtime.Run("copy", src, dst)
		},
	}
	cmd.Flags().BoolP("archive", "a", false, "Archive mode (unsupported)")
	cmd.Flags().BoolP("follow-link", "L", false, "Always follow symbol link (unsupported)")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress progress output during copy (no-op)")
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
