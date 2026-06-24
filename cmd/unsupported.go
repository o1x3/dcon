package cmd

import "github.com/spf13/cobra"

// newManifestGroupCmd registers `docker manifest …`. The container backend has
// no manifest-list tooling, so every subcommand reports clearly rather than
// failing as an unknown command. Multi-arch images are produced at build time
// (`dcon build --platform`) and pushed with `dcon push`.
func newManifestGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "manifest COMMAND",
		Short: "Manage Docker image manifests and manifest lists",
	}
	const reason = "is not supported by the backend; build multi-arch images with `dcon build --platform` and push them with `dcon push`"
	group.AddCommand(
		stub("inspect [OPTIONS] [MANIFEST_LIST] MANIFEST", "Display an image manifest, or manifest list", "manifest inspect "+reason),
		stub("create MANIFEST_LIST MANIFEST [MANIFEST...]", "Create a local manifest list for annotating and pushing", "manifest create "+reason),
		stub("annotate MANIFEST_LIST MANIFEST", "Add additional information to a local image manifest", "manifest annotate "+reason),
		stub("push [OPTIONS] MANIFEST_LIST", "Push a manifest list to a repository", "manifest push "+reason),
		stub("rm MANIFEST_LIST [MANIFEST_LIST...]", "Delete one or more manifest lists from local storage", "manifest rm "+reason),
	)
	return group
}

// unsupportedTopLevelCmds are recognised-but-unsupported Docker commands. They
// are registered so scripts get a clear, specific message instead of cobra's
// generic "unknown command", which matters for CI that probes the CLI surface.
func unsupportedTopLevelCmds() []*cobra.Command {
	const swarm = "is a Swarm/daemon feature; dcon is a daemonless single-host translator over Apple's container runtime and does not provide it"
	return []*cobra.Command{
		newManifestGroupCmd(),
		stub("import [OPTIONS] file|URL|- [REPOSITORY[:TAG]]",
			"Import the contents from a tarball to create a filesystem image",
			"import is not supported: the backend loads OCI archives, not raw filesystem tarballs — use `dcon load` for an OCI archive, or build from a Dockerfile"),

		// Swarm / orchestration family — out of scope for a daemonless tool.
		stub("swarm", "Manage Swarm", "swarm "+swarm),
		stub("node", "Manage Swarm nodes", "node "+swarm),
		stub("service", "Manage Swarm services", "service "+swarm),
		stub("stack", "Manage Swarm stacks", "stack "+swarm),
		stub("secret", "Manage Swarm secrets", "secret "+swarm),
		stub("config", "Manage Swarm configs", "config "+swarm),
		stub("plugin", "Manage plugins", "plugin management "+swarm),
		stub("trust", "Manage trust on Docker images", "content trust "+swarm),
	}
}
