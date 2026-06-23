package cmd

import (
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

// newPassthrough builds a command that forwards all of its arguments verbatim
// to `container <prefix...> <args...>`. Used for Apple-container-native
// subcommands that have no Docker analogue (machine, system dns/kernel, ...),
// so every backend feature stays reachable through dcon.
func newPassthrough(use, short string, prefix []string, aliases ...string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		Aliases:            aliases,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Let `dcon <group> --help` show the backend's own help.
			if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
				return runtime.Run(append(prefix, "--help")...)
			}
			return runtime.Run(append(append([]string{}, prefix...), args...)...)
		},
	}
}
