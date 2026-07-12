package dockerfmt

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"text/template"
)

// fixtureContainer builds a representative backend container object.
func fixtureContainer() Container {
	return Container{
		ID: "web",
		Configuration: ContainerConfiguration{
			Image: ImageDescription{
				Reference:  "docker.io/library/nginx:latest",
				Descriptor: Descriptor{Digest: "sha256:abc123"},
			},
			Mounts: []Filesystem{
				{
					Type:        map[string]json.RawMessage{"virtiofs": json.RawMessage(`{}`)},
					Source:      "/host/data",
					Destination: "/data",
					Options:     []string{"ro"},
				},
				{
					Type:        map[string]json.RawMessage{"volume": json.RawMessage(`{}`)},
					Source:      "vol1",
					Destination: "/var/lib/db",
				},
			},
			Ports: []PublishPort{
				{HostAddress: "", HostPort: 8080, ContainerPort: 80, Proto: "tcp", Count: 1},
			},
			Labels:   map[string]string{"app": "web"},
			Networks: []AttachmentConf{{Network: "mynet"}},
			InitProcess: ProcessConfig{
				Executable:       "/docker-entrypoint.sh",
				Arguments:        []string{"nginx", "-g", "daemon off;"},
				Environment:      []string{"PATH=/usr/bin"},
				WorkingDirectory: "/app",
				Terminal:         true,
			},
			Platform:     Platform{OS: "linux", Architecture: "arm64"},
			Resources:    Resources{CPUs: 2, MemoryInBytes: 512 * 1024 * 1024},
			CreationDate: "2026-07-01T10:00:00Z",
		},
		Status: ContainerStatus{
			State:       "running",
			StartedDate: "2026-07-01T10:00:05Z",
			Networks: []Attachment{
				{Network: "mynet", Hostname: "web.test", IPv4Address: "192.168.64.3/24", IPv4Gateway: "192.168.64.1"},
			},
		},
	}
}

func TestNewContainerInspectView(t *testing.T) {
	v := NewContainerInspectView(fixtureContainer())

	if v.Id != "web" || v.Name != "/web" {
		t.Errorf("Id/Name = %q/%q, want web//web", v.Id, v.Name)
	}
	if !v.State.Running || v.State.Status != "running" || v.State.ExitCode != 0 {
		t.Errorf("State = %+v, want running/Running=true/ExitCode=0", v.State)
	}
	if v.State.StartedAt != "2026-07-01T10:00:05Z" {
		t.Errorf("StartedAt = %q", v.State.StartedAt)
	}
	if v.Image != "sha256:abc123" {
		t.Errorf("Image = %q, want the digest", v.Image)
	}
	if v.Config.Image != "nginx:latest" {
		t.Errorf("Config.Image = %q, want shortened ref", v.Config.Image)
	}
	if v.Config.WorkingDir != "/app" || !v.Config.Tty {
		t.Errorf("Config = %+v", v.Config)
	}
	if len(v.Config.Entrypoint) != 1 || v.Config.Entrypoint[0] != "/docker-entrypoint.sh" {
		t.Errorf("Entrypoint = %v", v.Config.Entrypoint)
	}
	if v.Config.Hostname != "web.test" {
		t.Errorf("Hostname = %q, want attachment hostname", v.Config.Hostname)
	}
	// docker's IPAddress has no CIDR suffix.
	if v.NetworkSettings.IPAddress != "192.168.64.3" {
		t.Errorf("IPAddress = %q, want CIDR stripped", v.NetworkSettings.IPAddress)
	}
	ep, ok := v.NetworkSettings.Networks["mynet"]
	if !ok || ep.Gateway != "192.168.64.1" {
		t.Errorf("Networks[mynet] = %+v ok=%v", ep, ok)
	}
	pb := v.NetworkSettings.Ports["80/tcp"]
	if len(pb) != 1 || pb[0].HostPort != "8080" || pb[0].HostIp != "0.0.0.0" {
		t.Errorf("Ports[80/tcp] = %+v", pb)
	}
	if len(v.Mounts) != 2 {
		t.Fatalf("Mounts = %+v, want 2", v.Mounts)
	}
	if v.Mounts[0].Type != "bind" || v.Mounts[0].RW {
		t.Errorf("mount 0 = %+v, want bind/ro", v.Mounts[0])
	}
	if v.Mounts[1].Type != "volume" || !v.Mounts[1].RW {
		t.Errorf("mount 1 = %+v, want volume/rw", v.Mounts[1])
	}
	if v.HostConfig.NetworkMode != "mynet" || v.HostConfig.Memory != 512*1024*1024 || v.HostConfig.NanoCpus != 2e9 {
		t.Errorf("HostConfig = %+v", v.HostConfig)
	}
	if v.Os != "linux" || v.Architecture != "arm64" || v.Platform != "linux" {
		t.Errorf("Platform/Os/Arch = %q/%q/%q", v.Platform, v.Os, v.Architecture)
	}
}

// TestContainerInspectViewStoppedDefaults checks the docker zero-values for a
// stopped container with no runtime attachments.
func TestContainerInspectViewStoppedDefaults(t *testing.T) {
	c := fixtureContainer()
	c.Status = ContainerStatus{State: "stopped"}
	v := NewContainerInspectView(c)
	if v.State.Status != "exited" || v.State.Running {
		t.Errorf("State = %+v, want exited/not running", v.State)
	}
	if v.State.StartedAt != "0001-01-01T00:00:00Z" || v.State.FinishedAt != "0001-01-01T00:00:00Z" {
		t.Errorf("zero times = %q/%q", v.State.StartedAt, v.State.FinishedAt)
	}
	if v.NetworkSettings.IPAddress != "" {
		t.Errorf("IPAddress = %q, want empty", v.NetworkSettings.IPAddress)
	}
	if v.Config.Hostname != "web" {
		t.Errorf("Hostname = %q, want container id fallback", v.Config.Hostname)
	}
}

// TestInspectViewTemplateIdioms executes the standard docker CI templates
// against the view — the exact idioms the raw backend schema failed on.
func TestInspectViewTemplateIdioms(t *testing.T) {
	v := NewContainerInspectView(fixtureContainer())
	cases := map[string]string{
		`{{.State.Running}}`:             "true",
		`{{.State.Status}}`:              "running",
		`{{.Id}}`:                        "web",
		`{{.Name}}`:                      "/web",
		`{{.Config.Image}}`:              "nginx:latest",
		`{{.NetworkSettings.IPAddress}}`: "192.168.64.3",
		`{{range .Mounts}}{{.Destination}} {{end}}`:                      "/data /var/lib/db ",
		`{{(index (index .NetworkSettings.Ports "80/tcp") 0).HostPort}}`: "8080",
		`{{index .Config.Labels "app"}}`:                                 "web",
	}
	for tpl, want := range cases {
		tm, err := template.New("t").Funcs(TemplateFuncs()).Parse(tpl)
		if err != nil {
			t.Fatalf("parse %q: %v", tpl, err)
		}
		var buf bytes.Buffer
		if err := tm.Execute(&buf, v); err != nil {
			t.Errorf("execute %q: %v", tpl, err)
			continue
		}
		if got := buf.String(); got != want {
			t.Errorf("%q = %q, want %q", tpl, got, want)
		}
	}
}

func TestDockerState(t *testing.T) {
	cases := map[string]string{
		"stopped": "exited", "stopping": "removing", "": "created",
		"unknown": "created", "running": "running",
	}
	for in, want := range cases {
		if got := DockerState(in); got != want {
			t.Errorf("DockerState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewNetworkInspectView(t *testing.T) {
	n := Network{
		ID: "net123",
		Configuration: NetworkConfiguration{
			Name: "mynet", Mode: "nat", CreationDate: "2026-07-01T00:00:00Z",
			Labels: map[string]string{"a": "b"},
		},
		Status: NetworkStatus{IPv4Subnet: "192.168.64.0/24", IPv4Gateway: "192.168.64.1"},
	}
	v := NewNetworkInspectView(n)
	if v.Name != "mynet" || v.Id != "net123" || v.Driver != "bridge" || v.Scope != "local" {
		t.Errorf("view = %+v", v)
	}
	if len(v.IPAM.Config) != 1 || v.IPAM.Config[0].Subnet != "192.168.64.0/24" || v.IPAM.Config[0].Gateway != "192.168.64.1" {
		t.Errorf("IPAM = %+v", v.IPAM)
	}
	// The documented template idioms must execute.
	for _, tpl := range []string{`{{.Name}}`, `{{.Id}}`, `{{.Driver}}`, `{{.Scope}}`, `{{.IPAM.Config}}`} {
		tm, err := template.New("t").Parse(tpl)
		if err != nil {
			t.Fatalf("parse %q: %v", tpl, err)
		}
		var buf bytes.Buffer
		if err := tm.Execute(&buf, v); err != nil {
			t.Errorf("execute %q: %v", tpl, err)
		}
	}
}

func TestNewVolumeInspectView(t *testing.T) {
	v := NewVolumeInspectView(Volume{
		ID: "vol1",
		Configuration: VolumeConfiguration{
			Name: "vol1", Source: "/vols/vol1", CreationDate: "2026-07-01T00:00:00Z",
			Labels: map[string]string{"k": "v"},
		},
	})
	if v.Name != "vol1" || v.Driver != "local" || v.Mountpoint != "/vols/vol1" || v.Scope != "local" {
		t.Errorf("view = %+v", v)
	}
	var buf bytes.Buffer
	tm := template.Must(template.New("t").Parse(`{{.Name}} {{.Driver}} {{.Mountpoint}}`))
	if err := tm.Execute(&buf, v); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "vol1 local /vols/vol1") {
		t.Errorf("template output = %q", buf.String())
	}
}
