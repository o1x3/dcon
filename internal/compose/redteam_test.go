package compose

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestVolumeListMarshalNoTmpfsSentinel locks the round-9 fix: `compose config`
// marshals the project, and a long-form tmpfs volume (stored internally as a
// NUL-byte sentinel) must render as a clean {type: tmpfs, target: ...} mapping
// rather than leaking the sentinel / emitting invalid YAML.
func TestVolumeListMarshalNoTmpfsSentinel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	src := "services:\n  web:\n    image: nginx\n    volumes:\n      - type: tmpfs\n        target: /cache\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(p)
	if err != nil {
		t.Fatalf("compose config marshal failed: %v", err)
	}
	s := string(out)
	if strings.ContainsRune(s, '\x00') {
		t.Errorf("compose config leaked NUL-byte tmpfs sentinel: %q", s)
	}
	if !strings.Contains(s, "tmpfs") || !strings.Contains(s, "/cache") {
		t.Errorf("tmpfs volume not rendered as a mapping:\n%s", s)
	}
}

// TestLoadDropsNilServices locks the systemic fix for the nil-*Service panic
// class: an empty/null service body decodes to a nil *Service, which several
// commands (pull/push/down --rmi/run/scale) iterate without a nil guard. Load
// now drops them, so no downstream command can dereference a nil service.
func TestLoadDropsNilServices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	yaml := "services:\n  web:\n    image: nginx\n  placeholder:\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Services["placeholder"]; ok {
		t.Errorf("nil-body service 'placeholder' should be dropped, services=%v", p.Services)
	}
	if p.Services["web"] == nil {
		t.Error("web service missing/nil after load")
	}
	// Walking the service map must not panic now that nils are gone.
	_ = p.Order()
	_ = p.Levels()
}

// TestNetworkNameExternal locks the fix where an external network with no
// explicit `name:` was project-prefixed (proj_shared) instead of referenced by
// its bare key. dcon never creates external networks, so the prefixed name names
// a network that doesn't exist and the container fails to attach. Mirrors
// VolumeName's external handling.
func TestNetworkNameExternal(t *testing.T) {
	p := &Project{Name: "proj"}
	if got := p.NetworkName("shared", &NetworkSpec{External: true}); got != "shared" {
		t.Errorf("NetworkName(external, no name) = %q, want %q", got, "shared")
	}
	if got := p.NetworkName("shared", &NetworkSpec{External: true, Name: "infra"}); got != "infra" {
		t.Errorf("NetworkName(external, named) = %q, want %q", got, "infra")
	}
	if got := p.NetworkName("front", &NetworkSpec{}); got != "proj_front" {
		t.Errorf("NetworkName(internal) = %q, want %q", got, "proj_front")
	}
}

// TestRunArgsExternalNetworkBareName reproduces the full path: ensureNetworks
// stores NetworkName into p.Nets, then runArgs reads it. The container must join
// the bare external name, not the project-prefixed one.
func TestRunArgsExternalNetworkBareName(t *testing.T) {
	p := &Project{Name: "proj", Nets: map[string]string{}}
	spec := &NetworkSpec{External: true}
	p.Nets["shared"] = p.NetworkName("shared", spec) // what ensureNetworks records
	svc := &Service{Image: "img", Networks: StringKeys{"shared"}}
	args := p.RunArgs("web", svc, 1, "", nil)
	mustContainPair(t, args, "--network", "shared")
	if containsSub(args, "proj_shared") {
		t.Errorf("external network was project-prefixed: %v", args)
	}
}

// TestRunArgsStripsVolumeOpts locks the fix where compose service volumes kept
// macOS-irrelevant mount options (:cached/:delegated/:z/:Z/:consistent) that the
// backend rejects — the run path strips them, the compose path didn't.
func TestRunArgsStripsVolumeOpts(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp/proj"}
	svc := &Service{Image: "img", Volumes: VolumeList{
		"./src:/app:cached", // :cached must be stripped, bind source resolved
		"data:/d:ro",        // :ro preserved (undeclared name passes through)
		"/abs:/a:z",         // SELinux :z stripped
	}}
	args := p.RunArgs("web", svc, 1, "", nil)
	mustContainPair(t, args, "--volume", "/tmp/proj/src:/app")
	mustContainPair(t, args, "--volume", "data:/d:ro")
	mustContainPair(t, args, "--volume", "/abs:/a")
	if containsSub(args, "cached") || containsSub(args, ":z") {
		t.Errorf("backend-rejected mount option not stripped: %v", args)
	}
}

// TestRunArgsShellSplitsStringEntrypoint locks the fix where a string-form
// entrypoint ("python -m app") was passed verbatim to --entrypoint, so the
// backend tried to exec a program literally named "python -m app". Docker
// shell-splits it like command.
func TestRunArgsShellSplitsStringEntrypoint(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp/proj"}
	svc := &Service{Image: "img", Entrypoint: StringList{"python -m app"}}
	args := p.RunArgs("web", svc, 1, "", nil)
	mustContainPair(t, args, "--entrypoint", "python")
	imgIdx := indexOf(args, "img")
	if imgIdx < 0 {
		t.Fatalf("image token not found: %v", args)
	}
	if post := args[imgIdx+1:]; !reflect.DeepEqual(post, []string{"-m", "app"}) {
		t.Errorf("post-image tokens = %v, want [-m app]", post)
	}
	// A list-form entrypoint is taken verbatim (no shell split).
	svc2 := &Service{Image: "img", Entrypoint: StringList{"/bin/sh", "-c"}}
	a2 := p.RunArgs("web", svc2, 1, "", nil)
	mustContainPair(t, a2, "--entrypoint", "/bin/sh")
	if post := a2[indexOf(a2, "img")+1:]; !reflect.DeepEqual(post, []string{"-c"}) {
		t.Errorf("list entrypoint extras = %v, want [-c]", post)
	}
}

// TestRunArgsRoundsFractionalCPUs locks the fix where a fractional compose cpus
// value (0.5, '0.50' under deploy.resources.limits) was passed raw to a backend
// that accepts only whole CPUs — the run path rounds up, the compose path didn't.
func TestRunArgsRoundsFractionalCPUs(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp/proj"}
	mustContainPair(t, p.RunArgs("w", &Service{Image: "img", CPUs: "0.5"}, 1, "", nil), "--cpus", "1")

	svc := &Service{Image: "img"}
	svc.Deploy.Resources.Limits.CPUs = "1.5"
	mustContainPair(t, p.RunArgs("w", svc, 1, "", nil), "--cpus", "2")

	// Integer values pass through unchanged.
	mustContainPair(t, p.RunArgs("w", &Service{Image: "img", CPUs: "2"}, 1, "", nil), "--cpus", "2")
}

// TestOneOffArgsStripsServicePorts locks the round-6 fix: `compose run` must NOT
// publish the service's declared ports by default (docker omits them to avoid
// colliding with the live service; --service-ports re-enables them).
func TestOneOffArgsStripsServicePorts(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "img", Ports: PortList{"8080:80"}}
	got := p.OneOffArgs("web", svc, "", []string{"bash"}, nil, "", false, true)
	if indexOf(got, "--publish") != -1 {
		t.Errorf("compose run must not publish the service's ports by default: %v", got)
	}
}

// TestReplicasHonorsDeployReplicas locks the round-6 fix: deploy.replicas sets
// the instance count, with CLI override > top-level scale: > deploy.replicas > 1.
func TestReplicasHonorsDeployReplicas(t *testing.T) {
	three := 3
	p := &Project{Name: "proj"}

	svc := &Service{Image: "img"}
	svc.Deploy.Replicas = &three
	if n := p.Replicas(svc, 0); n != 3 {
		t.Errorf("deploy.replicas=3 -> %d, want 3", n)
	}
	if n := p.Replicas(svc, 5); n != 5 {
		t.Errorf("CLI override should win -> %d, want 5", n)
	}

	svc2 := &Service{Image: "img", Scale: 2}
	svc2.Deploy.Replicas = &three
	if n := p.Replicas(svc2, 0); n != 2 {
		t.Errorf("top-level scale: should win over deploy.replicas -> %d, want 2", n)
	}

	if n := p.Replicas(&Service{Image: "img"}, 0); n != 1 {
		t.Errorf("unset -> %d, want 1", n)
	}
}

// TestLoadDeployReplicas locks that `deploy: { replicas: N }` parses and drives
// the replica count end-to-end.
func TestLoadDeployReplicas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	yaml := "services:\n  web:\n    image: nginx\n    deploy:\n      replicas: 3\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	pr, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	svc := pr.Services["web"]
	if svc.Deploy.Replicas == nil || *svc.Deploy.Replicas != 3 {
		t.Fatalf("deploy.replicas not parsed: %v", svc.Deploy.Replicas)
	}
	if n := pr.Replicas(svc, 0); n != 3 {
		t.Errorf("Replicas = %d, want 3", n)
	}
}

// TestOneOffArgsEntrypointReset locks the round-3 fix: `compose run --entrypoint
// ""` (entrypointSet=true, entrypoint="") clears the entrypoint and drops the
// service's, while an unset entrypoint (entrypointSet=false) keeps the service's.
func TestOneOffArgsEntrypointReset(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "img", Entrypoint: StringList{"/svc-ep"}}

	// explicit reset: emit --entrypoint "" and drop the service entrypoint.
	got := p.OneOffArgs("web", svc, "", []string{"sh"}, nil, "", true, true)
	mustContainPair(t, got, "--entrypoint", "")
	if indexOf(got, "/svc-ep") != -1 {
		t.Errorf("explicit entrypoint reset must drop the service entrypoint: %v", got)
	}

	// unset: keep the service entrypoint, no override emitted.
	keep := p.OneOffArgs("web", svc, "", []string{"sh"}, nil, "", false, true)
	mustContainPair(t, keep, "--entrypoint", "/svc-ep")
}

// TestOrderNilServiceNoPanic guards Order() against a present-but-nil *Service
// (an empty service body `web:` decodes to a nil pointer). It must skip the nil
// service rather than dereference it and panic.
func TestOrderNilServiceNoPanic(t *testing.T) {
	p := &Project{Name: "t", Services: map[string]*Service{
		"web": nil, // empty body -> nil *Service
		"db":  {Image: "postgres"},
	}}
	order := p.Order() // must not panic
	for _, n := range order {
		if n == "web" {
			t.Errorf("nil service should be skipped, got order %v", order)
		}
	}
	if indexOf(order, "db") < 0 {
		t.Errorf("db missing from order %v", order)
	}
}

// TestLevelsNilServiceDependencyNoPanic guards Levels() (and Order() beneath it)
// when a service depends_on a present-but-nil service.
func TestLevelsNilServiceDependencyNoPanic(t *testing.T) {
	p := &Project{Name: "t", Services: map[string]*Service{
		"web": {Image: "nginx", DependsOn: DependsList{"db"}},
		"db":  nil,
	}}
	levels := p.Levels() // must not panic
	seen := false
	for _, lv := range levels {
		if indexOf(lv, "web") >= 0 {
			seen = true
		}
	}
	if !seen {
		t.Errorf("web missing from levels %v", levels)
	}
}
