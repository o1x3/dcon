package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"dcon/internal/netflag"
)

// sortedKeys returns the keys of m in lexical order so generated arg lists are
// deterministic across runs (map iteration order is randomized in Go).
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Compose label keys (compatible with Docker Compose so `dcon ps` and tooling
// can recognise project membership).
const (
	LabelProject   = "com.docker.compose.project"
	LabelService   = "com.docker.compose.service"
	LabelNumber    = "com.docker.compose.container-number"
	LabelOneoff    = "com.docker.compose.oneoff"
	LabelConfigDir = "com.docker.compose.project.working_dir"

	// LabelConfigHash fingerprints the generated run-arg list so `up` can
	// recreate a container when its compose config changed (the
	// com.docker.compose.config-hash pattern).
	LabelConfigHash = "dcon.compose.confighash"
)

// configHash fingerprints a generated arg list (computed before the hash
// label itself is inserted, so it is stable and comparable).
func configHash(args []string) string {
	h := sha256.Sum256([]byte(strings.Join(args, "\x1f")))
	return hex.EncodeToString(h[:])
}

// ConfigHashFromArgs extracts the LabelConfigHash value stamped into a
// generated arg list, or "" when absent.
func ConfigHashFromArgs(args []string) string {
	for i, a := range args {
		if a == "--label" && i+1 < len(args) {
			if v, ok := strings.CutPrefix(args[i+1], LabelConfigHash+"="); ok {
				return v
			}
		}
	}
	return ""
}

// ContainerName returns the conventional compose container name.
func (p *Project) ContainerName(service string, index int, svc *Service) string {
	if svc != nil && svc.ContainerName != "" {
		return svc.ContainerName
	}
	return fmt.Sprintf("%s-%s-%d", p.Name, service, index)
}

// DefaultNetwork is the implicit per-project network name.
func (p *Project) DefaultNetwork() string {
	return p.Name + "_default"
}

// ShellSplit performs a minimal POSIX-ish split of a command string, honouring
// single and double quotes. It is used for compose `command:`/`entrypoint:`
// given in string (shell) form.
func ShellSplit(s string) []string {
	var args []string
	var cur strings.Builder
	var quote rune
	inWord := false
	flush := func() {
		if inWord {
			args = append(args, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			inWord = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return args
}

// entrypointTokens normalizes a service entrypoint to argv tokens. A
// single-element (string / shell form) entrypoint is shell-split exactly like a
// string command, so `entrypoint: "python -m app"` becomes ["python","-m","app"]
// rather than one executable literally named "python -m app"; a multi-element
// list form is taken verbatim. An empty or reset entrypoint (entrypoint: "" or
// []) yields no tokens. The first token is the backend --entrypoint; the rest
// become leading process args after the image.
func entrypointTokens(svc *Service) []string {
	if len(svc.Entrypoint) == 1 {
		return ShellSplit(svc.Entrypoint[0])
	}
	return svc.Entrypoint
}

// entrypointReset reports whether the service EXPLICITLY clears the image
// ENTRYPOINT — `entrypoint: ""` or `entrypoint: []` — as opposed to leaving it
// unset (a nil StringList). Compose, like `docker run --entrypoint ""`, treats
// an explicit empty entrypoint as "ignore the image's ENTRYPOINT", so dcon must
// forward `--entrypoint ""` rather than silently keeping the image's. Mirrors
// the run path's f.Changed("entrypoint") handling (cmd/run.go).
func entrypointReset(svc *Service) bool {
	ep := svc.Entrypoint
	return ep != nil && (len(ep) == 0 || (len(ep) == 1 && ep[0] == ""))
}

// roundCPUs converts a compose cpus value (which may be fractional, e.g. "0.5")
// into the whole-CPU count the backend accepts, rounding up so 0<f<1 never
// yields 0 — matching cmd.parseCPUs on the run path (CLAUDE.md: run/create and
// compose --cpus handling must not drift). A non-numeric / non-finite /
// non-positive value is returned unchanged so the backend surfaces the error.
func roundCPUs(cv string) string {
	fv, err := strconv.ParseFloat(cv, 64)
	if err != nil || math.IsNaN(fv) || math.IsInf(fv, 0) || fv <= 0 {
		return cv
	}
	return strconv.Itoa(int(math.Ceil(fv)))
}

// macOS-irrelevant bind-mount options the container backend rejects (SELinux
// :z/:Z and the legacy :cached/:delegated/:consistent performance hints). The
// run path strips these in cmd.normalizeVolume; the compose path must too, or a
// service volume like "./src:/app:cached" reaches the backend verbatim and the
// service fails to start (while `dcon run -v ./src:/app:cached` works).
var droppedVolumeOpts = map[string]bool{"z": true, "Z": true, "cached": true, "delegated": true, "consistent": true}

// stripVolumeOpts removes droppedVolumeOpts from a resolved volume spec's
// trailing options field, preserving ro/rw and any other tokens.
func stripVolumeOpts(spec string) string {
	parts := strings.Split(spec, ":")
	if len(parts) < 3 {
		return spec // src:dst or named:dst — no options field to strip
	}
	opts := strings.Split(parts[len(parts)-1], ",")
	var kept []string
	for _, o := range opts {
		if droppedVolumeOpts[o] {
			continue
		}
		kept = append(kept, o)
	}
	base := strings.Join(parts[:len(parts)-1], ":")
	if len(kept) == 0 {
		return base
	}
	return base + ":" + strings.Join(kept, ",")
}

// RunArgs builds the `container run ...` argument list for a service.
// netName, when non-empty, is attached via --network. index is the replica
// number (1-based).
func (p *Project) RunArgs(service string, svc *Service, index int, netName string, extraEnv map[string]string) []string {
	a, _ := p.runArgs(service, svc, index, netName, extraEnv, false)
	return a
}

// runArgs builds the run args and returns the index of the image token, so
// callers can split flags / image / command positionally instead of by fragile
// string matching. oneoff stamps the compose oneoff label True/False so a
// `compose run` container is distinguishable from a service replica (and is
// excluded by the replica resolvers), matching Docker.
func (p *Project) runArgs(service string, svc *Service, index int, netName string, extraEnv map[string]string, oneoff bool) (args []string, imageIdx int) {
	args = []string{"run", "--detach"}
	args = append(args, "--name", p.ContainerName(service, index, svc))

	oneoffVal := "False"
	if oneoff {
		oneoffVal = "True"
	}
	// compose identity labels
	args = append(args,
		"--label", LabelProject+"="+p.Name,
		"--label", LabelService+"="+service,
		"--label", fmt.Sprintf("%s=%d", LabelNumber, index),
		"--label", LabelOneoff+"="+oneoffVal,
		"--label", LabelConfigDir+"="+p.Dir,
	)
	for _, k := range sortedKeys(svc.Labels) {
		args = append(args, "--label", k+"="+svc.Labels[k])
	}

	// Attach to the service's declared networks (resolved to backend names via
	// p.Nets) when present; otherwise attach to the default network. Compose
	// `mac_address` maps onto Apple's `--network name,mac=…` on the first
	// attachment (Docker applies the service MAC to the primary interface).
	macLeft := strings.TrimSpace(svc.MacAddress)
	if macLeft != "" {
		if err := netflag.ValidateMAC(macLeft); err != nil {
			warnOnce("mac-"+service, "mac_address %q ignored: %v", macLeft, err)
			macLeft = ""
		}
	}
	attachNet := func(n string) {
		if macLeft != "" {
			// Network names from the project have no mac= yet; AttachMAC only
			// fails on ValidateMAC (already checked) or an existing mac=.
			if spec, err := netflag.AttachMAC(n, macLeft); err == nil {
				n = spec
				macLeft = ""
			}
		}
		args = append(args, "--network", n)
	}
	if len(svc.Networks) > 0 && p.Nets != nil {
		for _, key := range svc.Networks {
			n := p.Nets[key]
			if n == "" {
				n = key
			}
			attachNet(n)
		}
	} else if netName != "" {
		attachNet(netName)
	} else if macLeft != "" {
		// No explicit network: still emit default,mac=… so the MAC is honored.
		attachNet("default")
	}
	if svc.WorkingDir != "" {
		args = append(args, "--workdir", svc.WorkingDir)
	}
	if svc.User != "" {
		args = append(args, "--user", svc.User)
	}
	if svc.Platform != "" {
		args = append(args, "--platform", svc.Platform)
	}
	// cpus/memory: top-level wins, else fall back to deploy.resources.limits.
	// cpus: top-level wins, else deploy.resources.limits. A fractional Docker
	// quota (e.g. 0.5) is rounded up to a whole CPU, matching cmd.parseCPUs on
	// the run path (the backend accepts whole CPUs only).
	cpus := svc.CPUs
	if cpus == "" {
		cpus = svc.Deploy.Resources.Limits.CPUs
	}
	if cpus != "" {
		args = append(args, "--cpus", roundCPUs(cpus))
	}
	mem := svc.MemLimit
	if mem == "" {
		mem = svc.Deploy.Resources.Limits.Memory
	}
	if mem != "" {
		args = append(args, "--memory", mem)
	}
	if svc.ShmSize != "" {
		args = append(args, "--shm-size", svc.ShmSize)
	}
	if svc.ReadOnly {
		args = append(args, "--read-only")
	}
	if svc.Init != nil && *svc.Init {
		args = append(args, "--init")
	}
	if svc.TTY {
		args = append(args, "--tty")
	}
	if svc.Privileged {
		args = append(args, "--cap-add", "ALL")
	}
	// container --entrypoint takes a single executable token; the rest become
	// leading process args after the image. A string-form entrypoint is
	// shell-split (like command), and the empty/reset forms (entrypoint: "" or
	// []) yield no tokens.
	ep := entrypointTokens(svc)
	if len(ep) > 0 {
		args = append(args, "--entrypoint", ep[0])
	} else if entrypointReset(svc) {
		// Explicit `entrypoint: ""` / `entrypoint: []`: clear the image ENTRYPOINT.
		args = append(args, "--entrypoint", "")
	}

	// env_file is parsed by dcon and merged UNDER environment (environment
	// wins), then emitted as plain --env pairs. Forwarding --env-file to the
	// backend delegated to its different dotenv dialect (no `export ` prefix,
	// no quote stripping) and its precedence (env-file AFTER --env flags),
	// silently changing values.
	env := map[string]string{}
	for _, ef := range svc.EnvFile {
		rp := p.resolve(ef.Path)
		m, err := ParseEnvFile(rp)
		if err != nil {
			if ef.Required {
				warnOnce("envfile-"+rp, "env_file %s could not be read and was skipped: %v", rp, err)
			}
			continue // optional (or unreadable) env_file — skip
		}
		for k, v := range m {
			env[k] = v
		}
	}
	for k, v := range svc.Environment {
		env[k] = v
	}
	for _, k := range sortedKeys(env) {
		args = append(args, "--env", k+"="+env[k])
	}
	for _, k := range sortedKeys(extraEnv) {
		args = append(args, "--env", k+"="+extraEnv[k])
	}
	for _, c := range svc.CapAdd {
		args = append(args, "--cap-add", c)
	}
	for _, c := range svc.CapDrop {
		args = append(args, "--cap-drop", c)
	}
	for _, d := range svc.DNS {
		args = append(args, "--dns", d)
	}
	for _, ul := range svc.Ulimits {
		args = append(args, "--ulimit", ul)
	}
	for _, t := range svc.Tmpfs {
		path := t
		if i := strings.Index(t, ":"); i >= 0 {
			path = t[:i]
		}
		args = append(args, "--tmpfs", path)
	}
	for _, port := range svc.Ports {
		args = append(args, "--publish", normalizePort(port))
	}
	for _, vol := range svc.Volumes {
		// A long-form `type: tmpfs` mount is routed to --tmpfs, not --volume,
		// to preserve its in-memory semantics (see tmpfsVolumeMarker).
		if target, ok := strings.CutPrefix(vol, tmpfsVolumeMarker); ok {
			args = append(args, "--tmpfs", target)
			continue
		}
		args = append(args, "--volume", stripVolumeOpts(p.resolveVolume(vol)))
	}

	imageIdx = len(args)
	args = append(args, p.imageRef(service, svc))

	// After the image: extra entrypoint tokens (beyond entrypoint[0]) become
	// leading process args, then the command, matching Docker.
	if len(ep) > 1 {
		args = append(args, ep[1:]...)
	}
	if len(svc.Command) == 1 {
		args = append(args, ShellSplit(svc.Command[0])...)
	} else if len(svc.Command) > 1 {
		args = append(args, svc.Command...)
	}

	// Stamp the config hash (computed over the args WITHOUT the hash label,
	// so it is deterministic) right after `run --detach --name <name>` — a
	// fixed position, so CreateArgs' positional --detach drop still works.
	h := configHash(args)
	labeled := make([]string, 0, len(args)+2)
	labeled = append(labeled, args[:4]...)
	labeled = append(labeled, "--label", LabelConfigHash+"="+h)
	labeled = append(labeled, args[4:]...)
	return labeled, imageIdx + 2
}

// imageRef returns the image to run: explicit image, else the build-tagged
// project image name.
func (p *Project) imageRef(service string, svc *Service) string {
	if svc.Image != "" {
		return svc.Image
	}
	return p.BuildImageName(service)
}

// ImageRef is the exported form of imageRef.
func (p *Project) ImageRef(service string, svc *Service) string {
	return p.imageRef(service, svc)
}

// OneOffArgs builds `container run [--rm] ...` for a `compose run` invocation:
// the service config without the fixed name/detach, plus CLI override flag
// tokens (overrides, e.g. ["--env","K=V","--volume","a:b"]) injected before the
// image, an optional entrypoint override (replacing the service's), and an
// optional command override. The image boundary is located positionally, never
// by string matching, so flag/command values equal to the image reference
// cannot misplace the override. rm controls --rm directly: it is added (or not)
// here rather than being stripped afterward, so a literal "--rm" in the user's
// command args is never removed.
// entrypointSet reports whether the CLI explicitly set --entrypoint (even to
// ""); when true, the override (including an empty value, which clears the
// entrypoint, matching `docker compose run --entrypoint ""`) replaces the
// service's. When false, entrypoint is ignored and the service's is kept.
func (p *Project) OneOffArgs(service string, svc *Service, netName string, cmdOverride, overrides []string, entrypoint string, entrypointSet, rm bool) []string {
	base, imageIdx := p.runArgs(service, svc, 1, netName, nil, true) // oneoff=true
	flags := base[1:imageIdx]                                        // the run flags span (after "run", before image)
	image := base[imageIdx]

	out := []string{"run"}
	if rm {
		out = append(out, "--rm")
	}
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--detach":
			continue
		case "--name":
			i++ // skip its value
			continue
		case "--publish":
			// docker compose run does NOT publish the service's declared ports
			// (it would collide with the already-running service); they are
			// re-injected by composeRun only when --service-ports is given.
			i++ // skip the port spec
			continue
		case "--entrypoint":
			if entrypointSet {
				i++ // drop the service entrypoint; the override replaces it
				continue
			}
		}
		out = append(out, flags[i])
	}
	if entrypointSet {
		out = append(out, "--entrypoint", entrypoint)
	}
	out = append(out, overrides...) // raw CLI override tokens, before the image
	out = append(out, image)

	// The tokens after the image are the service entrypoint's extra tokens
	// (Entrypoint[1:], which runArgs places here because the backend --entrypoint
	// takes a single token) followed by the service command. Split them so each is
	// handled per Docker semantics:
	//   - entrypoint extras: kept when the entrypoint is NOT overridden (they
	//     belong to the surviving entrypoint); dropped when --entrypoint replaces
	//     the whole entrypoint.
	//   - command: replaced by cmdOverride when given, else the service command.
	var extras, command []string
	if n := len(entrypointTokens(svc)) - 1; n > 0 {
		if avail := len(base) - (imageIdx + 1); n > avail {
			n = avail
		}
		extras = base[imageIdx+1 : imageIdx+1+n]
		command = base[imageIdx+1+n:]
	} else {
		command = base[imageIdx+1:]
	}
	if !entrypointSet {
		out = append(out, extras...) // keep the service entrypoint's own args
	}
	if len(cmdOverride) > 0 {
		return append(out, cmdOverride...) // override replaces the service command
	}
	return append(out, command...)
}

// CreateArgs builds the `container create ...` args for a service: the same
// configuration RunArgs produces but as a non-detached create. It drops the
// leading "--detach" positionally (runArgs always emits it at index 1), so a
// service command/entrypoint that itself contains a literal "--detach" token is
// never corrupted — unlike a blind token strip.
func (p *Project) CreateArgs(service string, svc *Service, index int, netName string) []string {
	base, _ := p.runArgs(service, svc, index, netName, nil, false)
	// base = ["run", "--detach", ...flags..., image, ...command...]
	out := append([]string{"create"}, base[2:]...)
	return out
}

// BuildImageName is the local tag used for services built from a Dockerfile.
func (p *Project) BuildImageName(service string) string {
	return fmt.Sprintf("%s-%s:latest", p.Name, service)
}

// resolve makes a path absolute relative to the compose file directory.
func (p *Project) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(p.Dir, path)
}

// resolveVolume rewrites a service volume spec's source so it matches what the
// up/create flow actually provisions: a relative bind source becomes absolute,
// and a declared named volume (a key in the top-level volumes:) becomes its
// project-scoped backend name (VolumeName). Without the latter, the container
// mounted a bare-keyed volume (e.g. `data`) while ensureVolumes created
// `<project>_data`, so the service got a different volume than declared and
// `down -v` removed the wrong one. Absolute paths and undeclared names pass
// through unchanged.
func (p *Project) resolveVolume(spec string) string {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) < 2 {
		return spec
	}
	src := parts[0]
	if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") || src == "." || src == ".." {
		return p.resolve(src) + ":" + parts[1]
	}
	// ~/ bind sources: the backend does no shell-style expansion, so a literal
	// "~" reached it as a (bogus) named volume. Expand against the home dir.
	if src == "~" || strings.HasPrefix(src, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(src, "~")) + ":" + parts[1]
		}
	}
	if vs, ok := p.Volumes[src]; ok { // declared named volume -> backend name
		return p.VolumeName(src, vs) + ":" + parts[1]
	}
	return spec
}

// normalizePort rewrites compose port forms the backend rejects. The backend
// --publish grammar is strictly [host-ip:]host-port:container-port[/proto]:
// it has no ephemeral-port support, so the container-only forms ("3000", and
// the long form's host_ip-with-no-published "ip::3000") would fail the whole
// service. They are published 1:1 (host port = container port) with a
// one-time warning. Port ranges pass through (so a future backend could
// accept them) but warn, since today's backend rejects them.
func normalizePort(spec string) string {
	body, proto := spec, ""
	if i := strings.LastIndexByte(spec, '/'); i >= 0 {
		body, proto = spec[:i], spec[i:]
	}
	if strings.HasPrefix(body, "[") { // bracketed IPv6 host: pass through
		return spec
	}
	parts := strings.Split(body, ":")
	switch len(parts) {
	case 1: // container-only "3000"
		if strings.Contains(parts[0], "-") {
			warnOnce("port-range", "port range %q: the backend does not support port ranges; the mapping may fail", spec)
			return spec
		}
		warnOnce("port-ephemeral",
			"container-only port %q: the backend cannot assign an ephemeral host port; publishing %s:%s instead",
			spec, parts[0], parts[0])
		return parts[0] + ":" + parts[0] + proto
	case 3:
		if parts[1] == "" { // "ip::3000" (long form with host_ip, no published)
			warnOnce("port-ephemeral",
				"port %q has no published (host) port: the backend cannot assign an ephemeral one; publishing container port %s as the host port",
				spec, parts[2])
			if parts[0] == "" { // degenerate "::3000"
				return parts[2] + ":" + parts[2] + proto
			}
			return parts[0] + ":" + parts[2] + ":" + parts[2] + proto
		}
	}
	if strings.Contains(body, "-") {
		warnOnce("port-range", "port range %q: the backend does not support port ranges; the mapping may fail", spec)
	}
	return spec
}

// BuildArgs returns the `container build ...` args for a service with build:.
// It tags the built image with imageRef — i.e. the service's explicit `image:`
// when set, otherwise the derived project image name. This must match what the
// container is run as (RunArgs/OneOffArgs also use imageRef): tagging only the
// derived name while running `image:` would build an image the run never uses.
func (p *Project) BuildArgs(service string, svc *Service) []string {
	ctx := svc.Build.Context
	if ctx == "" {
		ctx = "."
	}
	ctx = p.resolve(ctx)
	args := []string{"build", "--tag", p.imageRef(service, svc)}
	if svc.Build.Dockerfile != "" {
		args = append(args, "--file", filepath.Join(ctx, svc.Build.Dockerfile))
	}
	for _, k := range sortedKeys(svc.Build.Args) {
		args = append(args, "--build-arg", k+"="+svc.Build.Args[k])
	}
	if svc.Build.Target != "" {
		args = append(args, "--target", svc.Build.Target)
	}
	if svc.Platform != "" {
		args = append(args, "--platform", svc.Platform)
	}
	args = append(args, ctx)
	return args
}

// NetworkName resolves a declared network key to its backend network name,
// honouring an explicit `name:` and otherwise prefixing the project name.
func (p *Project) NetworkName(key string, spec *NetworkSpec) string {
	if spec != nil && spec.Name != "" {
		return spec.Name
	}
	// An external network already exists outside the project; reference it by its
	// exact key, never the project-prefixed name (mirrors VolumeName). dcon never
	// creates external networks, so a prefixed name would name a network that does
	// not exist and the container would fail to attach.
	if spec != nil && spec.External {
		return key
	}
	return p.Name + "_" + key
}

// VolumeName resolves a declared volume key to its backend volume name,
// honouring an explicit `name:` and otherwise prefixing the project name.
func (p *Project) VolumeName(key string, spec *VolumeSpec) string {
	if spec != nil && spec.Name != "" {
		return spec.Name
	}
	// An external volume already exists outside the project; reference it by its
	// exact key, never the project-prefixed name (which would mount/create a
	// different volume than the one the user declared external).
	if spec != nil && spec.External {
		return key
	}
	return p.Name + "_" + key
}

// Replicas returns the number of instances to run for a service: the CLI/scale
// override if > 0, else the service's `scale:`, else 1.
func (p *Project) Replicas(svc *Service, override int) int {
	if override > 0 {
		return override
	}
	if svc.Scale >= 1 {
		return svc.Scale
	}
	if r := svc.Deploy.Replicas; r != nil { // modern deploy.replicas fallback
		if *r < 0 {
			return 0
		}
		return *r
	}
	return 1
}
