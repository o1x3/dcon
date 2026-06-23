package dockerfmt

import (
	"fmt"
	"strings"
	"time"
)

// HumanSize renders a byte count the way Docker does (decimal SI units).
func HumanSize(n float64) string {
	const unit = 1000.0
	if n < unit {
		return fmt.Sprintf("%dB", int64(n))
	}
	units := []string{"kB", "MB", "GB", "TB", "PB"}
	val := n
	for _, u := range units {
		val /= unit
		if val < unit {
			return fmt.Sprintf("%.3g%s", val, u)
		}
	}
	return fmt.Sprintf("%.3gEB", val)
}

// HumanSizeBytes is HumanSize over an unsigned value.
func HumanSizeBytes(n uint64) string { return HumanSize(float64(n)) }

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
func RelativeAgo(s string) string {
	t, ok := ParseTime(s)
	if !ok {
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

// TruncCommand renders a container command list the way `docker ps` does:
// space-joined, wrapped in quotes, truncated to 20 chars unless noTrunc.
func TruncCommand(parts []string, noTrunc bool) string {
	cmd := strings.Join(parts, " ")
	cmd = strings.ReplaceAll(cmd, "\n", " ")
	if !noTrunc && len(cmd) > 20 {
		cmd = cmd[:20]
	}
	return `"` + cmd + `"`
}
