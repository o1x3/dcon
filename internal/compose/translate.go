package compose

import (
	"fmt"
	"path/filepath"
	"strings"
)

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
	args := []string{"run", "--detach"}
	args = append(args, "--name", p.ContainerName(service, index, svc))

	// compose identity labels
	args = append(args,
		"--label", LabelProject+"="+p.Name,
		"--label", LabelService+"="+service,
		"--label", fmt.Sprintf("%s=%d", LabelNumber, index),
		"--label", LabelOneoff+"=False",
		"--label", LabelConfigDir+"="+p.Dir,
	)
	for k, v := range svc.Labels {
		args = append(args, "--label", k+"="+v)
	}

	if netName != "" {
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
	if svc.CPUs != "" {
		args = append(args, "--cpus", svc.CPUs)
	}
	if svc.MemLimit != "" {
		args = append(args, "--memory", svc.MemLimit)
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
	if svc.Entrypoint != nil && len(svc.Entrypoint) > 0 {
		// container --entrypoint takes a single command string.
		args = append(args, "--entrypoint", svc.Entrypoint[0])
	}

	for k, v := range svc.Environment {
		args = append(args, "--env", k+"="+v)
	}
	for k, v := range extraEnv {
		args = append(args, "--env", k+"="+v)
	}
	for _, ef := range svc.EnvFile {
		args = append(args, "--env-file", p.resolve(ef))
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

	args = append(args, p.imageRef(service, svc))

	// command (after image = container init args)
	if len(svc.Command) == 1 {
		// string form -> shell-split
		args = append(args, ShellSplit(svc.Command[0])...)
	} else if len(svc.Command) > 1 {
		args = append(args, svc.Command...)
	}
	// entrypoint extra args (beyond the executable) appended after command? In
	// compose, entrypoint replaces the image entrypoint and command are its
	// args. We already set --entrypoint to entrypoint[0]; pass remaining
	// entrypoint tokens as leading args when command is empty.
	if len(svc.Entrypoint) > 1 && len(svc.Command) == 0 {
		args = append(args, svc.Entrypoint[1:]...)
	}
	return args
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

// OneOffArgs builds `container run --rm ...` for a `compose run` invocation:
// the service config without the fixed name/detach, with an optional command
// override (when cmdOverride is empty the service's own command is used).
func (p *Project) OneOffArgs(service string, svc *Service, netName string, cmdOverride []string) []string {
	base := p.RunArgs(service, svc, 1, netName, nil)
	// base = ["run","--detach","--name",NAME, ...flags..., IMAGE, ...cmd...]
	image := p.imageRef(service, svc)
	out := []string{"run", "--rm"}
	skipNext := false
	pastImage := false
	for i := 1; i < len(base); i++ {
		a := base[i]
		if skipNext {
			skipNext = false
			continue
		}
		if a == "--detach" {
			continue
		}
		if a == "--name" {
			skipNext = true
			continue
		}
		out = append(out, a)
		if a == image && !pastImage {
			pastImage = true
			if len(cmdOverride) > 0 {
				// drop the service's own trailing command; use the override.
				return append(out, cmdOverride...)
			}
		}
	}
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
	for k, v := range svc.Build.Args {
		args = append(args, "--build-arg", k+"="+v)
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
