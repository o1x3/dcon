package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"dcon/internal/dockerfmt"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

func timeSince(t time.Time) time.Duration { return time.Since(t) }

// getContainers fetches the container list from the backend as JSON.
func getContainers(all bool) ([]dockerfmt.Container, error) {
	args := []string{"ls", "--format", "json"}
	if all {
		args = append(args, "--all")
	}
	var list []dockerfmt.Container
	if err := runtime.CaptureJSON(&list, args...); err != nil {
		return nil, err
	}
	return list, nil
}

// psView exposes Docker ps template fields (.ID, .Image, .Status, ...).
type psView struct {
	ID           string
	Image        string
	Command      string
	CreatedAt    string
	RunningFor   string
	Status       string
	Ports        string
	Names        string
	Labels       string
	Mounts       string
	Networks     string
	Size         string
	State        string
	LocalVolumes string
}

// matchStatusFilter maps Docker's status-filter vocabulary
// (created|running|exited|dead|paused|…) onto the backend's state names
// (unknown|running|stopped|stopping).
func matchStatusFilter(state, want string) bool {
	switch strings.ToLower(want) {
	case "running":
		return state == "running"
	case "exited", "dead":
		return state == "stopped"
	case "created":
		return state == "" || state == "unknown"
	case "stopping", "removing", "restarting":
		return state == "stopping"
	default:
		return strings.EqualFold(state, want)
	}
}

func portsString(c dockerfmt.Container) string {
	var parts []string
	for _, p := range c.Configuration.Ports {
		host := p.HostAddress
		if host == "" {
			host = "0.0.0.0"
		}
		if strings.Contains(host, ":") { // bracket IPv6 host addresses
			host = "[" + host + "]"
		}
		proto := p.Proto
		if proto == "" {
			proto = "tcp"
		}
		cnt := p.Count
		if cnt < 1 {
			cnt = 1
		}
		if cnt > 1 {
			parts = append(parts, fmt.Sprintf("%s:%d-%d->%d-%d/%s",
				host, p.HostPort, p.HostPort+cnt-1, p.ContainerPort, p.ContainerPort+cnt-1, proto))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%d->%d/%s", host, p.HostPort, p.ContainerPort, proto))
		}
	}
	return strings.Join(parts, ", ")
}

func statusString(c dockerfmt.Container) string {
	switch c.Status.State {
	case "running":
		if t, ok := dockerfmt.ParseTime(c.Status.StartedDate); ok {
			return "Up " + dockerfmt.HumanDuration(timeSince(t))
		}
		return "Up"
	case "stopped":
		return "Exited"
	case "stopping":
		return "Stopping"
	case "", "unknown":
		return "Created"
	default:
		return strings.Title(c.Status.State)
	}
}

func networksString(c dockerfmt.Container) string {
	var names []string
	for _, n := range c.Configuration.Networks {
		names = append(names, n.Network)
	}
	if len(names) == 0 {
		for _, n := range c.Status.Networks {
			names = append(names, n.Network)
		}
	}
	return strings.Join(names, ",")
}

func labelsString(m map[string]string) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func mountsString(c dockerfmt.Container) string {
	var parts []string
	for _, m := range c.Configuration.Mounts {
		src := m.Source
		if src == "" {
			src = m.Destination
		}
		parts = append(parts, src)
	}
	return strings.Join(parts, ",")
}

func buildPsView(c dockerfmt.Container, noTrunc bool) psView {
	id := c.ID
	if !noTrunc {
		id = dockerfmt.ShortID(id)
	}
	cmdParts := append([]string{c.Configuration.InitProcess.Executable}, c.Configuration.InitProcess.Arguments...)
	if c.Configuration.InitProcess.Executable == "" {
		cmdParts = c.Configuration.InitProcess.Arguments
	}
	createdAt := c.Configuration.CreationDate
	if t, ok := dockerfmt.ParseTime(createdAt); ok {
		createdAt = t.Format("2006-01-02 15:04:05 -0700 MST")
	}
	localVols := 0
	for _, m := range c.Configuration.Mounts {
		if m.IsVolume() {
			localVols++
		}
	}
	return psView{
		ID:           id,
		Image:        dockerfmt.ShortImage(c.Configuration.Image.Reference),
		Command:      dockerfmt.TruncCommand(cmdParts, noTrunc),
		CreatedAt:    createdAt,
		RunningFor:   dockerfmt.RelativeAgo(c.Configuration.CreationDate),
		Status:       statusString(c),
		Ports:        portsString(c),
		Names:        c.ID,
		Labels:       labelsString(c.Configuration.Labels),
		Mounts:       mountsString(c),
		Networks:     networksString(c),
		Size:         "N/A",
		State:        c.Status.State,
		LocalVolumes: fmt.Sprint(localVols),
	}
}

// hasStatusFilter reports whether any --filter is a status= predicate, so ps
// knows to fetch all states (not just running) before filtering.
func hasStatusFilter(filters []string) bool {
	for _, fl := range filters {
		if strings.HasPrefix(fl, "status=") {
			return true
		}
	}
	return false
}

// ancestorMatches implements docker's `--filter ancestor=` against an image
// reference: it matches the repo, the repo:tag, or the full reference — NOT a
// loose substring (which wrongly matched `myalpine` for ancestor=alpine).
func ancestorMatches(reference, val string) bool {
	short := dockerfmt.ShortImage(reference)
	repo, tag := dockerfmt.SplitRepoTag(short)
	return val == reference || val == short || val == repo || val == repo+":"+tag
}

// applyFilters implements the common docker ps --filter predicates.
func applyFilters(list []dockerfmt.Container, filters []string) []dockerfmt.Container {
	if len(filters) == 0 {
		return list
	}
	var out []dockerfmt.Container
	for _, c := range list {
		keep := true
		for _, fl := range filters {
			kv := strings.SplitN(fl, "=", 2)
			if len(kv) != 2 {
				continue
			}
			key, val := kv[0], kv[1]
			switch key {
			case "status":
				if !matchStatusFilter(c.Status.State, val) {
					keep = false
				}
			case "name":
				if !strings.Contains(c.ID, val) {
					keep = false
				}
			case "id":
				if !strings.HasPrefix(c.ID, val) {
					keep = false
				}
			case "ancestor":
				if !ancestorMatches(c.Configuration.Image.Reference, val) {
					keep = false
				}
			case "label":
				lkv := strings.SplitN(val, "=", 2)
				got, ok := c.Configuration.Labels[lkv[0]]
				if !ok || (len(lkv) == 2 && got != lkv[1]) {
					keep = false
				}
			}
		}
		if keep {
			out = append(out, c)
		}
	}
	return out
}

// trimLast applies docker's -n/--last semantics: a negative value (the unset
// sentinel) leaves the list untouched; 0 shows nothing (header only); a
// positive n keeps the first n. Docker's `ps -n 0` shows zero containers — the
// old `last > 0` guard wrongly treated 0 like "unset" and listed everything.
func trimLast(list []dockerfmt.Container, last int) []dockerfmt.Container {
	switch {
	case last < 0:
		return list
	case last == 0:
		return nil
	case last < len(list):
		return list[:last]
	default:
		return list
	}
}

func newPsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ps [OPTIONS]",
		Short:   "List containers",
		Aliases: []string{},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			quiet, _ := cmd.Flags().GetBool("quiet")
			noTrunc, _ := cmd.Flags().GetBool("no-trunc")
			format, _ := cmd.Flags().GetString("format")
			filters, _ := cmd.Flags().GetStringArray("filter")
			last, _ := cmd.Flags().GetInt("last")
			latest, _ := cmd.Flags().GetBool("latest")

			// A status= filter must see all states, else `ps --filter status=exited`
			// (without -a) fetches only running containers and matches nothing.
			list, err := getContainers(all || latest || last > 0 || hasStatusFilter(filters))
			if err != nil {
				return err
			}
			list = applyFilters(list, filters)

			// Sort newest first (docker order).
			sort.SliceStable(list, func(i, j int) bool {
				ti, _ := dockerfmt.ParseTime(list[i].Configuration.CreationDate)
				tj, _ := dockerfmt.ParseTime(list[j].Configuration.CreationDate)
				return ti.After(tj)
			})
			if latest {
				last = 1
			}
			list = trimLast(list, last)

			views := make([]any, 0, len(list))
			for _, c := range list {
				views = append(views, buildPsView(c, noTrunc))
			}
			def := dockerfmt.TableDef{
				Headers: []string{"CONTAINER ID", "IMAGE", "COMMAND", "CREATED", "STATUS", "PORTS", "NAMES"},
				Row: func(v any) []string {
					p := v.(psView)
					return []string{p.ID, p.Image, p.Command, p.RunningFor, p.Status, p.Ports, p.Names}
				},
				ID: func(v any) string { return v.(psView).ID },
			}
			return dockerfmt.Render(format, quiet, views, def)
		},
	}
	f := cmd.Flags()
	f.BoolP("all", "a", false, "Show all containers (default shows just running)")
	f.BoolP("quiet", "q", false, "Only display container IDs")
	f.Bool("no-trunc", false, "Don't truncate output")
	f.String("format", "", "Format output using a Go template or 'json'")
	// StringArray (not StringSlice): a single --filter value legitimately
	// contains commas (e.g. label=team=a,b), which StringSlice would split.
	f.StringArrayP("filter", "f", nil, "Filter output based on conditions provided")
	f.IntP("last", "n", -1, "Show n last created containers (includes all states)")
	f.BoolP("latest", "l", false, "Show the latest created container")
	f.BoolP("size", "s", false, "Display total file sizes")
	return cmd
}
