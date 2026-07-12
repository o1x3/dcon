package dockerfmt

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// HumanSize renders a byte count the way Docker (go-units) does: decimal SI
// units with 4 significant figures.
func HumanSize(n float64) string {
	const unit = 1000.0
	units := []string{"B", "kB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
	i := 0
	for n >= unit && i < len(units)-1 {
		n /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%dB", int64(n))
	}
	return fmt.Sprintf("%.4g%s", n, units[i])
}

// HumanSizeBytes is HumanSize over an unsigned value.
func HumanSizeBytes(n uint64) string { return HumanSize(float64(n)) }

// HumanSizeWithPrecision renders a byte count in decimal SI units (base 1000)
// with the given number of significant figures, matching go-units
// HumanSizeWithPrecision. `docker images` SIZE and `docker stats` NET/BLOCK I/O
// use precision 3 — the default HumanSize (precision 4) prints one digit too
// many for those columns.
func HumanSizeWithPrecision(n float64, precision int) string {
	const unit = 1000.0
	units := []string{"B", "kB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
	i := 0
	for n >= unit && i < len(units)-1 {
		n /= unit
		i++
	}
	return fmt.Sprintf("%.*g%s", precision, n, units[i])
}

// HumanSizeBinary renders a byte count in binary IEC units (base 1024) with 4
// significant figures, matching go-units BytesSize — what `docker stats` uses
// for the MEM USAGE / LIMIT column (KiB/MiB/GiB), distinct from the decimal SI
// units docker uses for sizes and net/block I/O.
func HumanSizeBinary(n float64) string {
	const unit = 1024.0
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB", "ZiB", "YiB"}
	i := 0
	for n >= unit && i < len(units)-1 {
		n /= unit
		i++
	}
	return fmt.Sprintf("%.4g%s", n, units[i])
}

// HumanSizeBinaryBytes is HumanSizeBinary over an unsigned value.
func HumanSizeBinaryBytes(n uint64) string { return HumanSizeBinary(float64(n)) }

// ParseTime parses the ISO-8601 timestamps container emits. It tolerates a few
// shapes (with/without fractional seconds, Z or offset).
func ParseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// RelativeAgo renders "5 minutes ago", matching docker's units.go output.
// A zero timestamp (e.g. 0001-01-01T00:00:00Z from a backend that never set
// the field) is meaningless, not "2025 years ago".
func RelativeAgo(s string) string {
	t, ok := ParseTime(s)
	if !ok || t.IsZero() {
		return "N/A"
	}
	return HumanDuration(time.Since(t)) + " ago"
}

// HumanDuration mirrors docker/go-units HumanDuration.
func HumanDuration(d time.Duration) string {
	seconds := int(d.Seconds())
	switch {
	case seconds < 1:
		return "Less than a second"
	case seconds == 1:
		return "1 second"
	case seconds < 60:
		return fmt.Sprintf("%d seconds", seconds)
	}
	minutes := int(d.Minutes())
	switch {
	case minutes == 1:
		return "About a minute"
	case minutes < 60:
		return fmt.Sprintf("%d minutes", minutes)
	}
	hours := int(d.Hours() + 0.5)
	switch {
	case hours == 1:
		return "About an hour"
	case hours < 48:
		return fmt.Sprintf("%d hours", hours)
	case hours < 24*7*2:
		return fmt.Sprintf("%d days", hours/24)
	case hours < 24*30*2:
		// go-units switches weeks->months at 24*30*2 (1440h / 60 days), verified
		// against docker/go-units duration.go. (An earlier edit to 24*30*3 was a
		// regression that mislabeled 60-89-day ages as weeks instead of months.)
		return fmt.Sprintf("%d weeks", hours/24/7)
	case hours < 24*365*2:
		return fmt.Sprintf("%d months", hours/24/30)
	}
	return fmt.Sprintf("%d years", int(d.Hours())/24/365)
}

// ShortID truncates an id to 12 chars the way Docker does, stripping any
// algorithm prefix (sha256:).
func ShortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// ShortImage strips the docker.io/library/ and docker.io/ prefixes so a
// reference like "docker.io/library/alpine:latest" displays as "alpine:latest",
// matching how the Docker CLI shows images the user pulled by short name.
func ShortImage(ref string) string {
	ref = strings.TrimPrefix(ref, "docker.io/library/")
	ref = strings.TrimPrefix(ref, "docker.io/")
	return ref
}

// SplitRepoTag splits "repo:tag" into ("repo","tag"), defaulting tag to
// "latest" and handling digests and registry ports correctly.
func SplitRepoTag(ref string) (repo, tag string) {
	repo = ref
	tag = "latest"
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		// digest reference: repo@sha256:...
		return ref[:i], ref[i+1:]
	}
	// A colon is a tag separator only if it appears after the last slash
	// (otherwise it's a registry port like registry:5000/img).
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		repo = ref[:i]
		tag = ref[i+1:]
	}
	return repo, tag
}

// Ellipsis truncates s to fit within maxDisplayWidth display columns (wide
// CJK runes count as 2) and appends "…", exactly like docker's
// formatter.Ellipsis. For maxDisplayWidth of 1 and lower, no ellipsis is
// appended. Counting display columns — not runes or bytes — is what keeps
// docker's tables aligned when a command or created_by contains wide runes.
func Ellipsis(s string, maxDisplayWidth int) string {
	if maxDisplayWidth <= 0 {
		return ""
	}
	rs := []rune(s)
	if maxDisplayWidth == 1 && len(rs) > 0 {
		return string(rs[0])
	}
	if runewidth.StringWidth(s) <= maxDisplayWidth {
		return s
	}
	var (
		out []rune
		w   int
	)
	for _, r := range rs {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxDisplayWidth-1 { // reserve 1 column for the ellipsis
			break
		}
		out = append(out, r)
		w += rw
	}
	return string(out) + "…"
}

// TruncCommand renders a container command list the way `docker ps` does:
// space-joined, truncated to 20 display columns (wide CJK runes count as 2)
// unless noTrunc, then strconv.Quote'd — which both wraps it in double quotes
// and escapes embedded quotes/backslashes/control chars exactly as the Docker
// CLI's strconv.Quote(Ellipsis(command, 20)) does. Quote escaping is also what
// keeps embedded newlines/tabs from breaking the tabwriter row.
func TruncCommand(parts []string, noTrunc bool) string {
	cmd := strings.Join(parts, " ")
	if !noTrunc {
		cmd = Ellipsis(cmd, 20)
	}
	return strconv.Quote(cmd)
}
