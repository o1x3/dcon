package compose

// Round-3 red-team regression tests: YAML merge keys/aliases, .env
// interpolation, COMPOSE_PROJECT_NAME/COMPOSE_FILE, multi-file merge, port
// normalization, dcon-side env_file parsing, config-hash labels, extends,
// nested interpolation, and volume source expansion.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeFiles writes name->content into a temp dir and returns it.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// unsetEnv unsets a variable for the test's duration (t.Setenv registers the
// restore; plain os.Unsetenv would leak into other tests).
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	os.Unsetenv(key)
}

func loadDir(t *testing.T, dir, file string) *Project {
	t.Helper()
	p, err := Load(filepath.Join(dir, file), "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return p
}

// --- item 1: YAML merge keys and alias items in the manual node iterators ---

func TestEnvMapMergeKeyAndAliases(t *testing.T) {
	dir := writeFiles(t, map[string]string{"compose.yaml": `
x-common: &common-env
  A: "1"
  B: base
x-port: &tcp "9000:90"
services:
  web:
    image: nginx
    environment:
      <<: *common-env
      B: local
    ports:
      - "8080:80"
      - *tcp
`})
	p := loadDir(t, dir, "compose.yaml")
	web := p.Services["web"]
	if web.Environment["A"] != "1" {
		t.Errorf("merge key <<: dropped: env = %v", web.Environment)
	}
	if web.Environment["B"] != "local" {
		t.Errorf("local key must win over merged default: B = %q", web.Environment["B"])
	}
	if len(web.Ports) != 2 || web.Ports[1] != "9000:90" {
		t.Errorf("alias sequence item dropped: ports = %v", web.Ports)
	}
}

func TestEnvFileAlias(t *testing.T) {
	dir := writeFiles(t, map[string]string{"compose.yaml": `
x-envfile: &ef common.env
services:
  web:
    image: nginx
    env_file:
      - *ef
`})
	p := loadDir(t, dir, "compose.yaml")
	ef := p.Services["web"].EnvFile
	if len(ef) != 1 || ef[0].Path != "common.env" {
		t.Errorf("aliased env_file entry dropped: %v", ef)
	}
}

// --- item 2: .env interpolation, precedence, COMPOSE_PROJECT_NAME, COMPOSE_FILE ---

func TestDotEnvInterpolation(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"compose.yaml": "services:\n  web:\n    image: ${IMG}:${TAG}\n",
		".env":         "IMG=nginx\nTAG=file-tag\n",
	})
	unsetEnv(t, "IMG")
	unsetEnv(t, "COMPOSE_PROJECT_NAME")
	t.Setenv("TAG", "os-tag") // OS env must beat the .env file
	p := loadDir(t, dir, "compose.yaml")
	if got := p.Services["web"].Image; got != "nginx:os-tag" {
		t.Errorf("image = %q; want nginx:os-tag (.env for IMG, OS env for TAG)", got)
	}
}

func TestExplicitEnvFileReplacesDotEnv(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"compose.yaml": "services:\n  web:\n    image: ${IMG:-fallback}\n",
		".env":         "IMG=from-dotenv\n",
		"custom.env":   "IMG=from-custom\n",
	})
	unsetEnv(t, "IMG")
	p, err := LoadFiles([]string{filepath.Join(dir, "compose.yaml")}, "", []string{filepath.Join(dir, "custom.env")})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Services["web"].Image; got != "from-custom" {
		t.Errorf("image = %q; want from-custom (--env-file replaces .env)", got)
	}
}

func TestComposeProjectNameEnv(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"compose.yaml": "name: fromyaml\nservices:\n  web:\n    image: nginx\n",
	})
	t.Setenv("COMPOSE_PROJECT_NAME", "From_ENV")
	p := loadDir(t, dir, "compose.yaml")
	if p.Name != "from_env" {
		t.Errorf("name = %q; want from_env (COMPOSE_PROJECT_NAME beats top-level name)", p.Name)
	}
	// Explicit -p still wins over the env var.
	p2, err := Load(filepath.Join(dir, "compose.yaml"), "cli")
	if err != nil {
		t.Fatal(err)
	}
	if p2.Name != "cli" {
		t.Errorf("name = %q; want cli (-p beats COMPOSE_PROJECT_NAME)", p2.Name)
	}
}

func TestComposeProjectNameFromDotEnv(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"compose.yaml": "services:\n  web:\n    image: nginx\n",
		".env":         "COMPOSE_PROJECT_NAME=dotenvproj\n",
	})
	unsetEnv(t, "COMPOSE_PROJECT_NAME")
	p := loadDir(t, dir, "compose.yaml")
	if p.Name != "dotenvproj" {
		t.Errorf("name = %q; want dotenvproj (COMPOSE_PROJECT_NAME from .env)", p.Name)
	}
}

func TestFindFilesComposeFileEnv(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.yaml": "services: {}\n",
		"b.yaml": "services: {}\n",
	})
	t.Chdir(dir)
	t.Setenv("COMPOSE_FILE", "a.yaml:b.yaml")
	got, err := FindFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml")}
	if !pathsEqual(got, want) {
		t.Errorf("FindFiles = %v; want %v", got, want)
	}
	// Custom separator.
	t.Setenv("COMPOSE_PATH_SEPARATOR", ";")
	t.Setenv("COMPOSE_FILE", "b.yaml;a.yaml")
	got, err = FindFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{filepath.Join(dir, "b.yaml"), filepath.Join(dir, "a.yaml")}
	if !pathsEqual(got, want) {
		t.Errorf("FindFiles with separator = %v; want %v", got, want)
	}
}

// pathsEqual compares path slices after symlink resolution (macOS tempdirs
// live under /private/var vs /var).
func pathsEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, _ := filepath.EvalSymlinks(got[i])
		w, _ := filepath.EvalSymlinks(want[i])
		if g != w {
			return false
		}
	}
	return true
}

// --- item 16: override auto-load + multi-file merge semantics ---

func TestFindFilesAutoLoadsOverride(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"compose.yaml":          "services: {}\n",
		"compose.override.yaml": "services: {}\n",
	})
	t.Chdir(dir)
	unsetEnv(t, "COMPOSE_FILE")
	got, err := FindFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || filepath.Base(got[0]) != "compose.yaml" || filepath.Base(got[1]) != "compose.override.yaml" {
		t.Errorf("FindFiles = %v; want [compose.yaml compose.override.yaml]", got)
	}
}

func TestLoadFilesMerge(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"base.yaml": `
services:
  web:
    image: nginx:1.0
    command: ["run", "--base"]
    environment:
      A: base
      B: base
    ports:
      - "8080:80"
    volumes:
      - ./base:/app
  db:
    image: postgres
`,
		"override.yaml": `
services:
  web:
    image: nginx:2.0
    command: ["serve"]
    environment:
      B: override
      C: added
    ports:
      - "9090:90"
    volumes:
      - ./override:/app
  cache:
    image: redis
`,
	})
	p, err := LoadFiles([]string{filepath.Join(dir, "base.yaml"), filepath.Join(dir, "override.yaml")}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	web := p.Services["web"]
	if web == nil {
		t.Fatal("web missing after merge")
	}
	if web.Image != "nginx:2.0" {
		t.Errorf("scalar override: image = %q; want nginx:2.0", web.Image)
	}
	if !reflect.DeepEqual([]string(web.Command), []string{"serve"}) {
		t.Errorf("command must be REPLACED, not concatenated: %v", web.Command)
	}
	wantEnv := map[string]string{"A": "base", "B": "override", "C": "added"}
	if !reflect.DeepEqual(map[string]string(web.Environment), wantEnv) {
		t.Errorf("environment merge by key = %v; want %v", web.Environment, wantEnv)
	}
	if !reflect.DeepEqual([]string(web.Ports), []string{"8080:80", "9090:90"}) {
		t.Errorf("ports must concatenate: %v", web.Ports)
	}
	// Volumes are keyed by container target: the override's /app mount wins.
	if len(web.Volumes) != 1 || !strings.HasSuffix(web.Volumes[0], "/override:/app") {
		t.Errorf("volume for same target must be replaced by the later file: %v", web.Volumes)
	}
	if p.Services["db"] == nil || p.Services["cache"] == nil {
		t.Errorf("services from both files must survive: %v", len(p.Services))
	}
}

// --- item 4: container-only port forms ---

func TestNormalizePortContainerOnly(t *testing.T) {
	resetWarnings()
	cases := map[string]string{
		"3000":              "3000:3000",
		"3000/udp":          "3000:3000/udp",
		"8080:80":           "8080:80",
		"1.2.3.4:8080:80":   "1.2.3.4:8080:80",
		"1.2.3.4::3000":     "1.2.3.4:3000:3000",
		"::3000":            "3000:3000",
		"1.2.3.4::53/udp":   "1.2.3.4:53:53/udp",
		"8000-8010:80-90":   "8000-8010:80-90", // ranges pass through (warned)
		"3000-3010":         "3000-3010",
		"[::1]:8080:80":     "[::1]:8080:80", // bracketed IPv6 untouched
		"127.0.0.1:5353:53": "127.0.0.1:5353:53",
	}
	for in, want := range cases {
		if got := normalizePort(in); got != want {
			t.Errorf("normalizePort(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestRunArgsPublishesContainerOnlyPort(t *testing.T) {
	dir := writeFiles(t, map[string]string{"compose.yaml": `
services:
  web:
    image: nginx
    ports:
      - "3000"
`})
	p := loadDir(t, dir, "compose.yaml")
	args := p.RunArgs("web", p.Services["web"], 1, "", nil)
	found := false
	for i, a := range args {
		if a == "--publish" && i+1 < len(args) {
			if args[i+1] != "3000:3000" {
				t.Errorf("--publish %q; want 3000:3000", args[i+1])
			}
			found = true
		}
	}
	if !found {
		t.Errorf("no --publish emitted: %v", args)
	}
}

// --- item 5: config-hash label ---

func TestRunArgsConfigHashLabel(t *testing.T) {
	dir := writeFiles(t, map[string]string{"compose.yaml": `
services:
  web:
    image: nginx
`})
	p := loadDir(t, dir, "compose.yaml")
	args1 := p.RunArgs("web", p.Services["web"], 1, "", nil)
	h1 := ConfigHashFromArgs(args1)
	if h1 == "" {
		t.Fatalf("no config-hash label stamped: %v", args1)
	}
	// Deterministic for the same config.
	if h2 := ConfigHashFromArgs(p.RunArgs("web", p.Services["web"], 1, "", nil)); h2 != h1 {
		t.Errorf("hash not stable: %q vs %q", h1, h2)
	}
	// Changes when the config changes.
	p.Services["web"].Image = "nginx:2"
	if h3 := ConfigHashFromArgs(p.RunArgs("web", p.Services["web"], 1, "", nil)); h3 == h1 {
		t.Error("hash must change when the service config changes")
	}
}

// --- item 6: env_file parsed by dcon, merged under environment ---

func TestEnvFileParsedNotForwarded(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"compose.yaml": `
services:
  web:
    image: nginx
    env_file: vars.env
    environment:
      FROM_ENV: wins
`,
		"vars.env": "# comment\nexport EXPORTED=yes\nQUOTED='sq'\nDQUOTED=\"dq\"\nFROM_ENV=loses\n\nPLAIN=v\n",
	})
	p := loadDir(t, dir, "compose.yaml")
	args := p.RunArgs("web", p.Services["web"], 1, "", nil)
	for _, a := range args {
		if a == "--env-file" {
			t.Fatalf("--env-file must not reach the backend: %v", args)
		}
	}
	envs := map[string]bool{}
	for i, a := range args {
		if a == "--env" && i+1 < len(args) {
			envs[args[i+1]] = true
		}
	}
	for _, want := range []string{"EXPORTED=yes", "QUOTED=sq", "DQUOTED=dq", "PLAIN=v", "FROM_ENV=wins"} {
		if !envs[want] {
			t.Errorf("missing --env %s (got %v)", want, envs)
		}
	}
	if envs["FROM_ENV=loses"] {
		t.Error("environment: must win over env_file")
	}
}

func TestParseDotenv(t *testing.T) {
	got := parseDotenv("A=1\nexport B=two\nC='three'\nD=\"four\"\n# skip\n\nnoequals\n =bad\nE=tr\"im\n")
	want := map[string]string{"A": "1", "B": "two", "C": "three", "D": "four", "E": "tr\"im"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseDotenv = %v; want %v", got, want)
	}
}

// --- item 7: extends parsed (and no longer silently dropped) ---

func TestExtendsParsed(t *testing.T) {
	dir := writeFiles(t, map[string]string{"compose.yaml": `
services:
  base:
    image: nginx
  web:
    image: nginx
    extends:
      file: other.yaml
      service: base
`})
	p := loadDir(t, dir, "compose.yaml")
	e := p.Services["web"].Extends
	if !e.IsSet() || e.Service != "base" || e.File != "other.yaml" {
		t.Errorf("extends not parsed: %+v", e)
	}
	if p.Services["base"].Extends.IsSet() {
		t.Error("extends set on a service without it")
	}
}

// --- item 8: depends_on long-form conditions still yield the dependency ---

func TestDependsOnConditionParsed(t *testing.T) {
	dir := writeFiles(t, map[string]string{"compose.yaml": `
services:
  db:
    image: postgres
  web:
    image: nginx
    depends_on:
      db:
        condition: service_healthy
`})
	p := loadDir(t, dir, "compose.yaml")
	if got := []string(p.Services["web"].DependsOn); !reflect.DeepEqual(got, []string{"db"}) {
		t.Errorf("depends_on = %v; want [db]", got)
	}
}

// --- item 12: ~/ and bare .. bind sources ---

func TestResolveVolumeHomeAndDotDot(t *testing.T) {
	dir := writeFiles(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: nginx\n"})
	p := loadDir(t, dir, "compose.yaml")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got := p.resolveVolume("~/data:/data"); got != filepath.Join(home, "data")+":/data" {
		t.Errorf("~/ not expanded: %q", got)
	}
	if got := p.resolveVolume("~:/host-home"); got != home+":/host-home" {
		t.Errorf("bare ~ not expanded: %q", got)
	}
	if got := p.resolveVolume("..:/parent"); got != filepath.Dir(p.Dir)+":/parent" {
		t.Errorf("bare .. not resolved: %q", got)
	}
	// Named volumes must still not be treated as paths.
	if got := p.resolveVolume("data:/data"); got != "data:/data" {
		t.Errorf("undeclared named volume must pass through: %q", got)
	}
}

// --- item 13: nested interpolation ---

func TestNestedInterpolation(t *testing.T) {
	lookup := func(k string) (string, bool) {
		env := map[string]string{"TAG": "1.27", "SET": "x"}
		v, ok := env[k]
		return v, ok
	}
	cases := map[string]string{
		"${IMG:-nginx:${TAG}}":   "nginx:1.27",
		"${SET:-nginx:${TAG}}":   "x",
		"${IMG:-${MISS:-deep}}":  "deep",
		"plain $TAG and ${TAG}":  "plain 1.27 and 1.27",
		"$$TAG":                  "$TAG",
		"${SET:+alt-${TAG}}":     "alt-1.27",
		"literal } after ${TAG}": "literal } after 1.27",
	}
	for in, want := range cases {
		got, err := expandString(in, lookup)
		if err != nil {
			t.Errorf("expandString(%q) err: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("expandString(%q) = %q; want %q", in, got, want)
		}
	}
	if _, err := expandString("${MISS:?need it}", lookup); err == nil {
		t.Error("required-but-missing must still error")
	}
}

// --- item 15: undefined depends_on warns but never panics ---

func TestUndefinedDependsOnWarns(t *testing.T) {
	resetWarnings()
	dir := writeFiles(t, map[string]string{"compose.yaml": `
services:
  web:
    image: nginx
    depends_on:
      - ghost
`})
	p := loadDir(t, dir, "compose.yaml")
	// Capture stderr around Order().
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	order := p.Order()
	w.Close()
	os.Stderr = old
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	msg := string(buf[:n])
	if !reflect.DeepEqual(order, []string{"web"}) {
		t.Errorf("order = %v; want [web]", order)
	}
	if !strings.Contains(msg, "dcon: warning:") || !strings.Contains(msg, "ghost") {
		t.Errorf("missing-dependency warning not emitted; stderr = %q", msg)
	}
}
