package ui

import (
	"strings"
	"testing"
)

func TestEnabledOverride(t *testing.T) {
	t.Run("forced on", func(t *testing.T) {
		defer SetEnabled(true)()
		if !Enabled() {
			t.Fatal("SetEnabled(true) should force Enabled() true")
		}
	})
	t.Run("forced off", func(t *testing.T) {
		defer SetEnabled(false)()
		if Enabled() {
			t.Fatal("SetEnabled(false) should force Enabled() false")
		}
	})
	t.Run("restore", func(t *testing.T) {
		restore := SetEnabled(true)
		inner := SetEnabled(false)
		inner()
		if !Enabled() {
			t.Error("restore should reinstate the previous override")
		}
		restore()
	})
}

func TestEnabledEnv(t *testing.T) {
	defer func(prev func() bool) { isTTY = prev }(isTTY)
	isTTY = func() bool { return true }

	t.Setenv("DCON_PLAIN", "")
	t.Setenv("NO_COLOR", "")
	if !Enabled() {
		t.Error("a TTY with no opt-out env should be enabled")
	}
	t.Setenv("NO_COLOR", "1")
	if Enabled() {
		t.Error("NO_COLOR should disable styling")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("DCON_PLAIN", "1")
	if Enabled() {
		t.Error("DCON_PLAIN=1 should disable styling")
	}
	t.Setenv("DCON_PLAIN", "0")
	if !Enabled() {
		t.Error("DCON_PLAIN=0 should be ignored (still enabled on a TTY)")
	}
	t.Setenv("DCON_PLAIN", "")
	isTTY = func() bool { return false }
	if Enabled() {
		t.Error("a non-TTY stdout should never be enabled")
	}
}

// TestHelpersIdentityWhenDisabled is the load-bearing contract test: with
// styling off, every inline helper must return its input untouched and emit no
// ANSI, so piped/CI output is byte-for-byte unchanged.
func TestHelpersIdentityWhenDisabled(t *testing.T) {
	defer SetEnabled(false)()
	for name, f := range map[string]func(string) string{
		"Accent": Accent, "Success": Success, "Warning": Warning,
		"Error": Error, "Dim": Dim, "Bold": Bold, "Title": Title,
	} {
		if got := f("xyz"); got != "xyz" {
			t.Errorf("%s(%q) when disabled = %q, want unchanged", name, "xyz", got)
		}
	}
	syms := Symbol("ok") + Symbol("warn") + Symbol("err")
	if strings.Contains(syms, "\x1b") {
		t.Errorf("symbols should be plain glyphs when disabled, got %q", syms)
	}
	if Symbol("ok") != "✓" || Symbol("warn") != "!" || Symbol("err") != "✗" || Symbol("x") != "✗" {
		t.Errorf("symbol glyphs wrong: %q %q %q %q", Symbol("ok"), Symbol("warn"), Symbol("err"), Symbol("x"))
	}
}

// TestHelpersEmitANSIWhenEnabled locks the escape sequences themselves: each
// helper wraps its input in the expected 256-colour (or bold) SGR and a reset,
// with the input preserved verbatim in between.
func TestHelpersEmitANSIWhenEnabled(t *testing.T) {
	defer SetEnabled(true)()
	cases := map[string]struct {
		f    func(string) string
		want string
	}{
		"Accent":  {Accent, "\x1b[38;5;39mhi\x1b[0m"},
		"Success": {Success, "\x1b[38;5;42mhi\x1b[0m"},
		"Warning": {Warning, "\x1b[38;5;214mhi\x1b[0m"},
		"Error":   {Error, "\x1b[38;5;203mhi\x1b[0m"},
		"Dim":     {Dim, "\x1b[38;5;244mhi\x1b[0m"},
		"Bold":    {Bold, "\x1b[1mhi\x1b[0m"},
		"Title":   {Title, "\x1b[1;38;5;39mhi\x1b[0m"},
	}
	for name, c := range cases {
		if got := c.f("hi"); got != c.want {
			t.Errorf("%s(\"hi\") = %q, want %q", name, got, c.want)
		}
	}
	if !strings.Contains(Symbol("ok"), "\x1b[38;5;42m") {
		t.Errorf("Symbol(\"ok\") should be green when enabled, got %q", Symbol("ok"))
	}
}

func TestTableNoPanic(t *testing.T) {
	cases := [][][]string{
		nil,
		{{"a", "b"}},
		{{"a", "b"}, {"c", "d"}},
		{{"a"}},                // ragged: fewer cells than headers
		{{"a", "b", "c", "d"}}, // ragged: more cells than headers
	}
	for _, enabled := range []bool{true, false} {
		restore := SetEnabled(enabled)
		for i, rows := range cases {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("Table panicked (enabled=%v, case %d): %v", enabled, i, r)
					}
				}()
				_ = Table([]string{"X", "Y"}, rows)
			}()
		}
		restore()
	}
}

func TestTableStyledHasBorderAndCells(t *testing.T) {
	defer SetEnabled(true)()
	out := Table([]string{"NAME", "STATE"}, [][]string{{"alpine", "running"}})
	for _, want := range []string{"╭", "╮", "╰", "╯", "┬", "┴", "├", "┤", "┼", "─", "│"} {
		if !strings.Contains(out, want) {
			t.Errorf("styled table missing border glyph %q in %q", want, out)
		}
	}
	for _, want := range []string{"NAME", "STATE", "alpine", "running"} {
		if !strings.Contains(out, want) {
			t.Errorf("styled table missing %q in %q", want, out)
		}
	}
	if strings.HasSuffix(out, "\n") {
		t.Errorf("Table should not end with a trailing newline (callers Println it)")
	}
	// STATE column is tinted by value: running → green.
	if !strings.Contains(out, "\x1b[38;5;42m") {
		t.Errorf("running state cell should be green, got %q", out)
	}
}

// stripANSI removes SGR escape sequences so alignment can be checked on the
// visible characters alone.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// TestTableWideRuneAlignment: CJK and emoji names occupy two terminal cells
// per rune; every row (including the border rules) must still end at the same
// column or the box falls apart.
func TestTableWideRuneAlignment(t *testing.T) {
	defer SetEnabled(true)()
	out := Table(
		[]string{"NAME", "STATUS"},
		[][]string{
			{"日本語コンテナ", "Up 2 hours"},
			{"🚀-rocket", "Exited (0)"},
			{"plain", "created"},
		},
	)
	lines := strings.Split(stripANSI(out), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected a multi-line table, got %q", out)
	}
	// Width in terminal cells: wide runes (CJK, emoji) count 2, ASCII 1.
	width := func(s string) int {
		w := 0
		for _, r := range s {
			switch {
			case r >= 0x1100 && (r <= 0x115F || // Hangul Jamo
				r == 0x2329 || r == 0x232A ||
				(r >= 0x2E80 && r <= 0xA4CF) || // CJK
				(r >= 0xAC00 && r <= 0xD7A3) ||
				(r >= 0xF900 && r <= 0xFAFF) ||
				(r >= 0xFE30 && r <= 0xFE6F) ||
				(r >= 0xFF00 && r <= 0xFF60) ||
				(r >= 0xFFE0 && r <= 0xFFE6) ||
				(r >= 0x1F300 && r <= 0x1FAFF)): // emoji
				w += 2
			default:
				w++
			}
		}
		return w
	}
	first := width(lines[0])
	for i, l := range lines {
		if width(l) != first {
			t.Errorf("line %d width %d != %d; misaligned table:\n%s", i, width(l), first, stripANSI(out))
		}
	}
}

// TestTablePlainWhenDisabled: the box is still drawn (callers gate on
// Enabled() themselves) but no ANSI escapes may appear.
func TestTablePlainWhenDisabled(t *testing.T) {
	defer SetEnabled(false)()
	out := Table([]string{"NAME"}, [][]string{{"alpine"}})
	if strings.Contains(out, "\x1b") {
		t.Errorf("Table when disabled must not emit ANSI, got %q", out)
	}
	if !strings.Contains(out, "alpine") || !strings.Contains(out, "─") {
		t.Errorf("Table when disabled should still render content and borders, got %q", out)
	}
}

func TestStateColour(t *testing.T) {
	cases := map[string]string{
		"Up 2 hours": colOK,
		"running":    colOK,
		"Exited (0)": colErr,
		"stopped":    colErr,
		"dead":       colErr,
		"created":    colWarn, // transient state, not a failure
		"restarting": colWarn,
		"":           colWarn,
	}
	for in, want := range cases {
		if got := stateColour(in); got != want {
			t.Errorf("stateColour(%q) = %v, want %v", in, got, want)
		}
	}
}
