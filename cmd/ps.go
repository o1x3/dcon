package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
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

// dockerState maps the backend's state vocabulary onto docker's .State enum
// (created|running|paused|restarting|removing|exited|dead) so templates like
// `--format '{{.State}}'` see docker's words, not the backend's. It is the
// inverse of matchStatusFilter's mapping.
func dockerState(state string) string {
	switch state {
	case "stopped":
		return "exited"
	case "stopping":
		return "removing"
	case "", "unknown":
		return "created"
	default:
		return state
	}
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
		State:        dockerState(c.Status.State),
		LocalVolumes: fmt.Sprint(localVols),
	}
}

// hasFilterKey reports whether any --filter uses the given key.
func hasFilterKey(filters []string, key string) bool {
	for _, fl := range filters {
		if strings.HasPrefix(fl, key+"=") {
			return true
		}
	}
	return false
}

// hasStatusFilter reports whether any --filter is a status= predicate, so ps
// knows to fetch all states (not just running) before filtering.
func hasStatusFilter(filters []string) bool { return hasFilterKey(filters, "status") }

// hasTimeFilter reports whether any --filter is before=/since=, whose
// reference container may be in any state and so needs the all-states fetch.
func hasTimeFilter(filters []string) bool {
	return hasFilterKey(filters, "before") || hasFilterKey(filters, "since")
}

// ancestorMatches implements docker's `--filter ancestor=` against an image
// reference: it matches the repo, the repo:tag, or the full reference — NOT a
// loose substring (which wrongly matched `myalpine` for ancestor=alpine).
func ancestorMatches(reference, val string) bool {
	// Normalize both sides: a fully-qualified filter (docker.io/library/alpine)
	// must still match a container whose stored ref is already shortened (alpine),
	// and vice-versa, so shorten val too before comparing.
	shortRef := dockerfmt.ShortImage(reference)
	shortVal := dockerfmt.ShortImage(val)
	repo, tag := dockerfmt.SplitRepoTag(shortRef)
	return shortVal == shortRef || shortVal == repo || shortVal == repo+":"+tag
}

// validStatusFilterValues is docker's status enum plus the backend's own
// vocabulary (accepted as a convenience). Anything else is an error, like
// docker: a typo such as `--filter exited=0` or `status=exted` silently
// matching everything is catastrophic in `dcon rm $(dcon ps -aq --filter …)`.
var validStatusFilterValues = map[string]bool{
	"created": true, "restarting": true, "running": true, "removing": true,
	"paused": true, "exited": true, "dead": true,
	// backend names, matched verbatim by matchStatusFilter's fallback
	"stopped": true, "stopping": true, "unknown": true,
}

// portRange is a parsed publish= filter value: a container-port range plus
// protocol.
type portRange struct {
	lo, hi int
	proto  string
}

// parsePublishFilter parses docker's publish filter forms: <port>[/<proto>]
// or <startport-endport>[/<proto>].
func parsePublishFilter(val string) (portRange, error) {
	pr := portRange{proto: "tcp"}
	spec := val
	if i := strings.Index(spec, "/"); i >= 0 {
		pr.proto = strings.ToLower(spec[i+1:])
		spec = spec[:i]
	}
	lo, hi := spec, spec
	if i := strings.Index(spec, "-"); i >= 0 {
		lo, hi = spec[:i], spec[i+1:]
	}
	var err error
	if pr.lo, err = strconv.Atoi(lo); err != nil {
		return pr, fmt.Errorf("invalid filter 'publish=%s'", val)
	}
	if pr.hi, err = strconv.Atoi(hi); err != nil {
		return pr, fmt.Errorf("invalid filter 'publish=%s'", val)
	}
	if pr.lo > pr.hi {
		return pr, fmt.Errorf("invalid filter 'publish=%s'", val)
	}
	return pr, nil
}

// publishMatches mirrors docker's publish= filter: the container publishes the
// given container-side port (docker matches the PortBindings key, which is the
// private port), optionally constrained by range and protocol.
func publishMatches(c dockerfmt.Container, pr portRange) bool {
	for _, p := range c.Configuration.Ports {
		proto := p.Proto
		if proto == "" {
			proto = "tcp"
		}
		if !strings.EqualFold(proto, pr.proto) {
			continue
		}
		cnt := p.Count
		if cnt < 1 {
			cnt = 1
		}
		if p.ContainerPort <= pr.hi && p.ContainerPort+cnt-1 >= pr.lo {
			return true
		}
	}
	return false
}

// containerOnNetwork reports whether the container is attached to the named
// network (configured or runtime attachment).
func containerOnNetwork(c dockerfmt.Container, name string) bool {
	for _, n := range c.Configuration.Networks {
		if n.Network == name {
			return true
		}
	}
	for _, n := range c.Status.Networks {
		if n.Network == name {
			return true
		}
	}
	return false
}

// volumeMatches mirrors docker's volume= filter: the value matches a mount's
// volume name (source) or its destination path.
func volumeMatches(c dockerfmt.Container, val string) bool {
	for _, m := range c.Configuration.Mounts {
		if m.Source == val || m.Destination == val {
			return true
		}
	}
	return false
}

// containerCreatedTime resolves a before=/since= reference (exact name/ID or
// ID prefix) to that container's creation time. Docker errors when the
// reference container does not exist; so do we.
func containerCreatedTime(list []dockerfmt.Container, val string) (time.Time, error) {
	var found *dockerfmt.Container
	for i := range list {
		if list[i].ID == val {
			found = &list[i]
			break
		}
		if found == nil && strings.HasPrefix(list[i].ID, val) {
			found = &list[i]
		}
	}
	if found == nil {
		return time.Time{}, fmt.Errorf("no such container: %s", val)
	}
	t, ok := dockerfmt.ParseTime(found.Configuration.CreationDate)
	if !ok {
		return time.Time{}, fmt.Errorf("container %s has no creation time", val)
	}
	return t, nil
}

// psFilterCtx holds the prevalidated --filter state: compiled name regexes,
// parsed publish ranges, and resolved before=/since= cut-off times, keyed by
// the raw filter value so the per-container loop can look them up.
type psFilterCtx struct {
	nameRe  map[string]*regexp.Regexp
	publish map[string]portRange
	before  map[string]time.Time
	since   map[string]time.Time
	exclude bool // a recognized-but-unsupported filter matches nothing
}

// prepareFilters validates every --filter up front, once — invalid values
// error (like docker), recognized-but-unsupported docker keys (exited, health)
// warn and exclude everything so destructive `rm $(ps -q --filter …)`
// pipelines fail safe, and truly unknown keys warn loudly.
func prepareFilters(list []dockerfmt.Container, filters []string) (psFilterCtx, error) {
	fctx := psFilterCtx{
		nameRe:  map[string]*regexp.Regexp{},
		publish: map[string]portRange{},
		before:  map[string]time.Time{},
		since:   map[string]time.Time{},
	}
	warned := map[string]bool{}
	for _, fl := range filters {
		kv := strings.SplitN(fl, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := kv[0], kv[1]
		switch key {
		case "status":
			if !validStatusFilterValues[strings.ToLower(val)] {
				return fctx, fmt.Errorf("invalid filter 'status=%s'", val)
			}
		case "name":
			// Docker matches names as an unanchored regex (substring by
			// default, ^web$ anchors work); an invalid pattern is an error.
			re, err := regexp.Compile(val)
			if err != nil {
				return fctx, fmt.Errorf("invalid filter 'name=%s': %v", val, err)
			}
			fctx.nameRe[val] = re
		case "publish":
			pr, err := parsePublishFilter(val)
			if err != nil {
				return fctx, err
			}
			fctx.publish[val] = pr
		case "before", "since":
			t, err := containerCreatedTime(list, val)
			if err != nil {
				return fctx, err
			}
			if key == "before" {
				fctx.before[val] = t
			} else {
				fctx.since[val] = t
			}
		case "id", "ancestor", "label", "network", "volume":
			// matched per container below
		case "exited", "health":
			fctx.exclude = true
			if !warned[key] {
				warned[key] = true
				fmt.Fprintf(os.Stderr, "dcon: warning: ps filter %q is not supported by the backend; matching no containers\n", key)
			}
		default:
			if !warned[key] {
				warned[key] = true
				fmt.Fprintf(os.Stderr, "dcon: warning: unknown ps filter %q was ignored\n", key)
			}
		}
	}
	return fctx, nil
}

// applyFilters implements the docker ps --filter predicates docker's daemon
// evaluates server-side, entirely from the already-fetched container JSON.
// Docker OR-combines repeated values of the SAME key and AND-combines across
// DISTINCT keys; the special `label` key is AND-combined (every label
// predicate must match). A naive "set keep=false on any failing predicate"
// loop would AND same-key filters too, so `--filter status=running --filter
// status=exited` (Docker's union idiom) would wrongly return nothing.
func applyFilters(list []dockerfmt.Container, filters []string) ([]dockerfmt.Container, error) {
	if len(filters) == 0 {
		return list, nil
	}
	fctx, err := prepareFilters(list, filters)
	if err != nil {
		return nil, err
	}
	if fctx.exclude {
		return nil, nil
	}
	byKey := map[string][]string{}
	var labels []string
	for _, fl := range filters {
		kv := strings.SplitN(fl, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if kv[0] == "label" {
			labels = append(labels, kv[1])
			continue
		}
		byKey[kv[0]] = append(byKey[kv[0]], kv[1])
	}
	matchKey := func(c dockerfmt.Container, key, val string) bool {
		switch key {
		case "status":
			return matchStatusFilter(c.Status.State, val)
		case "name":
			// Docker matches names as an unanchored regex (substring by
			// default, ^web$ anchors work); compiled in prepareFilters.
			return fctx.nameRe[val].MatchString(c.ID)
		case "id":
			return strings.HasPrefix(c.ID, val)
		case "ancestor":
			return ancestorMatches(c.Configuration.Image.Reference, val)
		case "network":
			return containerOnNetwork(c, val)
		case "publish":
			return publishMatches(c, fctx.publish[val])
		case "volume":
			return volumeMatches(c, val)
		case "before":
			t, ok := dockerfmt.ParseTime(c.Configuration.CreationDate)
			return ok && t.Before(fctx.before[val])
		case "since":
			t, ok := dockerfmt.ParseTime(c.Configuration.CreationDate)
			return ok && t.After(fctx.since[val])
		}
		return true // unknown key: warned in prepareFilters, never excludes
	}
	var out []dockerfmt.Container
	for _, c := range list {
		keep := true
		// Across distinct keys: AND. Within a key: OR (match ANY value).
		for key, vals := range byKey {
			matched := false
			for _, v := range vals {
				if matchKey(c, key, v) {
					matched = true
					break
				}
			}
			if !matched {
				keep = false
				break
			}
		}
		// label predicates: AND (every one must match).
		for _, lv := range labels {
			if !keep {
				break
			}
			lkv := strings.SplitN(lv, "=", 2)
			got, ok := c.Configuration.Labels[lkv[0]]
			if !ok || (len(lkv) == 2 && got != lkv[1]) {
				keep = false
			}
		}
		if keep {
			out = append(out, c)
		}
	}
	return out, nil
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
			// before=/since= also need the all-states fetch: the reference
			// container may be stopped.
			timeFilter := hasTimeFilter(filters)
			list, err := getContainers(all || latest || last > 0 || hasStatusFilter(filters) || timeFilter)
			if err != nil {
				return err
			}
			list, err = applyFilters(list, filters)
			if err != nil {
				return err
			}
			// If only before=/since= forced the all-states fetch, docker still
			// lists just running containers without -a; drop the rest.
			if timeFilter && !all && !latest && last <= 0 && !hasStatusFilter(filters) {
				var running []dockerfmt.Container
				for _, c := range list {
					if c.Status.State == "running" {
						running = append(running, c)
					}
				}
				list = running
			}

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
			// docker ps -s appends a trailing SIZE column; the default (non -s)
			// table is left exactly as-is to preserve byte-identity.
			size, _ := cmd.Flags().GetBool("size")
			headers := []string{"CONTAINER ID", "IMAGE", "COMMAND", "CREATED", "STATUS", "PORTS", "NAMES"}
			rowFn := func(v any) []string {
				p := v.(psView)
				return []string{p.ID, p.Image, p.Command, p.RunningFor, p.Status, p.Ports, p.Names}
			}
			if size {
				headers = append(headers, "SIZE")
				rowFn = func(v any) []string {
					p := v.(psView)
					return []string{p.ID, p.Image, p.Command, p.RunningFor, p.Status, p.Ports, p.Names, p.Size}
				}
			}
			def := dockerfmt.TableDef{
				Headers: headers,
				Row:     rowFn,
				ID:      func(v any) string { return v.(psView).ID },
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
