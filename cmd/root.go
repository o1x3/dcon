// Package cmd implements the dcon command tree: a Docker-CLI-compatible front
// end whose backend is Apple's `container` runtime.
package cmd

import (
	"fmt"
	"os"

	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

// Version is the dcon CLI version (the Docker-compatibility layer's own
// version, distinct from the backend container engine version).
const Version = "1.0.0"

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

// Execute runs the root command and maps errors to a process exit code that
// mirrors the underlying container invocation where possible.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
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
	_ = rootCmd.PersistentFlags().MarkHidden("log-level")

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
