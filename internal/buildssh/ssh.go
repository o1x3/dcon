// Package buildssh translates Docker/BuildKit `--ssh` specs into Apple
// container's `--ssh default` form (agent forwarding only; apple/container ≥ 1.2.1).
package buildssh

import (
	"fmt"
	"strings"
)

// Translate maps Docker-style `--ssh` values onto Apple's single supported
// form. Returns whether to emit `--ssh default`, plus warnings for specs the
// backend cannot honor (named agents, key files, etc.).
func Translate(specs []string) (enable bool, warnings []string) {
	for _, raw := range specs {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if IsDefaultAgent(s) {
			enable = true
			continue
		}
		warnings = append(warnings, fmt.Sprintf("--ssh %q is not supported by the backend (only --ssh default / id=default agent forwarding); ignored", s))
	}
	return enable, warnings
}

// IsDefaultAgent reports whether a Docker/BuildKit SSH spec refers solely to
// the default SSH agent (no key file / named agent).
//
// Accepted forms: "default", "id=default", and comma-lists that contain only
// those tokens (e.g. "id=default,"). Anything with src=, a path after
// default=, or a non-default id is rejected.
func IsDefaultAgent(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	seen := false
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		switch {
		case part == "default" || part == "id=default":
			seen = true
		case strings.HasPrefix(part, "id="):
			if strings.TrimPrefix(part, "id=") != "default" {
				return false
			}
			seen = true
		default:
			// default=/path/to/key, src=…, mode=…, etc.
			return false
		}
	}
	return seen
}
