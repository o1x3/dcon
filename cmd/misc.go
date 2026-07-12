package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
			tail, _ := f.GetString("tail")
			tailArg, err := validateTail(tail)
			if err != nil {
				return err
			}
			if tailArg != "" {
				cargs = append(cargs, "-n", tailArg)
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
	f.StringP("tail", "n", "all", "Number of lines to show from the end of the logs")
	f.Bool("boot", false, "Show the VM boot log instead of container stdio (backend extra)")
	f.BoolP("timestamps", "t", false, "Show timestamps (unsupported)")
	f.String("since", "", "Show logs since timestamp (unsupported)")
	f.String("until", "", "Show logs before timestamp (unsupported)")
	f.Bool("details", false, "")
	_ = f.MarkHidden("details")
	return cmd
}

// validateTail maps a docker --tail value onto the backend's -n argument:
// ""/"all" means everything (no -n), a number passes through, and anything
// else errors like docker (previously it was silently ignored, so a typo like
// `--tail latest` dumped the entire log). Shared with compose logs.
func validateTail(tail string) (string, error) {
	if tail == "" || tail == "all" {
		return "", nil
	}
	if _, err := strconv.Atoi(tail); err != nil {
		return "", fmt.Errorf("invalid --tail value %q: must be \"all\" or a number", tail)
	}
	return tail, nil
}

// validInspectTypes is the --type vocabulary `dcon inspect` accepts.
var validInspectTypes = map[string]bool{
	"": true, "container": true, "image": true, "network": true, "volume": true,
}

func newInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect [OPTIONS] NAME|ID [NAME|ID...]",
		Short: "Return low-level information on dcon objects",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			typ, _ := cmd.Flags().GetString("type")
			format, _ := cmd.Flags().GetString("format")
			if !validInspectTypes[typ] {
				return fmt.Errorf("%q is not a valid value for --type (use container, image, network, or volume)", typ)
			}

			kind, raw, missing, err := resolveInspect(typ, args)
			if err != nil {
				return err
			}
			// Print the objects that resolved, then — like `docker inspect` — exit
			// non-zero if any requested id was missing, so CI guards that check the
			// exit code still fire.
			if raw != "" {
				if rerr := renderInspectTyped(kind, raw, format); rerr != nil {
					return rerr
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("no such object: %s", strings.Join(missing, " "))
			}
			return nil
		},
	}
	cmd.Flags().StringP("format", "f", "", "Format output using a Go template (docker inspect schema; raw backend JSON without --format)")
	cmd.Flags().String("type", "", "Return JSON for specified type: container|image|network|volume")
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

// inspectRaw returns the pretty JSON array for the given ids (see
// resolveInspect); kept as a thin wrapper for callers that don't need the
// resolved kind (e.g. `image inspect`).
func inspectRaw(typ string, ids []string) (raw string, missing []string, err error) {
	_, raw, missing, err = resolveInspect(typ, ids)
	return raw, missing, err
}

// inspectProbes is the auto-detect order for a bare `dcon inspect ID`:
// containers first (docker's precedence), then images, networks, volumes.
var inspectProbes = []struct {
	kind string
	args []string
}{
	{"container", []string{"inspect"}},
	{"image", []string{"image", "inspect"}},
	{"network", []string{"network", "inspect"}},
	{"volume", []string{"volume", "inspect"}},
}

// probeOK reports whether an inspect attempt genuinely resolved objects: some
// backend inspects exit 0 with an empty array for unknown names, which must
// count as a miss or auto-detect would stop at the wrong namespace.
func probeOK(out string, err error) bool {
	trimmed := strings.TrimSpace(out)
	return err == nil && trimmed != "" && trimmed != "[]"
}

// resolveInspect returns the object kind ("container", "image", "network",
// "volume", or "mixed") and the pretty JSON array for the given ids,
// auto-detecting across all four namespaces when typ is unset. The returned
// missing slice lists ids that resolved to no object (only populated on the
// auto-detect path); the caller renders the found JSON and then exits
// non-zero, matching docker.
func resolveInspect(typ string, ids []string) (kind, raw string, missing []string, err error) {
	switch typ {
	case "image":
		out, err := runtime.CaptureSilent(append([]string{"image", "inspect"}, ids...)...)
		return typ, out, nil, err
	case "container":
		out, err := runtime.CaptureSilent(append([]string{"inspect"}, ids...)...)
		return typ, out, nil, err
	case "network":
		out, err := runtime.CaptureSilent(append([]string{"network", "inspect"}, ids...)...)
		return typ, out, nil, err
	case "volume":
		out, err := runtime.CaptureSilent(append([]string{"volume", "inspect"}, ids...)...)
		return typ, out, nil, err
	default:
		// Fast path: all ids resolve in one namespace — a single batch call
		// preserves the backend's native formatting.
		for _, p := range inspectProbes {
			if out, err := runtime.CaptureSilent(append(append([]string{}, p.args...), ids...)...); probeOK(out, err) {
				return p.kind, out, nil, nil
			}
		}
		// Mixed namespaces or partly missing: resolve each id independently and
		// merge, so `inspect <container> <image>` works like docker instead of
		// failing because no single namespace holds them all.
		var results []string
		for _, id := range ids {
			found := false
			for _, p := range inspectProbes {
				if out, err := runtime.CaptureSilent(append(append([]string{}, p.args...), id)...); probeOK(out, err) {
					results = append(results, out)
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, id)
			}
		}
		merged, err := mergeInspectArrays(results)
		if err != nil {
			return "", "", nil, err
		}
		return "mixed", merged, missing, nil
	}
}

// renderInspectTyped prints inspect JSON honoring --format. Without a format
// (or with "json") the raw backend JSON passes through — dcon's documented
// no-format behavior. A Go template executes against docker-shaped views for
// containers, networks, and volumes (`docker inspect -f` semantics: CI idioms
// like {{.State.Running}} work); images and mixed sets keep templating over
// the raw backend JSON.
func renderInspectTyped(kind, raw, format string) error {
	if format == "" || format == "json" {
		return renderInspect(raw, format)
	}
	switch kind {
	case "container":
		var list []dockerfmt.Container
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return renderInspect(raw, format) // unexpected shape: raw fallback
		}
		views := make([]any, 0, len(list))
		for _, c := range list {
			views = append(views, dockerfmt.NewContainerInspectView(c))
		}
		return renderInspectViews(format, views)
	case "network":
		var list []dockerfmt.Network
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return renderInspect(raw, format)
		}
		views := make([]any, 0, len(list))
		for _, n := range list {
			views = append(views, dockerfmt.NewNetworkInspectView(n))
		}
		return renderInspectViews(format, views)
	case "volume":
		var list []dockerfmt.Volume
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return renderInspect(raw, format)
		}
		views := make([]any, 0, len(list))
		for _, v := range list {
			views = append(views, dockerfmt.NewVolumeInspectView(v))
		}
		return renderInspectViews(format, views)
	default: // image, mixed: backend JSON schema (documented dcon behavior)
		return renderInspect(raw, format)
	}
}

// renderInspectViews executes a docker-style inspect template once per view.
func renderInspectViews(format string, views []any) error {
	tmpl, err := template.New("inspect").Funcs(dockerfmt.TemplateFuncs()).Parse(format + "\n")
	if err != nil {
		return err
	}
	for _, v := range views {
		if err := tmpl.Execute(os.Stdout, v); err != nil {
			return err
		}
	}
	return nil
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

// cpHostPath absolutizes a `cp` host-side path so backend versions < 1.1.0,
// which mis-resolve relative host paths (apple/container#1738), still copy
// to/from the right place. Docker's copy-contents suffixes ("/." and a
// trailing "/") are semantic and filepath.Abs would clean them away, so they
// are preserved explicitly.
func cpHostPath(p string) string {
	suffix := ""
	switch {
	case strings.HasSuffix(p, "/.") && p != "/.":
		suffix = "/."
	case strings.HasSuffix(p, "/") && p != "/":
		suffix = "/"
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p // fall back to the original; the backend will report it
	}
	if suffix != "" && !strings.HasSuffix(abs, suffix) {
		if abs == "/" { // avoid "//" / "//."
			suffix = strings.TrimPrefix(suffix, "/")
		}
		abs += suffix
	}
	return abs
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
			// Absolutize the host side: backend < 1.1.0 breaks on relative
			// host paths (apple/container#1738).
			if !cpIsContainerRef(src) {
				src = cpHostPath(src)
			}
			if !cpIsContainerRef(dst) {
				dst = cpHostPath(dst)
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
