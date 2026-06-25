// Package ui is dcon's optional terminal styling layer, built on Charm's
// lipgloss. It exists to make interactive output (tables, the doctor report,
// compose progress, version/info) nicer to read WITHOUT ever changing the
// bytes a script or pipeline sees.
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

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
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

// Palette — 256-colour values chosen to stay legible on both light and dark
// terminals. Kept few and named by role so the whole CLI shares one vocabulary.
var (
	colAccent = lipgloss.Color("39")  // cyan-blue: names, headers, the "current" marker
	colOK     = lipgloss.Color("42")  // green: running / success / ✓
	colWarn   = lipgloss.Color("214") // amber: warnings / !
	colErr    = lipgloss.Color("203") // red: stopped / failure / ✗
	colDim    = lipgloss.Color("244") // grey: secondary text, borders
)

var (
	accentStyle  = lipgloss.NewStyle().Foreground(colAccent)
	okStyle      = lipgloss.NewStyle().Foreground(colOK)
	warnStyle    = lipgloss.NewStyle().Foreground(colWarn)
	errStyle     = lipgloss.NewStyle().Foreground(colErr)
	dimStyle     = lipgloss.NewStyle().Foreground(colDim)
	boldStyle    = lipgloss.NewStyle().Bold(true)
	titleStyle   = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	headerStyle  = lipgloss.NewStyle().Foreground(colAccent).Bold(true).Padding(0, 1)
	cellStyle    = lipgloss.NewStyle().Padding(0, 1)
	borderColour = lipgloss.NewStyle().Foreground(colDim)
)

// apply renders s with st only when styling is enabled; otherwise returns s
// untouched so plain output is byte-for-byte what it always was.
func apply(st lipgloss.Style, s string) string {
	if !Enabled() {
		return s
	}
	return st.Render(s)
}

// Inline colour/emphasis helpers. Each is a no-op when styling is disabled.
func Accent(s string) string  { return apply(accentStyle, s) }
func Success(s string) string { return apply(okStyle, s) }
func Warning(s string) string { return apply(warnStyle, s) }
func Error(s string) string   { return apply(errStyle, s) }
func Dim(s string) string     { return apply(dimStyle, s) }
func Bold(s string) string    { return apply(boldStyle, s) }
func Title(s string) string   { return apply(titleStyle, s) }

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
func Table(headers []string, rows [][]string) string {
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(borderColour).
		BorderRow(false).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			// Light semantic colouring: tint a STATUS/STATE column by value and
			// accent the first (id/name) column. Everything else is plain so the
			// table stays readable rather than gaudy. Bounds are checked against
			// both headers and the (possibly ragged) row before indexing.
			if row >= 0 && row < len(rows) && col >= 0 && col < len(headers) && col < len(rows[row]) {
				switch strings.ToUpper(strings.TrimSpace(headers[col])) {
				case "STATUS", "STATE":
					return cellStyle.Foreground(stateColour(rows[row][col]))
				}
				if col == 0 {
					return cellStyle.Foreground(colAccent)
				}
			}
			return cellStyle
		})
	return t.String()
}

// stateColour maps a Docker-style status/state string to a palette colour:
// green for up/running, red for exited/stopped/dead, amber otherwise.
func stateColour(s string) lipgloss.Color {
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
