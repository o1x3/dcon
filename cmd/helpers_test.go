package cmd

import (
	"encoding/json"
	"testing"

	"dcon/internal/dockerfmt"
)

func TestMatchStatusFilter(t *testing.T) {
	cases := []struct {
		state, want string
		ok          bool
	}{
		{"running", "running", true},
		{"stopped", "exited", true},
		{"stopped", "dead", true},
		{"unknown", "created", true},
		{"", "created", true},
		{"stopping", "removing", true},
		{"running", "exited", false},
		{"stopped", "running", false},
		{"running", "paused", false},
	}
	for _, c := range cases {
		if got := matchStatusFilter(c.state, c.want); got != c.ok {
			t.Errorf("matchStatusFilter(%q,%q)=%v want %v", c.state, c.want, got, c.ok)
		}
	}
}

func TestDriverForMode(t *testing.T) {
	if driverForMode("nat") != "bridge" {
		t.Error("nat -> bridge")
	}
	if driverForMode("hostOnly") != "host" {
		t.Error("hostOnly -> host")
	}
	if driverForMode("custom") != "custom" {
		t.Error("passthrough")
	}
}

func TestPortsString(t *testing.T) {
	c := dockerfmt.Container{}
	c.Configuration.Ports = []dockerfmt.PublishPort{
		{HostAddress: "", HostPort: 8080, ContainerPort: 80, Proto: "tcp", Count: 1},
		{HostAddress: "::1", HostPort: 9000, ContainerPort: 9000, Proto: "udp", Count: 3},
	}
	got := portsString(c)
	if got != "0.0.0.0:8080->80/tcp, [::1]:9000-9002->9000-9002/udp" {
		t.Errorf("portsString = %q", got)
	}
}

func TestStatusStringStates(t *testing.T) {
	mk := func(state string) dockerfmt.Container {
		var c dockerfmt.Container
		c.Status.State = state
		return c
	}
	if statusString(mk("stopped")) != "Exited" {
		t.Error("stopped -> Exited")
	}
	if statusString(mk("stopping")) != "Stopping" {
		t.Error("stopping -> Stopping")
	}
	if statusString(mk("")) != "Created" {
		t.Error("empty -> Created")
	}
	if got := statusString(mk("running")); got != "Up" {
		t.Errorf("running w/o start -> Up, got %q", got)
	}
}

func TestBuildPsViewAndLocalVolumes(t *testing.T) {
	var c dockerfmt.Container
	c.ID = "abcdef0123456789"
	c.Configuration.Image.Reference = "docker.io/library/nginx:1.27"
	c.Configuration.InitProcess.Executable = "nginx"
	c.Configuration.InitProcess.Arguments = []string{"-g", "daemon off;"}
	c.Configuration.Mounts = []dockerfmt.Filesystem{
		{Type: map[string]json.RawMessage{"volume": json.RawMessage("{}")}, Source: "vol"},
		{Type: map[string]json.RawMessage{"virtiofs": json.RawMessage("{}")}, Source: "/host"},
	}
	c.Status.State = "running"
	v := buildPsView(c, false)
	if v.ID != "abcdef012345" {
		t.Errorf("short id = %q", v.ID)
	}
	if v.Image != "nginx:1.27" {
		t.Errorf("image shortened = %q", v.Image)
	}
	if v.LocalVolumes != "1" {
		t.Errorf("local volumes = %q (want 1)", v.LocalVolumes)
	}
	// no-trunc keeps the full id
	if buildPsView(c, true).ID != c.ID {
		t.Error("no-trunc should keep full id")
	}
}

func TestApplyFilters(t *testing.T) {
	mk := func(id, state, ref string) dockerfmt.Container {
		var c dockerfmt.Container
		c.ID = id
		c.Status.State = state
		c.Configuration.Image.Reference = ref
		return c
	}
	list := []dockerfmt.Container{
		mk("web1", "running", "nginx"),
		mk("db1", "stopped", "postgres"),
	}
	got := applyFilters(list, []string{"status=running"})
	if len(got) != 1 || got[0].ID != "web1" {
		t.Errorf("status filter wrong: %+v", got)
	}
	got = applyFilters(list, []string{"ancestor=postgres"})
	if len(got) != 1 || got[0].ID != "db1" {
		t.Errorf("ancestor filter wrong: %+v", got)
	}
	got = applyFilters(list, []string{"name=web"})
	if len(got) != 1 || got[0].ID != "web1" {
		t.Errorf("name filter wrong: %+v", got)
	}
}

func TestImageSizeBytesHostArch(t *testing.T) {
	img := dockerfmt.Image{Variants: []dockerfmt.ImageVariant{
		{Platform: dockerfmt.Platform{OS: "linux", Architecture: "amd64"}, Size: 100},
		{Platform: dockerfmt.Platform{OS: "linux", Architecture: "arm64"}, Size: 200},
	}}
	got := imageSizeBytes(img)
	if got != 100 && got != 200 {
		t.Errorf("imageSizeBytes should pick a linux variant, got %d", got)
	}
	// non-linux variants ignored
	none := dockerfmt.Image{Variants: []dockerfmt.ImageVariant{
		{Platform: dockerfmt.Platform{OS: "windows", Architecture: "amd64"}, Size: 50}}}
	if imageSizeBytes(none) != 0 {
		t.Error("non-linux variants should yield 0")
	}
}

func TestMatchVolumeFilters(t *testing.T) {
	var v dockerfmt.Volume
	v.Configuration.Name = "appdata"
	v.Configuration.Labels = map[string]string{"env": "prod"}
	if !matchVolumeFilters(v, "local", []string{"name=app", "driver=local", "label=env=prod"}) {
		t.Error("should match")
	}
	if matchVolumeFilters(v, "local", []string{"name=zzz"}) {
		t.Error("name mismatch should fail")
	}
	if matchVolumeFilters(v, "local", []string{"label=env=dev"}) {
		t.Error("label value mismatch should fail")
	}
}

func TestMatchNetworkFilters(t *testing.T) {
	var n dockerfmt.Network
	n.ID = "net12345"
	n.Configuration.Name = "backend"
	n.Configuration.Mode = "nat"
	if !matchNetworkFilters(n, []string{"name=back", "id=net1", "driver=bridge", "scope=local"}) {
		t.Error("should match")
	}
	if matchNetworkFilters(n, []string{"driver=host"}) {
		t.Error("driver mismatch should fail")
	}
}

func TestParseScale(t *testing.T) {
	got := parseScale([]string{"web=3", "db=2", "bad"})
	if got["web"] != 3 || got["db"] != 2 || len(got) != 2 {
		t.Errorf("parseScale = %v", got)
	}
}

func TestNormalizeVolume(t *testing.T) {
	var warns []string
	if got := normalizeVolume("/h:/c:ro,z", &warns); got != "/h:/c:ro" {
		t.Errorf("normalizeVolume = %q", got)
	}
	if len(warns) != 1 {
		t.Errorf("expected 1 warning, got %v", warns)
	}
	// named:dst (2 fields) untouched
	if normalizeVolume("vol:/data", &warns) != "vol:/data" {
		t.Error("2-field volume should pass through")
	}
}

func TestNormalizeMount(t *testing.T) {
	var warns []string
	got := normalizeMount("type=tmpfs,destination=/run,tmpfs-size=64m,tmpfs-mode=1777", &warns)
	if got != "type=tmpfs,destination=/run,size=64m,mode=1777" {
		t.Errorf("normalizeMount = %q", got)
	}
	warns = nil
	got = normalizeMount("type=volume,volume-opt=x,destination=/d", &warns)
	if got != "type=volume,destination=/d" || len(warns) != 1 {
		t.Errorf("volume-opt should be dropped+warned: %q %v", got, warns)
	}
}

func TestTmpfsArgs(t *testing.T) {
	var warns []string
	if got := tmpfsArgs("/tmp", &warns); len(got) != 2 || got[0] != "--tmpfs" || got[1] != "/tmp" {
		t.Errorf("path-only tmpfs = %v", got)
	}
	got := tmpfsArgs("/run:size=64m,mode=1777", &warns)
	if len(got) != 2 || got[0] != "--mount" || got[1] != "type=tmpfs,destination=/run,size=64m,mode=1777" {
		t.Errorf("size tmpfs = %v", got)
	}
}

func TestMatchImageFilters(t *testing.T) {
	v := imageView{Repository: "myorg/api", Tag: "1.4"}
	if !matchImageFilters(v, []string{"reference=myorg/*"}) {
		t.Error("glob should match")
	}
	if matchImageFilters(v, []string{"reference=other/*"}) {
		t.Error("non-matching glob should fail")
	}
	if matchImageFilters(v, []string{"dangling=true"}) {
		t.Error("dangling=true matches no tagged image")
	}
}
