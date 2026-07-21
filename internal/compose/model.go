// Package compose parses Docker Compose files and models them for translation
// into Apple `container` invocations.
package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Project is a parsed compose file.
type Project struct {
	Name     string                  `yaml:"name"`
	Services map[string]*Service     `yaml:"services"`
	Volumes  map[string]*VolumeSpec  `yaml:"volumes"`
	Networks map[string]*NetworkSpec `yaml:"networks"`

	// Dir is the directory the compose file lives in (for relative paths).
	Dir  string `yaml:"-"`
	File string `yaml:"-"`

	// Nets maps a compose network key to its resolved backend network name,
	// populated by the up/create flow before RunArgs is called.
	Nets map[string]string `yaml:"-"`
}

// Service is a single compose service definition (subset of the spec that the
// container backend can act on).
type Service struct {
	Image         string      `yaml:"image"`
	Build         Build       `yaml:"build"`
	ContainerName string      `yaml:"container_name"`
	Command       StringList  `yaml:"command"`
	Entrypoint    StringList  `yaml:"entrypoint"`
	Environment   EnvMap      `yaml:"environment"`
	EnvFile       EnvFile     `yaml:"env_file"`
	Ports         PortList    `yaml:"ports"`
	Expose        []string    `yaml:"expose"`
	Volumes       VolumeList  `yaml:"volumes"`
	Networks      StringKeys  `yaml:"networks"`
	DependsOn     DependsList `yaml:"depends_on"`
	Restart       string      `yaml:"restart"`
	Labels        MapList     `yaml:"labels"`
	WorkingDir    string      `yaml:"working_dir"`
	User          string      `yaml:"user"`
	Platform      string      `yaml:"platform"`
	CPUs          string      `yaml:"cpus"`
	MemLimit      string      `yaml:"mem_limit"`
	Privileged    bool        `yaml:"privileged"`
	CapAdd        []string    `yaml:"cap_add"`
	CapDrop       []string    `yaml:"cap_drop"`
	DNS           StringList  `yaml:"dns"`
	TTY           bool        `yaml:"tty"`
	StdinOpen     bool        `yaml:"stdin_open"`
	Init          *bool       `yaml:"init"`
	ShmSize       string      `yaml:"shm_size"`
	MacAddress    string      `yaml:"mac_address"`
	Tmpfs         StringList  `yaml:"tmpfs"`
	ReadOnly      bool        `yaml:"read_only"`
	Hostname      string      `yaml:"hostname"`
	Scale         int         `yaml:"scale"`
	Profiles      []string    `yaml:"profiles"`
	Ulimits       Ulimits     `yaml:"ulimits"`
	Deploy        Deploy      `yaml:"deploy"`
	Extends       ExtendsSpec `yaml:"extends"`
}

// ExtendsSpec captures `extends:` (string or {file, service} mapping) so its
// presence can be detected and warned about — dcon does not implement the
// extends merge, and silently dropping it left users baffled by a service
// missing its inherited config.
type ExtendsSpec struct {
	File    string `yaml:"file"`
	Service string `yaml:"service"`
	set     bool
}

func (e *ExtendsSpec) IsSet() bool { return e.set }

func (e *ExtendsSpec) UnmarshalYAML(value *yaml.Node) error {
	e.set = true
	value = deref(value)
	if value.Kind == yaml.ScalarNode { // `extends: other-service` short form
		e.Service = value.Value
		return nil
	}
	type raw ExtendsSpec
	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}
	*e = ExtendsSpec(r)
	e.set = true
	return nil
}

// Enabled reports whether a service runs given the set of active profiles. A
// service with no profiles always runs; one with profiles runs only if at least
// one is active.
func (s *Service) Enabled(active map[string]bool) bool {
	if len(s.Profiles) == 0 {
		return true
	}
	for _, p := range s.Profiles {
		if active[p] {
			return true
		}
	}
	return false
}

// Deploy models the subset of compose `deploy:` dcon can honor (replica count
// and resource limits). Replicas is a pointer so an explicit `replicas: 0` is
// distinguishable from unset (nil).
type Deploy struct {
	Replicas  *int `yaml:"replicas"`
	Resources struct {
		Limits       ResourceSpec `yaml:"limits"`
		Reservations ResourceSpec `yaml:"reservations"`
	} `yaml:"resources"`
}

type ResourceSpec struct {
	CPUs   string `yaml:"cpus"`
	Memory string `yaml:"memory"`
}

// Ulimits normalizes compose `ulimits:` short (name: N) and long
// (name: {soft, hard}) forms into sorted "name=value" / "name=soft:hard" specs.
type Ulimits []string

func (u *Ulimits) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return nil
	}
	raw := map[string]yaml.Node{}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		n := raw[k]
		switch n.Kind {
		case yaml.ScalarNode:
			out = append(out, k+"="+n.Value)
		case yaml.MappingNode:
			var m struct {
				Soft string `yaml:"soft"`
				Hard string `yaml:"hard"`
			}
			if err := n.Decode(&m); err != nil {
				return err
			}
			spec := m.Soft
			if m.Hard != "" {
				spec += ":" + m.Hard
			}
			out = append(out, k+"="+spec)
		}
	}
	*u = out
	return nil
}

// Build is `build:` which may be a string (context) or a mapping.
type Build struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
	Args       EnvMap `yaml:"args"`
	Target     string `yaml:"target"`
	set        bool
}

func (b *Build) IsSet() bool { return b.set }

func (b *Build) UnmarshalYAML(value *yaml.Node) error {
	b.set = true
	if value.Kind == yaml.ScalarNode {
		return value.Decode(&b.Context)
	}
	type raw Build
	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}
	*b = Build(r)
	b.set = true
	return nil
}

type VolumeSpec struct {
	Driver     string            `yaml:"driver"`
	DriverOpts map[string]string `yaml:"driver_opts"`
	External   bool              `yaml:"external"`
	Name       string            `yaml:"name"`
	Labels     MapList           `yaml:"labels"`
}

type NetworkSpec struct {
	Driver   string  `yaml:"driver"`
	External bool    `yaml:"external"`
	Name     string  `yaml:"name"`
	Internal bool    `yaml:"internal"`
	Labels   MapList `yaml:"labels"`
}

// deref follows YAML alias nodes to their anchored target. The manual node
// iterators below (EnvMap, EnvFile, decodeShortOrLong) walk node.Content
// directly, so an alias item (`- *tcp`) or aliased value arrives as an
// AliasNode whose .Value is empty — without dereferencing it the entry is
// silently dropped. yaml.v3's Decode does this automatically; hand-rolled
// iterators must do it themselves.
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// EnvFileEntry is one env_file reference; Required defaults to true.
type EnvFileEntry struct {
	Path     string
	Required bool
}

// EnvFile handles env_file in scalar, list-of-strings, and list-of-maps
// ({path, required}) forms.
type EnvFile []EnvFileEntry

func (e *EnvFile) UnmarshalYAML(value *yaml.Node) error {
	add := func(n yaml.Node) error {
		switch n.Kind {
		case yaml.ScalarNode:
			*e = append(*e, EnvFileEntry{Path: n.Value, Required: true})
		case yaml.MappingNode:
			var m struct {
				Path     string `yaml:"path"`
				Required *bool  `yaml:"required"`
			}
			if err := n.Decode(&m); err != nil {
				return err
			}
			req := true
			if m.Required != nil {
				req = *m.Required
			}
			*e = append(*e, EnvFileEntry{Path: m.Path, Required: req})
		}
		return nil
	}
	value = deref(value)
	switch value.Kind {
	case yaml.ScalarNode:
		return add(*value)
	case yaml.SequenceNode:
		for _, it := range value.Content {
			if err := add(*deref(it)); err != nil {
				return err
			}
		}
	}
	return nil
}

// StringList handles a YAML field that may be a scalar or a sequence.
type StringList []string

func (s *StringList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var str string
		if err := value.Decode(&str); err != nil {
			return err
		}
		*s = []string{str}
		return nil
	}
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	if list == nil {
		// An explicit empty sequence (`entrypoint: []`) must stay non-nil so it is
		// distinguishable from an unset field (nil) — the entrypoint-reset case
		// needs that distinction to clear the image ENTRYPOINT.
		list = []string{}
	}
	*s = list
	return nil
}

// MapList handles environment/labels/args that may be a map or a `k=v` list.
type MapList map[string]string

func (m *MapList) UnmarshalYAML(value *yaml.Node) error {
	out := map[string]string{}
	switch value.Kind {
	case yaml.MappingNode:
		raw := map[string]yaml.Node{}
		if err := value.Decode(&raw); err != nil {
			return err
		}
		for k, v := range raw {
			if v.Kind == yaml.ScalarNode {
				out[k] = v.Value
			}
		}
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		for _, item := range list {
			kv := strings.SplitN(item, "=", 2)
			// Skip blank / keyless entries (e.g. "" or "=value"): they would
			// otherwise inject a malformed empty-key `--env =…` arg.
			if kv[0] == "" {
				continue
			}
			if len(kv) == 2 {
				out[kv[0]] = kv[1]
			} else {
				out[kv[0]] = ""
			}
		}
	}
	*m = out
	return nil
}

// EnvMap is like MapList but with docker-compose environment/build-arg
// passthrough semantics: a bare key — list form `- FOO` or map form `FOO:`
// (null value) — inherits FOO from the host environment, and is OMITTED when
// the host doesn't set it. (MapList, used for labels, instead sets bare keys to
// "" — and emitting `--env FOO=` would clobber a host FOO with empty.)
type EnvMap map[string]string

func (m *EnvMap) UnmarshalYAML(value *yaml.Node) error {
	out := map[string]string{}
	value = deref(value)
	switch value.Kind {
	case yaml.MappingNode:
		expandEnvMapping(value, out)
	case yaml.SequenceNode:
		for _, it := range value.Content {
			it = deref(it)
			if it.Kind != yaml.ScalarNode {
				continue
			}
			kv := strings.SplitN(it.Value, "=", 2)
			if kv[0] == "" {
				continue
			}
			if len(kv) == 2 {
				out[kv[0]] = kv[1]
			} else if v, ok := os.LookupEnv(kv[0]); ok { // `- FOO` -> host passthrough
				out[kv[0]] = v
			}
		}
	}
	*m = out
	return nil
}

// expandEnvMapping walks a mapping node into out, expanding YAML merge keys
// (`<<: *anchor` / `<<: [*a, *b]`) by recursing into the dereferenced
// mapping(s). Merged keys are applied first so the mapping's own (local) keys
// win, per the YAML merge-key spec.
func expandEnvMapping(node *yaml.Node, out map[string]string) {
	// First pass: merge keys (defaults).
	for i := 0; i+1 < len(node.Content); i += 2 {
		kn, vn := node.Content[i], deref(node.Content[i+1])
		if kn.Value != "<<" && kn.Tag != "!!merge" {
			continue
		}
		switch vn.Kind {
		case yaml.MappingNode:
			expandEnvMapping(vn, out)
		case yaml.SequenceNode: // `<<: [*a, *b]`
			for _, it := range vn.Content {
				if it = deref(it); it.Kind == yaml.MappingNode {
					expandEnvMapping(it, out)
				}
			}
		}
	}
	// Second pass: local keys override merged ones.
	for i := 0; i+1 < len(node.Content); i += 2 {
		kn, vn := node.Content[i], deref(node.Content[i+1])
		k := kn.Value
		if k == "" || k == "<<" || kn.Tag == "!!merge" {
			continue
		}
		if vn.Kind == yaml.ScalarNode && vn.Tag == "!!null" {
			if v, ok := os.LookupEnv(k); ok { // `FOO:` -> host passthrough
				out[k] = v
			} else {
				delete(out, k) // an explicit null also masks a merged default
			}
			continue
		}
		if vn.Kind == yaml.ScalarNode {
			out[k] = vn.Value
		}
	}
}

// PortList handles compose `ports:` in both the short string form
// ("8080:80", "127.0.0.1:8080:80/tcp", "3000") and the long mapping form
// ({target, published, protocol, host_ip}), flattening the long form into the
// [host_ip:][published:]target[/protocol] string the translator emits via
// --publish. Without this, a sequence-of-maps fails to unmarshal into []string
// and breaks the entire compose file.
type PortList []string

func (p *PortList) UnmarshalYAML(value *yaml.Node) error {
	*p = decodeShortOrLong(value, flattenLongPort)
	return nil
}

func flattenLongPort(m map[string]string) string {
	target := m["target"]
	pub := m["published"]
	s := target
	if pub != "" {
		s = pub + ":" + target
	}
	if hip := m["host_ip"]; hip != "" {
		// A host_ip needs a published segment to be parsed as an IP (the 3-part
		// host_ip:host:container form). With no published port, emit the explicit
		// empty-published form host_ip::container — otherwise "host_ip:container"
		// is misread as host_port:container_port (host port = the IP literal).
		if pub != "" {
			s = hip + ":" + s
		} else {
			s = hip + "::" + target
		}
	}
	if proto := m["protocol"]; proto != "" {
		s += "/" + proto
	}
	return s
}

// VolumeList handles compose `volumes:` in both the short string form
// ("./data:/data", "named:/data:ro") and the long mapping form
// ({type, source, target, read_only}), flattening the long form into the
// [source:]target[:ro] string the translator emits via --volume.
type VolumeList []string

func (v *VolumeList) UnmarshalYAML(value *yaml.Node) error {
	*v = decodeShortOrLong(value, flattenLongVolume)
	return nil
}

// MarshalYAML renders the list for `compose config` without leaking the internal
// tmpfs sentinel (a NUL-byte marker that is invalid YAML): a flattened long-form
// tmpfs volume is emitted back as a `{type: tmpfs, target: <path>}` mapping;
// everything else is its plain short-form string.
func (v VolumeList) MarshalYAML() (any, error) {
	out := make([]any, 0, len(v))
	for _, vol := range v {
		if target, ok := strings.CutPrefix(vol, tmpfsVolumeMarker); ok {
			out = append(out, map[string]string{"type": "tmpfs", "target": target})
		} else {
			out = append(out, vol)
		}
	}
	return out, nil
}

// tmpfsVolumeMarker prefixes a flattened long-form volume whose type is tmpfs so
// RunArgs emits `--tmpfs <target>` instead of `--volume`. A NUL byte can't occur
// in a real mount spec, so it can't collide. Without this, `{type: tmpfs, target:
// /cache}` flattened to a bare "/cache" and became a disk-backed anonymous volume
// — silently losing the in-memory, ephemeral tmpfs semantics.
const tmpfsVolumeMarker = "\x00tmpfs\x00"

func flattenLongVolume(m map[string]string) string {
	if m["type"] == "tmpfs" {
		return tmpfsVolumeMarker + m["target"]
	}
	s := m["target"]
	if src := m["source"]; src != "" {
		s = src + ":" + s
	}
	// read_only is a YAML boolean; its node text may be true/True/TRUE (and 1).
	// Parse it rather than comparing to the lowercase literal, so a read-only
	// mount spelled `read_only: True` isn't silently emitted as writable.
	if b, err := strconv.ParseBool(m["read_only"]); err == nil && b {
		s += ":ro"
	}
	return s
}

// decodeShortOrLong flattens a compose sequence whose items are either scalar
// strings (short form, kept verbatim) or mappings (long form, flattened by fn).
// A bare scalar (single, unwrapped value) is also accepted. yaml scalar nodes
// carry their raw text in .Value, so numeric ports like `- 8080` and
// `target: 80` are read as "8080"/"80" without an int→string decode error.
func decodeShortOrLong(value *yaml.Node, fn func(map[string]string) string) []string {
	var out []string
	value = deref(value)
	switch value.Kind {
	case yaml.ScalarNode:
		out = append(out, value.Value)
	case yaml.SequenceNode:
		for _, it := range value.Content {
			it = deref(it)
			switch it.Kind {
			case yaml.ScalarNode:
				out = append(out, it.Value)
			case yaml.MappingNode:
				m := map[string]string{}
				for i := 0; i+1 < len(it.Content); i += 2 {
					m[it.Content[i].Value] = deref(it.Content[i+1]).Value
				}
				if s := fn(m); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// StringKeys handles networks which can be a list or a map (keyed by name).
type StringKeys []string

func (s *StringKeys) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*s = list
	case yaml.MappingNode:
		raw := map[string]yaml.Node{}
		if err := value.Decode(&raw); err != nil {
			return err
		}
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		*s = keys
	}
	return nil
}

// DependsList handles depends_on as a list or a conditions map. Long-form
// conditions other than service_started (service_healthy,
// service_completed_successfully) cannot be honored — the backend exposes no
// health or exit state — so they degrade to plain start ordering with a
// one-time warning instead of silently.
type DependsList []string

func (d *DependsList) UnmarshalYAML(value *yaml.Node) error {
	value = deref(value)
	switch value.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*d = list
	case yaml.MappingNode:
		raw := map[string]struct {
			Condition string `yaml:"condition"`
		}{}
		if err := value.Decode(&raw); err != nil {
			return err
		}
		keys := make([]string, 0, len(raw))
		for k, v := range raw {
			keys = append(keys, k)
			if v.Condition != "" && v.Condition != "service_started" {
				warnOnce("depends-condition-"+v.Condition,
					"depends_on condition %q is not supported by the backend; falling back to service_started (start ordering only)", v.Condition)
			}
		}
		sort.Strings(keys)
		*d = keys
	}
	return nil
}

// candidateFiles are the default compose filenames in priority order.
var candidateFiles = []string{
	"compose.yaml", "compose.yml",
	"docker-compose.yaml", "docker-compose.yml",
}

// overrideFiles are the conventional override filenames auto-merged on top of
// the base file when no -f is given, like docker compose.
var overrideFiles = []string{
	"compose.override.yaml", "compose.override.yml",
	"docker-compose.override.yaml", "docker-compose.override.yml",
}

// FindFiles resolves the compose file set, in merge order: the explicit -f
// paths when given; else the COMPOSE_FILE environment variable (split on
// COMPOSE_PATH_SEPARATOR, default ":"); else the first conventional filename
// in the working directory plus, when present, the first conventional
// override file (compose.override.yaml and friends).
func FindFiles(explicit []string) ([]string, error) {
	if len(explicit) == 0 {
		if cf := os.Getenv("COMPOSE_FILE"); cf != "" {
			sep := os.Getenv("COMPOSE_PATH_SEPARATOR")
			if sep == "" {
				sep = ":"
			}
			for _, part := range strings.Split(cf, sep) {
				if part != "" {
					explicit = append(explicit, part)
				}
			}
		}
	}
	if len(explicit) > 0 {
		out := make([]string, 0, len(explicit))
		for _, e := range explicit {
			abs, err := filepath.Abs(e)
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(abs); err != nil {
				return nil, fmt.Errorf("compose file %q not found", e)
			}
			out = append(out, abs)
		}
		return out, nil
	}
	for _, name := range candidateFiles {
		if _, err := os.Stat(name); err != nil {
			continue
		}
		abs, _ := filepath.Abs(name)
		out := []string{abs}
		for _, o := range overrideFiles {
			if _, err := os.Stat(o); err == nil {
				oabs, _ := filepath.Abs(o)
				out = append(out, oabs)
				break
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("no compose file found (looked for %s)", strings.Join(candidateFiles, ", "))
}

// Find locates a single compose file, honouring an explicit path (no override
// auto-load, no COMPOSE_FILE). Kept for callers that need exactly one path.
func Find(explicit string) (string, error) {
	if explicit != "" {
		paths, err := FindFiles([]string{explicit})
		if err != nil {
			return "", err
		}
		return paths[0], nil
	}
	for _, name := range candidateFiles {
		if _, err := os.Stat(name); err == nil {
			abs, _ := filepath.Abs(name)
			return abs, nil
		}
	}
	return "", fmt.Errorf("no compose file found (looked for %s)", strings.Join(candidateFiles, ", "))
}

// Load parses one compose file (see LoadFiles).
func Load(path, projectOverride string) (*Project, error) {
	return LoadFiles([]string{path}, projectOverride, nil)
}

// LoadFiles parses and merges one or more compose files. Later files override
// earlier ones with docker's merge semantics: scalars replace, mappings merge
// recursively (so environment/labels merge by key), sequences concatenate —
// except command/entrypoint, which docker treats as single-valued and
// replaces; volume mounts are keyed by container target (a later mount of the
// same target replaces the earlier one).
//
// ${VAR} interpolation consults the OS environment first, then the env files
// (the envFiles paths when given, else <dir>/.env), matching docker compose
// precedence. The project name comes from, in order: the explicit override
// (-p), COMPOSE_PROJECT_NAME (environment or env file), the top-level
// `name:`, the compose directory name.
func LoadFiles(paths []string, projectOverride string, envFiles []string) (*Project, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no compose file given")
	}
	dir := filepath.Dir(paths[0])
	lookup, err := envLookup(dir, envFiles)
	if err != nil {
		return nil, err
	}

	var merged *yaml.Node
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		expanded, err := expandString(string(data), lookup)
		if err != nil {
			return nil, err
		}
		doc := &yaml.Node{}
		if err := yaml.Unmarshal([]byte(expanded), doc); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		if merged == nil {
			merged = doc
			continue
		}
		mergeDocs(merged, doc)
	}

	var p Project
	if merged != nil && len(merged.Content) > 0 {
		if err := merged.Decode(&p); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", paths[0], err)
		}
	}
	// Drop empty/null service bodies (`web:` with no mapping under it), which yaml
	// decodes to a present key with a nil *Service. They are not runnable services;
	// removing them here spares every command (up/pull/push/down/run/scale and any
	// future one that iterates p.Services) a nil-pointer dereference, matching how
	// Order()/Levels() already skip them. A lookup of a dropped name then cleanly
	// reports "no such service" instead of panicking.
	for name, svc := range p.Services {
		if svc == nil {
			delete(p.Services, name)
		}
	}
	p.File = paths[0]
	p.Dir = dir
	if projectOverride != "" {
		p.Name = projectOverride
	} else if v, ok := lookup("COMPOSE_PROJECT_NAME"); ok && v != "" {
		p.Name = SanitizeName(v)
	}
	if p.Name == "" {
		p.Name = SanitizeName(filepath.Base(p.Dir))
	}
	dedupeVolumeTargets(&p)
	warnUnsupported(&p)
	return &p, nil
}

// mergeDocs merges the src compose document into dst (both document nodes).
func mergeDocs(dst, src *yaml.Node) {
	if len(src.Content) == 0 {
		return
	}
	if len(dst.Content) == 0 {
		dst.Content = src.Content
		return
	}
	d, s := deref(dst.Content[0]), deref(src.Content[0])
	if d.Kind == yaml.MappingNode && s.Kind == yaml.MappingNode {
		mergeMappings(d, s)
	} else {
		dst.Content[0] = src.Content[0]
	}
}

// replaceListKeys are keys whose sequence value docker treats as single-valued
// in the multi-file merge: the later file replaces it instead of concatenating.
var replaceListKeys = map[string]bool{"command": true, "entrypoint": true}

// mergeMappings merges src's entries into dst per docker's multi-file rules:
// same-key mappings merge recursively, same-key sequences concatenate (except
// replaceListKeys), anything else from src replaces, and new keys append.
func mergeMappings(dst, src *yaml.Node) {
	for i := 0; i+1 < len(src.Content); i += 2 {
		k, v := src.Content[i], src.Content[i+1]
		found := false
		for j := 0; j+1 < len(dst.Content); j += 2 {
			if dst.Content[j].Value != k.Value {
				continue
			}
			found = true
			dv, sv := deref(dst.Content[j+1]), deref(v)
			switch {
			case dv.Kind == yaml.MappingNode && sv.Kind == yaml.MappingNode:
				mergeMappings(dv, sv)
			case dv.Kind == yaml.SequenceNode && sv.Kind == yaml.SequenceNode && !replaceListKeys[k.Value]:
				dv.Content = append(dv.Content, sv.Content...)
			default:
				dst.Content[j+1] = v
			}
			break
		}
		if !found {
			dst.Content = append(dst.Content, k, v)
		}
	}
}

// dedupeVolumeTargets keeps only the last volume spec per container target in
// each service: docker's multi-file merge keys volumes by target (an override
// file remaps the mount), and the sequence concat in mergeMappings would
// otherwise double-mount the target.
func dedupeVolumeTargets(p *Project) {
	for _, svc := range p.Services {
		if svc == nil || len(svc.Volumes) < 2 {
			continue
		}
		seen := map[string]int{} // target -> index in out
		out := svc.Volumes[:0]
		for _, v := range svc.Volumes {
			t := volumeTarget(v)
			if j, ok := seen[t]; ok && t != "" {
				out[j] = v // later spec replaces the earlier mount of the same target
				continue
			}
			seen[t] = len(out)
			out = append(out, v)
		}
		svc.Volumes = out
	}
}

// volumeTarget extracts the container path from a flattened volume spec.
func volumeTarget(spec string) string {
	if t, ok := strings.CutPrefix(spec, tmpfsVolumeMarker); ok {
		return t
	}
	parts := strings.Split(spec, ":")
	if len(parts) == 1 {
		return parts[0] // anonymous volume: the spec IS the target
	}
	return parts[1]
}

// warnUnsupported emits one-time warnings for parsed-but-unimplemented
// service options that would otherwise be silently dropped.
func warnUnsupported(p *Project) {
	names := make([]string, 0, len(p.Services))
	for n := range p.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		svc := p.Services[n]
		if svc != nil && svc.Extends.IsSet() {
			warnOnce("extends-"+n, "service %q uses extends:, which dcon does not support; the extends configuration is IGNORED", n)
		}
	}
}

// envLookup builds the interpolation resolver: OS environment first, then the
// env files (explicit paths when given — replacing the default <dir>/.env —
// with later files winning), matching docker compose precedence.
func envLookup(dir string, envFiles []string) (func(string) (string, bool), error) {
	fileEnv := map[string]string{}
	if len(envFiles) == 0 {
		def := filepath.Join(dir, ".env")
		if _, err := os.Stat(def); err == nil {
			m, err := ParseEnvFile(def)
			if err != nil {
				return nil, err
			}
			fileEnv = m
		}
	}
	for _, f := range envFiles {
		m, err := ParseEnvFile(f)
		if err != nil {
			return nil, fmt.Errorf("env file %s: %w", f, err)
		}
		for k, v := range m {
			fileEnv[k] = v
		}
	}
	return func(key string) (string, bool) {
		if v, ok := os.LookupEnv(key); ok {
			return v, true
		}
		v, ok := fileEnv[key]
		return v, ok
	}, nil
}

// expandString interpolates $VAR / ${VAR...} throughout s using lookup. Unlike
// os.Expand it is brace-depth aware, so nested defaults like
// ${IMG:-nginx:${TAG}} resolve instead of splitting at the first "}". "$$"
// escapes a literal "$".
func expandString(s string, lookup func(string) (string, bool)) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if c != '$' || i+1 >= len(s) {
			b.WriteByte(c)
			i++
			continue
		}
		switch {
		case s[i+1] == '$': // $$ escape
			b.WriteByte('$')
			i += 2
		case s[i+1] == '{':
			depth, j := 1, i+2
			for ; j < len(s); j++ {
				if s[j] == '$' && j+1 < len(s) && s[j+1] == '{' {
					depth++
					j++
					continue
				}
				if s[j] == '}' {
					if depth--; depth == 0 {
						break
					}
				}
			}
			if depth != 0 { // unterminated ${...}: keep literally
				b.WriteString(s[i:])
				return b.String(), nil
			}
			v, err := expandBody(s[i+2:j], lookup)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
			i = j + 1
		case isNameChar(s[i+1]):
			j := i + 1
			for j < len(s) && isNameChar(s[j]) {
				j++
			}
			v, _ := lookup(s[i+1 : j])
			b.WriteString(v)
			i = j
		default: // "$-", "$ ", ...: literal $
			b.WriteByte('$')
			i++
		}
	}
	return b.String(), nil
}

// expandBody resolves one ${...} body using docker-compose / bash
// parameter-expansion semantics. Supported:
//
//	${VAR}               -> value (empty if unset)
//	${VAR:-d} / ${VAR-d} -> value, or d when unset (:- also when empty)
//	${VAR:=d} / ${VAR=d} -> same substitution as :- here (no real assignment)
//	${VAR:+a} / ${VAR+a} -> a when set (:+ requires non-empty), else ""
//	${VAR:?m} / ${VAR?m} -> value when set, else an error (m is the message)
//
// The operator argument is itself interpolated (only when used), so nested
// forms like ${IMG:-nginx:${TAG}} work.
func expandBody(body string, lookup func(string) (string, bool)) (string, error) {
	i := 0
	for i < len(body) && isNameChar(body[i]) {
		i++
	}
	name, rest := body[:i], body[i:]
	if name == "" {
		v, _ := lookup(body)
		return v, nil // unrecognized form: best-effort
	}
	val, set := lookup(name)
	if rest == "" {
		return val, nil // ${VAR}
	}
	colon := rest[0] == ':'
	if colon {
		rest = rest[1:]
	}
	if rest == "" {
		return val, nil
	}
	op, arg := rest[0], rest[1:]
	// With a colon, an empty value counts as "not present" (bash semantics).
	present := set && (!colon || val != "")
	switch op {
	case '-', '=':
		if present {
			return val, nil
		}
		return expandString(arg, lookup)
	case '+':
		if present {
			return expandString(arg, lookup)
		}
		return "", nil
	case '?':
		if present {
			return val, nil
		}
		msg := arg
		if msg == "" {
			msg = "required variable is not set"
		}
		return "", fmt.Errorf("compose variable %q: %s", name, msg)
	default:
		v, _ := lookup(body)
		return v, nil // unknown operator: best-effort
	}
}

// expandVar resolves one interpolation body against the OS environment alone;
// retained as the historical entry point (expandBody is the general form).
func expandVar(key string) (string, error) {
	if key == "$" { // $$ escape (os.Expand-style body)
		return "$", nil
	}
	return expandBody(key, os.LookupEnv)
}

func isNameChar(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// SanitizeName makes a string safe for use as a container/network/volume name.
func SanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		out = "default"
	}
	return out
}

// Order returns service names in dependency (topological) order. Cycles fall
// back to alphabetical to avoid deadlock.
func (p *Project) Order() []string {
	visited := map[string]bool{}
	temp := map[string]bool{}
	var order []string
	var visit func(string)
	visit = func(name string) {
		if visited[name] || temp[name] {
			return
		}
		svc, ok := p.Services[name]
		if !ok || svc == nil {
			// Undefined dependency (typo / external), or a present-but-empty
			// service body (`web:` with nothing under it, which yaml decodes to a
			// nil *Service) — never emit it as a service to start, so callers
			// don't dereference a nil *Service. Skipping instead of panicking with
			// a stack trace on a half-edited compose file.
			visited[name] = true
			return
		}
		temp[name] = true
		deps := append([]string{}, svc.DependsOn...)
		sort.Strings(deps)
		for _, d := range deps {
			if _, ok := p.Services[d]; !ok {
				// Keep the no-panic skip, but say so: a typo'd depends_on
				// otherwise vanishes without a trace.
				warnOnce("missing-dep-"+name+"/"+d,
					"service %q depends on undefined service %q; ignoring the dependency", name, d)
				continue
			}
			visit(d)
		}
		temp[name] = false
		visited[name] = true
		order = append(order, name)
	}
	names := make([]string, 0, len(p.Services))
	for n := range p.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		visit(n)
	}
	return order
}

// Levels groups services into dependency levels: every service in level i
// depends only on services in earlier levels, so all services within a level
// have no ordering constraint between them and can be brought up concurrently.
// Built on Order(), so undefined deps and cycles degrade safely without
// deadlocking: a cycle's members are still all emitted and started, but (because
// Order breaks the cycle's back-edge) they land on successive non-zero levels
// with an empty leading level rather than collapsing to level 0. The empty level
// is a harmless no-op in the up loop. Returns nil for an empty project.
func (p *Project) Levels() [][]string {
	order := p.Order()
	if len(order) == 0 {
		return nil
	}
	level := make(map[string]int, len(order))
	maxLevel := 0
	for _, name := range order {
		svc := p.Services[name]
		lv := 0
		for _, d := range svc.DependsOn {
			if dep, ok := p.Services[d]; !ok || dep == nil {
				continue // undefined or empty (nil) dependency: ignored, as in Order()
			}
			if level[d]+1 > lv {
				lv = level[d] + 1
			}
		}
		level[name] = lv
		if lv > maxLevel {
			maxLevel = lv
		}
	}
	levels := make([][]string, maxLevel+1)
	for _, name := range order { // preserves Order()'s deterministic sort within a level
		levels[level[name]] = append(levels[level[name]], name)
	}
	return levels
}
