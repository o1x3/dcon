// Package ui is dcon's optional terminal styling layer: a small hand-rolled
// ANSI implementation (256-colour escapes + box-drawing tables). It exists to
// make interactive output (tables, the doctor report, compose progress,
// version/info) nicer to read WITHOUT ever changing the bytes a script or
// pipeline sees.
//
// The cardinal rule: styling is applied only when Enabled() is true, which
// requires stdout to be an interactive terminal (and neither DCON_PLAIN nor
// NO_COLOR set). When dcon's stdout is redirected to a file or pipe — the case
// every Docker-drop-in script and CI job hits — Enabled() is false and callers
// fall back to the exact plain output dcon has always produced. Color codes and
// box-drawing characters therefore never leak into machine-read output.
package ui

import (
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// override forces Enabled() on (+1) or off (-1) regardless of the environment.
// 0 means "decide from the environment". It exists so tests (and the rest of
// dcon's own tests, which run without a TTY) can exercise both the styled and
// plain code paths deterministically. Not safe for concurrent use, which is
// fine: dcon is a short-lived single-threaded CLI and tests set it serially.
var override int

// SetEnabled forces styling on or off and returns a function that restores the
// previous state. Intended for tests and for callers that have already resolved
// a --no-color/--plain flag; everyday code should just let Enabled() consult the
// environment.
func SetEnabled(on bool) (restore func()) {
	prev := override
	if on {
		override = 1
	} else {
		override = -1
	}
	return func() { override = prev }
}

// isTTY is indirected so tests don't depend on the real stdout being a terminal.
var isTTY = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// Enabled reports whether output should be styled. False whenever the result
// would be parsed by something other than a human: a non-terminal stdout, an
// explicit DCON_PLAIN, or the NO_COLOR convention.
func Enabled() bool {
	switch override {
	case 1:
		return true
	case -1:
		return false
	}
	if v := os.Getenv("DCON_PLAIN"); v != "" && v != "0" && v != "false" {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTTY()
}

// Palette — 256-colour indexes chosen to stay legible on both light and dark
// terminals. Kept few and named by role so the whole CLI shares one vocabulary.
// (Same indexes the previous lipgloss implementation used.)
const (
	colAccent = "39"  // cyan-blue: names, headers, the "current" marker
	colOK     = "42"  // green: running / success / ✓
	colWarn   = "214" // amber: warnings / !
	colErr    = "203" // red: stopped / failure / ✗
	colDim    = "244" // grey: secondary text, borders
)

const reset = "\x1b[0m"

// fg wraps s in a 256-colour foreground escape when styling is enabled;
// otherwise returns s untouched so plain output is byte-for-byte what it
// always was.
func fg(col, s string) string {
	if !Enabled() {
		return s
	}
	return "\x1b[38;5;" + col + "m" + s + reset
}

// Inline colour/emphasis helpers. Each is a no-op when styling is disabled.
func Accent(s string) string  { return fg(colAccent, s) }
func Success(s string) string { return fg(colOK, s) }
func Warning(s string) string { return fg(colWarn, s) }
func Error(s string) string   { return fg(colErr, s) }
func Dim(s string) string     { return fg(colDim, s) }

func Bold(s string) string {
	if !Enabled() {
		return s
	}
	return "\x1b[1m" + s + reset
}

func Title(s string) string {
	if !Enabled() {
		return s
	}
	return "\x1b[1;38;5;" + colAccent + "m" + s + reset
}

// Symbol returns a status glyph (✓ / ! / ✗) coloured by kind, falling back to
// the bare glyph when styling is off. kind is "ok", "warn", or anything else
// (treated as error).
func Symbol(kind string) string {
	switch kind {
	case "ok":
		return Success("✓")
	case "warn":
		return Warning("!")
	default:
		return Error("✗")
	}
}

// Table renders headers + rows as a rounded, lightly coloured table. Callers
// MUST only reach this when Enabled() is true and the user asked for the
// default (non --format/-q) view; the plain tabwriter path remains the
// fallback for every machine-readable case.
//
// Cell widths are measured with go-runewidth so CJK/emoji container names keep
// the borders aligned. Ragged rows are tolerated: missing cells render empty,
// extra cells are dropped.
func Table(headers []string, rows [][]string) string {
	// Column widths in terminal cells (not runes/bytes).
	w := make([]int, len(headers))
	for i, h := range headers {
		w[i] = runewidth.StringWidth(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(w) {
				if cw := runewidth.StringWidth(c); cw > w[i] {
					w[i] = cw
				}
			}
		}
	}

	var b strings.Builder
	rule := func(left, mid, right string) {
		var l strings.Builder
		l.WriteString(left)
		for i := range w {
			l.WriteString(strings.Repeat("─", w[i]+2))
			if i < len(w)-1 {
				l.WriteString(mid)
			}
		}
		l.WriteString(right)
		b.WriteString(Dim(l.String()))
		b.WriteString("\n")
	}
	// cell pads s to column i's width plus one space either side.
	cell := func(s string, i int) string {
		pad := w[i] - runewidth.StringWidth(s)
		if pad < 0 {
			pad = 0
		}
		return " " + s + strings.Repeat(" ", pad) + " "
	}
	sep := Dim("│")

	rule("╭", "┬", "╮")
	b.WriteString(sep)
	for i, h := range headers {
		b.WriteString(Title(cell(h, i)))
		b.WriteString(sep)
	}
	b.WriteString("\n")
	rule("├", "┼", "┤")
	for _, r := range rows {
		b.WriteString(sep)
		for i := range headers {
			c := ""
			if i < len(r) {
				c = r[i]
			}
			s := cell(c, i)
			// Light semantic colouring: tint a STATUS/STATE column by value and
			// accent the first (id/name) column. Everything else is plain so the
			// table stays readable rather than gaudy.
			switch strings.ToUpper(strings.TrimSpace(headers[i])) {
			case "STATUS", "STATE":
				s = fg(stateColour(c), s)
			default:
				if i == 0 {
					s = Accent(s)
				}
			}
			b.WriteString(s)
			b.WriteString(sep)
		}
		b.WriteString("\n")
	}
	rule("╰", "┴", "╯")
	return strings.TrimSuffix(b.String(), "\n")
}

// stateColour maps a Docker-style status/state string to a palette colour:
// green for up/running, red for exited/stopped/dead, amber otherwise.
func stateColour(s string) string {
	l := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(l, "up"), strings.HasPrefix(l, "running"):
		return colOK
	case strings.HasPrefix(l, "exit"), strings.HasPrefix(l, "stop"), strings.HasPrefix(l, "dead"):
		return colErr
	default:
		// "created", "restarting", etc. are transient, not failures — amber.
		return colWarn
	}
}
