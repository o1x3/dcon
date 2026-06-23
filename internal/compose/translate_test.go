package compose

import (
	"reflect"
	"sort"
	"testing"
)

func TestShellSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"nginx -g daemon", []string{"nginx", "-g", "daemon"}},
		{`sh -c "echo hi there"`, []string{"sh", "-c", "echo hi there"}},
		{`echo 'a b' c`, []string{"echo", "a b", "c"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
		{"", nil},
	}
	for _, c := range cases {
		got := ShellSplit(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ShellSplit(%q) = %#v; want %#v", c.in, got, c.want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"MyApp":     "myapp",
		"my app!":   "myapp",
		"foo-bar_1": "foo-bar_1",
		"":          "default",
		"...":       "default",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestOrderRespectsDependsOn(t *testing.T) {
	p := &Project{
		Name: "t",
		Services: map[string]*Service{
			"web":   {DependsOn: DependsList{"db"}},
			"db":    {},
			"cache": {DependsOn: DependsList{"db"}},
		},
	}
	order := p.Order()
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["db"] > pos["web"] || pos["db"] > pos["cache"] {
		t.Errorf("db must precede its dependents; order=%v", order)
	}
}

func TestLevels(t *testing.T) {
	// db, cache are independent roots; api depends on both; web depends on api.
	p := &Project{
		Name: "t",
		Services: map[string]*Service{
			"db":    {},
			"cache": {},
			"api":   {DependsOn: DependsList{"db", "cache"}},
			"web":   {DependsOn: DependsList{"api"}},
		},
	}
	levels := p.Levels()
	if len(levels) != 3 {
		t.Fatalf("got %d levels (%v), want 3", len(levels), levels)
	}
	wantSet := func(got []string, want ...string) {
		t.Helper()
		g := append([]string{}, got...)
		sort.Strings(g)
		sort.Strings(want)
		if !reflect.DeepEqual(g, want) {
			t.Errorf("level mismatch: got %v, want %v", g, want)
		}
	}
	wantSet(levels[0], "cache", "db") // independent roots run together
	wantSet(levels[1], "api")
	wantSet(levels[2], "web")

	// Invariant: no service shares a level with one of its dependencies.
	levelOf := map[string]int{}
	for i, lv := range levels {
		for _, n := range lv {
			levelOf[n] = i
		}
	}
	for name, svc := range p.Services {
		for _, d := range svc.DependsOn {
			if levelOf[d] >= levelOf[name] {
				t.Errorf("%s (level %d) must be a later level than its dep %s (level %d)", name, levelOf[name], d, levelOf[d])
			}
		}
	}

	// Empty project yields no levels.
	if got := (&Project{Services: map[string]*Service{}}).Levels(); got != nil {
		t.Errorf("empty project Levels() = %v, want nil", got)
	}
}

func TestRunArgsBasics(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp/proj"}
	svc := &Service{
		Image:       "nginx",
		Environment: MapList{"A": "1"},
		Ports:       []string{"8080:80"},
		Command:     StringList{"nginx -g daemon"},
	}
	args := p.RunArgs("web", svc, 1, "proj_default", nil)
	joined := args
	mustContain(t, joined, "run")
	mustContain(t, joined, "--detach")
	mustContainPair(t, joined, "--name", "proj-web-1")
	mustContainPair(t, joined, "--network", "proj_default")
	mustContainPair(t, joined, "--env", "A=1")
	mustContainPair(t, joined, "--publish", "8080:80")
	mustContain(t, joined, "nginx")
	// command shell-split appears after the image
	mustContain(t, joined, "daemon")
}

func mustContain(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("args %v missing %q", args, want)
}

func mustContainPair(t *testing.T, args []string, flag, val string) {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return
		}
	}
	t.Errorf("args %v missing pair %q %q", args, flag, val)
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func TestOneOffArgsEnvAndOverride(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "nginx", Command: StringList{"orig"}}
	got := p.OneOffArgs("web", svc, "proj_default", []string{"echo", "hi"}, []string{"--env", "FOO=bar"}, "")
	mustContain(t, got, "--rm")
	mustContainPair(t, got, "--env", "FOO=bar")
	// --name and --detach must be dropped for one-off
	if indexOf(got, "--name") != -1 || indexOf(got, "--detach") != -1 {
		t.Errorf("one-off must drop --name/--detach: %v", got)
	}
	// override command after image, service's own command dropped
	img := indexOf(got, "nginx")
	if img < 0 || indexOf(got, "echo") < img || indexOf(got, "orig") != -1 {
		t.Errorf("override command placement wrong: %v", got)
	}
	// env before image
	if indexOf(got, "FOO=bar") > img {
		t.Errorf("env must precede image: %v", got)
	}
}

func TestOneOffArgsImageEqualFlagValue(t *testing.T) {
	// regression: a flag value equal to the image ref must not break splitting
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "myimg", Platform: "myimg"} // platform value == image
	got := p.OneOffArgs("web", svc, "", []string{"run-cmd"}, nil, "")
	img := indexOf(got, "myimg")
	// the image is the LAST occurrence (platform value is earlier as a flag arg)
	last := -1
	for i, a := range got {
		if a == "myimg" {
			last = i
		}
	}
	if last == img && indexOf(got, "run-cmd") < last {
		t.Errorf("command override must land after the image token: %v", got)
	}
	if indexOf(got, "run-cmd") < last {
		t.Errorf("override should be after image; got %v", got)
	}
}

func TestOneOffArgsOverridesEntrypoint(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "nginx", Entrypoint: StringList{"/svc-ep"}}
	got := p.OneOffArgs("web", svc, "", nil, []string{"--volume", "/a:/b"}, "/override-ep")
	// CLI entrypoint replaces the service entrypoint (no duplicate, no /svc-ep)
	if indexOf(got, "/svc-ep") != -1 {
		t.Errorf("service entrypoint should be replaced: %v", got)
	}
	mustContainPair(t, got, "--entrypoint", "/override-ep")
	mustContainPair(t, got, "--volume", "/a:/b")
	// override volume before the image
	if indexOf(got, "/a:/b") > indexOf(got, "nginx") {
		t.Errorf("override must precede image: %v", got)
	}
}

func TestRunArgsEntrypointAndCommandOrder(t *testing.T) {
	p := &Project{Name: "p", Dir: "/tmp"}
	svc := &Service{Image: "img", Entrypoint: StringList{"/ep", "--flag"}, Command: StringList{"arg1"}}
	args := p.RunArgs("s", svc, 1, "", nil)
	mustContainPair(t, args, "--entrypoint", "/ep")
	img := indexOf(args, "img")
	if indexOf(args, "--flag") < img || indexOf(args, "arg1") < img {
		t.Errorf("entrypoint extras and command must follow the image: %v", args)
	}
	if indexOf(args, "--flag") > indexOf(args, "arg1") {
		t.Errorf("entrypoint extras must precede command: %v", args)
	}
}

func TestRunArgsEmptyEntrypointNotEmitted(t *testing.T) {
	p := &Project{Name: "p", Dir: "/tmp"}
	svc := &Service{Image: "img", Entrypoint: StringList{""}}
	args := p.RunArgs("s", svc, 1, "", nil)
	if indexOf(args, "--entrypoint") != -1 {
		t.Errorf(`entrypoint: "" must not emit --entrypoint: %v`, args)
	}
}

func TestRunArgsDeterministic(t *testing.T) {
	p := &Project{Name: "p", Dir: "/tmp"}
	svc := &Service{Image: "img", Environment: MapList{"B": "2", "A": "1", "C": "3"}, Labels: MapList{"z": "1", "a": "2"}}
	first := p.RunArgs("s", svc, 1, "", nil)
	for i := 0; i < 20; i++ {
		if !reflect.DeepEqual(first, p.RunArgs("s", svc, 1, "", nil)) {
			t.Fatalf("RunArgs output is nondeterministic")
		}
	}
}

func TestRunArgsPerServiceNetworks(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp", Nets: map[string]string{"frontend": "proj_frontend", "backend": "proj_backend"}}
	svc := &Service{Image: "img", Networks: StringKeys{"frontend", "backend"}}
	args := p.RunArgs("web", svc, 1, "proj_default", nil)
	mustContainPair(t, args, "--network", "proj_frontend")
	mustContainPair(t, args, "--network", "proj_backend")
	// must NOT also attach to default when service declares networks
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--network" && args[i+1] == "proj_default" {
			t.Errorf("should not attach default when networks declared: %v", args)
		}
	}
}

func TestRunArgsDefaultNetworkFallback(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "img"}
	args := p.RunArgs("web", svc, 1, "proj_default", nil)
	mustContainPair(t, args, "--network", "proj_default")
}

func TestRunArgsUlimitsAndDeploy(t *testing.T) {
	p := &Project{Name: "p", Dir: "/tmp"}
	svc := &Service{Image: "img", Ulimits: Ulimits{"nofile=1024:65535"}}
	svc.Deploy.Resources.Limits.CPUs = "2"
	svc.Deploy.Resources.Limits.Memory = "512m"
	args := p.RunArgs("s", svc, 1, "", nil)
	mustContainPair(t, args, "--ulimit", "nofile=1024:65535")
	mustContainPair(t, args, "--cpus", "2")
	mustContainPair(t, args, "--memory", "512m")
}
