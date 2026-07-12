package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"text/template"

	"dcon/internal/dockerfmt"
	rt "dcon/internal/runtime"
	"dcon/internal/ui"

	"github.com/spf13/cobra"
)

// pruneStep is one backend prune invocation with its progress message.
type pruneStep struct {
	msg  string
	args []string
}

// systemPrunePlan is the ordered set of backend prune calls `system prune`
// makes, varying with --all (images) and --volumes. Pure, so the plan is
// unit-testable without a backend.
func systemPrunePlan(all, volumes bool) []pruneStep {
	imageArgs := []string{"image", "prune"}
	if all {
		imageArgs = append(imageArgs, "--all")
	}
	steps := []pruneStep{
		{"Deleting stopped containers...", []string{"prune"}},
		{"Deleting unused images...", imageArgs},
		{"Deleting unused networks...", []string{"network", "prune"}},
	}
	if volumes {
		steps = append(steps, pruneStep{"Deleting unused volumes...", []string{"volume", "prune"}})
	}
	return steps
}

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
			fmt.Printf("%s dcon (Docker-compatible)\n", ui.Title("Client:"))
			fmt.Printf(" Version:    %s\n", info.Client.Version)
			if Commit != "none" {
				fmt.Printf(" Git commit: %s\n", Commit)
			}
			if Date != "unknown" {
				fmt.Printf(" Built:      %s\n", Date)
			}
			fmt.Printf(" OS/Arch:    %s\n", info.Client.Platform)
			fmt.Printf("\n%s Apple container\n", ui.Title("Server:"))
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
	ID                 string
	Containers         int
	ContainersRunning  int
	ContainersPaused   int
	ContainersStopped  int
	Images             int
	Driver             string
	ServerVersion      string
	OperatingSystem    string
	OSType             string
	Architecture       string
	KernelVersion      string
	NCPU               int
	MemTotal           int64
	IndexServerAddress string
	Name               string
	DockerRootDir      string
	Isolation          string
	ServerState        string
}

// infoExit mirrors `docker info`, which prints the client/server sections AND
// exits non-zero when the engine is unreachable. Readiness gates such as
// `until docker info; do sleep 1; done` and `docker info >/dev/null 2>&1 && …`
// rely on that exit code; returning 0 with the backend down makes them report
// the engine up when it is not.
func infoExit(serverState string) error {
	if serverState != "running" {
		return fmt.Errorf("errors pretty printing info: Apple container backend is not running")
	}
	return nil
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

			// status check — exact field match (like doctor), not a substring:
			// "running" is a substring of "not running".
			statusOut, _ := rt.CaptureSilent("system", "status")
			serverState := "stopped"
			if parseSystemStatus(statusOut)["status"] == "running" {
				serverState = "running"
			}

			if format, _ := cmd.Flags().GetString("format"); format != "" {
				data := infoData{
					ID:                 "apple-container",
					Containers:         len(all),
					ContainersRunning:  running,
					ContainersStopped:  stopped,
					Images:             len(imgs),
					Driver:             "virtualization.framework",
					ServerVersion:      ver.Version,
					OperatingSystem:    "macOS",
					OSType:             "linux",
					Architecture:       runtime.GOARCH,
					KernelVersion:      hostKernelVersion(),
					NCPU:               runtime.NumCPU(),
					MemTotal:           hostMemTotal(),
					IndexServerAddress: "https://index.docker.io/v1/",
					Name:               hostnameOrUnknown(),
					Isolation:          "vm",
					ServerState:        serverState,
				}
				if format == "json" {
					b, err := json.Marshal(data)
					if err != nil {
						return err
					}
					fmt.Println(string(b))
					return infoExit(serverState)
				}
				tmpl, err := template.New("info").Funcs(dockerfmt.TemplateFuncs()).Parse(format + "\n")
				if err != nil {
					return err
				}
				if err := tmpl.Execute(os.Stdout, data); err != nil {
					return err
				}
				return infoExit(serverState)
			}

			fmt.Printf("%s\n", ui.Title("Client:"))
			fmt.Printf(" Version:    %s\n", Version)
			fmt.Printf(" Context:    default\n")
			fmt.Printf("\n%s\n", ui.Title("Server:"))
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
			if kv := hostKernelVersion(); kv != "" {
				fmt.Printf(" Kernel Version: %s (host)\n", kv)
			}
			fmt.Printf(" CPUs: %d\n", runtime.NumCPU())
			if mt := hostMemTotal(); mt > 0 {
				fmt.Printf(" Total Memory: %s\n", dockerfmt.HumanSizeBinaryBytes(uint64(mt)))
			}
			fmt.Printf(" Index Server Address: https://index.docker.io/v1/\n")
			fmt.Printf(" Name: %s\n", hostnameOrUnknown())
			return infoExit(serverState)
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

// dfUsage is one bucket of `container system df --format json` output.
type dfUsage struct {
	Active      int   `json:"active"`
	Reclaimable int64 `json:"reclaimable"`
	SizeInBytes int64 `json:"sizeInBytes"`
	Total       int   `json:"total"`
}

// backendDF mirrors the backend's `system df` JSON document.
type backendDF struct {
	Containers dfUsage `json:"containers"`
	Images     dfUsage `json:"images"`
	Volumes    dfUsage `json:"volumes"`
}

// dfView exposes docker's `system df` template fields (.Type, .TotalCount,
// .Active, .Size, .Reclaimable).
type dfView struct {
	Type        string
	TotalCount  string
	Active      string
	Size        string
	Reclaimable string
}

// dfViews renders the backend usage document into docker's four rows (Build
// Cache is always zero: the backend does not track it separately). Pure for
// testability.
func dfViews(d backendDF) []any {
	mk := func(typ string, u dfUsage) dfView {
		reclaim := dockerfmt.HumanSize(float64(u.Reclaimable))
		if u.SizeInBytes > 0 {
			reclaim += fmt.Sprintf(" (%d%%)", int(float64(u.Reclaimable)/float64(u.SizeInBytes)*100))
		}
		return dfView{
			Type:        typ,
			TotalCount:  strconv.Itoa(u.Total),
			Active:      strconv.Itoa(u.Active),
			Size:        dockerfmt.HumanSize(float64(u.SizeInBytes)),
			Reclaimable: reclaim,
		}
	}
	return []any{
		mk("Images", d.Images),
		mk("Containers", d.Containers),
		mk("Local Volumes", d.Volumes),
		mk("Build Cache", dfUsage{}),
	}
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
			if cmd.Flags().Changed("verbose") {
				fmt.Fprintln(os.Stderr, "dcon: warning: --verbose is not supported by the backend and was ignored")
			}
			format, _ := cmd.Flags().GetString("format")
			if format == "" {
				// Backend-native table (current documented behavior).
				return rt.Run("system", "df")
			}
			// --format: the backend only accepts json|table|yaml|toml, so a Go
			// template (docker's convention) hard-failed. Parse the backend JSON
			// and render docker-style client-side, like ps/images.
			var usage backendDF
			if err := rt.CaptureJSON(&usage, "system", "df", "--format", "json"); err != nil {
				return err
			}
			def := dockerfmt.TableDef{
				Headers: []string{"TYPE", "TOTAL", "ACTIVE", "SIZE", "RECLAIMABLE"},
				Row: func(v any) []string {
					d := v.(dfView)
					return []string{d.Type, d.TotalCount, d.Active, d.Size, d.Reclaimable}
				},
				ID:           func(v any) string { return v.(dfView).Type },
				FieldHeaders: map[string]string{".TotalCount": "TOTAL"},
			}
			return dockerfmt.Render(format, false, dfViews(usage), def)
		},
	}
	df.Flags().BoolP("verbose", "v", false, "Show detailed information on space usage")
	df.Flags().String("format", "", "Format output using a Go template or 'json'")

	prune := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused data (stopped containers, unused images/networks)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			volumes, _ := cmd.Flags().GetBool("volumes")
			if cmd.Flags().Changed("filter") {
				fmt.Fprintln(os.Stderr, "dcon: warning: --filter is not supported by the backend and was ignored")
			}
			// Run every step but propagate failures: previously all errors were
			// discarded and the command always exited 0, so a wholly failed prune
			// (e.g. backend down) reported success and misled scripts/CI.
			var errs []error
			for _, step := range systemPrunePlan(all, volumes) {
				fmt.Println(step.msg)
				errs = append(errs, rt.Run(step.args...))
			}
			return errors.Join(errs...)
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
		newKernelCmd(),
		newPassthrough("property [SUBCOMMAND]", "Manage system property values (backend)", []string{"system", "property"}),
		newPassthrough("logs", "Fetch backend service logs", []string{"system", "logs"}),
		newPassthrough("start", "Start backend container services", []string{"system", "start"}),
		newPassthrough("stop", "Stop backend container services", []string{"system", "stop"}),
		newPassthrough("status", "Show backend service status", []string{"system", "status"}),
	)
	return group
}

// newKernelCmd forwards `dcon system kernel …` to the backend, but makes
// `kernel set` idempotent. Apple's `container system kernel set` always
// re-downloads the kernel and copies it into place with a plain copy that has
// no overwrite path, so re-running it (or running it when the kernel is already
// present, e.g. from install.sh) fails with NSCocoa 516 / POSIX EEXIST even
// though nothing is wrong. dcon treats that one specific failure as a
// successful no-op; every other error and every other subcommand passes
// through unchanged.
func newKernelCmd() *cobra.Command {
	prefix := []string{"system", "kernel"}
	return &cobra.Command{
		Use:                "kernel [SUBCOMMAND]",
		Short:              "Manage the default kernel (backend)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
				return rt.Run(append(prefix, "--help")...)
			}
			if len(args) >= 1 && args[0] == "set" {
				cargs := append(append([]string{}, prefix...), args...)
				out, err := rt.CaptureSilent(cargs...)
				if out != "" {
					fmt.Print(out)
				}
				if err != nil {
					if kernelAlreadyInstalled(err) {
						fmt.Fprintln(os.Stderr, "dcon: the requested kernel is already installed; nothing to do")
						return nil
					}
					return err
				}
				return nil
			}
			return rt.Run(append(append([]string{}, prefix...), args...)...)
		},
	}
}

// kernelAlreadyInstalled reports whether a `kernel set` error is the backend's
// non-idempotent "the kernel file already exists" failure (NSCocoa 516 /
// POSIX EEXIST) rather than a genuine problem.
func kernelAlreadyInstalled(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exists") || strings.Contains(s, "file exists")
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
