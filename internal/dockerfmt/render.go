package dockerfmt

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"text/template"
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

var fieldTokenRe = regexp.MustCompile(`{{\s*(\.[A-Za-z0-9_]+)`)

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
		// Header row: substitute each field token with its column header.
		header := fieldTokenRe.ReplaceAllStringFunc(body, func(m string) string {
			tok := fieldTokenRe.FindStringSubmatch(m)[1]
			if h, ok := fieldHeader[tok]; ok {
				return strings.Replace(m, tok, h, 1)
			}
			return strings.Replace(m, tok, strings.ToUpper(strings.TrimPrefix(tok, ".")), 1)
		})
		header = strings.NewReplacer("{{", "", "}}", "", ".", "").Replace(header)
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
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"title": strings.Title,
	"truncate": func(s string, n int) string {
		if len(s) > n {
			return s[:n]
		}
		return s
	},
}
