package compose

import (
	"reflect"
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
