package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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

func TestAccentEmitsANSIWhenEnabled(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	defer SetEnabled(true)()
	if got := Accent("hi"); !strings.Contains(got, "\x1b[") {
		t.Errorf("Accent when enabled with a colour profile should emit ANSI, got %q", got)
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
	if !strings.ContainsAny(out, "─│╭╮╰╯") {
		t.Errorf("styled table should draw a border, got %q", out)
	}
	for _, want := range []string{"NAME", "STATE", "alpine", "running"} {
		if !strings.Contains(out, want) {
			t.Errorf("styled table missing %q in %q", want, out)
		}
	}
}

func TestStateColour(t *testing.T) {
	cases := map[string]lipgloss.Color{
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
