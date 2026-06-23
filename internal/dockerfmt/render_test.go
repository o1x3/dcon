package dockerfmt

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type row struct {
	ID   string
	Name string
}

func sampleDef() ([]any, TableDef) {
	views := []any{row{"id1", "alpha"}, row{"id2", "beta"}}
	def := TableDef{
		Headers: []string{"ID", "NAME"},
		Row:     func(v any) []string { return []string{v.(row).ID, v.(row).Name} },
		ID:      func(v any) string { return v.(row).ID },
	}
	return views, def
}

// captureStdout runs f and returns whatever it wrote to os.Stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	f()
	w.Close()
	os.Stdout = orig
	return <-done
}

func TestRenderDefaultTable(t *testing.T) {
	views, def := sampleDef()
	out := captureStdout(t, func() { Render("", false, views, def) })
	if !strings.Contains(out, "ID") || !strings.Contains(out, "NAME") {
		t.Errorf("missing headers: %q", out)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("missing rows: %q", out)
	}
}

func TestRenderQuiet(t *testing.T) {
	views, def := sampleDef()
	out := captureStdout(t, func() { Render("", true, views, def) })
	if strings.TrimSpace(out) != "id1\nid2" {
		t.Errorf("quiet should print ids only; got %q", out)
	}
}

func TestRenderJSON(t *testing.T) {
	views, def := sampleDef()
	out := captureStdout(t, func() { Render("json", false, views, def) })
	if !strings.Contains(out, `"ID":"id1"`) || !strings.Contains(out, `"Name":"beta"`) {
		t.Errorf("json render wrong: %q", out)
	}
	// one object per line
	if lines := strings.Count(strings.TrimSpace(out), "\n"); lines != 1 {
		t.Errorf("expected 2 json lines; got %d (%q)", lines+1, out)
	}
}

func TestRenderTemplate(t *testing.T) {
	views, def := sampleDef()
	out := captureStdout(t, func() { Render("{{.Name}}={{.ID}}", false, views, def) })
	if !strings.Contains(out, "alpha=id1") || !strings.Contains(out, "beta=id2") {
		t.Errorf("template render wrong: %q", out)
	}
}

func TestRenderTableTemplateHeaders(t *testing.T) {
	views, def := sampleDef()
	out := captureStdout(t, func() { Render("table {{.Name}}\t{{.ID}}", false, views, def) })
	if !strings.Contains(out, "NAME") {
		t.Errorf("table template should derive headers: %q", out)
	}
}

func TestParseTimeAndRelative(t *testing.T) {
	now := time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
	if _, ok := ParseTime(now); !ok {
		t.Fatalf("ParseTime failed for %q", now)
	}
	rel := RelativeAgo(now)
	if !strings.HasSuffix(rel, "ago") {
		t.Errorf("RelativeAgo = %q", rel)
	}
	if RelativeAgo("") != "N/A" {
		t.Errorf("empty time should be N/A")
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		500 * time.Millisecond: "Less than a second",
		1 * time.Second:        "1 second",
		45 * time.Second:       "45 seconds",
		90 * time.Second:       "About a minute",
		10 * time.Minute:       "10 minutes",
		60 * time.Minute:       "About an hour",
		90 * time.Minute:       "2 hours",
		5 * time.Hour:          "5 hours",
	}
	for d, want := range cases {
		if got := HumanDuration(d); got != want {
			t.Errorf("HumanDuration(%v) = %q; want %q", d, got, want)
		}
	}
}
