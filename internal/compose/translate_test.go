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
		Environment: EnvMap{"A": "1"},
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
	got := p.OneOffArgs("web", svc, "proj_default", []string{"echo", "hi"}, []string{"--env", "FOO=bar"}, "", true)
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
	got := p.OneOffArgs("web", svc, "", []string{"run-cmd"}, nil, "", true)
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
	got := p.OneOffArgs("web", svc, "", nil, []string{"--volume", "/a:/b"}, "/override-ep", true)
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

// TestOneOffArgsRmFalsePreservesCommandRm reproduces the bug where, with
// rm=false, a global token strip removed EVERY "--rm" — including one the user
// passed as an argument to the in-container command (compose run --rm=false web
// mytool --rm). With rm=false the run-level --rm must be absent, but the
// command's own --rm must survive.
func TestOneOffArgsRmFalsePreservesCommandRm(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "nginx"}
	got := p.OneOffArgs("web", svc, "", []string{"mytool", "--rm"}, nil, "", false)
	img := indexOf(got, "nginx")
	if img < 0 {
		t.Fatalf("image not found: %v", got)
	}
	// No run-level --rm before the image.
	for i := 0; i < img; i++ {
		if got[i] == "--rm" {
			t.Errorf("rm=false must not emit a run-level --rm: %v", got)
		}
	}
	// The command's own --rm (after the image) must be preserved.
	foundCmdRm := false
	for i := img + 1; i < len(got); i++ {
		if got[i] == "--rm" {
			foundCmdRm = true
		}
	}
	if !foundCmdRm {
		t.Errorf("the command's own --rm arg was stripped: %v", got)
	}
}

// TestCreateArgsPreservesDetachInCommand verifies CreateArgs drops only the
// leading run-level --detach (positionally), not a literal "--detach" the
// service's own command contains.
func TestCreateArgsPreservesDetachInCommand(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "agent", Command: StringList{"serve", "--detach"}}
	got := p.CreateArgs("web", svc, 1, "proj_default")
	if len(got) == 0 || got[0] != "create" {
		t.Fatalf("CreateArgs must start with create: %v", got)
	}
	img := indexOf(got, "agent")
	if img < 0 {
		t.Fatalf("image not found: %v", got)
	}
	// No run-level --detach before the image.
	for i := 0; i < img; i++ {
		if got[i] == "--detach" {
			t.Errorf("CreateArgs must not emit a run-level --detach: %v", got)
		}
	}
	// The command's own --detach (after the image) must survive.
	if indexOf(got[img+1:], "--detach") < 0 {
		t.Errorf("command's --detach was stripped: %v", got)
	}
}

// TestOneOffArgsEntrypointOverrideDropsExtras reproduces the bug where a
// service with a multi-token entrypoint (["/svc-ep","--flag"]), run with
// --entrypoint override and no command, leaked the old entrypoint's "--flag" as
// an argument to the new entrypoint. Overriding the entrypoint replaces it whole.
func TestOneOffArgsEntrypointOverrideDropsExtras(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "nginx", Entrypoint: StringList{"/svc-ep", "--flag"}}
	got := p.OneOffArgs("web", svc, "", nil, nil, "/override-ep", true)
	if indexOf(got, "--flag") != -1 {
		t.Errorf("stale entrypoint extra --flag must be dropped on override: %v", got)
	}
	mustContainPair(t, got, "--entrypoint", "/override-ep")
	// Without an override, the extras stay (they belong to the service entrypoint).
	keep := p.OneOffArgs("web", svc, "", nil, nil, "", true)
	if indexOf(keep, "--flag") == -1 {
		t.Errorf("without override, the entrypoint extra should remain: %v", keep)
	}
}

// TestOneOffArgsLabelsOneoff reproduces the cross-talk bug: a one-off `compose
// run` container was labeled oneoff=False, indistinguishable from service
// replica #1. It must carry oneoff=True so ps/exec/down don't treat it as a
// replica, while RunArgs (real services) stays oneoff=False.
func TestOneOffArgsLabelsOneoff(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "nginx"}
	one := p.OneOffArgs("web", svc, "", []string{"sh"}, nil, "", true)
	mustContainPair(t, one, "--label", LabelOneoff+"=True")
	if indexOf(one, LabelOneoff+"=False") != -1 {
		t.Errorf("one-off must not also carry oneoff=False: %v", one)
	}
	svc2 := &Service{Image: "nginx"}
	rep := p.RunArgs("web", svc2, 1, "", nil)
	mustContainPair(t, rep, "--label", LabelOneoff+"=False")
}

// TestFlattenLongPortHostIP reproduces the bug where host_ip without published
// produced "host_ip:target" (misread as host_port:container). It must use the
// explicit empty-published 3-part form host_ip::target.
func TestFlattenLongPortHostIP(t *testing.T) {
	if got := flattenLongPort(map[string]string{"target": "80", "host_ip": "127.0.0.1"}); got != "127.0.0.1::80" {
		t.Errorf("host_ip without published = %q, want 127.0.0.1::80", got)
	}
	if got := flattenLongPort(map[string]string{"target": "80", "published": "8080", "host_ip": "127.0.0.1"}); got != "127.0.0.1:8080:80" {
		t.Errorf("full form = %q, want 127.0.0.1:8080:80", got)
	}
	if got := flattenLongPort(map[string]string{"target": "80", "published": "8080", "protocol": "udp"}); got != "8080:80/udp" {
		t.Errorf("proto form = %q, want 8080:80/udp", got)
	}
}

// TestFlattenLongVolumeReadOnlyCasing reproduces the bug where read_only was
// honored only for lowercase "true"; True/TRUE/1 are valid YAML booleans and
// must also produce a :ro (read-only) mount, not a silently writable one.
func TestFlattenLongVolumeReadOnlyCasing(t *testing.T) {
	for _, v := range []string{"true", "True", "TRUE", "1"} {
		got := flattenLongVolume(map[string]string{"source": "./d", "target": "/d", "read_only": v})
		if got != "./d:/d:ro" {
			t.Errorf("read_only=%q -> %q, want ./d:/d:ro", v, got)
		}
	}
	for _, v := range []string{"false", "False", "0", ""} {
		got := flattenLongVolume(map[string]string{"source": "./d", "target": "/d", "read_only": v})
		if got != "./d:/d" {
			t.Errorf("read_only=%q -> %q, want ./d:/d (writable)", v, got)
		}
	}
}

// TestOneOffArgsKeepsEntrypointExtrasWithCmdOverride reproduces the bug where a
// command override dropped the service entrypoint's extra tokens when the
// entrypoint itself was NOT overridden. `compose run web shell` on a service
// with entrypoint [python,-m,flask] + command [run] must run
// `python -m flask shell` (command replaced, entrypoint kept whole).
func TestOneOffArgsKeepsEntrypointExtrasWithCmdOverride(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "nginx", Entrypoint: StringList{"python", "-m", "flask"}, Command: StringList{"run"}}
	got := p.OneOffArgs("web", svc, "", []string{"shell"}, nil, "", true)
	mustContainPair(t, got, "--entrypoint", "python")
	img := indexOf(got, "nginx")
	if img < 0 {
		t.Fatalf("image not found: %v", got)
	}
	post := got[img+1:]
	want := []string{"-m", "flask", "shell"}
	if !reflect.DeepEqual(post, want) {
		t.Errorf("post-image tokens = %v, want %v (entrypoint extras kept, command replaced)", post, want)
	}
}

// TestResolveVolumeNamedVolume reproduces the bug where a declared named volume
// was mounted by its bare compose key instead of the project-scoped backend name
// that ensureVolumes actually creates (e.g. `data` vs `proj_data`), so the
// container got a different volume than declared.
func TestResolveVolumeNamedVolume(t *testing.T) {
	p := &Project{
		Name:    "proj",
		Dir:     "/tmp/proj",
		Volumes: map[string]*VolumeSpec{"data": {}, "named": {Name: "custom"}},
	}
	svc := &Service{Image: "x", Volumes: VolumeList{
		"data:/var/lib", // declared -> proj_data
		"named:/n",      // declared with explicit name -> custom
		"ext:/e",        // undeclared -> passthrough
		"/abs:/a",       // absolute bind -> passthrough
		"./rel:/r",      // relative bind -> resolved to absolute
	}}
	args := p.RunArgs("web", svc, 1, "", nil)
	mustContainPair(t, args, "--volume", "proj_data:/var/lib")
	mustContainPair(t, args, "--volume", "custom:/n")
	mustContainPair(t, args, "--volume", "ext:/e")
	mustContainPair(t, args, "--volume", "/abs:/a")
	if !containsSub(args, "/tmp/proj/rel:/r") {
		t.Errorf("relative bind not resolved to absolute: %v", args)
	}
}

// TestLevelsCycleNoDeadlock guards that a depends_on cycle is handled safely:
// every service is still emitted (no deadlock, no panic), even if the leading
// level is empty.
func TestLevelsCycleNoDeadlock(t *testing.T) {
	p := &Project{Name: "t", Services: map[string]*Service{
		"a": {DependsOn: DependsList{"b"}},
		"b": {DependsOn: DependsList{"a"}},
	}}
	levels := p.Levels()
	seen := map[string]bool{}
	for _, lv := range levels {
		for _, n := range lv {
			seen[n] = true
		}
	}
	if !seen["a"] || !seen["b"] {
		t.Errorf("cycle members must all be emitted; got levels %v", levels)
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
	svc := &Service{Image: "img", Environment: EnvMap{"B": "2", "A": "1", "C": "3"}, Labels: MapList{"z": "1", "a": "2"}}
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
