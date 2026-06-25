// Package compose parses Docker Compose files and models them for translation
// into Apple `container` invocations.
package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	Tmpfs         StringList  `yaml:"tmpfs"`
	ReadOnly      bool        `yaml:"read_only"`
	Hostname      string      `yaml:"hostname"`
	Scale         int         `yaml:"scale"`
	Profiles      []string    `yaml:"profiles"`
	Ulimits       Ulimits     `yaml:"ulimits"`
	Deploy        Deploy      `yaml:"deploy"`
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

// Deploy models the subset of compose `deploy:` dcon can honor (resource limits).
type Deploy struct {
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
	switch value.Kind {
	case yaml.ScalarNode:
		return add(*value)
	case yaml.SequenceNode:
		for _, it := range value.Content {
			if err := add(*it); err != nil {
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
	switch value.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(value.Content); i += 2 {
			k := value.Content[i].Value
			vn := value.Content[i+1]
			if k == "" {
				continue
			}
			if vn.Kind == yaml.ScalarNode && vn.Tag == "!!null" {
				if v, ok := os.LookupEnv(k); ok { // `FOO:` -> host passthrough
					out[k] = v
				}
				continue
			}
			if vn.Kind == yaml.ScalarNode {
				out[k] = vn.Value
			}
		}
	case yaml.SequenceNode:
		for _, it := range value.Content {
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

func flattenLongVolume(m map[string]string) string {
	s := m["target"]
	if src := m["source"]; src != "" {
		s = src + ":" + s
	}
	if m["read_only"] == "true" {
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
	switch value.Kind {
	case yaml.ScalarNode:
		out = append(out, value.Value)
	case yaml.SequenceNode:
		for _, it := range value.Content {
			switch it.Kind {
			case yaml.ScalarNode:
				out = append(out, it.Value)
			case yaml.MappingNode:
				m := map[string]string{}
				for i := 0; i+1 < len(it.Content); i += 2 {
					m[it.Content[i].Value] = it.Content[i+1].Value
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

// DependsList handles depends_on as a list or a conditions map.
type DependsList []string

func (d *DependsList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*d = list
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
		*d = keys
	}
	return nil
}

// candidateFiles are the default compose filenames in priority order.
var candidateFiles = []string{
	"compose.yaml", "compose.yml",
	"docker-compose.yaml", "docker-compose.yml",
}

// Find locates the compose file, honouring an explicit path.
func Find(explicit string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("compose file %q not found", explicit)
		}
		return abs, nil
	}
	for _, name := range candidateFiles {
		if _, err := os.Stat(name); err == nil {
			abs, _ := filepath.Abs(name)
			return abs, nil
		}
	}
	return "", fmt.Errorf("no compose file found (looked for %s)", strings.Join(candidateFiles, ", "))
}

// Load parses the compose file at path, defaulting the project name to the
// containing directory when not set.
func Load(path, projectOverride string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Expand ${VAR} / $VAR from the environment, like compose does.
	expanded := os.Expand(string(data), func(key string) string {
		// support ${VAR:-default}
		if i := strings.Index(key, ":-"); i >= 0 {
			name, def := key[:i], key[i+2:]
			if v, ok := os.LookupEnv(name); ok {
				return v
			}
			return def
		}
		return os.Getenv(key)
	})

	var p Project
	if err := yaml.Unmarshal([]byte(expanded), &p); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	p.File = path
	p.Dir = filepath.Dir(path)
	if projectOverride != "" {
		p.Name = projectOverride
	}
	if p.Name == "" {
		p.Name = SanitizeName(filepath.Base(p.Dir))
	}
	return &p, nil
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
		if !ok {
			// Undefined dependency (typo / external) — never emit it as a
			// service to start, so callers don't index a nil *Service.
			visited[name] = true
			return
		}
		temp[name] = true
		deps := append([]string{}, svc.DependsOn...)
		sort.Strings(deps)
		for _, d := range deps {
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
			if _, ok := p.Services[d]; !ok {
				continue // undefined dependency: ignored, as in Order()
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
