package dockerfmt

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStderr runs f and returns whatever it wrote to os.Stderr.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	f()
	w.Close()
	os.Stderr = orig
	return <-done
}

// TestEllipsis locks docker's formatter.Ellipsis semantics: display-column
// counting (CJK runes are 2 columns), a reserved column for the ellipsis, and
// pass-through when the string already fits.
func TestEllipsis(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 20, "short"},                               // fits: unchanged
		{"exactly-twenty-chars", 20, "exactly-twenty-chars"}, // width == max: unchanged
		{"this-is-a-very-long-command-indeed", 20, "this-is-a-very-long…"},
		{"日本語日本語日本語日本語", 20, "日本語日本語日本語…"}, // 24 cols -> 9 runes (18) + …
		{"abcdef", 1, "a"}, // maxDisplayWidth 1: first rune, no ellipsis
		{"abcdef", 0, ""},
		{"", 1, ""},
		{"", 20, ""},
	}
	for _, c := range cases {
		if got := Ellipsis(c.in, c.max); got != c.want {
			t.Errorf("Ellipsis(%q, %d) = %q; want %q", c.in, c.max, got, c.want)
		}
	}
}

// TestRelativeAgoZeroTime reproduces the bug where a zero timestamp
// (0001-01-01T00:00:00Z) rendered as "2025 years ago" instead of N/A.
func TestRelativeAgoZeroTime(t *testing.T) {
	if got := RelativeAgo("0001-01-01T00:00:00Z"); got != "N/A" {
		t.Errorf("zero time should render N/A, got %q", got)
	}
}

// TestPadTemplateFunc covers docker's `pad` template function: N spaces before
// and after a non-empty value; an empty value passes through with no padding.
func TestPadTemplateFunc(t *testing.T) {
	type padRow struct{ ID, Name string }
	views := []any{padRow{"id1", "alpha"}, padRow{"id2", ""}}
	def := TableDef{
		Headers: []string{"ID", "NAME"},
		Row:     func(v any) []string { return []string{v.(padRow).ID, v.(padRow).Name} },
		ID:      func(v any) string { return v.(padRow).ID },
	}
	out := captureStdout(t, func() { Render("[{{pad .Name 2 1}}]", false, views, def) })
	if !strings.Contains(out, "[  alpha ]") {
		t.Errorf("pad should surround with 2+1 spaces: %q", out)
	}
	if !strings.Contains(out, "[]") {
		t.Errorf("pad must pass empty string through unchanged: %q", out)
	}
}

// TestRenderQuietFormatWarning locks docker >= 25 behavior: when both --quiet
// and --format are set, a WARNING goes to stderr and quiet wins on stdout.
func TestRenderQuietFormatWarning(t *testing.T) {
	views, def := sampleDef()
	var out string
	errOut := captureStderr(t, func() {
		out = captureStdout(t, func() { Render("{{.Name}}", true, views, def) })
	})
	if !strings.Contains(errOut, "WARNING: Ignoring custom format, because both --format and --quiet are set.") {
		t.Errorf("missing quiet+format warning on stderr: %q", errOut)
	}
	if strings.TrimSpace(out) != "id1\nid2" {
		t.Errorf("quiet must win over --format: %q", out)
	}
	// quiet alone stays silent
	errOut = captureStderr(t, func() { captureStdout(t, func() { Render("", true, views, def) }) })
	if errOut != "" {
		t.Errorf("quiet without format must not warn: %q", errOut)
	}
}
