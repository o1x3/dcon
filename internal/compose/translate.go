package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
)

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

// RunArgs builds the `container run ...` argument list for a service.
// netName, when non-empty, is attached via --network. index is the replica
// number (1-based).
func (p *Project) RunArgs(service string, svc *Service, index int, netName string, extraEnv map[string]string) []string {
	a, _ := p.runArgs(service, svc, index, netName, extraEnv)
	return a
}

// runArgs builds the run args and returns the index of the image token, so
// callers can split flags / image / command positionally instead of by fragile
// string matching.
func (p *Project) runArgs(service string, svc *Service, index int, netName string, extraEnv map[string]string) (args []string, imageIdx int) {
	args = []string{"run", "--detach"}
	args = append(args, "--name", p.ContainerName(service, index, svc))

	// compose identity labels
	args = append(args,
		"--label", LabelProject+"="+p.Name,
		"--label", LabelService+"="+service,
		"--label", fmt.Sprintf("%s=%d", LabelNumber, index),
		"--label", LabelOneoff+"=False",
		"--label", LabelConfigDir+"="+p.Dir,
	)
	for _, k := range sortedKeys(svc.Labels) {
		args = append(args, "--label", k+"="+svc.Labels[k])
	}

	// Attach to the service's declared networks (resolved to backend names via
	// p.Nets) when present; otherwise attach to the default network.
	if len(svc.Networks) > 0 && p.Nets != nil {
		for _, key := range svc.Networks {
			n := p.Nets[key]
			if n == "" {
				n = key
			}
			args = append(args, "--network", n)
		}
	} else if netName != "" {
		args = append(args, "--network", netName)
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
	cpus := svc.CPUs
	if cpus == "" {
		cpus = svc.Deploy.Resources.Limits.CPUs
	}
	if cpus != "" {
		args = append(args, "--cpus", cpus)
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
	// container --entrypoint takes a single command string; skip the empty/reset
	// forms (entrypoint: "" or []).
	if len(svc.Entrypoint) > 0 && svc.Entrypoint[0] != "" {
		args = append(args, "--entrypoint", svc.Entrypoint[0])
	}

	for _, k := range sortedKeys(svc.Environment) {
		args = append(args, "--env", k+"="+svc.Environment[k])
	}
	for _, k := range sortedKeys(extraEnv) {
		args = append(args, "--env", k+"="+extraEnv[k])
	}
	for _, ef := range svc.EnvFile {
		rp := p.resolve(ef.Path)
		if !ef.Required {
			if _, err := os.Stat(rp); err != nil {
				continue // optional env_file absent — skip
			}
		}
		args = append(args, "--env-file", rp)
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
		args = append(args, "--volume", p.resolveVolume(vol))
	}

	imageIdx = len(args)
	args = append(args, p.imageRef(service, svc))

	// After the image: extra entrypoint tokens (beyond entrypoint[0]) become
	// leading process args, then the command, matching Docker.
	if len(svc.Entrypoint) > 1 {
		args = append(args, svc.Entrypoint[1:]...)
	}
	if len(svc.Command) == 1 {
		args = append(args, ShellSplit(svc.Command[0])...)
	} else if len(svc.Command) > 1 {
		args = append(args, svc.Command...)
	}
	return args, imageIdx
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
func (p *Project) OneOffArgs(service string, svc *Service, netName string, cmdOverride, overrides []string, entrypoint string, rm bool) []string {
	base, imageIdx := p.runArgs(service, svc, 1, netName, nil)
	flags := base[1:imageIdx] // the run flags span (after "run", before image)
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
		case "--entrypoint":
			if entrypoint != "" {
				i++ // drop the service entrypoint; the override replaces it
				continue
			}
		}
		out = append(out, flags[i])
	}
	if entrypoint != "" {
		out = append(out, "--entrypoint", entrypoint)
	}
	out = append(out, overrides...) // raw CLI override tokens, before the image
	out = append(out, image)
	if len(cmdOverride) > 0 {
		return append(out, cmdOverride...) // override replaces the service command
	}
	return append(out, base[imageIdx+1:]...) // keep the service's own command
}

// CreateArgs builds the `container create ...` args for a service: the same
// configuration RunArgs produces but as a non-detached create. It drops the
// leading "--detach" positionally (runArgs always emits it at index 1), so a
// service command/entrypoint that itself contains a literal "--detach" token is
// never corrupted — unlike a blind token strip.
func (p *Project) CreateArgs(service string, svc *Service, index int, netName string) []string {
	base, _ := p.runArgs(service, svc, index, netName, nil)
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

// resolveVolume rewrites a bind-mount source to an absolute path (named volumes
// and absolute paths pass through unchanged).
func (p *Project) resolveVolume(spec string) string {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) < 2 {
		return spec
	}
	src := parts[0]
	if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") || src == "." {
		return p.resolve(src) + ":" + parts[1]
	}
	return spec
}

// normalizePort strips the optional host IP and any long-form mapping down to
// what container --publish accepts ([host-ip:]host:container[/proto]).
func normalizePort(spec string) string {
	// already in host:container or ip:host:container form; pass through.
	return spec
}

// BuildArgs returns the `container build ...` args for a service with build:.
func (p *Project) BuildArgs(service string, svc *Service) []string {
	ctx := svc.Build.Context
	if ctx == "" {
		ctx = "."
	}
	ctx = p.resolve(ctx)
	args := []string{"build", "--tag", p.BuildImageName(service)}
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
	return p.Name + "_" + key
}

// VolumeName resolves a declared volume key to its backend volume name,
// honouring an explicit `name:` and otherwise prefixing the project name.
func (p *Project) VolumeName(key string, spec *VolumeSpec) string {
	if spec != nil && spec.Name != "" {
		return spec.Name
	}
	return p.Name + "_" + key
}

// Replicas returns the number of instances to run for a service: the CLI/scale
// override if > 0, else the service's `scale:`, else 1.
func (p *Project) Replicas(svc *Service, override int) int {
	n := svc.Scale
	if override > 0 {
		n = override
	}
	if n < 1 {
		n = 1
	}
	return n
}
