package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestNoFlagShorthandCollisions walks the whole command tree and merges each
// command's persistent/inherited flags. cobra panics if a subcommand reuses a
// shorthand already claimed by a parent's persistent flag (e.g. compose -p vs
// run -p), so a clean walk proves the tree is collision-free.
func TestNoFlagShorthandCollisions(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("flag shorthand collision in command tree: %v", r)
		}
	}()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		_ = c.InheritedFlags() // triggers mergePersistentFlags
		c.InitDefaultHelpFlag()
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

// TestCoreCommandsRegistered sanity-checks that the headline commands exist.
func TestCoreCommandsRegistered(t *testing.T) {
	want := []string{"run", "ps", "images", "build", "compose", "exec", "logs", "volume", "network", "version"}
	have := map[string]bool{}
	for _, c := range rootCmd.Commands() {
		have[c.Name()] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("top-level command %q not registered", w)
		}
	}
}
