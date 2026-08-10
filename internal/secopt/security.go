// Package secopt translates Docker `--security-opt` values that Apple
// container can approximate (apple/container ≥ 1.2.1 masked/readonly paths).
package secopt

import (
	"fmt"
	"strings"
)

// Result is the Apple-native flags derived from Docker security options,
// plus warnings for options that remain unsupported.
type Result struct {
	// MaskedPaths are `--masked-path` values to forward (may include "NONE").
	MaskedPaths []string
	// Warnings describe security-opt values that were ignored.
	Warnings []string
}

// Translate maps Docker `--security-opt` values onto Apple container flags.
//
// Currently honored:
//   - systempaths=unconfined → --masked-path NONE (clears OCI masked-path defaults)
//
// Everything else is warned and ignored (seccomp, apparmor, label, …).
func Translate(opts []string) Result {
	var r Result
	for _, raw := range opts {
		o := strings.TrimSpace(raw)
		if o == "" {
			continue
		}
		key, val, _ := strings.Cut(strings.ToLower(o), "=")
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch {
		case key == "systempaths" && val == "unconfined":
			r.MaskedPaths = append(r.MaskedPaths, "NONE")
		default:
			r.Warnings = append(r.Warnings, fmt.Sprintf("--security-opt %s is not supported by the container backend and was ignored", o))
		}
	}
	return r
}
