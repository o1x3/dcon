package dockerfmt

import (
	"fmt"
	"strconv"
	"strings"
)

// This file synthesizes Docker-schema inspect views from the backend's JSON
// models, so `dcon inspect --format` can execute the standard docker template
// idioms ({{.State.Running}}, {{.Id}}, {{.Config.Image}},
// {{.NetworkSettings.IPAddress}}, {{range .Mounts}}, …) that the raw backend
// schema cannot satisfy. Only the commonly-templated subset of docker's
// inspect payload is modeled; the no-format path keeps printing the raw
// backend JSON (documented dcon behavior).

// DockerState maps the backend's state vocabulary onto docker's .State enum
// (created|running|paused|restarting|removing|exited|dead). Shared by ps and
// the inspect view so the two can never drift.
func DockerState(state string) string {
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

// zeroTime is docker's zero-value timestamp for unset State times.
const zeroTime = "0001-01-01T00:00:00Z"

// InspectState mirrors docker's .State. The backend exposes no exit code or
// pid for stopped containers; docker's zero values are used.
type InspectState struct {
	Status     string
	Running    bool
	Paused     bool
	Restarting bool
	OOMKilled  bool
	Dead       bool
	Pid        int
	ExitCode   int
	Error      string
	StartedAt  string
	FinishedAt string
}

// InspectConfig mirrors docker's .Config.
type InspectConfig struct {
	Hostname   string
	User       string
	Tty        bool
	Env        []string
	Cmd        []string
	Image      string
	WorkingDir string
	Entrypoint []string
	Labels     map[string]string
}

// InspectPortBinding mirrors one docker .NetworkSettings.Ports binding.
type InspectPortBinding struct {
	HostIp   string
	HostPort string
}

// InspectEndpoint mirrors one docker .NetworkSettings.Networks value.
type InspectEndpoint struct {
	IPAddress string
	Gateway   string
}

// InspectNetworkSettings mirrors docker's .NetworkSettings.
type InspectNetworkSettings struct {
	IPAddress string
	Gateway   string
	Ports     map[string][]InspectPortBinding
	Networks  map[string]InspectEndpoint
}

// InspectMount mirrors one docker .Mounts entry.
type InspectMount struct {
	Type        string
	Source      string
	Destination string
	Mode        string
	RW          bool
}

// InspectRestartPolicy mirrors docker's .HostConfig.RestartPolicy.
type InspectRestartPolicy struct {
	Name              string
	MaximumRetryCount int
}

// InspectHostConfig is a minimal docker .HostConfig (the backend has no
// daemon-side host config; only what dcon can derive is populated).
type InspectHostConfig struct {
	NetworkMode   string
	Memory        int64
	NanoCpus      int64
	RestartPolicy InspectRestartPolicy
}

// ContainerInspectView is the docker-schema container inspect object.
type ContainerInspectView struct {
	Id              string
	Created         string
	Path            string
	Args            []string
	State           InspectState
	Image           string
	Name            string
	RestartCount    int
	Platform        string
	Os              string
	Architecture    string
	Mounts          []InspectMount
	Config          InspectConfig
	NetworkSettings InspectNetworkSettings
	HostConfig      InspectHostConfig
}

// stripCIDR removes a /prefix suffix from an address ("192.168.64.3/24" →
// "192.168.64.3"); docker's IPAddress fields carry no prefix length.
func stripCIDR(addr string) string {
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		return addr[:i]
	}
	return addr
}

// mountType maps the backend's FSType single-key object onto docker's mount
// type vocabulary (bind|volume|tmpfs).
func mountType(f Filesystem) string {
	for k := range f.Type {
		switch k {
		case "volume":
			return "volume"
		case "tmpfs":
			return "tmpfs"
		default: // virtiofs/bind/block → docker calls host mounts "bind"
			return "bind"
		}
	}
	return "bind"
}

// NewContainerInspectView synthesizes the docker inspect schema from a
// backend container object. Pure, so the mapping is unit-testable.
func NewContainerInspectView(c Container) ContainerInspectView {
	state := DockerState(c.Status.State)
	startedAt := c.Status.StartedDate
	if startedAt == "" {
		startedAt = zeroTime
	}

	// Mounts
	mounts := make([]InspectMount, 0, len(c.Configuration.Mounts))
	for _, m := range c.Configuration.Mounts {
		rw := true
		for _, o := range m.Options {
			if o == "ro" {
				rw = false
			}
		}
		mounts = append(mounts, InspectMount{
			Type:        mountType(m),
			Source:      m.Source,
			Destination: m.Destination,
			Mode:        strings.Join(m.Options, ","),
			RW:          rw,
		})
	}

	// NetworkSettings from the runtime attachments.
	ns := InspectNetworkSettings{
		Ports:    map[string][]InspectPortBinding{},
		Networks: map[string]InspectEndpoint{},
	}
	for i, a := range c.Status.Networks {
		ep := InspectEndpoint{IPAddress: stripCIDR(a.IPv4Address), Gateway: stripCIDR(a.IPv4Gateway)}
		name := a.Network
		if name == "" {
			name = "default"
		}
		ns.Networks[name] = ep
		if i == 0 {
			ns.IPAddress = ep.IPAddress
			ns.Gateway = ep.Gateway
		}
	}
	for _, p := range c.Configuration.Ports {
		proto := p.Proto
		if proto == "" {
			proto = "tcp"
		}
		host := p.HostAddress
		if host == "" {
			host = "0.0.0.0"
		}
		cnt := p.Count
		if cnt < 1 {
			cnt = 1
		}
		for k := 0; k < cnt; k++ {
			key := fmt.Sprintf("%d/%s", p.ContainerPort+k, proto)
			ns.Ports[key] = append(ns.Ports[key], InspectPortBinding{
				HostIp:   host,
				HostPort: strconv.Itoa(p.HostPort + k),
			})
		}
	}

	// Hostname: the backend names the guest after its first attachment
	// hostname when present, else the container id (docker's default too).
	hostname := c.ID
	if len(c.Status.Networks) > 0 && c.Status.Networks[0].Hostname != "" {
		hostname = c.Status.Networks[0].Hostname
	}

	networkMode := "default"
	if len(c.Configuration.Networks) > 0 && c.Configuration.Networks[0].Network != "" {
		networkMode = c.Configuration.Networks[0].Network
	}

	var entrypoint []string
	if c.Configuration.InitProcess.Executable != "" {
		entrypoint = []string{c.Configuration.InitProcess.Executable}
	}

	return ContainerInspectView{
		Id:      c.ID,
		Created: c.Configuration.CreationDate,
		Path:    c.Configuration.InitProcess.Executable,
		Args:    c.Configuration.InitProcess.Arguments,
		State: InspectState{
			Status:     state,
			Running:    state == "running",
			StartedAt:  startedAt,
			FinishedAt: zeroTime,
		},
		Image:        c.Configuration.Image.Descriptor.Digest,
		Name:         "/" + c.ID,
		Platform:     c.Configuration.Platform.OS,
		Os:           c.Configuration.Platform.OS,
		Architecture: c.Configuration.Platform.Architecture,
		Mounts:       mounts,
		Config: InspectConfig{
			Hostname:   hostname,
			Tty:        c.Configuration.InitProcess.Terminal,
			Env:        c.Configuration.InitProcess.Environment,
			Cmd:        c.Configuration.InitProcess.Arguments,
			Image:      ShortImage(c.Configuration.Image.Reference),
			WorkingDir: c.Configuration.InitProcess.WorkingDirectory,
			Entrypoint: entrypoint,
			Labels:     c.Configuration.Labels,
		},
		NetworkSettings: ns,
		HostConfig: InspectHostConfig{
			NetworkMode:   networkMode,
			Memory:        int64(c.Configuration.Resources.MemoryInBytes),
			NanoCpus:      int64(c.Configuration.Resources.CPUs) * 1e9,
			RestartPolicy: InspectRestartPolicy{Name: "no"},
		},
	}
}

// NetworkDriver maps the backend's network mode onto docker's driver
// vocabulary. Shared by `network ls` and the network inspect view.
func NetworkDriver(mode string) string {
	switch mode {
	case "nat":
		return "bridge"
	case "hostOnly":
		return "host"
	default:
		return mode
	}
}

// InspectIPAMConfig mirrors one docker .IPAM.Config entry.
type InspectIPAMConfig struct {
	Subnet  string
	Gateway string
}

// InspectIPAM mirrors docker's network .IPAM.
type InspectIPAM struct {
	Driver string
	Config []InspectIPAMConfig
}

// NetworkInspectView is the docker-schema network inspect object.
type NetworkInspectView struct {
	Name     string
	Id       string
	Created  string
	Scope    string
	Driver   string
	Internal bool
	IPAM     InspectIPAM
	Labels   map[string]string
}

// NewNetworkInspectView synthesizes docker's network inspect schema from a
// backend network object.
func NewNetworkInspectView(n Network) NetworkInspectView {
	subnet := n.Status.IPv4Subnet
	if subnet == "" {
		subnet = n.Configuration.IPv4Subnet
	}
	var cfg []InspectIPAMConfig
	if subnet != "" || n.Status.IPv4Gateway != "" {
		cfg = append(cfg, InspectIPAMConfig{Subnet: subnet, Gateway: n.Status.IPv4Gateway})
	}
	return NetworkInspectView{
		Name:    n.Configuration.Name,
		Id:      n.ID,
		Created: n.Configuration.CreationDate,
		Scope:   "local",
		Driver:  NetworkDriver(n.Configuration.Mode),
		IPAM:    InspectIPAM{Driver: "default", Config: cfg},
		Labels:  n.Configuration.Labels,
	}
}

// VolumeInspectView is the docker-schema volume inspect object.
type VolumeInspectView struct {
	Name       string
	Driver     string
	Mountpoint string
	CreatedAt  string
	Scope      string
	Labels     map[string]string
	Options    map[string]string
}

// NewVolumeInspectView synthesizes docker's volume inspect schema from a
// backend volume object.
func NewVolumeInspectView(v Volume) VolumeInspectView {
	driver := v.Configuration.Driver
	if driver == "" {
		driver = "local"
	}
	return VolumeInspectView{
		Name:       v.Configuration.Name,
		Driver:     driver,
		Mountpoint: v.Configuration.Source,
		CreatedAt:  v.Configuration.CreationDate,
		Scope:      "local",
		Labels:     v.Configuration.Labels,
		Options:    v.Configuration.Options,
	}
}
