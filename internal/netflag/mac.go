// Package netflag translates Docker-style network options into Apple
// container's `--network <name>[,mac=…][,mtu=…]` form.
package netflag

import (
	"fmt"
	"strings"
)

// WithMAC merges a network name (or Apple network option list) with a Docker
// `--mac-address` / Compose `mac_address` value.
//
// Empty / "default" / "bridge" networks are omitted unless a MAC is set (Apple
// attaches to `default` automatically); with a MAC we emit `default,mac=…`.
// A network spec that already carries `mac=` conflicts with an explicit MAC.
func WithMAC(net, mac string) (string, error) {
	net = strings.TrimSpace(net)
	mac = strings.TrimSpace(mac)
	if mac != "" {
		if err := ValidateMAC(mac); err != nil {
			return "", err
		}
	}
	if HasMAC(net) {
		if mac != "" {
			return "", fmt.Errorf("--mac-address conflicts with mac= already set on --network %q", net)
		}
		return net, nil
	}
	if mac == "" {
		if net == "" || net == "default" || net == "bridge" {
			return "", nil
		}
		return net, nil
	}
	// MAC set: Docker's default/bridge and an unset network all map to Apple's
	// default network once we need an explicit --network carrier for mac=.
	if net == "" || net == "bridge" {
		net = "default"
	}
	return net + ",mac=" + mac, nil
}

// AttachMAC appends `,mac=<mac>` to a concrete network name that does not
// already carry mac=. Unlike WithMAC it never rewrites empty/default/bridge to
// "" — callers that already emit an explicit `--network` use this.
func AttachMAC(net, mac string) (string, error) {
	net = strings.TrimSpace(net)
	mac = strings.TrimSpace(mac)
	if mac == "" {
		return net, nil
	}
	if err := ValidateMAC(mac); err != nil {
		return "", err
	}
	if HasMAC(net) {
		return "", fmt.Errorf("mac_address conflicts with mac= already set on network %q", net)
	}
	if net == "" {
		net = "default"
	}
	return net + ",mac=" + mac, nil
}

// HasMAC reports whether an Apple `--network` option list already includes mac=.
func HasMAC(net string) bool {
	for _, part := range strings.Split(net, ",") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(part)), "mac=") {
			return true
		}
	}
	return false
}

// ValidateMAC accepts XX:XX:XX:XX:XX:XX or XX-XX-XX-XX-XX-XX (Apple's
// documented forms). Hex digits only; length/shape checked, not LAA/unicast bits.
func ValidateMAC(mac string) error {
	sep := byte(0)
	for i := 0; i < len(mac); i++ {
		c := mac[i]
		if c == ':' || c == '-' {
			if sep == 0 {
				sep = c
			} else if c != sep {
				return fmt.Errorf("invalid MAC address %q: mix of ':' and '-' separators", mac)
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return fmt.Errorf("invalid MAC address %q: expected hex octets separated by ':' or '-'", mac)
		}
	}
	if sep == 0 {
		return fmt.Errorf("invalid MAC address %q: expected XX:XX:XX:XX:XX:XX", mac)
	}
	parts := strings.Split(mac, string(sep))
	if len(parts) != 6 {
		return fmt.Errorf("invalid MAC address %q: expected 6 octets", mac)
	}
	for _, p := range parts {
		if len(p) != 2 {
			return fmt.Errorf("invalid MAC address %q: each octet must be two hex digits", mac)
		}
	}
	return nil
}
