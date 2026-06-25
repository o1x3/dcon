package dockerfmt

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"text/template"

	"dcon/internal/ui"
)

// TableDef describes how to render a list of view objects as the default
// Docker table and how to pull the quiet (-q) ID field from a row.
type TableDef struct {
	Headers []string
	Row     func(v any) []string
	ID      func(v any) string
}

// NewTabWriter returns a tabwriter configured like Docker's (3-space padding).
func NewTabWriter() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 1, 3, ' ', 0)
}

// fieldHeader maps template field tokens to the column headers Docker prints
// for `--format "table ..."`.
var fieldHeader = map[string]string{
	".ID":           "CONTAINER ID",
	".Image":        "IMAGE",
	".Command":      "COMMAND",
	".CreatedAt":    "CREATED AT",
	".RunningFor":   "CREATED",
	".CreatedSince": "CREATED",
	".Ports":        "PORTS",
	".Status":       "STATUS",
	".Size":         "SIZE",
	".Names":        "NAMES",
	".Name":         "NAME",
	".LocalVolumes": "LOCAL VOLUMES",
	".Labels":       "LABELS",
	".Mounts":       "MOUNTS",
	".Networks":     "NETWORKS",
	".Repository":   "REPOSITORY",
	".Tag":          "TAG",
	".Digest":       "DIGEST",
	".Driver":       "DRIVER",
	".Scope":        "SCOPE",
	".VolumeName":   "VOLUME NAME",
	".Container":    "CONTAINER",
	".CPUPerc":      "CPU %",
	".MemUsage":     "MEM USAGE / LIMIT",
	".MemPerc":      "MEM %",
	".NetIO":        "NET I/O",
	".BlockIO":      "BLOCK I/O",
	".PIDs":         "PIDS",
}

// actionRe matches a whole `{{ … }}` template action; fieldRefRe finds a
// `.Field` reference within one. Together they let the header derivation replace
// each action with its column header, leaving surrounding literal text (dots,
// version strings) untouched — and crucially handling actions where the field is
// NOT the first token, e.g. `{{upper .Names}}` or `{{printf "%s" .ID}}`, which an
// anchored `{{\s*\.Field` regex missed (leaking the raw template into the header).
var (
	actionRe   = regexp.MustCompile(`{{[^}]*}}`)
	fieldRefRe = regexp.MustCompile(`\.([A-Za-z0-9_]+)`)
	// strLitRe matches Go-template string literals ("…", `…`, '…') so a dotted
	// token inside one (e.g. the "%s.txt" in `{{.Names | printf "%s.txt"}}`) is
	// not mistaken for a field reference during header derivation.
	strLitRe = regexp.MustCompile("\"[^\"]*\"|`[^`]*`|'[^']*'")
)

// Render emits a list of view objects honouring Docker's -q / --format /
// default-table conventions.
func Render(format string, quiet bool, views []any, def TableDef) error {
	if quiet {
		for _, v := range views {
			fmt.Println(def.ID(v))
		}
		return nil
	}

	switch {
	case format == "":
		// Interactive terminal: render a styled table. This is the ONLY visual
		// upgrade — and it is gated on ui.Enabled() (a real TTY), so piped/CI
		// output still takes the plain tabwriter path below, byte-for-byte. An
		// empty list falls through to the plain header-only output (Docker always
		// shows the header; a lone bordered box would read as an error).
		if ui.Enabled() && len(views) > 0 {
			rows := make([][]string, 0, len(views))
			for _, v := range views {
				rows = append(rows, def.Row(v))
			}
			fmt.Println(ui.Table(def.Headers, rows))
			return nil
		}
		w := NewTabWriter()
		fmt.Fprintln(w, strings.Join(def.Headers, "\t"))
		for _, v := range views {
			fmt.Fprintln(w, strings.Join(def.Row(v), "\t"))
		}
		return w.Flush()

	case format == "json":
		// Docker prints one JSON object per line.
		for _, v := range views {
			b, err := json.Marshal(v)
			if err != nil {
				return err
			}
			fmt.Println(string(b))
		}
		return nil

	case strings.HasPrefix(format, "table"):
		body := strings.TrimSpace(strings.TrimPrefix(format, "table"))
		if body == "" {
			// `--format table` with no template => default table.
			return Render("", false, views, def)
		}
		tmpl, err := template.New("row").Funcs(tmplFuncs).Parse(body + "\n")
		if err != nil {
			return err
		}
		w := NewTabWriter()
		// Header row: replace each whole {{…}} action with its column header,
		// derived from the LAST .Field reference inside the action (so function-
		// or pipeline-prefixed actions resolve too); literal text between actions
		// is preserved verbatim. An action with no field reference is dropped.
		header := actionRe.ReplaceAllStringFunc(body, func(m string) string {
			clean := strLitRe.ReplaceAllString(m, "") // drop string literals first
			refs := fieldRefRe.FindAllStringSubmatch(clean, -1)
			if len(refs) == 0 {
				return ""
			}
			tok := "." + refs[len(refs)-1][1]
			if h, ok := fieldHeader[tok]; ok {
				return h
			}
			return strings.ToUpper(strings.TrimPrefix(tok, "."))
		})
		fmt.Fprintln(w, header)
		for _, v := range views {
			if err := tmpl.Execute(w, v); err != nil {
				return err
			}
		}
		return w.Flush()

	default:
		tmpl, err := template.New("row").Funcs(tmplFuncs).Parse(format + "\n")
		if err != nil {
			return err
		}
		for _, v := range views {
			if err := tmpl.Execute(os.Stdout, v); err != nil {
				return err
			}
		}
		return nil
	}
}

var tmplFuncs = template.FuncMap{
	"json": func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	},
	"prettyjson": func(v any) string {
		b, _ := json.MarshalIndent(v, "", "    ")
		return string(b)
	},
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"title": strings.Title,
	"join":  strings.Join,
	"split": strings.Split,
	"truncate": func(s string, n int) string {
		if len(s) > n {
			return s[:n]
		}
		return s
	},
}

// TemplateFuncs returns the Docker-compatible template helper functions
// (json, upper, lower, …) so other packages can honour --format templates that
// use them, matching `docker inspect --format '{{json .}}'`.
func TemplateFuncs() template.FuncMap { return tmplFuncs }
