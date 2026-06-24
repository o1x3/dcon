package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dcon/internal/compose"
	"dcon/internal/dockerfmt"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func loadProject(cmd *cobra.Command) (*compose.Project, error) {
	files, _ := cmd.Flags().GetStringArray("file")
	project, _ := cmd.Flags().GetString("project-name")
	var explicit string
	if len(files) > 0 {
		explicit = files[0]
	}
	path, err := compose.Find(explicit)
	if err != nil {
		return nil, err
	}
	return compose.Load(path, project)
}

// projectContainers returns all backend containers belonging to a project.
func projectContainers(project string) ([]dockerfmt.Container, error) {
	all, err := getContainers(true)
	if err != nil {
		return nil, err
	}
	var out []dockerfmt.Container
	for _, c := range all {
		if c.Configuration.Labels[compose.LabelProject] == project {
			out = append(out, c)
		}
	}
	return out, nil
}

func serviceOf(c dockerfmt.Container) string { return c.Configuration.Labels[compose.LabelService] }

// enabledProfiles collects active compose profiles from --profile and the
// COMPOSE_PROFILES environment variable.
func enabledProfiles(cmd *cobra.Command) map[string]bool {
	active := map[string]bool{}
	profs, _ := cmd.Flags().GetStringArray("profile")
	for _, p := range profs {
		active[p] = true
	}
	for _, p := range strings.Split(os.Getenv("COMPOSE_PROFILES"), ",") {
		if p != "" {
			active[p] = true
		}
	}
	return active
}

// skipService reports whether a service should be skipped: not explicitly
// selected AND not enabled by an active profile.
func skipService(p *compose.Project, name string, selected map[string]bool, active map[string]bool) bool {
	if selected != nil {
		return !selected[name]
	}
	svc := p.Services[name]
	return svc != nil && !svc.Enabled(active)
}

func newComposeCmd() *cobra.Command {
	group := &cobra.Command{
		Use:     "compose",
		Short:   "Define and run multi-container applications with a compose file",
		Aliases: []string{},
	}
	// --file/--project-name are long-only here so they don't clobber the
	// conventional subcommand shorthands (logs -f, rm -f, run -p), which cobra
	// would otherwise refuse to merge. Same rationale as the root flags.
	pf := group.PersistentFlags()
	pf.StringArray("file", nil, "Compose configuration files")
	pf.String("project-name", "", "Project name")
	pf.String("project-directory", "", "Alternate working directory (accepted; default is compose file dir)")
	pf.StringArray("profile", nil, "Specify a profile to enable")

	group.AddCommand(
		composeUp(), composeDown(), composePs(), composeLogs(), composeBuild(),
		composePull(), composeStart(), composeStop(), composeRestart(), composeKill(),
		composeRm(), composeConfig(), composeLs(), composeCreate(), composeExec(),
		composeRun(), composeTop(), composeImages(), composeVersion(),
		composeScale(), composeWait(), composeCp(),
	)
	return group
}

// ensureOneNetwork inspects-then-creates a backend network, honoring internal/
// labels/subnet. Returns false only on a genuine creation failure.
func ensureOneNetwork(name string, internal bool, labels map[string]string, subnet string) bool {
	if _, err := runtime.CaptureSilent("network", "inspect", name); err == nil {
		return true
	}
	cargs := []string{"network", "create"}
	if internal {
		cargs = append(cargs, "--internal")
	}
	for k, v := range labels {
		cargs = append(cargs, "--label", k+"="+v)
	}
	if subnet != "" {
		cargs = append(cargs, "--subnet", subnet)
	}
	cargs = append(cargs, name)
	if _, err := runtime.CaptureSilent(cargs...); err != nil {
		return false
	}
	return true
}

// ensureNetworks creates the implicit default network plus every declared
// (non-external) network, populates p.Nets (compose key -> backend name), and
// returns the default network name for services that declare none.
func ensureNetworks(p *compose.Project) string {
	def := p.DefaultNetwork()
	p.Nets = map[string]string{"default": def}
	if !ensureOneNetwork(def, false, nil, "") {
		fmt.Fprintf(os.Stderr, "dcon: warning: could not create project network %q; services may run without a shared network\n", def)
		p.Nets["default"] = ""
		def = ""
	}
	for key, spec := range p.Networks {
		name := p.NetworkName(key, spec)
		p.Nets[key] = name
		if spec != nil && spec.External {
			continue
		}
		internal := spec != nil && spec.Internal
		var labels map[string]string
		if spec != nil {
			labels = spec.Labels
		}
		if !ensureOneNetwork(name, internal, labels, "") {
			fmt.Fprintf(os.Stderr, "dcon: warning: could not create network %q\n", name)
		}
	}
	return def
}

// ensureNetwork is the single-network entry point used by one-off run paths.
func ensureNetwork(p *compose.Project) string {
	return ensureNetworks(p)
}

func ensureVolumes(p *compose.Project) {
	for name, spec := range p.Volumes {
		if spec != nil && spec.External {
			continue
		}
		volName := p.VolumeName(name, spec)
		if _, err := runtime.CaptureSilent("volume", "inspect", volName); err == nil {
			continue
		}
		if _, err := runtime.CaptureSilent("volume", "create", volName); err != nil {
			fmt.Fprintf(os.Stderr, "dcon: warning: could not create volume %q: %v\n", volName, err)
		}
	}
}

func composeUp() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up [OPTIONS] [SERVICE...]",
		Short: "Create and start containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			detach, _ := cmd.Flags().GetBool("detach")
			doBuild, _ := cmd.Flags().GetBool("build")
			noBuild, _ := cmd.Flags().GetBool("no-build")
			noStart, _ := cmd.Flags().GetBool("no-start")
			forceRecreate, _ := cmd.Flags().GetBool("force-recreate")
			removeOrphans, _ := cmd.Flags().GetBool("remove-orphans")
			scaleSpecs, _ := cmd.Flags().GetStringArray("scale")
			scale := parseScale(scaleSpecs)

			selected := serviceSet(args)
			active := enabledProfiles(cmd)
			net := ensureNetwork(p)
			ensureVolumes(p)

			// bringUp creates/starts every replica of one service and returns the
			// container names it brought up. Pure per-service work so independent
			// services can run concurrently.
			bringUp := func(name string) ([]string, error) {
				svc := p.Services[name]
				if svc.Hostname != "" {
					fmt.Fprintf(os.Stderr, "dcon: warning: service %q hostname is not supported by the backend and was ignored\n", name)
				}
				if svc.Build.IsSet() && !noBuild && (doBuild || svc.Image == "") {
					fmt.Printf("Building %s...\n", name)
					if err := runtime.Run(p.BuildArgs(name, svc)...); err != nil {
						return nil, fmt.Errorf("build %s: %w", name, err)
					}
				}
				count := p.Replicas(svc, scale[name])
				if count > 1 && svc.ContainerName != "" {
					return nil, fmt.Errorf("service %q has a container_name and cannot be scaled to %d", name, count)
				}
				var local []string
				for i := 1; i <= count; i++ {
					cname := p.ContainerName(name, i, svc)
					if forceRecreate {
						_, _ = runtime.CaptureSilent("delete", "--force", cname)
					} else if _, err := runtime.CaptureSilent("inspect", cname); err == nil {
						fmt.Printf("Container %s is up-to-date\n", cname)
						local = append(local, cname)
						continue
					}
					if noStart {
						rargs := dropFlag(p.RunArgs(name, svc, i, net, nil), "--detach")
						rargs[0] = "create"
						fmt.Printf("Creating %s...\n", cname)
						if err := runtime.Run(rargs...); err != nil {
							return local, err
						}
						continue
					}
					fmt.Printf("Creating %s...\n", cname)
					if err := runtime.Run(p.RunArgs(name, svc, i, net, nil)...); err != nil {
						return local, fmt.Errorf("start %s: %w", name, err)
					}
					local = append(local, cname)
				}
				return local, nil
			}

			// Bring services up one dependency level at a time; within a level
			// (no ordering constraints between members) start them concurrently,
			// capped by parallelLimit() so a wide stack can't boot dozens of
			// microVMs at once. depends_on ordering is preserved across levels.
			var started []string
			var mu sync.Mutex
			sem := make(chan struct{}, parallelLimit())
			for _, level := range p.Levels() {
				var wg sync.WaitGroup
				var levelErr error
				var errOnce sync.Once
				for _, name := range level {
					if skipService(p, name, selected, active) {
						continue
					}
					wg.Add(1)
					go func(name string) {
						defer wg.Done()
						if cap(sem) > 0 {
							sem <- struct{}{}
							defer func() { <-sem }()
						}
						names, err := bringUp(name)
						mu.Lock()
						started = append(started, names...)
						mu.Unlock()
						if err != nil {
							errOnce.Do(func() { levelErr = err })
						}
					}(name)
				}
				wg.Wait()
				if levelErr != nil {
					return levelErr
				}
			}

			if removeOrphans {
				removeOrphanContainers(p)
			}
			if detach || noStart {
				return nil
			}
			// Foreground: stream aggregated logs until interrupted, then stop.
			return followAndWait(p, started)
		},
	}
	f := cmd.Flags()
	f.BoolP("detach", "d", false, "Detached mode: run containers in the background")
	f.Bool("build", false, "Build images before starting containers")
	f.Bool("no-build", false, "Don't build an image, even if it's policy")
	f.Bool("no-start", false, "Don't start the services after creating them")
	f.Bool("force-recreate", false, "Recreate containers even if their configuration hasn't changed")
	f.Bool("remove-orphans", false, "Remove containers for services not defined in the compose file")
	f.StringArray("scale", nil, "Scale SERVICE to NUM instances (format: service=num)")
	f.String("pull", "", "Pull image before running (always|missing|never)")
	f.IntP("timeout", "t", 10, "Use this timeout in seconds for container shutdown")
	f.Bool("wait", false, "Wait for services to be running|healthy")
	return cmd
}

// parallelLimit is the max number of services brought up concurrently within a
// dependency level. Defaults to 8 (each microVM boot is heavy); override with
// DCON_COMPOSE_PARALLEL (<=0 means unlimited).
func parallelLimit() int {
	if v := os.Getenv("DCON_COMPOSE_PARALLEL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n <= 0 {
				return 0 // unlimited
			}
			return n
		}
	}
	return 8
}

// removeOrphanContainers deletes project containers whose service is no longer
// defined in the compose file (compose up/down --remove-orphans).
func removeOrphanContainers(p *compose.Project) {
	containers, err := projectContainers(p.Name)
	if err != nil {
		return
	}
	for _, c := range containers {
		if _, ok := p.Services[serviceOf(c)]; !ok {
			fmt.Printf("Removing orphan %s...\n", c.ID)
			_, _ = runtime.CaptureSilent("delete", "--force", c.ID)
		}
	}
}

func followAndWait(p *compose.Project, names []string) error {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup
	cmds := make([]*exec.Cmd, 0, len(names))
	for _, n := range names {
		svc := n
		c := exec.Command(runtime.Bin(), "logs", "--follow", n)
		stdout, _ := c.StdoutPipe()
		c.Stderr = c.Stdout
		if err := c.Start(); err != nil {
			continue
		}
		cmds = append(cmds, c)
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 1024*1024), 1024*1024)
			for sc.Scan() {
				fmt.Printf("%s | %s\n", svc, sc.Text())
			}
		}()
	}

	fmt.Println("Attached to project; press Ctrl-C to stop.")
	<-sigc
	fmt.Println("\nGracefully stopping...")
	for _, n := range names {
		_, _ = runtime.CaptureSilent("stop", n)
	}
	for _, c := range cmds {
		_ = c.Process.Kill()
	}
	wg.Wait()
	return nil
}

func composeDown() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down [OPTIONS]",
		Short: "Stop and remove containers, networks",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			rmVolumes, _ := cmd.Flags().GetBool("volumes")
			containers, err := projectContainers(p.Name)
			if err != nil {
				return err
			}
			for _, c := range containers {
				fmt.Printf("Removing %s...\n", c.ID)
				_, _ = runtime.CaptureSilent("delete", "--force", c.ID)
			}
			// Remove the default network plus any declared (non-external) ones.
			netNames := []string{p.DefaultNetwork()}
			for key, spec := range p.Networks {
				if spec != nil && spec.External {
					continue
				}
				netNames = append(netNames, p.NetworkName(key, spec))
			}
			for _, net := range netNames {
				if _, err := runtime.CaptureSilent("network", "inspect", net); err == nil {
					fmt.Printf("Removing network %s...\n", net)
					_, _ = runtime.CaptureSilent("network", "delete", net)
				}
			}
			if rmVolumes {
				for name, spec := range p.Volumes {
					if spec != nil && spec.External {
						continue
					}
					vol := p.VolumeName(name, spec)
					fmt.Printf("Removing volume %s...\n", vol)
					_, _ = runtime.CaptureSilent("volume", "delete", vol)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolP("volumes", "v", false, "Remove named volumes declared in the volumes section")
	cmd.Flags().String("rmi", "", "Remove images used by services (all|local)")
	cmd.Flags().Bool("remove-orphans", false, "Remove containers for services not defined in the compose file")
	cmd.Flags().IntP("timeout", "t", 10, "Shutdown timeout in seconds")
	return cmd
}

type composePsView struct {
	Name    string
	Image   string
	Command string
	Service string
	Created string
	Status  string
	Ports   string
	State   string
}

func composePs() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ps [OPTIONS] [SERVICE...]",
		Short: "List containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			quiet, _ := cmd.Flags().GetBool("quiet")
			all, _ := cmd.Flags().GetBool("all")
			servicesOnly, _ := cmd.Flags().GetBool("services")
			format, _ := cmd.Flags().GetString("format")
			containers, err := projectContainers(p.Name)
			if err != nil {
				return err
			}
			selected := serviceSet(args)
			if servicesOnly {
				seen := map[string]bool{}
				for _, c := range containers {
					s := serviceOf(c)
					if !seen[s] {
						seen[s] = true
						fmt.Println(s)
					}
				}
				return nil
			}
			views := make([]any, 0, len(containers))
			for _, c := range containers {
				if selected != nil && !selected[serviceOf(c)] {
					continue
				}
				if !all && c.Status.State != "running" {
					continue
				}
				cmdParts := append([]string{c.Configuration.InitProcess.Executable}, c.Configuration.InitProcess.Arguments...)
				views = append(views, composePsView{
					Name:    c.ID,
					Image:   dockerfmt.ShortImage(c.Configuration.Image.Reference),
					Command: dockerfmt.TruncCommand(cmdParts, false),
					Service: serviceOf(c),
					Created: dockerfmt.RelativeAgo(c.Configuration.CreationDate),
					Status:  statusString(c),
					Ports:   portsString(c),
					State:   c.Status.State,
				})
			}
			def := dockerfmt.TableDef{
				Headers: []string{"NAME", "IMAGE", "COMMAND", "SERVICE", "CREATED", "STATUS", "PORTS"},
				Row: func(v any) []string {
					p := v.(composePsView)
					return []string{p.Name, p.Image, p.Command, p.Service, p.Created, p.Status, p.Ports}
				},
				ID: func(v any) string { return v.(composePsView).Name },
			}
			return dockerfmt.Render(format, quiet, views, def)
		},
	}
	cmd.Flags().BoolP("quiet", "q", false, "Only display IDs")
	cmd.Flags().BoolP("all", "a", false, "Show all stopped containers")
	cmd.Flags().Bool("services", false, "Display services")
	cmd.Flags().String("format", "", "Format output using a Go template or 'json'")
	return cmd
}

func composeLogs() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [OPTIONS] [SERVICE...]",
		Short: "View output from containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			follow, _ := cmd.Flags().GetBool("follow")
			noPrefix, _ := cmd.Flags().GetBool("no-log-prefix")
			tail := composeLogsTail(cmd)
			// Flags Docker honors but the container backend cannot: warn once,
			// like the top-level `dcon logs`, rather than ignoring them silently.
			for _, flag := range []string{"since", "until", "timestamps"} {
				if cmd.Flags().Changed(flag) {
					fmt.Fprintf(os.Stderr, "dcon: warning: --%s is not supported by the container backend and was ignored\n", flag)
				}
			}
			containers, err := projectContainers(p.Name)
			if err != nil {
				return err
			}
			selected := serviceSet(args)
			var targets []dockerfmt.Container
			for _, c := range containers {
				if selected == nil || selected[serviceOf(c)] {
					targets = append(targets, c)
				}
			}
			if follow {
				return followLogs(targets, tail, noPrefix)
			}
			for _, c := range targets {
				cargs := []string{"logs"}
				if tail != "" {
					cargs = append(cargs, "-n", tail)
				}
				cargs = append(cargs, c.ID)
				out, _ := runtime.CaptureSilent(cargs...)
				trimmed := strings.TrimRight(out, "\n")
				if trimmed == "" {
					continue
				}
				for _, line := range strings.Split(trimmed, "\n") {
					printLogLine(serviceOf(c), line, noPrefix)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "Follow log output")
	cmd.Flags().String("tail", "all", "Number of lines to show from the end of the logs")
	cmd.Flags().Bool("no-log-prefix", false, "Don't print the service-name prefix on each line")
	cmd.Flags().BoolP("timestamps", "t", false, "Show timestamps (unsupported by backend)")
	cmd.Flags().String("since", "", "Show logs since timestamp (unsupported by backend)")
	cmd.Flags().String("until", "", "Show logs before timestamp (unsupported by backend)")
	return cmd
}

// composeLogsTail returns the validated --tail value to pass to `container
// logs -n`, or "" for the Docker default ("all" = everything).
func composeLogsTail(cmd *cobra.Command) string {
	t, _ := cmd.Flags().GetString("tail")
	if t == "" || t == "all" {
		return ""
	}
	if _, err := strconv.Atoi(t); err != nil {
		return ""
	}
	return t
}

// printLogLine writes one aggregated log line, with the Docker-style
// "service | …" prefix unless --no-log-prefix was given.
func printLogLine(svc, line string, noPrefix bool) {
	if noPrefix {
		fmt.Println(line)
	} else {
		fmt.Printf("%s | %s\n", svc, line)
	}
}

// followLogs streams `container logs --follow` for each target concurrently,
// honoring --tail (seed the last N lines) and --no-log-prefix, until interrupted.
func followLogs(targets []dockerfmt.Container, tail string, noPrefix bool) error {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	var wg sync.WaitGroup
	for _, c := range targets {
		svc := serviceOf(c)
		cargs := []string{"logs", "--follow"}
		if tail != "" {
			cargs = append(cargs, "-n", tail)
		}
		cargs = append(cargs, c.ID)
		cc := exec.Command(runtime.Bin(), cargs...)
		stdout, _ := cc.StdoutPipe()
		cc.Stderr = cc.Stdout
		if err := cc.Start(); err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 1024*1024), 1024*1024)
			for sc.Scan() {
				printLogLine(svc, sc.Text(), noPrefix)
			}
		}()
	}
	<-sigc
	return nil
}

func composeBuild() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [SERVICE...]",
		Short: "Build or rebuild services",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			selected := serviceSet(args)
			for _, name := range p.Order() {
				if selected != nil && !selected[name] {
					continue
				}
				svc := p.Services[name]
				if !svc.Build.IsSet() {
					continue
				}
				fmt.Printf("Building %s...\n", name)
				if err := runtime.Run(p.BuildArgs(name, svc)...); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("no-cache", false, "Do not use cache when building the image")
	cmd.Flags().Bool("pull", false, "Always attempt to pull a newer version of the image")
	return cmd
}

func composePull() *cobra.Command {
	return &cobra.Command{
		Use:   "pull [SERVICE...]",
		Short: "Pull service images",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			selected := serviceSet(args)
			for name, svc := range p.Services {
				if selected != nil && !selected[name] {
					continue
				}
				if svc.Image == "" {
					continue
				}
				fmt.Printf("Pulling %s (%s)...\n", name, svc.Image)
				if err := runtime.Run("image", "pull", svc.Image); err != nil {
					fmt.Fprintf(os.Stderr, "dcon: warning: pull %s failed: %v\n", svc.Image, err)
				}
			}
			return nil
		},
	}
}

// lifecycleOnProject applies a simple verb to all (or selected) project
// containers.
func lifecycleOnProject(verb string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		p, err := loadProject(cmd)
		if err != nil {
			return err
		}
		containers, err := projectContainers(p.Name)
		if err != nil {
			return err
		}
		selected := serviceSet(args)
		for _, c := range containers {
			if selected != nil && !selected[serviceOf(c)] {
				continue
			}
			switch verb {
			case "rm":
				_, _ = runtime.CaptureSilent("delete", "--force", c.ID)
			case "stop":
				_, _ = runtime.CaptureSilent("stop", c.ID)
			case "start":
				_, _ = runtime.CaptureSilent("start", c.ID)
			case "kill":
				_, _ = runtime.CaptureSilent("kill", c.ID)
			case "restart":
				_, _ = runtime.CaptureSilent("stop", c.ID)
				_, _ = runtime.CaptureSilent("start", c.ID)
			}
			fmt.Println(c.ID)
		}
		return nil
	}
}

func composeStart() *cobra.Command {
	return &cobra.Command{Use: "start [SERVICE...]", Short: "Start services", RunE: lifecycleOnProject("start")}
}
func composeStop() *cobra.Command {
	c := &cobra.Command{Use: "stop [SERVICE...]", Short: "Stop services", RunE: lifecycleOnProject("stop")}
	c.Flags().IntP("timeout", "t", 10, "Shutdown timeout in seconds")
	return c
}
func composeRestart() *cobra.Command {
	c := &cobra.Command{Use: "restart [SERVICE...]", Short: "Restart service containers", RunE: lifecycleOnProject("restart")}
	c.Flags().IntP("timeout", "t", 10, "Shutdown timeout in seconds")
	return c
}
func composeKill() *cobra.Command {
	c := &cobra.Command{Use: "kill [SERVICE...]", Short: "Force stop service containers", RunE: lifecycleOnProject("kill")}
	c.Flags().StringP("signal", "s", "SIGKILL", "SIGNAL to send to the container")
	return c
}
func composeRm() *cobra.Command {
	c := &cobra.Command{Use: "rm [SERVICE...]", Short: "Remove stopped service containers", RunE: lifecycleOnProject("rm")}
	c.Flags().BoolP("force", "f", false, "Don't ask to confirm removal")
	c.Flags().BoolP("stop", "s", false, "Stop the containers, if required, before removing")
	c.Flags().BoolP("volumes", "v", false, "Remove any anonymous volumes attached to containers")
	return c
}

func composeConfig() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config [OPTIONS]",
		Aliases: []string{"convert"},
		Short:   "Parse, resolve and render compose file in canonical format",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			if v, _ := cmd.Flags().GetBool("services"); v {
				names := make([]string, 0, len(p.Services))
				for n := range p.Services {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					fmt.Println(n)
				}
				return nil
			}
			if v, _ := cmd.Flags().GetBool("volumes"); v {
				for n := range p.Volumes {
					fmt.Println(n)
				}
				return nil
			}
			out, err := yaml.Marshal(p)
			if err != nil {
				return err
			}
			fmt.Print(string(out))
			return nil
		},
	}
	cmd.Flags().Bool("services", false, "Print the service names, one per line")
	cmd.Flags().Bool("volumes", false, "Print the volume names, one per line")
	cmd.Flags().BoolP("quiet", "q", false, "Only validate the configuration, don't print anything")
	return cmd
}

func composeLs() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls [OPTIONS]",
		Short: "List running compose projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			all, err := getContainers(true)
			if err != nil {
				return err
			}
			projects := map[string]map[string]int{} // project -> state -> count
			configDir := map[string]string{}
			for _, c := range all {
				proj := c.Configuration.Labels[compose.LabelProject]
				if proj == "" {
					continue
				}
				if projects[proj] == nil {
					projects[proj] = map[string]int{}
				}
				projects[proj][c.Status.State]++
				if d := c.Configuration.Labels[compose.LabelConfigDir]; d != "" {
					configDir[proj] = d
				}
			}
			w := dockerfmt.NewTabWriter()
			fmt.Fprintln(w, "NAME\tSTATUS\tCONFIG FILES")
			names := make([]string, 0, len(projects))
			for n := range projects {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				running := projects[n]["running"]
				total := 0
				for _, c := range projects[n] {
					total += c
				}
				status := fmt.Sprintf("running(%d)", running)
				if running < total {
					status += fmt.Sprintf(" exited(%d)", total-running)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", n, status, configDir[n])
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolP("all", "a", false, "Show all stopped projects")
	cmd.Flags().BoolP("quiet", "q", false, "Only display project names")
	return cmd
}

func composeCreate() *cobra.Command {
	return &cobra.Command{
		Use:   "create [SERVICE...]",
		Short: "Create containers for a service (without starting them)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			selected := serviceSet(args)
			active := enabledProfiles(cmd)
			net := ensureNetwork(p)
			ensureVolumes(p)
			for _, name := range p.Order() {
				if skipService(p, name, selected, active) {
					continue
				}
				svc := p.Services[name]
				if svc.Build.IsSet() && svc.Image == "" {
					if err := runtime.Run(p.BuildArgs(name, svc)...); err != nil {
						return err
					}
				}
				rargs := dropFlag(p.RunArgs(name, svc, 1, net, nil), "--detach")
				rargs[0] = "create"
				cname := p.ContainerName(name, 1, svc)
				fmt.Printf("Creating %s...\n", cname)
				if err := runtime.Run(rargs...); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func composeExec() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "exec [OPTIONS] SERVICE COMMAND [ARGS...]",
		Short:              "Execute a command in a running container",
		Args:               cobra.MinimumNArgs(2),
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			svc := args[0]
			rest := args[1:]
			idx, _ := cmd.Flags().GetInt("index")
			c, err := serviceContainerByIndex(p.Name, svc, idx)
			if err != nil {
				return err
			}
			cargs := []string{"exec"}
			if v, _ := cmd.Flags().GetBool("detach"); v {
				cargs = append(cargs, "--detach")
			}
			if v, _ := cmd.Flags().GetBool("interactive"); v && isTerminal(os.Stdin) {
				cargs = append(cargs, "--interactive")
			}
			// Only allocate a PTY when one actually exists (docker behaviour),
			// so `compose exec svc cmd | grep ...` works.
			if v, _ := cmd.Flags().GetBool("tty"); v && haveTTY() {
				cargs = append(cargs, "--tty")
			}
			if u, _ := cmd.Flags().GetString("user"); u != "" {
				cargs = append(cargs, "--user", u)
			}
			if w, _ := cmd.Flags().GetString("workdir"); w != "" {
				cargs = append(cargs, "--workdir", w)
			}
			for _, e := range mustStringArray(cmd.Flags(), "env") {
				cargs = append(cargs, "--env", e)
			}
			cargs = append(cargs, c)
			cargs = append(cargs, rest...)
			return runtime.Run(cargs...)
		},
	}
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolP("interactive", "i", true, "Keep STDIN open")
	cmd.Flags().BoolP("tty", "t", true, "Allocate a pseudo-TTY")
	cmd.Flags().BoolP("detach", "d", false, "Detached mode")
	cmd.Flags().StringP("user", "u", "", "Run the command as this user")
	cmd.Flags().StringP("workdir", "w", "", "Path to workdir directory")
	cmd.Flags().StringArrayP("env", "e", nil, "Set environment variables")
	cmd.Flags().Int("index", 1, "Index of the container if service has multiple replicas")
	return cmd
}

// serviceContainerByIndex resolves a project service's container by replica
// index (1-based), preferring a running one.
func serviceContainerByIndex(project, service string, idx int) (string, error) {
	containers, err := projectContainers(project)
	if err != nil {
		return "", err
	}
	want := strconv.Itoa(idx)
	var fallback string
	for _, c := range containers {
		if serviceOf(c) != service || c.Configuration.Labels[compose.LabelNumber] != want {
			continue
		}
		if c.Status.State == "running" {
			return c.ID, nil
		}
		fallback = c.ID
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("no container for service %q (index %d)", service, idx)
}

func composeRun() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [OPTIONS] SERVICE [COMMAND] [ARGS...]",
		Short: "Run a one-off command on a service",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			svcName := args[0]
			svc, ok := p.Services[svcName]
			if !ok {
				return fmt.Errorf("no such service: %s", svcName)
			}
			net := ensureNetwork(p)

			// Build CLI override flag tokens, inserted before the image.
			var overrides []string
			addEach := func(flag string, vals []string) {
				for _, v := range vals {
					overrides = append(overrides, flag, v)
				}
			}
			addEach("--env", mustStringArray(cmd.Flags(), "env"))
			addEach("--volume", mustStringArray(cmd.Flags(), "volume"))
			addEach("--publish", mustStringArray(cmd.Flags(), "publish"))
			addEach("--label", mustStringArray(cmd.Flags(), "label"))
			if name, _ := cmd.Flags().GetString("name"); name != "" {
				overrides = append(overrides, "--name", name)
			}
			if w, _ := cmd.Flags().GetString("workdir"); w != "" {
				overrides = append(overrides, "--workdir", w)
			}
			if u, _ := cmd.Flags().GetString("user"); u != "" {
				overrides = append(overrides, "--user", u)
			}
			entrypoint, _ := cmd.Flags().GetString("entrypoint")

			run := p.OneOffArgs(svcName, svc, net, args[1:], overrides, entrypoint)
			if rm, _ := cmd.Flags().GetBool("rm"); !rm {
				run = dropFlag(run, "--rm")
			}
			if d, _ := cmd.Flags().GetBool("detach"); d {
				run = append([]string{run[0], "--detach"}, run[1:]...)
			}
			return runtime.Run(run...)
		},
	}
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().Bool("rm", true, "Remove container after run")
	cmd.Flags().BoolP("detach", "d", false, "Run container in background")
	cmd.Flags().StringArrayP("env", "e", nil, "Set environment variables")
	cmd.Flags().StringArrayP("volume", "v", nil, "Bind mount a volume")
	cmd.Flags().StringArrayP("publish", "p", nil, "Publish a container's port(s) to the host")
	cmd.Flags().StringArrayP("label", "l", nil, "Add or override a label")
	cmd.Flags().String("name", "", "Assign a name to the container")
	cmd.Flags().StringP("workdir", "w", "", "Working directory inside the container")
	cmd.Flags().StringP("user", "u", "", "Username or UID")
	cmd.Flags().String("entrypoint", "", "Override the entrypoint of the image")
	return cmd
}

// parseScale turns ["web=3","db=2"] into {web:3, db:2}.
func parseScale(specs []string) map[string]int {
	out := map[string]int{}
	for _, s := range specs {
		kv := strings.SplitN(s, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if n, err := strconv.Atoi(kv[1]); err == nil {
			out[kv[0]] = n
		}
	}
	return out
}

func composeTop() *cobra.Command {
	return &cobra.Command{
		Use:   "top [SERVICE...]",
		Short: "Display the running processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			containers, err := projectContainers(p.Name)
			if err != nil {
				return err
			}
			selected := serviceSet(args)
			for _, c := range containers {
				if c.Status.State != "running" {
					continue
				}
				if selected != nil && !selected[serviceOf(c)] {
					continue
				}
				fmt.Printf("%s\n", c.ID)
				_ = runtime.Run("exec", c.ID, "ps", "-ef")
			}
			return nil
		},
	}
}

func composeImages() *cobra.Command {
	return &cobra.Command{
		Use:   "images [SERVICE...]",
		Short: "List images used by the created containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			containers, err := projectContainers(p.Name)
			if err != nil {
				return err
			}
			w := dockerfmt.NewTabWriter()
			fmt.Fprintln(w, "CONTAINER\tREPOSITORY\tTAG\tSIZE")
			for _, c := range containers {
				repo, tag := dockerfmt.SplitRepoTag(dockerfmt.ShortImage(c.Configuration.Image.Reference))
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.ID, repo, tag, "N/A")
			}
			return w.Flush()
		},
	}
}

func composeVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the Docker Compose version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("dcon compose version v2 (Apple container backend)\n")
			return nil
		},
	}
}

// composeScale scales services to N replicas: `compose scale web=3 db=2`.
func composeScale() *cobra.Command {
	return &cobra.Command{
		Use:   "scale [SERVICE=NUM...]",
		Short: "Scale services to a number of instances",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			net := ensureNetwork(p)
			for svcName, n := range parseScale(args) {
				svc, ok := p.Services[svcName]
				if !ok {
					return fmt.Errorf("no such service: %s", svcName)
				}
				if n > 1 && svc.ContainerName != "" {
					return fmt.Errorf("service %q has a container_name and cannot be scaled", svcName)
				}
				// Start/create replicas 1..n.
				for i := 1; i <= n; i++ {
					cname := p.ContainerName(svcName, i, svc)
					if _, err := runtime.CaptureSilent("inspect", cname); err == nil {
						continue
					}
					fmt.Printf("Creating %s...\n", cname)
					if err := runtime.Run(p.RunArgs(svcName, svc, i, net, nil)...); err != nil {
						return err
					}
				}
				// Remove surplus replicas (index > n).
				containers, err := projectContainers(p.Name)
				if err != nil {
					return err
				}
				for _, c := range containers {
					if serviceOf(c) != svcName {
						continue
					}
					if num, _ := strconv.Atoi(c.Configuration.Labels[compose.LabelNumber]); num > n {
						fmt.Printf("Removing %s...\n", c.ID)
						_, _ = runtime.CaptureSilent("delete", "--force", c.ID)
					}
				}
			}
			return nil
		},
	}
}

// composeWait blocks until project containers stop, then prints exit codes.
func composeWait() *cobra.Command {
	return &cobra.Command{
		Use:   "wait [SERVICE...]",
		Short: "Block until containers stop, then print exit codes",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			containers, err := projectContainers(p.Name)
			if err != nil {
				return err
			}
			selected := serviceSet(args)
			for _, c := range containers {
				if selected != nil && !selected[serviceOf(c)] {
					continue
				}
				for {
					var list []dockerfmt.Container
					if err := runtime.CaptureJSON(&list, "inspect", c.ID); err != nil {
						break
					}
					st := ""
					if len(list) > 0 {
						st = list[0].Status.State
					}
					if st != "running" && st != "stopping" {
						fmt.Println("0") // backend does not expose exit codes
						break
					}
					time.Sleep(500 * time.Millisecond)
				}
			}
			return nil
		},
	}
}

// composeCp copies files between a service container and the local filesystem.
func composeCp() *cobra.Command {
	return &cobra.Command{
		Use:   "cp SRC DST",
		Short: "Copy files/folders between a service container and the local filesystem",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			rewrite := func(arg string) (string, bool, error) {
				i := strings.IndexByte(arg, ':')
				if i <= 0 {
					return arg, false, nil
				}
				svcName := arg[:i]
				if _, ok := p.Services[svcName]; !ok {
					return arg, false, nil // not a service ref (e.g. a local path)
				}
				cid, err := firstServiceContainer(p.Name, svcName)
				if err != nil {
					return "", false, err
				}
				return cid + ":" + arg[i+1:], true, nil
			}
			src, srcCtr, err := rewrite(args[0])
			if err != nil {
				return err
			}
			dst, dstCtr, err := rewrite(args[1])
			if err != nil {
				return err
			}
			if srcCtr == dstCtr {
				return fmt.Errorf("exactly one of SRC or DST must be SERVICE:PATH")
			}
			return runtime.Run("copy", src, dst)
		},
	}
}

// --- helpers ---

func serviceSet(args []string) map[string]bool {
	if len(args) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, a := range args {
		m[a] = true
	}
	return m
}

func firstServiceContainer(project, service string) (string, error) {
	containers, err := projectContainers(project)
	if err != nil {
		return "", err
	}
	for _, c := range containers {
		if serviceOf(c) == service && c.Status.State == "running" {
			return c.ID, nil
		}
	}
	// fall back to any state
	for _, c := range containers {
		if serviceOf(c) == service {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no running container for service %q", service)
}

func dropFlag(args []string, flag string) []string {
	out := args[:0:0]
	for _, a := range args {
		if a == flag {
			continue
		}
		out = append(out, a)
	}
	return out
}
