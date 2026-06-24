package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"text/template"

	"dcon/internal/dockerfmt"
	rt "dcon/internal/runtime"

	"github.com/spf13/cobra"
)

var verRe = regexp.MustCompile(`version\s+([0-9][^\s]*)\s*\(build:\s*([^,]+),\s*commit:\s*([^)]+)\)`)

type versionComponent struct {
	Version  string
	Build    string
	Commit   string
	Platform string
}

type versionInfo struct {
	Client versionComponent
	Server versionComponent
}

func backendVersion() versionComponent {
	out, err := rt.CaptureSilent("--version")
	c := versionComponent{Version: "unknown"}
	if err == nil {
		if m := verRe.FindStringSubmatch(out); m != nil {
			c.Version = m[1]
			c.Build = strings.TrimSpace(m[2])
			c.Commit = strings.TrimSpace(m[3])
		}
	}
	c.Platform = runtime.GOOS + "/" + runtime.GOARCH
	return c
}

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version [OPTIONS]",
		Short: "Show the dcon and backend engine version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := versionInfo{
				Client: versionComponent{Version: Version, Platform: runtime.GOOS + "/" + runtime.GOARCH},
				Server: backendVersion(),
			}
			if format, _ := cmd.Flags().GetString("format"); format != "" {
				if format == "json" {
					b, err := json.Marshal(info)
					if err != nil {
						return err
					}
					fmt.Println(string(b))
					return nil
				}
				tmpl, err := template.New("v").Funcs(dockerfmt.TemplateFuncs()).Parse(format + "\n")
				if err != nil {
					return err
				}
				return tmpl.Execute(os.Stdout, info)
			}
			fmt.Printf("Client: dcon (Docker-compatible)\n")
			fmt.Printf(" Version:    %s\n", info.Client.Version)
			if Commit != "none" {
				fmt.Printf(" Git commit: %s\n", Commit)
			}
			if Date != "unknown" {
				fmt.Printf(" Built:      %s\n", Date)
			}
			fmt.Printf(" OS/Arch:    %s\n", info.Client.Platform)
			fmt.Printf("\nServer: Apple container\n")
			fmt.Printf(" Engine:\n")
			fmt.Printf("  Version:   %s\n", info.Server.Version)
			if info.Server.Build != "" {
				fmt.Printf("  Build:     %s\n", info.Server.Build)
			}
			if info.Server.Commit != "" {
				fmt.Printf("  GitCommit: %s\n", info.Server.Commit)
			}
			fmt.Printf("  OS/Arch:   %s\n", info.Server.Platform)
			return nil
		},
	}
	cmd.Flags().StringP("format", "f", "", "Format output using a Go template")
	return cmd
}

// infoData mirrors the subset of `docker info` JSON keys that scripts commonly
// read (e.g. `docker info -f '{{.ServerVersion}}'`), so --format works.
type infoData struct {
	ID                string
	Containers        int
	ContainersRunning int
	ContainersPaused  int
	ContainersStopped int
	Images            int
	Driver            string
	ServerVersion     string
	OperatingSystem   string
	OSType            string
	Architecture      string
	NCPU              int
	Name              string
	DockerRootDir     string
	Isolation         string
	ServerState       string
}

func newInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info [OPTIONS]",
		Short: "Display system-wide information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := getContainers(true)
			var running, stopped int
			for _, c := range all {
				if c.Status.State == "running" {
					running++
				} else {
					stopped++
				}
			}
			imgs, _ := getImages()
			ver := backendVersion()

			// status check
			statusOut, _ := rt.CaptureSilent("system", "status")
			serverState := "running"
			if !strings.Contains(statusOut, "running") {
				serverState = "stopped"
			}

			if format, _ := cmd.Flags().GetString("format"); format != "" {
				data := infoData{
					ID:                "apple-container",
					Containers:        len(all),
					ContainersRunning: running,
					ContainersStopped: stopped,
					Images:            len(imgs),
					Driver:            "virtualization.framework",
					ServerVersion:     ver.Version,
					OperatingSystem:   "macOS",
					OSType:            "linux",
					Architecture:      runtime.GOARCH,
					NCPU:              runtime.NumCPU(),
					Name:              hostnameOrUnknown(),
					Isolation:         "vm",
					ServerState:       serverState,
				}
				if format == "json" {
					b, err := json.Marshal(data)
					if err != nil {
						return err
					}
					fmt.Println(string(b))
					return nil
				}
				tmpl, err := template.New("info").Funcs(dockerfmt.TemplateFuncs()).Parse(format + "\n")
				if err != nil {
					return err
				}
				return tmpl.Execute(os.Stdout, data)
			}

			fmt.Printf("Client:\n")
			fmt.Printf(" Version:    %s\n", Version)
			fmt.Printf(" Context:    default\n")
			fmt.Printf("\nServer:\n")
			fmt.Printf(" Containers: %d\n", len(all))
			fmt.Printf("  Running: %d\n", running)
			fmt.Printf("  Paused: 0\n")
			fmt.Printf("  Stopped: %d\n", stopped)
			fmt.Printf(" Images: %d\n", len(imgs))
			fmt.Printf(" Server Version: %s\n", ver.Version)
			fmt.Printf(" Storage Driver: virtualization.framework\n")
			fmt.Printf(" Backend: Apple container (%s)\n", serverState)
			fmt.Printf(" Isolation: vm\n")
			fmt.Printf(" Operating System: macOS\n")
			fmt.Printf(" OSType: linux (guest)\n")
			fmt.Printf(" Architecture: %s\n", runtime.GOARCH)
			fmt.Printf(" Name: %s\n", hostnameOrUnknown())
			return nil
		},
	}
	cmd.Flags().StringP("format", "f", "", "Format output using a Go template or 'json'")
	return cmd
}

func hostnameOrUnknown() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

// newSystemGroupCmd builds `dcon system ...`: Docker-shaped df/prune/info plus
// passthrough to every backend-native `container system` subcommand.
func newSystemGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:     "system",
		Short:   "Manage Docker / backend system",
		Aliases: []string{"s"},
	}

	df := &cobra.Command{
		Use:   "df [OPTIONS]",
		Short: "Show docker disk usage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"system", "df"}
			if f, _ := cmd.Flags().GetString("format"); f != "" {
				cargs = append(cargs, "--format", f)
			}
			return rt.Run(cargs...)
		},
	}
	df.Flags().BoolP("verbose", "v", false, "Show detailed information on space usage")
	df.Flags().String("format", "", "Format output using a Go template")

	prune := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused data (stopped containers, unused images/networks)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			volumes, _ := cmd.Flags().GetBool("volumes")
			fmt.Println("Deleting stopped containers...")
			_ = rt.Run("prune")
			fmt.Println("Deleting unused images...")
			if all {
				_ = rt.Run("image", "prune", "--all")
			} else {
				_ = rt.Run("image", "prune")
			}
			fmt.Println("Deleting unused networks...")
			_ = rt.Run("network", "prune")
			if volumes {
				fmt.Println("Deleting unused volumes...")
				_ = rt.Run("volume", "prune")
			}
			return nil
		},
	}
	prune.Flags().BoolP("all", "a", false, "Remove all unused images, not just dangling ones")
	prune.Flags().BoolP("force", "f", false, "Do not prompt for confirmation (no-op)")
	prune.Flags().Bool("volumes", false, "Prune volumes too")
	prune.Flags().String("filter", "", "Provide filter values (unsupported)")

	events := &cobra.Command{
		Use:   "events",
		Short: "Get real time events from the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("events stream is not supported by the backend")
		},
	}

	group.AddCommand(df, prune, events, newInfoCmd(), newVersionCmd(), newDoctorCmd())
	// Backend-native passthroughs
	group.AddCommand(
		newPassthrough("dns [SUBCOMMAND]", "Manage local DNS domains (backend)", []string{"system", "dns"}),
		newPassthrough("kernel [SUBCOMMAND]", "Manage the default kernel (backend)", []string{"system", "kernel"}),
		newPassthrough("property [SUBCOMMAND]", "Manage system property values (backend)", []string{"system", "property"}),
		newPassthrough("logs", "Fetch backend service logs", []string{"system", "logs"}),
		newPassthrough("start", "Start backend container services", []string{"system", "start"}),
		newPassthrough("stop", "Stop backend container services", []string{"system", "stop"}),
		newPassthrough("status", "Show backend service status", []string{"system", "status"}),
	)
	return group
}

func newBuilderGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "builder",
		Short: "Manage builds and the backend builder instance",
	}
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Remove build cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stderr, "dcon: note: the backend builder has no separate cache prune; restart the builder to reset it")
			return nil
		},
	}
	group.AddCommand(
		newPassthrough("start", "Start the builder container", []string{"builder", "start"}),
		newPassthrough("status", "Display builder status", []string{"builder", "status"}),
		newPassthrough("stop", "Stop the builder container", []string{"builder", "stop"}),
		newPassthrough("rm", "Delete the builder container", []string{"builder", "delete"}, "delete"),
		prune,
	)
	return group
}

func newMachineGroupCmd() *cobra.Command {
	// machine is entirely backend-native; forward the whole group.
	m := newPassthrough("machine [SUBCOMMAND]", "Manage container machines (backend-native)", []string{"machine"}, "m")
	return m
}
