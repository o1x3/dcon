package dockerfmt

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"dcon/internal/ui"
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

// TestRenderTableHeaderFunctionPrefixed reproduces the regression where a
// table format whose action starts with a function/pipeline (e.g.
// `{{upper .Name}}`) leaked the raw `{{upper .Name}}` text into the header row
// because the header regex only matched actions beginning with `.Field`. The
// header must derive from the field reference regardless of leading function.
func TestRenderTableHeaderFunctionPrefixed(t *testing.T) {
	views, def := sampleDef()
	out := captureStdout(t, func() { Render("table {{.ID}}\t{{upper .Name}}", false, views, def) })
	header := strings.SplitN(out, "\n", 2)[0]
	if strings.Contains(header, "{{") || strings.Contains(header, "}}") {
		t.Errorf("header leaked raw template text: %q", header)
	}
	if !strings.Contains(header, "NAME") || !strings.Contains(header, "ID") {
		t.Errorf("header should derive NAME and ID columns: %q", header)
	}
}

// TestRenderTableHeaderDottedLiteral reproduces the regression where a dotted
// token inside a string literal in a pipeline action (e.g. "%s.txt") was
// mistaken for the field reference, deriving the wrong header (TXT) instead of
// the actual field's column (NAME).
func TestRenderTableHeaderDottedLiteral(t *testing.T) {
	views, def := sampleDef()
	out := captureStdout(t, func() { Render(`table {{.Name | printf "%s.txt"}}`, false, views, def) })
	header := strings.SplitN(out, "\n", 2)[0]
	if strings.Contains(header, "TXT") {
		t.Errorf("dotted literal inside a string must not become the header: %q", header)
	}
	if !strings.Contains(header, "NAME") {
		t.Errorf("header should derive from the real field .Name: %q", header)
	}
}

// TestRenderTableFormatUnescapesTabs reproduces the bug where a shell-supplied
// `--format 'table {{.Name}}\t{{.ID}}'` (literal backslash-t) emitted a literal
// "\t" and never aligned columns. The escape must become a real tab (matching
// docker), so no literal backslash-t survives in header or rows.
func TestRenderTableFormatUnescapesTabs(t *testing.T) {
	views, def := sampleDef()
	// The Go literal `\t` here is a real backslash + t — what a single-quoted
	// shell argument delivers.
	out := captureStdout(t, func() { Render(`table {{.Name}}\t{{.ID}}`, false, views, def) })
	if strings.Contains(out, `\t`) {
		t.Errorf("literal backslash-t must be unescaped to a tab; got %q", out)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "ID") {
		t.Errorf("header columns missing: %q", out)
	}
	// The plain (non-table) template branch unescapes too.
	out2 := captureStdout(t, func() { Render(`{{.Name}}\t{{.ID}}`, false, views, def) })
	if strings.Contains(out2, `\t`) {
		t.Errorf("plain template must unescape backslash-t; got %q", out2)
	}
}

// TestRenderPlainPathByteIdentical locks the drop-in contract: with styling
// forced OFF, the default table is byte-for-byte the tabwriter output and
// carries no ANSI/box-drawing characters — exactly what pipes and CI parse.
// Column widths follow docker's tabwriter settings (minwidth 10, padding 3),
// so a short column is still 10 columns wide, matching real docker output.
func TestRenderPlainPathByteIdentical(t *testing.T) {
	defer ui.SetEnabled(false)()
	views, def := sampleDef()
	out := captureStdout(t, func() { Render("", false, views, def) })

	const want = "ID        NAME\nid1       alpha\nid2       beta\n"
	if out != want {
		t.Errorf("plain table mismatch:\n got %q\nwant %q", out, want)
	}
	if strings.ContainsAny(out, "\x1b─│╭╮╰╯") {
		t.Errorf("plain table leaked ANSI/border chars: %q", out)
	}
}

// TestRenderMachineReadablePathsUnaffectedByStyling proves the -q / json /
// template branches are identical whether styling is on or off — styling must
// only ever touch the default (empty-format, non-quiet) human view.
func TestRenderMachineReadablePathsUnaffectedByStyling(t *testing.T) {
	views, def := sampleDef()
	probe := func(format string, quiet bool) string {
		return captureStdout(t, func() { Render(format, quiet, views, def) })
	}
	for _, c := range []struct {
		format string
		quiet  bool
	}{
		{"", true},                   // -q
		{"json", false},              // json
		{"{{.Name}}={{.ID}}", false}, // template
	} {
		restoreOn := ui.SetEnabled(true)
		styled := probe(c.format, c.quiet)
		restoreOn()
		restoreOff := ui.SetEnabled(false)
		plain := probe(c.format, c.quiet)
		restoreOff()
		if styled != plain {
			t.Errorf("format=%q quiet=%v differs with styling on/off:\n on  %q\n off %q", c.format, c.quiet, styled, plain)
		}
		if strings.Contains(styled, "\x1b") {
			t.Errorf("format=%q quiet=%v leaked ANSI: %q", c.format, c.quiet, styled)
		}
	}
}

// TestRenderStyledPath drives the styled branch in Render itself (not just
// ui.Table in isolation): with styling forced on and non-empty views, the
// default view becomes a bordered table that still carries every value.
func TestRenderStyledPath(t *testing.T) {
	defer ui.SetEnabled(true)()
	views, def := sampleDef()
	out := captureStdout(t, func() { Render("", false, views, def) })
	if !strings.ContainsAny(out, "─│╭╮╰╯") {
		t.Errorf("styled Render should draw a border, got %q", out)
	}
	for _, want := range []string{"ID", "NAME", "alpha", "beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("styled Render missing %q in %q", want, out)
		}
	}
}

// TestRenderEmptyListNeverBordered guards the empty-list fall-through: even on
// a (forced) TTY, zero rows produce the plain header-only output, not a lone
// bordered box.
func TestRenderEmptyListNeverBordered(t *testing.T) {
	defer ui.SetEnabled(true)()
	_, def := sampleDef()
	out := captureStdout(t, func() { Render("", false, nil, def) })
	if strings.ContainsAny(out, "─│╭╮╰╯") {
		t.Errorf("empty list should not render a bordered box: %q", out)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "NAME") {
		t.Errorf("empty list should still print headers: %q", out)
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
