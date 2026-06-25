package cmd

import (
	"fmt"

	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

// stub returns a command that cleanly reports an unsupported Docker feature
// (only used where the container backend genuinely cannot provide it).
func stub(use, short, reason string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("%s", reason)
		},
	}
}

// newContainerPruneCmd maps `docker container prune` to `container prune`.
func newContainerPruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove all stopped containers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runtime.Run("prune")
		},
	}
}

func newContainerGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "container",
		Short: "Manage containers",
	}
	ls := newPsCmd()
	ls.Use = "ls [OPTIONS]"
	ls.Aliases = []string{"list"}
	group.AddCommand(
		newRunCmd(), newCreateCmd(), ls, newExecCmd(),
		newStartCmd(), newStopCmd(), newRestartCmd(), newKillCmd(), newRmCmd(),
		newLogsCmd(), newInspectCmd(), newCpCmd(), newExportCmd(), newStatsCmd(),
		newTopCmd(), newPortCmd(), newAttachCmd(),
		newPauseCmd(), newUnpauseCmd(), newWaitCmd(), newRenameCmd(),
		newContainerPruneCmd(),
		stub("diff CONTAINER", "Inspect changes to files or directories on a container's filesystem", "diff is not supported by the container backend"),
		stub("commit CONTAINER [REPOSITORY[:TAG]]", "Create a new image from a container's changes", "commit is not supported by the backend; build an image from a Dockerfile instead"),
		stub("update CONTAINER", "Update configuration of one or more containers", "update is not supported by the backend"),
	)
	return group
}

func init() {
	// Top-level Docker commands.
	rootCmd.AddCommand(
		newRunCmd(), newCreateCmd(), newPsCmd(), newExecCmd(),
		newStartCmd(), newStopCmd(), newRestartCmd(), newKillCmd(), newRmCmd(),
		newLogsCmd(), newInspectCmd(), newCpCmd(), newExportCmd(), newStatsCmd(),
		newTopCmd(), newPortCmd(), newAttachCmd(),
		newPauseCmd(), newUnpauseCmd(), newWaitCmd(), newRenameCmd(),

		// images / registry
		newImagesCmd(), newPullCmd(), newPushCmd(), newRmiCmd(), newTagCmd(),
		newBuildCmd(), newHistoryCmd(),
		newLoginCmd(), newLogoutCmd(),

		// system / info
		newVersionCmd(), newInfoCmd(),

		// dcon-native: warm-VM pool (sub-OrbStack start latency) + setup doctor
		newWarmCmd(), newDoctorCmd(),

		// management groups
		newContainerGroupCmd(), newImageGroupCmd(), newVolumeGroupCmd(),
		newNetworkGroupCmd(), newSystemGroupCmd(), newBuilderGroupCmd(),
		newRegistryGroupCmd(), newMachineCmd(), newContextGroupCmd(),
		newBuildxCmd(), newComposeCmd(),

		// unsupported-but-recognised
		stub("search TERM", "Search the Docker Hub for images", "search is not supported by the backend"),
		stub("events", "Get real time events from the server", "events stream is not supported by the backend"),
		stub("diff CONTAINER", "Inspect changes on a container's filesystem", "diff is not supported by the backend"),
		stub("commit CONTAINER", "Create a new image from a container's changes", "commit is not supported by the backend"),
		stub("update CONTAINER", "Update configuration of one or more containers", "update is not supported by the backend"),
	)

	// manifest, import, and recognised-but-unsupported Swarm/orchestration cmds.
	rootCmd.AddCommand(unsupportedTopLevelCmds()...)

	// top-level `save`/`load` aliases
	save := newImageSaveCmd()
	save.Use = "save [OPTIONS] IMAGE [IMAGE...]"
	load := newImageLoadCmd()
	load.Use = "load [OPTIONS]"
	rootCmd.AddCommand(save, load)
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
	return group
}
