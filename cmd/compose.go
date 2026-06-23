package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"

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

func newComposeCmd() *cobra.Command {
	group := &cobra.Command{
		Use:     "compose",
		Short:   "Define and run multi-container applications with a compose file",
		Aliases: []string{},
	}
	pf := group.PersistentFlags()
	pf.StringArrayP("file", "f", nil, "Compose configuration files")
	pf.StringP("project-name", "p", "", "Project name")
	pf.String("project-directory", "", "Alternate working directory (accepted; default is compose file dir)")
	pf.StringArray("profile", nil, "Specify a profile to enable")

	group.AddCommand(
		composeUp(), composeDown(), composePs(), composeLogs(), composeBuild(),
		composePull(), composeStart(), composeStop(), composeRestart(), composeKill(),
		composeRm(), composeConfig(), composeLs(), composeCreate(), composeExec(),
		composeRun(), composeTop(), composeImages(), composeVersion(),
	)
	return group
}

// ensureNetwork attempts to create the project network, returning the name to
// attach (empty if creation is unavailable).
func ensureNetwork(p *compose.Project) string {
	net := p.DefaultNetwork()
	if _, err := runtime.CaptureSilent("network", "inspect", net); err == nil {
		return net
	}
	if _, err := runtime.CaptureSilent("network", "create", net); err != nil {
		fmt.Fprintf(os.Stderr, "dcon: warning: could not create project network %q (%v); services will run without a shared network\n", net, err)
		return ""
	}
	return net
}

func ensureVolumes(p *compose.Project) {
	for name, spec := range p.Volumes {
		if spec != nil && spec.External {
			continue
		}
		volName := p.Name + "_" + name
		if spec != nil && spec.Name != "" {
			volName = spec.Name
		}
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

			selected := serviceSet(args)
			net := ensureNetwork(p)
			ensureVolumes(p)

			var started []string
			for _, name := range p.Order() {
				if selected != nil && !selected[name] {
					continue
				}
				svc := p.Services[name]
				cname := p.ContainerName(name, 1, svc)

				if svc.Build.IsSet() && !noBuild && (doBuild || svc.Image == "") {
					fmt.Printf("Building %s...\n", name)
					if err := runtime.Run(p.BuildArgs(name, svc)...); err != nil {
						return fmt.Errorf("build %s: %w", name, err)
					}
				}
				// Recreate: remove any existing container with this name.
				if forceRecreate {
					_, _ = runtime.CaptureSilent("delete", "--force", cname)
				} else if _, err := runtime.CaptureSilent("inspect", cname); err == nil {
					// already exists; leave running container as-is
					fmt.Printf("Container %s is up-to-date\n", cname)
					started = append(started, cname)
					continue
				}
				if noStart {
					rargs := p.RunArgs(name, svc, 1, net, nil)
					rargs[0] = "create" // create instead of run
					// drop --detach for create
					rargs = dropFlag(rargs, "--detach")
					fmt.Printf("Creating %s...\n", cname)
					if err := runtime.Run(rargs...); err != nil {
						return err
					}
					continue
				}
				fmt.Printf("Creating %s...\n", cname)
				if err := runtime.Run(p.RunArgs(name, svc, 1, net, nil)...); err != nil {
					return fmt.Errorf("start %s: %w", name, err)
				}
				started = append(started, cname)
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
	f.String("pull", "", "Pull image before running (always|missing|never)")
	f.IntP("timeout", "t", 10, "Use this timeout in seconds for container shutdown")
	f.Bool("wait", false, "Wait for services to be running|healthy")
	return cmd
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
			// Remove default network.
			net := p.DefaultNetwork()
			if _, err := runtime.CaptureSilent("network", "inspect", net); err == nil {
				fmt.Printf("Removing network %s...\n", net)
				_, _ = runtime.CaptureSilent("network", "delete", net)
			}
			if rmVolumes {
				for name, spec := range p.Volumes {
					if spec != nil && spec.External {
						continue
					}
					vol := p.Name + "_" + name
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
				return followAndWaitNoStop(targets)
			}
			for _, c := range targets {
				out, _ := runtime.CaptureSilent("logs", c.ID)
				for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
					fmt.Printf("%s | %s\n", serviceOf(c), line)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "Follow log output")
	cmd.Flags().String("tail", "all", "Number of lines to show from the end of the logs")
	cmd.Flags().BoolP("timestamps", "t", false, "Show timestamps")
	return cmd
}

func followAndWaitNoStop(targets []dockerfmt.Container) error {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	var wg sync.WaitGroup
	for _, c := range targets {
		svc := serviceOf(c)
		cc := exec.Command(runtime.Bin(), "logs", "--follow", c.ID)
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
				fmt.Printf("%s | %s\n", svc, sc.Text())
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
		Use:   "config [OPTIONS]",
		Short: "Parse, resolve and render compose file in canonical format",
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
			net := ensureNetwork(p)
			ensureVolumes(p)
			for _, name := range p.Order() {
				if selected != nil && !selected[name] {
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
			c, err := firstServiceContainer(p.Name, svc)
			if err != nil {
				return err
			}
			cargs := []string{"exec"}
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
	return cmd
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
			run := p.OneOffArgs(svcName, svc, net, args[1:])
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
	return cmd
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
			containers, _ := projectContainers(p.Name)
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
			containers, _ := projectContainers(p.Name)
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
