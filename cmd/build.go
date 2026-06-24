package cmd

import (
	"fmt"
	"os"
	"strings"

	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [OPTIONS] PATH | URL | -",
		Short: "Build an image from a Dockerfile",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs, err := buildBuildArgs(cmd, args)
			if err != nil {
				return err
			}
			return runtime.Run(cargs...)
		},
	}
	addBuildFlags(cmd)
	return cmd
}

// translateOutput maps a Docker --output exporter spec onto what the container
// backend accepts (oci|tar|local). docker/image become oci; registry errors.
func translateOutput(spec string) (string, error) {
	fields := strings.Split(spec, ",")
	var typ string
	for _, fld := range fields {
		if strings.HasPrefix(fld, "type=") {
			typ = strings.TrimPrefix(fld, "type=")
		}
	}
	switch typ {
	case "", "oci", "tar", "local":
		return spec, nil
	case "docker", "image":
		// load into the local image store: remap to oci, keep other fields.
		var out []string
		for _, fld := range fields {
			if strings.HasPrefix(fld, "type=") {
				out = append(out, "type=oci")
			} else if strings.HasPrefix(fld, "name=") {
				continue // covered by --tag
			} else {
				out = append(out, fld)
			}
		}
		return strings.Join(out, ","), nil
	default:
		return "", fmt.Errorf("--output type=%s is not supported by the backend (use docker, image, oci, tar, or local); push separately with 'dcon push'", typ)
	}
}

// buildBuildArgs translates docker build flags on cmd into `container build …`.
func buildBuildArgs(cmd *cobra.Command, args []string) ([]string, error) {
	f := cmd.Flags()
	cargs := []string{"build"}

	for _, t := range mustStringArray(f, "tag") {
		cargs = append(cargs, "--tag", t)
	}
	if file, _ := f.GetString("file"); file != "" {
		cargs = append(cargs, "--file", file)
	}
	for _, b := range mustStringArray(f, "build-arg") {
		cargs = append(cargs, "--build-arg", b)
	}
	for _, l := range mustStringArray(f, "label") {
		cargs = append(cargs, "--label", l)
	}
	for _, s := range mustStringArray(f, "secret") {
		if strings.Contains(s, "type=") {
			fmt.Fprintf(os.Stderr, "dcon: warning: --secret type= field is not supported by the backend; use id=<key>[,src=<path>|,env=<VAR>]\n")
		}
		cargs = append(cargs, "--secret", s)
	}
	if v, _ := f.GetBool("no-cache"); v {
		cargs = append(cargs, "--no-cache")
	}
	if v, _ := f.GetBool("pull"); v {
		cargs = append(cargs, "--pull")
	}
	if v, _ := f.GetBool("quiet"); v {
		cargs = append(cargs, "--quiet")
	}
	if t, _ := f.GetString("target"); t != "" {
		cargs = append(cargs, "--target", t)
	}
	if p, _ := f.GetString("platform"); p != "" {
		cargs = append(cargs, "--platform", p)
	}
	for _, o := range mustStringArray(f, "output") {
		mapped, err := translateOutput(o)
		if err != nil {
			return nil, err
		}
		cargs = append(cargs, "--output", mapped)
	}
	if pr, _ := f.GetString("progress"); pr != "" && pr != "auto" {
		// docker has rawjson/quiet; container supports auto|plain|tty.
		if pr == "rawjson" {
			pr = "plain"
		}
		cargs = append(cargs, "--progress", pr)
	}
	if c, _ := f.GetString("cpus"); c != "" {
		cargs = append(cargs, "--cpus", c)
	}
	if m, _ := f.GetString("memory"); m != "" {
		cargs = append(cargs, "--memory", m)
	}
	if a, _ := f.GetString("arch"); a != "" {
		cargs = append(cargs, "--arch", a)
	}
	if o, _ := f.GetString("os"); o != "" {
		cargs = append(cargs, "--os", o)
	}
	// docker cache-from/to -> container hidden cache-in/out (best effort)
	for _, cf := range mustStringArray(f, "cache-from") {
		cargs = append(cargs, "--cache-in", cf)
	}
	for _, ct := range mustStringArray(f, "cache-to") {
		cargs = append(cargs, "--cache-out", ct)
	}

	if f.Changed("push") {
		fmt.Fprintln(os.Stderr, "dcon: warning: --push is not supported by the backend; build, then push separately with 'dcon push'")
	}
	for _, name := range []string{
		"network", "add-host", "ssh", "squash", "iidfile", "build-context",
		"no-cache-filter", "cgroup-parent", "isolation", "shm-size", "ulimit",
		"memory-swap", "security-opt", "metadata-file", "allow", "builder",
		"provenance", "sbom", "attest", "annotation", "compress",
	} {
		if f.Changed(name) {
			fmt.Fprintf(os.Stderr, "dcon: warning: --%s is not supported by the backend and was ignored\n", name)
		}
	}

	ctx := "."
	if len(args) == 1 {
		ctx = args[0]
	}
	cargs = append(cargs, ctx)
	return cargs, nil
}

func addBuildFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringArrayP("tag", "t", nil, "Name and optionally a tag (format: name:tag)")
	f.StringP("file", "f", "", "Name of the Dockerfile (default: PATH/Dockerfile)")
	f.StringArray("build-arg", nil, "Set build-time variables")
	f.StringArrayP("label", "l", nil, "Set metadata for an image")
	f.StringArray("secret", nil, "Secret to expose to the build")
	f.Bool("no-cache", false, "Do not use cache when building the image")
	f.Bool("pull", false, "Always attempt to pull a newer version of the image")
	f.BoolP("quiet", "q", false, "Suppress the build output and print image ID on success")
	f.String("target", "", "Set the target build stage to build")
	f.String("platform", "", "Set platform if server is multi-platform capable")
	f.StringArrayP("output", "o", nil, "Output destination (format: type=local,dest=path)")
	f.String("progress", "auto", "Set type of progress output (auto, plain, tty, rawjson)")
	f.StringP("cpus", "c", "", "CPUs to allocate to the builder (backend extra)")
	f.StringP("memory", "m", "", "Memory for the builder (backend extra)")
	f.StringP("arch", "a", "", "Target architecture (backend extra)")
	f.String("os", "", "Target OS (backend extra)")
	f.StringArray("cache-from", nil, "External cache sources")
	f.StringArray("cache-to", nil, "Cache export destinations")
	// Accepted-but-ignored docker build flags
	f.String("network", "", "Networking mode for RUN instructions (unsupported)")
	f.StringSlice("add-host", nil, "Add a custom host-to-IP mapping (unsupported)")
	f.StringSlice("ssh", nil, "SSH agent socket or keys to expose (unsupported)")
	f.Bool("squash", false, "Squash newly built layers (unsupported)")
	f.String("iidfile", "", "Write the image ID to the file (unsupported)")
	f.StringArray("build-context", nil, "Additional build contexts (unsupported)")
	f.Bool("rm", true, "Remove intermediate containers after a successful build (no-op)")
	f.Bool("force-rm", false, "Always remove intermediate containers (no-op)")
	// --load is the backend's default (the build lands in the local image store),
	// so it is accepted as a silent no-op for buildx compatibility.
	f.Bool("load", false, "Shorthand for --output=type=docker (default behavior; no-op)")
	f.Bool("push", false, "Shorthand for --output=type=registry (unsupported; push separately)")
	f.Bool("disable-content-trust", true, "Skip image verification (no-op; backend has no content trust)")
	// Accepted-but-ignored buildx/BuildKit flags so buildx-style invocations
	// don't hard-fail on an unknown flag.
	f.StringArray("no-cache-filter", nil, "Do not cache specified stages (unsupported)")
	f.String("cgroup-parent", "", "Set the parent cgroup for RUN instructions (unsupported)")
	f.String("isolation", "", "Container isolation technology (unsupported)")
	f.String("shm-size", "", "Size of /dev/shm for RUN instructions (unsupported)")
	f.StringSlice("ulimit", nil, "Ulimit options for RUN instructions (unsupported)")
	f.String("memory-swap", "", "Swap limit equal to memory plus swap (unsupported)")
	f.StringArray("security-opt", nil, "Security options (unsupported)")
	f.String("metadata-file", "", "Write build result metadata to a file (unsupported)")
	f.StringSlice("allow", nil, "Allow extra privileged entitlements (unsupported)")
	f.String("builder", "", "Override the configured builder instance (unsupported)")
	f.String("provenance", "", "Add a provenance attestation (unsupported)")
	f.String("sbom", "", "Add an SBOM attestation (unsupported)")
	f.StringArray("attest", nil, "Attestation parameters (unsupported)")
	f.StringArray("annotation", nil, "Add an OCI annotation to the image (unsupported)")
	f.Bool("compress", false, "Compress the build context using gzip (unsupported)")
	_ = f.MarkHidden("rm")
	_ = f.MarkHidden("force-rm")
}
