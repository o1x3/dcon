package compose

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleCompose = `
name: shop
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    environment:
      - ENV=prod
      - DEBUG=1
    depends_on:
      - api
    volumes:
      - ./html:/usr/share/nginx/html
  api:
    build:
      context: ./api
      dockerfile: Dockerfile.api
      args:
        VERSION: "2"
    environment:
      KEY: value
    command: ./run --port 9000
volumes:
  data: {}
networks:
  default: {}
`

func loadSample(t *testing.T) *Project {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte(sampleCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return p
}

func TestLoadParsesServices(t *testing.T) {
	p := loadSample(t)
	if p.Name != "shop" {
		t.Errorf("name = %q; want shop", p.Name)
	}
	if len(p.Services) != 2 {
		t.Fatalf("want 2 services; got %d", len(p.Services))
	}
	web := p.Services["web"]
	if web.Image != "nginx:alpine" {
		t.Errorf("web image = %q", web.Image)
	}
	if web.Environment["ENV"] != "prod" || web.Environment["DEBUG"] != "1" {
		t.Errorf("web env list-form parse wrong: %v", web.Environment)
	}
	if len(web.DependsOn) != 1 || web.DependsOn[0] != "api" {
		t.Errorf("web depends_on wrong: %v", web.DependsOn)
	}
}

func TestLoadMacAddress(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	body := `
services:
  web:
    image: nginx
    mac_address: "02:42:ac:11:00:02"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := p.Services["web"].MacAddress; got != "02:42:ac:11:00:02" {
		t.Errorf("mac_address = %q", got)
	}
}

func TestLoadBuildMapForm(t *testing.T) {
	p := loadSample(t)
	api := p.Services["api"]
	if !api.Build.IsSet() {
		t.Fatal("api build not set")
	}
	if api.Build.Context != "./api" || api.Build.Dockerfile != "Dockerfile.api" {
		t.Errorf("build map parse wrong: %+v", api.Build)
	}
	if api.Build.Args["VERSION"] != "2" {
		t.Errorf("build args map-form wrong: %v", api.Build.Args)
	}
	if api.Environment["KEY"] != "value" {
		t.Errorf("env map-form wrong: %v", api.Environment)
	}
}

func TestOrderTopological(t *testing.T) {
	p := loadSample(t)
	order := p.Order()
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["api"] > pos["web"] {
		t.Errorf("api (dependency) must come before web; order=%v", order)
	}
}

func TestRunArgsResolvesRelativeVolume(t *testing.T) {
	p := loadSample(t)
	web := p.Services["web"]
	args := p.RunArgs("web", web, 1, "shop_default", nil)
	// the ./html bind source should be absolute (under the temp dir)
	found := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--volume" && filepath.IsAbs(splitColon(args[i+1])) {
			found = true
		}
	}
	if !found {
		t.Errorf("relative volume not resolved to absolute: %v", args)
	}
}

func TestRunArgsShellSplitsStringCommand(t *testing.T) {
	p := loadSample(t)
	api := p.Services["api"]
	args := p.RunArgs("api", api, 1, "", nil)
	// api's `command: ./run --port 9000` should shell-split after the image.
	if !containsSub(args, "9000") || !containsSub(args, "--port") || !containsSub(args, "./run") {
		t.Errorf("string command not shell-split: %v", args)
	}
}

func TestBuildArgs(t *testing.T) {
	p := loadSample(t)
	api := p.Services["api"]
	args := p.BuildArgs("api", api)
	if args[0] != "build" {
		t.Errorf("build args[0]=%q", args[0])
	}
	if !containsSub(args, p.BuildImageName("api")) {
		t.Errorf("build should tag project image: %v", args)
	}
}

// TestBuildArgsTagsExplicitImage reproduces the bug where a service with BOTH
// build: and image: built an image tagged with the derived project name while
// the container ran svc.Image — so the freshly built image was never used.
// BuildArgs must tag the explicit image: so build output and run ref agree.
func TestBuildArgsTagsExplicitImage(t *testing.T) {
	p := &Project{Name: "proj", Dir: "/tmp"}
	svc := &Service{Image: "myorg/web:1.0", Build: Build{Context: "."}}
	svc.Build.set = true
	args := p.BuildArgs("web", svc)
	if !containsSub(args, "myorg/web:1.0") {
		t.Errorf("build must tag the explicit image: ref; got %v", args)
	}
	if containsSub(args, p.BuildImageName("web")) {
		t.Errorf("build must NOT tag the derived name when image: is set; got %v", args)
	}
	// Sanity: build tag == run image ref.
	if p.ImageRef("web", svc) != "myorg/web:1.0" {
		t.Errorf("imageRef mismatch: %q", p.ImageRef("web", svc))
	}
}

func TestDefaultNetworkAndName(t *testing.T) {
	p := loadSample(t)
	if p.DefaultNetwork() != "shop_default" {
		t.Errorf("DefaultNetwork=%q", p.DefaultNetwork())
	}
	if p.ContainerName("web", 1, p.Services["web"]) != "shop-web-1" {
		t.Errorf("ContainerName=%q", p.ContainerName("web", 1, p.Services["web"]))
	}
}

func TestEnvVarExpansion(t *testing.T) {
	t.Setenv("DCON_TEST_TAG", "v9")
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	os.WriteFile(path, []byte("services:\n  a:\n    image: img:${DCON_TEST_TAG:-latest}\n"), 0o644)
	p, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Services["a"].Image != "img:v9" {
		t.Errorf("env expansion wrong: %q", p.Services["a"].Image)
	}
}

func TestEnvFileForms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	yaml := `
services:
  scalar:
    image: x
    env_file: .env
  list:
    image: x
    env_file:
      - a.env
      - path: b.env
        required: false
`
	os.WriteFile(path, []byte(yaml), 0o644)
	p, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Services["scalar"].EnvFile) != 1 || !p.Services["scalar"].EnvFile[0].Required {
		t.Errorf("scalar env_file wrong: %+v", p.Services["scalar"].EnvFile)
	}
	lf := p.Services["list"].EnvFile
	if len(lf) != 2 || lf[0].Path != "a.env" || !lf[0].Required || lf[1].Path != "b.env" || lf[1].Required {
		t.Errorf("list env_file wrong: %+v", lf)
	}
}

// TestLongFormPortsAndVolumes reproduces the bug where `ports:`/`volumes:` in
// the Compose long (mapping) form failed to unmarshal into []string, hard-
// erroring the entire compose file. They must parse and flatten to the same
// short-form strings the translator emits, alongside short-form entries.
func TestLongFormPortsAndVolumes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	yaml := `
services:
  web:
    image: nginx
    ports:
      - "8080:80"
      - target: 443
        published: "8443"
        protocol: tcp
      - 9000
    volumes:
      - ./short:/short
      - type: bind
        source: ./data
        target: /data
        read_only: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load must not fail on long-form ports/volumes: %v", err)
	}
	web := p.Services["web"]
	wantPorts := []string{"8080:80", "8443:443/tcp", "9000"}
	if len(web.Ports) != len(wantPorts) {
		t.Fatalf("ports = %v, want %v", web.Ports, wantPorts)
	}
	for i, w := range wantPorts {
		if web.Ports[i] != w {
			t.Errorf("ports[%d] = %q, want %q", i, web.Ports[i], w)
		}
	}
	wantVols := []string{"./short:/short", "./data:/data:ro"}
	if len(web.Volumes) != len(wantVols) {
		t.Fatalf("volumes = %v, want %v", web.Volumes, wantVols)
	}
	for i, w := range wantVols {
		if web.Volumes[i] != w {
			t.Errorf("volumes[%d] = %q, want %q", i, web.Volumes[i], w)
		}
	}
}

// TestMapListSkipsEmptyKeys reproduces the bug where a blank ("") or keyless
// ("=value") environment/labels list entry produced an empty-key map entry,
// later emitted as a malformed `--env =…` arg. Such entries must be dropped.
func TestMapListSkipsEmptyKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	yaml := `
services:
  web:
    image: nginx
    environment:
      - "GOOD=1"
      - ""
      - "=orphan"
    labels:
      - "owner=team"
      - ""
      - "=orphan"
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	env := p.Services["web"].Environment
	if _, bad := env[""]; bad {
		t.Errorf("empty-key env entry leaked: %v", env)
	}
	if env["GOOD"] != "1" || len(env) != 1 {
		t.Errorf("environment = %v, want only GOOD=1", env)
	}
	// labels go through MapList (not EnvMap); blank/keyless entries must drop too.
	labels := p.Services["web"].Labels
	if _, bad := labels[""]; bad {
		t.Errorf("empty-key label entry leaked: %v", labels)
	}
	if labels["owner"] != "team" || len(labels) != 1 {
		t.Errorf("labels = %v, want only owner=team", labels)
	}
}

// TestEnvBareKeyHostPassthrough reproduces the bug where a bare environment key
// (`- FOO` list form or `FOO:` null map form) was set to "" instead of
// inheriting the host's $FOO (docker compose passthrough). An unset bare key is
// omitted, not emitted as FOO="".
func TestEnvBareKeyHostPassthrough(t *testing.T) {
	t.Setenv("DCON_TEST_PASSTHRU", "from-host")
	mustUnsetForTest(t, "DCON_UNSET_XYZ") // assertion below assumes it is unset
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	yaml := `
services:
  list:
    image: x
    environment:
      - DCON_TEST_PASSTHRU
      - EXPLICIT=val
      - DCON_UNSET_XYZ
  mapform:
    image: x
    environment:
      DCON_TEST_PASSTHRU:
      KEYED: v2
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	le := p.Services["list"].Environment
	if le["DCON_TEST_PASSTHRU"] != "from-host" {
		t.Errorf("list bare key should inherit host value; got %q", le["DCON_TEST_PASSTHRU"])
	}
	if le["EXPLICIT"] != "val" {
		t.Errorf("explicit value wrong: %q", le["EXPLICIT"])
	}
	if _, present := le["DCON_UNSET_XYZ"]; present {
		t.Errorf("unset bare key must be omitted, not empty: %v", le)
	}
	me := p.Services["mapform"].Environment
	if me["DCON_TEST_PASSTHRU"] != "from-host" {
		t.Errorf("map null-value key should inherit host value; got %q", me["DCON_TEST_PASSTHRU"])
	}
	if me["KEYED"] != "v2" {
		t.Errorf("map keyed value wrong: %q", me["KEYED"])
	}
}

// TestDollarEscapePreserved reproduces the bug where `$$` (compose's escape for
// a literal `$`) was deleted instead of collapsed to a single `$`. e.g.
// `command: echo $$HOME` must keep a literal `$HOME`, not become `echo HOME`.
func TestDollarEscapePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	yaml := `
services:
  web:
    image: alpine
    command: echo $$HOME and price $$5
    environment:
      - LITERAL=a$$b
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	web := p.Services["web"]
	if len(web.Command) != 1 || web.Command[0] != "echo $HOME and price $5" {
		t.Errorf("command = %q, want 'echo $HOME and price $5'", web.Command)
	}
	if web.Environment["LITERAL"] != "a$b" {
		t.Errorf("env LITERAL = %q, want a$b", web.Environment["LITERAL"])
	}
}

// TestExpandVar reproduces the bug where only ${VAR:-default} was honored;
// every other operator (:?, :+, -, +, :=) silently produced empty, even for SET
// variables. Covers bash/compose parameter-expansion semantics.
func TestExpandVar(t *testing.T) {
	t.Setenv("SETV", "x")
	t.Setenv("EMPTYV", "")
	mustUnsetForTest(t, "UNSETV") // cases below assume UNSETV is absent from the env
	// (key, want, wantErr)
	cases := []struct {
		key     string
		want    string
		wantErr bool
	}{
		{"$", "$", false},         // $$ escape
		{"SETV", "x", false},      // ${SETV}
		{"UNSETV", "", false},     // ${UNSETV}
		{"SETV:-d", "x", false},   // set -> value
		{"UNSETV:-d", "d", false}, // unset -> default
		{"EMPTYV:-d", "d", false}, // empty (colon) -> default
		{"EMPTYV-d", "", false},   // empty (no colon) -> the empty value
		{"UNSETV-d", "d", false},  // unset (no colon) -> default
		{"SETV:+a", "a", false},   // set non-empty -> alternate
		{"EMPTYV:+a", "", false},  // empty (colon) -> "" (not "present")
		{"EMPTYV+a", "a", false},  // empty but set (no colon) -> alternate
		{"UNSETV:+a", "", false},  // unset -> ""
		{"SETV:?msg", "x", false}, // set -> value
		{"UNSETV:?msg", "", true}, // unset -> error
		{"EMPTYV:?msg", "", true}, // empty (colon) -> error
		{"SETV:=d", "x", false},   // set -> value
		{"UNSETV:=d", "d", false}, // unset -> default
	}
	for _, c := range cases {
		got, err := expandVar(c.key)
		if (err != nil) != c.wantErr {
			t.Errorf("expandVar(%q) err=%v, wantErr=%v", c.key, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("expandVar(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

// TestLoadRequiredVarErrors confirms a required-but-unset ${VAR:?} aborts Load.
func TestLoadRequiredVarErrors(t *testing.T) {
	mustUnsetForTest(t, "DCON_REQ_UNSET") // the required-var error depends on it being unset
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(path, []byte("services:\n  a:\n    image: img:${DCON_REQ_UNSET:?tag required}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Error("Load should fail when a required ${VAR:?} variable is unset")
	}
}

// mustUnsetForTest makes key deterministically absent for the duration of the
// test, restoring whatever the host had afterward. t.Setenv records the original
// value (and its restore), then Unsetenv clears it so assertions about an unset
// variable don't depend on the runner's environment.
func mustUnsetForTest(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}

func TestServiceEnabledProfiles(t *testing.T) {
	noProf := &Service{}
	if !noProf.Enabled(map[string]bool{}) {
		t.Error("service without profiles should always be enabled")
	}
	prof := &Service{Profiles: []string{"debug"}}
	if prof.Enabled(map[string]bool{}) {
		t.Error("profiled service should be disabled when profile inactive")
	}
	if !prof.Enabled(map[string]bool{"debug": true}) {
		t.Error("profiled service should be enabled when profile active")
	}
}

func splitColon(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i]
		}
	}
	return s
}

func containsSub(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
