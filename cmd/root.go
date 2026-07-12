// Package cmd implements the dcon command tree: a Docker-CLI-compatible front
// end whose backend is Apple's `container` runtime.
package cmd

import (
	"fmt"
	"os"

	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

// Build metadata for the dcon CLI (the Docker-compatibility layer, distinct
// from the backend container engine version). These are overridden at build
// time via -ldflags, e.g.:
//
//	go build -ldflags "-X dcon/cmd.Version=1.2.3 -X dcon/cmd.Commit=$(git rev-parse --short HEAD) -X dcon/cmd.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
var (
	Version = "1.0.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "dcon",
	Short: "A Docker-compatible CLI backed by Apple's container runtime",
	Long: `dcon is a drop-in Docker CLI replacement for macOS.

It speaks the Docker command surface (run, ps, images, build, compose, ...) and
translates every command to Apple's ` + "`container`" + ` runtime, which boots
each container in a lightweight virtual machine.

Set up a true drop-in by aliasing docker:  alias docker=dcon`,
	SilenceUsage:  true,
	SilenceErrors: true,
	// Docker treats unknown subcommands as an error with a hint; cobra default
	// is fine. Keep flag parsing permissive so `-it` style bundling works.
}

// cliFailureExitCode maps docker's CLI-level failure convention: run/create/
// exec failures that never reached the container (usage errors, dcon-level
// translation errors) exit 125, leaving 126/127 to the container runtime and
// the workload's own status untouched; every other command keeps exit 1. Pure
// for testability.
func cliFailureExitCode(cmdName string) int {
	switch cmdName {
	case "run", "create", "exec":
		return 125
	}
	return 1
}

// Execute runs the root command and maps errors to a process exit code that
// mirrors the underlying container invocation where possible.
func Execute() {
	// Rewrite `compose -f/-p` global shorthands to long forms before cobra
	// parses (see rewriteComposeGlobalShorthands for why this can't be a flag).
	argv := rewriteComposeGlobalShorthands(os.Args[1:])
	rootCmd.SetArgs(argv)
	if err := rootCmd.Execute(); err != nil {
		// A proxied backend workload that merely exited non-zero already wrote its
		// own stderr (inherited streams); docker prints nothing here. Only print
		// for genuine dcon-level errors — not Go's "exit status N" artifact.
		if !runtime.IsExitError(err) {
			fmt.Fprintln(os.Stderr, err)
			// docker exits 125 when run/create/exec fail at the CLI level
			// (before any container process ran); a backend exit error above
			// instead propagates the real workload status via ExitCode.
			// Gate on the parent so `compose run`/`compose exec` (which docker
			// compose exits 1 for) don't inherit the 125 convention.
			if c, _, ferr := rootCmd.Find(argv); ferr == nil && c != nil && c.Parent() != nil &&
				(c.Parent() == rootCmd || c.Parent().Name() == "container") {
				os.Exit(cliFailureExitCode(c.Name()))
			}
			os.Exit(1)
		}
		os.Exit(runtime.ExitCode(err))
	}
}

func init() {
	rootCmd.PersistentFlags().BoolP("debug", "D", false, "Enable debug mode (echo backend container commands)")
	rootCmd.PersistentFlags().StringP("host", "H", "", "Daemon socket to connect to (accepted for compatibility; ignored)")
	// --context and --log-level are long-only here to avoid clobbering the
	// `-c`/`-l` shorthands that subcommands (build, ps) use, matching docker.
	rootCmd.PersistentFlags().String("context", "", "Context to use (accepted for compatibility; ignored)")
	rootCmd.PersistentFlags().String("log-level", "", "Logging level (accepted for compatibility)")
	rootCmd.PersistentFlags().String("config", "", "Location of client config files (accepted for compatibility)")
	// TLS flags: dcon never dials a TCP daemon, so these are accepted and ignored
	// to keep `docker --tlsverify …`-style invocations working.
	rootCmd.PersistentFlags().Bool("tls", false, "Use TLS (accepted for compatibility; ignored)")
	rootCmd.PersistentFlags().Bool("tlsverify", false, "Use TLS and verify the remote (accepted for compatibility; ignored)")
	rootCmd.PersistentFlags().String("tlscacert", "", "Trust certs signed only by this CA (accepted; ignored)")
	rootCmd.PersistentFlags().String("tlscert", "", "Path to TLS certificate file (accepted; ignored)")
	rootCmd.PersistentFlags().String("tlskey", "", "Path to TLS key file (accepted; ignored)")
	_ = rootCmd.PersistentFlags().MarkHidden("log-level")
	_ = rootCmd.PersistentFlags().MarkHidden("tls")
	_ = rootCmd.PersistentFlags().MarkHidden("tlsverify")
	_ = rootCmd.PersistentFlags().MarkHidden("tlscacert")
	_ = rootCmd.PersistentFlags().MarkHidden("tlscert")
	_ = rootCmd.PersistentFlags().MarkHidden("tlskey")

	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if d, _ := cmd.Flags().GetBool("debug"); d {
			os.Setenv("DCON_DEBUG", "1")
		}
	}

	rootCmd.SetVersionTemplate("dcon version {{.Version}}\n")
	rootCmd.Version = Version
}

// AddCommand exposes registration to sub-packages / files in this package.
func AddCommand(cmds ...*cobra.Command) {
	rootCmd.AddCommand(cmds...)
}
