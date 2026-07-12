package cmd

import (
	"fmt"
	"os"
	goruntime "runtime"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// buildx compatibility: CI systems routinely probe `docker buildx ls/inspect`
// and create/use throwaway builders before building. dcon has exactly one
// implicit builder (the backend's builder container), so these commands
// present a single fake "default" builder — enough for probes and scripts to
// proceed — while create/use/rm are accepted as warned no-ops and bake (which
// has no backend equivalent at all) hard-errors.

// buildxPlatforms reports the platforms the backend can build for on this
// host: the native linux/GOARCH plus linux/amd64 via Rosetta on arm64.
func buildxPlatforms() string {
	native := "linux/" + goruntime.GOARCH
	if goruntime.GOARCH == "arm64" {
		return native + ", linux/amd64"
	}
	return native
}

// buildxNoop returns an accepted-but-no-op buildx subcommand that warns once
// on use, so `buildx create/use/rm` in CI scripts proceed instead of failing.
func buildxNoop(use, short, note string, echo string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true, // swallow buildx-specific flags
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stderr, "dcon: warning: "+note)
			if echo != "" {
				fmt.Println(echo)
			}
			return nil
		},
	}
}

func newBuildxCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "buildx",
		Short: "Docker Buildx (mapped to the backend builder)",
	}
	b := newBuildCmd()
	b.Use = "build [OPTIONS] PATH | URL | -"
	group.AddCommand(b)

	group.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Show buildx version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("dcon buildx (Apple container builder backend)")
			return nil
		},
	})

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List builder instances",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
			fmt.Fprintln(w, "NAME/NODE\tDRIVER/ENDPOINT\tSTATUS\tBUILDKIT\tPLATFORMS")
			fmt.Fprintln(w, "default*\tdocker\t\t\t")
			fmt.Fprintf(w, " \\_ default\t \\_ default\trunning\tapple-container\t%s\n", buildxPlatforms())
			return w.Flush()
		},
	}

	inspect := &cobra.Command{
		Use:   "inspect [NAME]",
		Short: "Inspect the current builder instance",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && args[0] != "default" {
				return fmt.Errorf("no builder %q found (dcon provides only the built-in \"default\" builder)", args[0])
			}
			fmt.Printf("Name:   default\nDriver: docker\n\nNodes:\nName:      default\nEndpoint:  default\nStatus:    running\nBuildkit:  apple-container\nPlatforms: %s\n", buildxPlatforms())
			return nil
		},
	}
	inspect.Flags().Bool("bootstrap", false, "Ensure builder has booted before inspecting (no-op)")

	group.AddCommand(
		ls, inspect,
		buildxNoop("create [OPTIONS] [CONTEXT|ENDPOINT]", "Create a new builder instance",
			"buildx create is a no-op: dcon has a single built-in builder (\"default\")", "default"),
		buildxNoop("use [OPTIONS] NAME", "Set the current builder instance",
			"buildx use is a no-op: dcon always uses the built-in \"default\" builder", ""),
		buildxNoop("rm [OPTIONS] [NAME...]", "Remove one or more builder instances",
			"buildx rm is a no-op: the built-in \"default\" builder cannot be removed", ""),
		buildxNoop("du", "Disk usage",
			"buildx du is not tracked per-builder by the backend; use `dcon system df`", ""),
		buildxNoop("prune", "Remove build cache",
			"the backend builder has no separate cache prune; restart the builder to reset it", ""),
		stub("bake [OPTIONS] [TARGET...]", "Build from a file",
			"buildx bake is not supported by the backend; invoke `dcon build` per target instead"),
	)
	return group
}
