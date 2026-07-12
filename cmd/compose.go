package cmd

import (
	"bufio"
	"encoding/json"
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
	"dcon/internal/ui"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// composeStep formats a compose progress line such as "Building web..." or
// "Creating db-1...", accenting the service/container name on a TTY. When
// styling is off (pipe/CI) it returns exactly "<verb> <name>..." as before.
func composeStep(verb, name string) string {
	return ui.Dim(verb+" ") + ui.Accent(name) + ui.Dim("...")
}

// composeValueFlags are the compose GLOBAL flags that consume a following
// value token; used to find the subcommand boundary without mistaking a flag's
// value for the subcommand.
var composeValueFlags = map[string]bool{
	"--file": true, "--project-name": true, "--project-directory": true,
	"--profile": true, "--env-file": true, "--parallel": true,
	"--progress": true, "--ansi": true,
}

// rewriteComposeGlobalShorthands rewrites the global `-f`/`-p` shorthands that
// appear BEFORE the compose subcommand into their long forms (--file /
// --project-name), so `dcon compose -f x.yml up` works like Docker.
//
// cobra cannot register -f/-p as persistent shorthands on the compose group:
// subcommands reuse -f (logs --follow, rm --force) and -p (run --publish), and
// pflag panics when an inherited persistent shorthand collides with a local
// one. Rewriting the leading tokens before parsing sidesteps that while leaving
// post-subcommand shorthands (the `-f` in `compose logs -f`) untouched.
// rootValueFlags are the root persistent flags (separated form) that consume the
// following token as their value, so composeIndex skips that value when locating
// the compose subcommand.
var rootValueFlags = map[string]bool{
	"-H": true, "--host": true, "--context": true, "--log-level": true,
	"--config": true, "--tlscacert": true, "--tlscert": true, "--tlskey": true,
}

// composeIndex returns the index of the `compose` subcommand token, skipping any
// root persistent flags (and their separated values) that precede it — so
// `dcon -D compose ...` / `dcon --host x compose ...` are still recognized. It
// returns -1 when the invocation is not a compose command.
func composeIndex(args []string) int {
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "compose" {
			return i
		}
		if strings.HasPrefix(a, "-") {
			if rootValueFlags[a] {
				i += 2 // skip the flag and its value
			} else {
				i++ // bool flag, --flag=value, or -Hvalue (self-contained)
			}
			continue
		}
		return -1 // a non-flag token that isn't compose => a different subcommand
	}
	return -1
}

func rewriteComposeGlobalShorthands(args []string) []string {
	ci := composeIndex(args)
	if ci < 0 {
		return args
	}
	// Preserve any root flags before `compose`, then rewrite the leading
	// -f/-p shorthands that sit between `compose` and its subcommand.
	out := append([]string{}, args[:ci+1]...)
	i := ci + 1
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			// First bare token is the subcommand; stop rewriting from here.
			return append(out, args[i:]...)
		}
		switch {
		case a == "-f":
			out = append(out, "--file")
			a = "--file" // normalize so the value-consume check below fires
		case a == "-p":
			out = append(out, "--project-name")
			a = "--project-name"
		case strings.HasPrefix(a, "-f") && !strings.HasPrefix(a, "--"):
			out = append(out, "--file", strings.TrimPrefix(strings.TrimPrefix(a, "-f"), "="))
			i++
			continue
		case strings.HasPrefix(a, "-p") && !strings.HasPrefix(a, "--"):
			out = append(out, "--project-name", strings.TrimPrefix(strings.TrimPrefix(a, "-p"), "="))
			i++
			continue
		default:
			out = append(out, a)
		}
		name := a
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			name = a[:eq]
		}
		if composeValueFlags[name] && !strings.Contains(a, "=") && i+1 < len(args) {
			out = append(out, args[i+1])
			i += 2
			continue
		}
		i++
	}
	return out
}

func loadProject(cmd *cobra.Command) (*compose.Project, error) {
	files, _ := cmd.Flags().GetStringArray("file")
	project, _ := cmd.Flags().GetString("project-name")
	envFiles, _ := cmd.Flags().GetStringArray("env-file")
	// FindFiles resolves -f paths, else COMPOSE_FILE (split on
	// COMPOSE_PATH_SEPARATOR), else the conventional filenames plus an
	// auto-loaded compose.override.yaml; LoadFiles merges them (later files
	// override) and threads the --env-file / .env interpolation variables.
	paths, err := compose.FindFiles(files)
	if err != nil {
		return nil, err
	}
	return compose.LoadFiles(paths, project, envFiles)
}

// withDeps expands a `SERVICE...` selection to its transitive depends_on
// closure, so `up web` also starts what web needs (docker behavior). A nil
// selection (= all services) passes through; unknown names are kept so the
// existing "silently no-op" behavior for typos is unchanged.
func withDeps(p *compose.Project, selected map[string]bool) map[string]bool {
	if selected == nil {
		return nil
	}
	out := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if out[name] {
			return
		}
		out[name] = true
		svc, ok := p.Services[name]
		if !ok {
			return
		}
		for _, d := range svc.DependsOn {
			visit(d)
		}
	}
	for name := range selected {
		visit(name)
	}
	return out
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
	// Docker Compose global flags. Accepted so `docker compose --progress plain
	// up`-style invocations don't hard-fail; dcon's output styling is fixed.
	pf.StringArray("env-file", nil, "Environment file(s) for interpolation (replaces the default .env)")
	pf.Int("parallel", -1, "Max parallelism, -1 for unlimited (accepted; see DCON_COMPOSE_PARALLEL)")
	pf.Bool("compatibility", false, "Run in backward-compatibility mode (accepted)")
	pf.String("progress", "", "Set type of progress output (accepted)")
	pf.String("ansi", "auto", "Control when to print ANSI control characters (accepted)")
	pf.Bool("dry-run", false, "Execute in dry-run mode (accepted; no special handling)")
	pf.Bool("all-resources", false, "Include all resources, even unused ones (accepted)")

	group.AddCommand(
		composeUp(), composeDown(), composePs(), composeLogs(), composeBuild(),
		composePull(), composePush(), composeStart(), composeStop(), composeRestart(),
		composeKill(), composeRm(), composeConfig(), composeLs(), composeCreate(),
		composeExec(), composeRun(), composeTop(), composeImages(), composeVersion(),
		composeScale(), composeWait(), composeCp(), composePort(), composeAttach(),
		composePause(), composeUnpause(), composeEvents(), composeWatch(),
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
			if nc, _ := cmd.Flags().GetBool("no-color"); nc {
				ui.SetEnabled(false)
			}
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
			abortOnExit, _ := cmd.Flags().GetBool("abort-on-container-exit")
			exitCodeFrom, _ := cmd.Flags().GetString("exit-code-from")
			if exitCodeFrom != "" {
				abortOnExit = true // docker: --exit-code-from implies --abort-on-container-exit
				fmt.Fprintln(os.Stderr, "dcon: warning: --exit-code-from is best-effort: the backend exposes no container exit codes")
			}
			if cmd.Flags().Changed("abort-on-container-failure") {
				fmt.Fprintln(os.Stderr, "dcon: warning: --abort-on-container-failure is not supported (the backend exposes no exit codes); use --abort-on-container-exit")
			}

			selected := serviceSet(args)
			// `up SERVICE` starts the selected services AND their transitive
			// depends_on closure (docker behavior) unless --no-deps.
			if noDeps, _ := cmd.Flags().GetBool("no-deps"); !noDeps {
				selected = withDeps(p, selected)
			}
			active := enabledProfiles(cmd)
			net := ensureNetwork(p)
			ensureVolumes(p)

			// --pull always: force-refresh each selected service's explicit image
			// before starting (missing/never rely on the backend's on-demand pull).
			// Done once per image, off the per-replica bringUp path.
			if pull, _ := cmd.Flags().GetString("pull"); pull == "always" {
				pulled := map[string]bool{}
				for _, name := range p.Order() {
					if skipService(p, name, selected, active) {
						continue
					}
					img := p.Services[name].Image
					if img == "" || pulled[img] {
						continue // built (no explicit image:) or already pulled
					}
					pulled[img] = true
					fmt.Println(composeStep("Pulling", name))
					if _, err := runtime.CaptureSilent("image", "pull", img); err != nil {
						fmt.Fprintf(os.Stderr, "dcon: warning: pull %s failed: %v\n", img, err)
					}
				}
			}

			// bringUp creates/starts every replica of one service and returns the
			// container names it brought up. existing is the per-level backend
			// snapshot (one `ls --all` for the whole level instead of one inspect
			// per replica), read-only here so concurrent services can share it.
			bringUp := func(name string, existing map[string]dockerfmt.Container) ([]string, error) {
				svc := p.Services[name]
				if svc.Hostname != "" {
					fmt.Fprintf(os.Stderr, "dcon: warning: service %q hostname is not supported by the backend and was ignored\n", name)
				}
				if svc.Restart != "" && svc.Restart != "no" {
					fmt.Fprintf(os.Stderr, "dcon: warning: service %q restart policy %q is not supported by the backend and was ignored\n", name, svc.Restart)
				}
				if svc.StdinOpen {
					fmt.Fprintf(os.Stderr, "dcon: warning: service %q stdin_open is not supported for detached services and was ignored\n", name)
				}
				if svc.Build.IsSet() && !noBuild && (doBuild || svc.Image == "") {
					fmt.Println(composeStep("Building", name))
					if err := runtime.Run(p.BuildArgs(name, svc)...); err != nil {
						return nil, fmt.Errorf("build %s: %w", name, err)
					}
				}
				count := effectiveReplicas(svc, scale, name)
				if count > 1 && svc.ContainerName != "" {
					return nil, fmt.Errorf("service %q has a container_name and cannot be scaled to %d", name, count)
				}
				var local []string
				for i := 1; i <= count; i++ {
					cname := p.ContainerName(name, i, svc)
					rargs := p.RunArgs(name, svc, i, net, nil)
					c, exists := existing[cname]
					if exists && forceRecreate {
						_, _ = runtime.CaptureSilent("delete", "--force", cname)
						exists = false
					}
					if exists {
						// Recreate when the stored config hash differs from the
						// one the current compose file generates (docker's
						// config-hash pattern). A container without the label
						// (pre-hash dcon, or hand-made) is left alone.
						stored := c.Configuration.Labels[compose.LabelConfigHash]
						if stored != "" && stored != compose.ConfigHashFromArgs(rargs) {
							fmt.Println(composeStep("Recreating", cname))
							_, _ = runtime.CaptureSilent("delete", "--force", cname)
						} else {
							// docker `compose up`: a running container with unchanged
							// config is left as-is ("up-to-date"); an existing but
							// STOPPED one is started (not skipped), so stop→up /
							// post-reboot up don't silently leave services down.
							fmt.Println(ui.Dim("Container ") + ui.Accent(cname) + ui.Dim(" is up-to-date"))
							if !noStart && c.Status.State != "running" {
								fmt.Println(composeStep("Starting", cname))
								if err := runtime.Run("start", cname); err != nil {
									return local, fmt.Errorf("start %s: %w", name, err)
								}
							}
							local = append(local, cname)
							continue
						}
					}
					if noStart {
						cargs := p.CreateArgs(name, svc, i, net)
						fmt.Println(composeStep("Creating", cname))
						if err := runtime.Run(cargs...); err != nil {
							return local, err
						}
						continue
					}
					fmt.Println(composeStep("Creating", cname))
					if err := runtime.Run(rargs...); err != nil {
						return local, fmt.Errorf("start %s: %w", name, err)
					}
					local = append(local, cname)
				}
				// Reconcile down: remove surplus replicas (index > count) so a
				// down-scale via `up --scale N` (or a plain up after a prior larger
				// scale) converges to exactly count, matching docker. composeScale
				// already does this; up must too. One-offs are never touched.
				if existing, err := projectContainers(p.Name); err == nil {
					for _, c := range existing {
						if serviceOf(c) != name || c.Configuration.Labels[compose.LabelOneoff] == "True" {
							continue
						}
						if num, _ := strconv.Atoi(c.Configuration.Labels[compose.LabelNumber]); num > count {
							fmt.Println(composeStep("Removing", c.ID))
							_, _ = runtime.CaptureSilent("delete", "--force", c.ID)
						}
					}
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
				// One backend snapshot per level: existence, run state, and the
				// config hash all come from this single `ls --all`, replacing
				// the previous one-inspect-per-replica pattern.
				existing := map[string]dockerfmt.Container{}
				if cs, err := projectContainers(p.Name); err == nil {
					for _, c := range cs {
						existing[c.ID] = c
					}
				}
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
						names, err := bringUp(name, existing)
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
			// len(started)==0 also short-circuits: effectiveReplicas can legally
			// return 0 (e.g. `up --scale web=0`), and followAndWait would otherwise
			// print the attach banner and block on the signal channel forever.
			// --wait implies detached mode in docker compose: bring services up,
			// (wait for running/healthy), then RETURN — never stream logs. The
			// backend has no healthcheck mechanism and `container run` returns once
			// the container is running, so the services are already up here; just
			// return instead of falling through to followAndWait (which would hang).
			wait, _ := cmd.Flags().GetBool("wait")
			if detach || noStart || wait || len(started) == 0 {
				return nil
			}
			// Foreground: stream aggregated logs until interrupted (or, with
			// --abort-on-container-exit, until any container stops), then stop.
			noPrefix, _ := cmd.Flags().GetBool("no-log-prefix")
			timeout, _ := cmd.Flags().GetInt("timeout")
			return followAndWait(p, started, noPrefix, timeout, abortOnExit, exitCodeFrom)
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
	// Additional Docker Compose `up` flags. Registered so real-world invocations
	// don't hard-fail; those whose semantics the backend can't reproduce warn.
	// --no-recreate matches dcon's default for unchanged configs; --no-deps
	// disables the depends_on closure expansion of `up SERVICE...`.
	f.Bool("no-recreate", false, "If containers already exist, don't recreate them")
	f.Bool("no-deps", false, "Don't start linked services")
	f.BoolP("renew-anon-volumes", "V", false, "Recreate anonymous volumes instead of retrieving data (no-op)")
	f.Bool("quiet-pull", false, "Pull without printing progress information")
	f.Bool("no-color", false, "Produce monochrome output")
	f.Bool("no-log-prefix", false, "Don't print prefix in logs")
	f.Bool("always-recreate-deps", false, "Recreate dependent containers (no-op)")
	f.Bool("recreate-deps", false, "Recreate dependent containers (no-op)")
	f.Bool("abort-on-container-exit", false, "Stop all containers if any container stopped")
	f.Bool("abort-on-container-failure", false, "Stop all containers if any container exited with failure")
	f.String("exit-code-from", "", "Return the exit code of the selected service container")
	f.StringArray("attach", nil, "Restrict attaching to the specified services")
	f.StringArray("no-attach", nil, "Do not attach (stream logs) to the specified services")
	f.Bool("attach-dependencies", false, "Automatically attach to log output of dependent services (no-op)")
	f.Int("wait-timeout", 0, "Maximum duration in seconds to wait for the project to be running|healthy")
	f.BoolP("yes", "y", false, "Assume \"yes\" as answer to all prompts (no-op)")
	f.Bool("menu", false, "Enable interactive shortcuts when running attached (no-op)")
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
			fmt.Println(composeStep("Removing orphan", c.ID))
			_, _ = runtime.CaptureSilent("delete", "--force", c.ID)
		}
	}
}

func followAndWait(p *compose.Project, names []string, noPrefix bool, timeout int, abortOnExit bool, exitCodeFrom string) error {
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
				fmt.Println(formatLogLine(svc, sc.Text(), noPrefix))
			}
		}()
	}

	// --abort-on-container-exit: poll the project until any started container
	// leaves the running state, then stop the rest (docker behavior).
	exited := make(chan struct{})
	if abortOnExit {
		go func() {
			for {
				time.Sleep(2 * time.Second)
				cs, err := projectContainers(p.Name)
				if err != nil {
					continue
				}
				state := map[string]string{}
				for _, c := range cs {
					state[c.ID] = c.Status.State
				}
				if anyExited(names, state) {
					close(exited)
					return
				}
			}
		}()
	}

	fmt.Println("Attached to project; press Ctrl-C to stop.")
	select {
	case <-sigc:
		fmt.Println("\nGracefully stopping...")
	case <-exited:
		fmt.Println("Aborting on container exit...")
	}
	for _, n := range names {
		// Always forward the grace period: docker's default stop grace is 10s
		// but the backend's is 5s, so an unset --timeout must still send 10.
		_, _ = runtime.CaptureSilent(composeStopArgs(true, timeout, n)...)
	}
	for _, c := range cmds {
		_ = c.Process.Kill()
	}
	wg.Wait()
	if exitCodeFrom != "" {
		// Best-effort: the backend exposes no exit codes, so all we can check
		// is that the selected service's container exists and reached a
		// cleanly-stopped state; anything else is reported as a failure (1).
		cid, err := serviceContainerByIndex(p.Name, exitCodeFrom, 1)
		if err != nil {
			return fmt.Errorf("--exit-code-from %s: %w", exitCodeFrom, err)
		}
		var list []dockerfmt.Container
		if err := runtime.CaptureJSON(&list, "inspect", cid); err != nil || len(list) == 0 {
			return fmt.Errorf("--exit-code-from %s: could not inspect %s", exitCodeFrom, cid)
		}
		if st := list[0].Status.State; st != "stopped" {
			return fmt.Errorf("--exit-code-from %s: container %s is in state %q", exitCodeFrom, cid, st)
		}
	}
	return nil
}

// anyExited reports whether any of the started container names is no longer
// running (missing from the state map counts as exited). Pure, for tests.
func anyExited(names []string, state map[string]string) bool {
	for _, n := range names {
		if state[n] != "running" {
			return true
		}
	}
	return false
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
			// docker compose down does a GRACEFUL shutdown (SIGTERM, wait up to -t,
			// then SIGKILL) before removing — not an immediate force-kill, which can
			// lose unflushed state on stateful services. Honor the -t/--timeout flag.
			timeoutChanged := cmd.Flags().Changed("timeout")
			timeout, _ := cmd.Flags().GetInt("timeout")
			for _, c := range containers {
				fmt.Println(composeStep("Removing", c.ID))
				_, _ = runtime.CaptureSilent(composeStopArgs(timeoutChanged, timeout, c.ID)...)
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
					fmt.Println(composeStep("Removing network", net))
					_, _ = runtime.CaptureSilent("network", "delete", net)
				}
			}
			if rmVolumes {
				for name, spec := range p.Volumes {
					if spec != nil && spec.External {
						continue
					}
					vol := p.VolumeName(name, spec)
					fmt.Println(composeStep("Removing volume", vol))
					_, _ = runtime.CaptureSilent("volume", "delete", vol)
				}
			}
			// --rmi all removes every service image; --rmi local removes only
			// images for services built from a Dockerfile (no pinned registry image).
			if rmi, _ := cmd.Flags().GetString("rmi"); rmi != "" {
				if rmi != "all" && rmi != "local" {
					return fmt.Errorf("invalid --rmi value %q: must be 'all' or 'local'", rmi)
				}
				seen := map[string]bool{}
				for name, svc := range p.Services {
					var ref string
					switch {
					case rmi == "all":
						ref = p.ImageRef(name, svc)
					case rmi == "local" && svc.Build.IsSet():
						ref = p.BuildImageName(name)
					}
					if ref == "" || seen[ref] {
						continue
					}
					seen[ref] = true
					fmt.Println(composeStep("Removing image", ref))
					_, _ = runtime.CaptureSilent("image", "delete", ref)
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
			status, _ := cmd.Flags().GetString("status")
			containers, err := projectContainers(p.Name)
			if err != nil {
				return err
			}
			selected := serviceSet(args)
			if servicesOnly {
				// --services honors the same SERVICE... selection and state
				// filters as the table view (it previously ignored both).
				seen := map[string]bool{}
				for _, c := range containers {
					if !composePsMatch(c, selected, all, status) {
						continue
					}
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
				if !composePsMatch(c, selected, all, status) {
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
	cmd.Flags().String("filter", "", "Filter services by a property (unsupported)")
	cmd.Flags().String("status", "", "Filter services by status (paused|restarting|removing|running|dead|created|exited)")
	cmd.Flags().Bool("no-trunc", false, "Don't truncate output (no-op)")
	cmd.Flags().Bool("orphans", true, "Include orphaned services (no-op)")
	return cmd
}

// composePsMatch is the shared `compose ps` filter: SERVICE... selection, then
// --status (Docker status vocabulary, mapped onto backend states) when given,
// else the running-only default unless -a. Pure, for tests.
func composePsMatch(c dockerfmt.Container, selected map[string]bool, all bool, status string) bool {
	if selected != nil && !selected[serviceOf(c)] {
		return false
	}
	if status != "" {
		return matchStatusFilter(c.Status.State, status)
	}
	return all || c.Status.State == "running"
}

func composeLogs() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [OPTIONS] [SERVICE...]",
		Short: "View output from containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			if nc, _ := cmd.Flags().GetBool("no-color"); nc {
				ui.SetEnabled(false)
			}
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
				// Nothing to follow: return immediately instead of blocking on the
				// signal channel forever (docker compose logs -f does the same).
				if len(targets) == 0 {
					return nil
				}
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
	cmd.Flags().Bool("no-color", false, "Produce monochrome output")
	cmd.Flags().Int("index", 0, "Index of the container if service has multiple replicas (no-op)")
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

// formatLogLine renders one aggregated log line with the Docker-style
// "service | …" prefix unless --no-log-prefix was given. Pure, so the prefix
// behavior is unit-testable.
func formatLogLine(svc, line string, noPrefix bool) string {
	if noPrefix {
		return line
	}
	return ui.Accent(svc) + " | " + line
}

// printLogLine writes one aggregated log line (see formatLogLine).
func printLogLine(svc, line string, noPrefix bool) {
	fmt.Println(formatLogLine(svc, line, noPrefix))
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
			noCache, _ := cmd.Flags().GetBool("no-cache")
			pull, _ := cmd.Flags().GetBool("pull")
			quiet, _ := cmd.Flags().GetBool("quiet")
			extraArgs, _ := cmd.Flags().GetStringArray("build-arg")
			selected := serviceSet(args)
			for _, name := range p.Order() {
				if selected != nil && !selected[name] {
					continue
				}
				svc := p.Services[name]
				if !svc.Build.IsSet() {
					continue
				}
				if !quiet {
					fmt.Println(composeStep("Building", name))
				}
				bargs := p.BuildArgs(name, svc)
				if noCache {
					bargs = append(bargs, "--no-cache")
				}
				if pull {
					bargs = append(bargs, "--pull")
				}
				if quiet {
					bargs = append(bargs, "--quiet")
				}
				for _, ba := range extraArgs {
					bargs = append(bargs, "--build-arg", ba)
				}
				if err := runtime.Run(bargs...); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("no-cache", false, "Do not use cache when building the image")
	cmd.Flags().Bool("pull", false, "Always attempt to pull a newer version of the image")
	cmd.Flags().BoolP("quiet", "q", false, "Don't print anything to STDOUT")
	cmd.Flags().StringArray("build-arg", nil, "Set build-time variables for services")
	cmd.Flags().String("progress", "", "Set type of progress output (accepted)")
	cmd.Flags().Bool("with-dependencies", false, "Also build dependencies (transitively) (no-op)")
	cmd.Flags().Bool("push", false, "Push service images (unsupported; use 'dcon compose push')")
	cmd.Flags().StringArray("ssh", nil, "Set SSH authentications used when building (unsupported)")
	cmd.Flags().StringP("memory", "m", "", "Set memory limit for the build container (accepted)")
	return cmd
}

func composePull() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull [SERVICE...]",
		Short: "Pull service images",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			quiet, _ := cmd.Flags().GetBool("quiet")
			ignoreFail, _ := cmd.Flags().GetBool("ignore-pull-failures")
			selected := serviceSet(args)
			for name, svc := range p.Services {
				if selected != nil && !selected[name] {
					continue
				}
				if svc.Image == "" {
					continue
				}
				if !quiet {
					fmt.Println(ui.Dim("Pulling ") + ui.Accent(name) + ui.Dim(" ("+svc.Image+")..."))
				}
				pargs := []string{"image", "pull"}
				if quiet {
					pargs = append(pargs, "--progress", "none")
				}
				pargs = append(pargs, svc.Image)
				if err := runtime.Run(pargs...); err != nil {
					if !ignoreFail {
						return fmt.Errorf("pull %s (%s): %w", name, svc.Image, err)
					}
					fmt.Fprintf(os.Stderr, "dcon: warning: pull %s failed: %v\n", svc.Image, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolP("quiet", "q", false, "Pull without printing progress information")
	cmd.Flags().Bool("ignore-pull-failures", false, "Pull what it can and ignores images with pull failures")
	cmd.Flags().Bool("include-deps", false, "Also pull services declared as dependencies (no-op)")
	cmd.Flags().String("policy", "", "Apply pull policy: missing|always (accepted)")
	cmd.Flags().Bool("no-parallel", false, "Disable parallel pulling (no-op)")
	return cmd
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
		// Forward the verb's honored flags (defined only on the relevant
		// subcommands; absent flags read as zero/unchanged): kill --signal, and
		// stop/restart --timeout. The grace period is ALWAYS forwarded for
		// stop/restart: docker's default is 10s but the backend's is 5s, so an
		// unset --timeout must still send --time 10.
		signal, _ := cmd.Flags().GetString("signal")
		timeout := 10
		if t, err := cmd.Flags().GetInt("timeout"); err == nil {
			timeout = t
		}
		var firstErr error
		for _, c := range containers {
			if lifecycleSkips(verb, c, selected) {
				continue
			}
			var err error
			switch verb {
			case "rm":
				// docker compose rm removes only STOPPED containers; a running one
				// is preserved unless -s/--stop is given. Force-deleting a running
				// service (the old unconditional behavior) destroyed it.
				stop, _ := cmd.Flags().GetBool("stop")
				if c.Status.State == "running" && !stop {
					fmt.Fprintf(os.Stderr, "dcon: warning: %s is running; not removed (use -s to stop and remove)\n", serviceOf(c))
					continue
				}
				_, err = runtime.CaptureSilent("delete", "--force", c.ID)
			case "stop":
				_, err = runtime.CaptureSilent(composeStopArgs(true, timeout, c.ID)...)
			case "start":
				_, err = runtime.CaptureSilent("start", c.ID)
			case "kill":
				_, err = runtime.CaptureSilent(composeKillArgs(signal, c.ID)...)
			case "restart":
				if _, err = runtime.CaptureSilent(composeStopArgs(true, timeout, c.ID)...); err == nil {
					_, err = runtime.CaptureSilent("start", c.ID)
				}
			}
			// Report per-container failures instead of exiting 0 and echoing
			// the ID as if the verb succeeded.
			if err != nil {
				fmt.Fprintf(os.Stderr, "dcon: %s %s: %v\n", verb, c.ID, err)
				if firstErr == nil {
					firstErr = fmt.Errorf("%s %s: %w", verb, c.ID, err)
				}
				continue
			}
			fmt.Println(c.ID)
		}
		return firstErr
	}
}

// lifecycleSkips filters project containers for a lifecycle verb: unselected
// services always skip, and one-off `compose run` containers are excluded
// from stop/kill (docker leaves one-offs alone there). Pure, for tests.
func lifecycleSkips(verb string, c dockerfmt.Container, selected map[string]bool) bool {
	if selected != nil && !selected[serviceOf(c)] {
		return true
	}
	if c.Configuration.Labels[compose.LabelOneoff] == "True" && (verb == "stop" || verb == "kill") {
		return true
	}
	return false
}

// composeStopArgs builds the backend stop argv for compose stop/restart,
// forwarding --time only when the user set --timeout.
func composeStopArgs(timeoutChanged bool, timeout int, id string) []string {
	args := []string{"stop"}
	if timeoutChanged {
		args = append(args, "--time", strconv.Itoa(timeout))
	}
	return append(args, id)
}

// composeKillArgs builds the backend kill argv for compose kill, forwarding the
// chosen --signal (defaults to SIGKILL).
func composeKillArgs(signal, id string) []string {
	args := []string{"kill"}
	if signal != "" {
		args = append(args, "--signal", signal)
	}
	return append(args, id)
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
			// -q/--quiet: only validate (loadProject above did that), print nothing.
			if q, _ := cmd.Flags().GetBool("quiet"); q {
				return nil
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
				names := make([]string, 0, len(p.Volumes))
				for n := range p.Volumes {
					names = append(names, n)
				}
				sort.Strings(names) // deterministic order, like the --services branch
				for _, n := range names {
					fmt.Println(n)
				}
				return nil
			}
			if v, _ := cmd.Flags().GetBool("profiles"); v {
				seen := map[string]bool{}
				var profiles []string
				for _, svc := range p.Services {
					for _, prof := range svc.Profiles {
						if !seen[prof] {
							seen[prof] = true
							profiles = append(profiles, prof)
						}
					}
				}
				sort.Strings(profiles)
				for _, prof := range profiles {
					fmt.Println(prof)
				}
				return nil
			}
			if v, _ := cmd.Flags().GetBool("images"); v {
				seen := map[string]bool{}
				var images []string
				for name, svc := range p.Services {
					img := p.ImageRef(name, svc)
					if img != "" && !seen[img] {
						seen[img] = true
						images = append(images, img)
					}
				}
				sort.Strings(images)
				for _, img := range images {
					fmt.Println(img)
				}
				return nil
			}
			out, err := yaml.Marshal(p)
			if err != nil {
				return err
			}
			format, _ := cmd.Flags().GetString("format")
			switch format {
			case "", "yaml":
				fmt.Print(string(out))
			case "json":
				// Round-trip through the yaml tree so the JSON keys match the
				// compose (yaml-tag) names rather than Go field names.
				var tree map[string]any
				if err := yaml.Unmarshal(out, &tree); err != nil {
					return err
				}
				js, err := json.MarshalIndent(tree, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(js))
			default:
				return fmt.Errorf("unsupported --format %q: must be yaml or json", format)
			}
			return nil
		},
	}
	cmd.Flags().Bool("services", false, "Print the service names, one per line")
	cmd.Flags().Bool("volumes", false, "Print the volume names, one per line")
	cmd.Flags().Bool("profiles", false, "Print the profile names, one per line")
	cmd.Flags().Bool("images", false, "Print the image names, one per line")
	cmd.Flags().String("format", "yaml", "Format the output (yaml|json)")
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
			// Default to projects with a running container; -a/--all includes
			// fully-stopped ones too (matches docker compose ls).
			showAll, _ := cmd.Flags().GetBool("all")
			names := make([]string, 0, len(projects))
			for n := range projects {
				if showAll || projects[n]["running"] > 0 {
					names = append(names, n)
				}
			}
			sort.Strings(names)
			// -q/--quiet: only project names, one per line (matches docker).
			if quiet, _ := cmd.Flags().GetBool("quiet"); quiet {
				for _, n := range names {
					fmt.Println(n)
				}
				return nil
			}
			w := dockerfmt.NewTabWriter()
			fmt.Fprintln(w, "NAME\tSTATUS\tCONFIG FILES")
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
	cmd := &cobra.Command{
		Use:   "create [SERVICE...]",
		Short: "Create containers for a service (without starting them)",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			forceRecreate, _ := cmd.Flags().GetBool("force-recreate")
			// `create SERVICE` also creates its depends_on closure, like docker.
			selected := withDeps(p, serviceSet(args))
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
				cname := p.ContainerName(name, 1, svc)
				if forceRecreate {
					_, _ = runtime.CaptureSilent("delete", "--force", cname)
				} else if _, err := runtime.CaptureSilent("inspect", cname); err == nil {
					// docker `compose create` is idempotent: an already-existing
					// container is left as-is rather than erroring on a duplicate
					// name. (composeUp's bringUp guards the same way.)
					fmt.Println(ui.Dim("Container ") + ui.Accent(cname) + ui.Dim(" exists"))
					continue
				}
				rargs := p.CreateArgs(name, svc, 1, net)
				fmt.Println(composeStep("Creating", cname))
				if err := runtime.Run(rargs...); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("build", false, "Build images before creating containers (no-op)")
	cmd.Flags().Bool("no-build", false, "Don't build an image, even if it's policy (no-op)")
	cmd.Flags().Bool("force-recreate", false, "Recreate containers even if their configuration hasn't changed")
	cmd.Flags().Bool("no-recreate", false, "If containers already exist, don't recreate them (no-op)")
	cmd.Flags().String("pull", "", "Pull image before running (always|missing|never|build)")
	cmd.Flags().Bool("remove-orphans", false, "Remove containers for services not defined in the compose file (no-op)")
	cmd.Flags().StringArray("scale", nil, "Scale SERVICE to NUM instances (accepted)")
	cmd.Flags().BoolP("yes", "y", false, "Assume \"yes\" as answer to all prompts (no-op)")
	return cmd
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
			if cmd.Flags().Changed("privileged") {
				fmt.Fprintln(os.Stderr, "dcon: warning: --privileged is not supported by exec and was ignored")
			}
			detach, _ := cmd.Flags().GetBool("detach")
			interactive, _ := cmd.Flags().GetBool("interactive")
			tty, _ := cmd.Flags().GetBool("tty")
			noTTY, _ := cmd.Flags().GetBool("no-TTY")
			user, _ := cmd.Flags().GetString("user")
			workdir, _ := cmd.Flags().GetString("workdir")
			env := mustStringArray(cmd.Flags(), "env")
			cargs := composeExecArgs(detach, interactive, tty, noTTY, haveTTY(), user, workdir, env, c, rest)
			return runtime.Run(cargs...)
		},
	}
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().BoolP("interactive", "i", true, "Keep STDIN open")
	cmd.Flags().BoolP("tty", "t", true, "Allocate a pseudo-TTY")
	cmd.Flags().BoolP("no-TTY", "T", false, "Disable pseudo-TTY allocation")
	cmd.Flags().BoolP("detach", "d", false, "Detached mode")
	cmd.Flags().StringP("user", "u", "", "Run the command as this user")
	cmd.Flags().StringP("workdir", "w", "", "Path to workdir directory")
	cmd.Flags().StringArrayP("env", "e", nil, "Set environment variables")
	cmd.Flags().Int("index", 1, "Index of the container if service has multiple replicas")
	cmd.Flags().Bool("privileged", false, "Give extended privileges to the process (unsupported)")
	return cmd
}

// composeExecArgs builds the `container exec ...` argv for `compose exec`.
// --interactive follows the flag value alone (it defaults to true), NOT whether
// stdin is a terminal: piping stdin (`compose exec -T db psql < dump.sql`) is
// exactly the case that needs STDIN kept open, and gating it on a TTY dropped
// the redirected input. The PTY (--tty) is separate: only when requested, not
// suppressed by -T/--no-TTY, and a real terminal is present.
func composeExecArgs(detach, interactive, tty, noTTY, hasTTY bool, user, workdir string, env []string, containerID string, rest []string) []string {
	cargs := []string{"exec"}
	if detach {
		cargs = append(cargs, "--detach")
	}
	if interactive {
		cargs = append(cargs, "--interactive")
	}
	if tty && !noTTY && hasTTY {
		cargs = append(cargs, "--tty")
	}
	if user != "" {
		cargs = append(cargs, "--user", user)
	}
	if workdir != "" {
		cargs = append(cargs, "--workdir", workdir)
	}
	for _, e := range env {
		cargs = append(cargs, "--env", e)
	}
	cargs = append(cargs, containerID)
	return append(cargs, rest...)
}

// composeRunPtyFlags returns the --interactive/--tty tokens for `compose run`.
// Docker compose run keeps STDIN open (-i, default true) and allocates a TTY by
// default unless -T/--no-TTY; the PTY is only allocated when a real terminal is
// present (so `compose run web cmd | cat` and CI pipelines still work).
func composeRunPtyFlags(interactive, noTTY, hasTTY bool) []string {
	var out []string
	if interactive {
		out = append(out, "--interactive")
	}
	if !noTTY && hasTTY {
		out = append(out, "--tty")
	}
	return out
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
		// Never resolve a service replica to a one-off `compose run` container:
		// it shares service/number labels but is a separate, ephemeral thing.
		if c.Configuration.Labels[compose.LabelOneoff] == "True" {
			continue
		}
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

// startDeps brings up the transitive depends_on closure of a service
// (excluding the service itself) before a one-off `compose run`, like docker:
// missing dependency containers are created and started, stopped ones
// restarted, running ones left alone. Failures warn rather than abort — the
// one-off command may not actually need the dependency.
func startDeps(p *compose.Project, service, net string) {
	deps := withDeps(p, map[string]bool{service: true})
	delete(deps, service)
	if len(deps) == 0 {
		return
	}
	existing := map[string]dockerfmt.Container{}
	if cs, err := projectContainers(p.Name); err == nil {
		for _, c := range cs {
			existing[c.ID] = c
		}
	}
	for _, name := range p.Order() { // dependency order
		if !deps[name] {
			continue
		}
		svc := p.Services[name]
		cname := p.ContainerName(name, 1, svc)
		if c, ok := existing[cname]; ok {
			if c.Status.State != "running" {
				_, _ = runtime.CaptureSilent("start", cname)
			}
			continue
		}
		fmt.Println(composeStep("Creating", cname))
		if err := runtime.Run(p.RunArgs(name, svc, 1, net, nil)...); err != nil {
			fmt.Fprintf(os.Stderr, "dcon: warning: could not start dependency %q: %v\n", name, err)
		}
	}
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
			if cmd.Flags().Changed("publish-all") {
				fmt.Fprintln(os.Stderr, "dcon: warning: --publish-all is not supported by the backend and was ignored")
			}
			if b, _ := cmd.Flags().GetBool("build"); b && svc.Build.IsSet() {
				fmt.Println(composeStep("Building", svcName))
				if err := runtime.Run(p.BuildArgs(svcName, svc)...); err != nil {
					return err
				}
			}
			net := ensureNetwork(p)
			// Bring the service's depends_on closure up first (docker compose
			// run does), unless --no-deps.
			if noDeps, _ := cmd.Flags().GetBool("no-deps"); !noDeps {
				ensureVolumes(p)
				startDeps(p, svcName, net)
			}

			// Build CLI override flag tokens, inserted before the image.
			var overrides []string
			addEach := func(flag string, vals []string) {
				for _, v := range vals {
					overrides = append(overrides, flag, v)
				}
			}
			addEach("--env", mustStringArray(cmd.Flags(), "env"))
			addEach("--env-file", mustStringArray(cmd.Flags(), "env-file"))
			// CLI -v overrides: strip the macOS-irrelevant mount options the backend
			// rejects (:z/:Z/:cached/:delegated/:consistent), exactly like `dcon run
			// -v` (normalizeVolume) and compose-file volumes (stripVolumeOpts) do —
			// otherwise `compose run -v X:Y:cached` fails where the others succeed.
			var volWarnings []string
			for _, v := range mustStringArray(cmd.Flags(), "volume") {
				overrides = append(overrides, "--volume", normalizeVolume(v, &volWarnings))
			}
			for _, w := range volWarnings {
				fmt.Fprintln(os.Stderr, "dcon: warning: "+w)
			}
			addEach("--publish", mustStringArray(cmd.Flags(), "publish"))
			addEach("--label", mustStringArray(cmd.Flags(), "label"))
			addEach("--cap-add", mustStringArray(cmd.Flags(), "cap-add"))
			// --service-ports re-publishes the service's declared ports (off by
			// default in docker compose run to avoid colliding with the live service).
			if sp, _ := cmd.Flags().GetBool("service-ports"); sp {
				addEach("--publish", svc.Ports)
			}
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
			// Changed (not non-empty): `--entrypoint ""` is an explicit reset that
			// must clear the entrypoint, matching the run path's f.Changed handling.
			entrypointSet := cmd.Flags().Changed("entrypoint")

			detach, _ := cmd.Flags().GetBool("detach")
			// Forward TTY/interactive to the backend (the run RunE previously read
			// neither, so `compose run web bash` got no PTY). Docker compose run
			// allocates a TTY by default unless -T, and keeps stdin open (-i).
			// Skip when detached.
			if !detach {
				interactive, _ := cmd.Flags().GetBool("interactive")
				noTTY, _ := cmd.Flags().GetBool("no-TTY")
				overrides = append(overrides, composeRunPtyFlags(interactive, noTTY, haveTTY())...)
			}

			rm, _ := cmd.Flags().GetBool("rm")
			run := p.OneOffArgs(svcName, svc, net, args[1:], overrides, entrypoint, entrypointSet, rm)
			if detach {
				run = append([]string{run[0], "--detach"}, run[1:]...)
			}
			return runtime.Run(run...)
		},
	}
	cmd.Flags().SetInterspersed(false)
	// Docker compose run KEEPS the one-off container unless --rm is given;
	// defaulting to true silently destroyed containers users expected to find.
	cmd.Flags().Bool("rm", false, "Automatically remove the container when it exits")
	cmd.Flags().BoolP("detach", "d", false, "Run container in background")
	cmd.Flags().StringArrayP("env", "e", nil, "Set environment variables")
	cmd.Flags().StringArrayP("volume", "v", nil, "Bind mount a volume")
	cmd.Flags().StringArrayP("publish", "p", nil, "Publish a container's port(s) to the host")
	cmd.Flags().StringArrayP("label", "l", nil, "Add or override a label")
	cmd.Flags().String("name", "", "Assign a name to the container")
	cmd.Flags().StringP("workdir", "w", "", "Working directory inside the container")
	cmd.Flags().StringP("user", "u", "", "Username or UID")
	cmd.Flags().String("entrypoint", "", "Override the entrypoint of the image")
	// Common Docker Compose `run` flags. Registered so invocations don't
	// hard-fail; the behavioral ones (TTY) are honored, the rest accepted.
	cmd.Flags().BoolP("interactive", "i", true, "Keep STDIN open even if not attached")
	cmd.Flags().BoolP("no-TTY", "T", false, "Disable pseudo-TTY allocation")
	cmd.Flags().Bool("no-deps", false, "Don't start linked services")
	cmd.Flags().Bool("service-ports", false, "Run with the service's ports enabled and mapped to the host (unsupported)")
	cmd.Flags().Bool("build", false, "Build image before starting the container")
	cmd.Flags().Bool("quiet-pull", false, "Pull without printing progress information")
	cmd.Flags().BoolP("publish-all", "P", false, "Publish all exposed ports (unsupported)")
	cmd.Flags().Bool("use-aliases", false, "Use the service's network aliases in the connected network(s) (no-op)")
	cmd.Flags().Bool("remove-orphans", false, "Remove containers for services not defined in the compose file (no-op)")
	cmd.Flags().StringArray("cap-add", nil, "Add Linux capabilities")
	cmd.Flags().StringArray("env-file", nil, "Set environment variable file")
	return cmd
}

// effectiveReplicas resolves how many replicas `up` should run for a service.
// An explicit --scale entry wins — INCLUDING 0 (docker's `up --scale web=0`
// runs zero). p.Replicas can't express this because it clamps a 0 override up
// to 1 (treating 0 as "unset"). A negative scale is floored to 0.
func effectiveReplicas(svc *compose.Service, scale map[string]int, name string) int {
	if v, ok := scale[name]; ok {
		if v < 0 {
			v = 0
		}
		return v
	}
	if svc.Scale >= 1 {
		return svc.Scale
	}
	// Modern `deploy: { replicas: N }` (no legacy top-level scale:). A pointer so
	// an explicit 0 is honored as "run nothing"; nil means unset.
	if r := svc.Deploy.Replicas; r != nil {
		if *r < 0 {
			return 0
		}
		return *r
	}
	return 1 // no --scale, scale:, or deploy.replicas: one replica
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
			// Map image reference -> short image id so the IMAGE ID column matches
			// what `dcon images` shows; fall back to the container's image
			// descriptor digest when the image isn't in the local list.
			idByRef := map[string]string{}
			if imgs, err := getImages(); err == nil {
				for _, im := range imgs {
					idByRef[dockerfmt.ShortImage(im.Configuration.Name)] = dockerfmt.ShortID(im.ID)
				}
			}
			selected := serviceSet(args) // nil => all services (docker: images [SERVICE...])
			w := dockerfmt.NewTabWriter()
			// docker `compose images` columns: CONTAINER REPOSITORY TAG IMAGE ID SIZE.
			fmt.Fprintln(w, "CONTAINER\tREPOSITORY\tTAG\tIMAGE ID\tSIZE")
			for _, c := range containers {
				if selected != nil && !selected[serviceOf(c)] {
					continue
				}
				ref := dockerfmt.ShortImage(c.Configuration.Image.Reference)
				repo, tag := dockerfmt.SplitRepoTag(ref)
				imgID := idByRef[ref]
				if imgID == "" {
					imgID = dockerfmt.ShortID(c.Configuration.Image.Descriptor.Digest)
				}
				if imgID == "" {
					imgID = "N/A"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.ID, repo, tag, imgID, "N/A")
			}
			return w.Flush()
		},
	}
}

// composeContainerState reports whether a backend container exists and, if so,
// whether it is currently running, via `container inspect` (which succeeds for
// stopped/created containers too). Used by `compose up` to decide whether an
// existing container is up-to-date (running) or needs starting (stopped).
func composeContainerState(name string) (exists, running bool) {
	var rows []struct {
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err := runtime.CaptureJSON(&rows, "inspect", name); err != nil || len(rows) == 0 {
		return false, false
	}
	return true, rows[0].Status.State == "running"
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
					fmt.Println(composeStep("Creating", cname))
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
					// Never touch one-off `compose run` containers (oneoff=True): scale
					// manages only service replicas, matching the other resolvers
					// (serviceContainerByIndex/firstServiceContainer) which skip oneoffs.
					if c.Configuration.Labels[compose.LabelOneoff] == "True" {
						continue
					}
					if num, _ := strconv.Atoi(c.Configuration.Labels[compose.LabelNumber]); num > n {
						fmt.Println(composeStep("Removing", c.ID))
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

// composePush pushes the image of each (selected) service to its registry.
func composePush() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push [SERVICE...]",
		Short: "Push service images",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			selected := serviceSet(args)
			ignoreFail, _ := cmd.Flags().GetBool("ignore-push-failures")
			quiet, _ := cmd.Flags().GetBool("quiet")
			names := make([]string, 0, len(p.Services))
			for n := range p.Services {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, name := range names {
				if selected != nil && !selected[name] {
					continue
				}
				svc := p.Services[name]
				if svc.Image == "" {
					continue // nothing to push for build-only services
				}
				if !quiet {
					fmt.Println(ui.Dim("Pushing ") + ui.Accent(name) + ui.Dim(" ("+svc.Image+")..."))
				}
				if err := runtime.Run("image", "push", svc.Image); err != nil {
					if ignoreFail {
						fmt.Fprintf(os.Stderr, "dcon: warning: push %s failed: %v\n", svc.Image, err)
						continue
					}
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("ignore-push-failures", false, "Push what it can and ignore images with push failures")
	cmd.Flags().BoolP("quiet", "q", false, "Push without printing progress information")
	return cmd
}

// composePort prints the host binding for a service container's private port.
func composePort() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "port [OPTIONS] SERVICE PRIVATE_PORT",
		Short: "Print the public port for a port binding",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			idx, _ := cmd.Flags().GetInt("index")
			proto, _ := cmd.Flags().GetString("protocol")
			proto = strings.ToLower(proto) // backend protos are lowercase; --protocol UDP must match
			cid, err := serviceContainerByIndex(p.Name, args[0], idx)
			if err != nil {
				return err
			}
			var list []dockerfmt.Container
			if err := runtime.CaptureJSON(&list, "inspect", cid); err != nil {
				return err
			}
			if len(list) == 0 {
				return fmt.Errorf("no such container: %s", cid)
			}
			for _, pt := range list[0].Configuration.Ports {
				pr := pt.Proto
				if pr == "" {
					pr = "tcp"
				}
				if strings.ToLower(pr) != proto {
					continue
				}
				host := pt.HostAddress
				if host == "" {
					host = "0.0.0.0"
				}
				cnt := pt.Count
				if cnt < 1 {
					cnt = 1
				}
				// A published range arrives as one PublishPort with Count>1; expand
				// it so an in-range query (e.g. 8001 within 8000-8002) resolves.
				for k := 0; k < cnt; k++ {
					if fmt.Sprint(pt.ContainerPort+k) == args[1] {
						fmt.Printf("%s:%d\n", host, pt.HostPort+k)
						return nil
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().Int("index", 1, "Index of the container if service has multiple replicas")
	cmd.Flags().String("protocol", "tcp", "tcp or udp")
	return cmd
}

// composeAttach streams a service container's output. As with the top-level
// attach, the backend forwards stdout/stderr only (no STDIN).
func composeAttach() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach [OPTIONS] SERVICE",
		Short: "Attach local standard output and error streams to a service's running container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject(cmd)
			if err != nil {
				return err
			}
			idx, _ := cmd.Flags().GetInt("index")
			cid, err := serviceContainerByIndex(p.Name, args[0], idx)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "dcon: note: backend supports stdout/stderr streaming only; STDIN is not forwarded on attach")
			return runtime.Run("logs", "--follow", cid)
		},
	}
	cmd.Flags().Int("index", 1, "Index of the container if service has multiple replicas")
	cmd.Flags().Bool("no-stdin", false, "Do not attach STDIN (no-op)")
	cmd.Flags().String("detach-keys", "", "Override the key sequence for detaching (no-op)")
	cmd.Flags().Bool("sig-proxy", true, "Proxy all received signals to the process (no-op)")
	return cmd
}

// composePause / composeUnpause: the backend has no freezer, so these are
// genuinely unsupported. Registered so the commands exist and report clearly
// rather than failing as "unknown command".
func composePause() *cobra.Command {
	return &cobra.Command{
		Use:   "pause [SERVICE...]",
		Short: "Pause services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("pause is not supported by the container backend")
		},
	}
}

func composeUnpause() *cobra.Command {
	return &cobra.Command{
		Use:   "unpause [SERVICE...]",
		Short: "Unpause services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("unpause is not supported by the container backend")
		},
	}
}

// composeEvents: the backend exposes no event stream.
func composeEvents() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events [OPTIONS] [SERVICE...]",
		Short: "Receive real time events from containers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("events stream is not supported by the container backend")
		},
	}
	cmd.Flags().Bool("json", false, "Output events as a stream of json objects")
	return cmd
}

// composeWatch: the backend has no file-sync/live-reload mechanism.
func composeWatch() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch [SERVICE...]",
		Short: "Watch build context for changes and rebuild/refresh services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("watch is not supported: the container backend has no file-sync/live-reload; rebuild manually with `dcon compose build` then `dcon compose up`")
		},
	}
	cmd.Flags().Bool("no-up", false, "Do not build & start services before watching")
	cmd.Flags().Bool("quiet", false, "Hide build output")
	cmd.Flags().Bool("prune", true, "Prune dangling images on rebuild")
	return cmd
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
		if c.Configuration.Labels[compose.LabelOneoff] == "True" {
			continue
		}
		if serviceOf(c) == service && c.Status.State == "running" {
			return c.ID, nil
		}
	}
	// fall back to any state
	for _, c := range containers {
		if c.Configuration.Labels[compose.LabelOneoff] == "True" {
			continue
		}
		if serviceOf(c) == service {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no running container for service %q", service)
}
