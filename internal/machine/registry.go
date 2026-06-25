// Package machine implements dcon's OrbStack-style Linux "machines": persistent,
// shell-able microVMs layered on Apple's container runtime. A machine is simply
// a long-lived detached container booted from a distro base image with a
// keep-alive PID 1, labelled so dcon can tell machines apart from ordinary
// containers. You create one, open a shell into it, stop/start it, and delete it
// — the container's writable filesystem is what persists between sessions.
//
// This file is the distro catalogue. The state, name, and run-argument logic
// live alongside it (state.go, run.go) and are pure/unit-testable so they can be
// exercised without the backend (CI has none).
package machine

import (
	"fmt"
	"sort"
	"strings"
)

// distroImage maps each OrbStack-supported distro id to the published image
// dcon boots it from. Several distros have no docker.io/library image, so we
// point at the canonical community/registry image instead. The tag baked in
// here is the default; a user spec of "distro:tag" overrides only the tag.
//
// Caveat: arm64 (Apple Silicon) manifests are NOT guaranteed for every image
// below (kali/void/openeuler/gentoo in particular). When a pull 404s on the
// host architecture, pass --arch to select a buildable variant; the backend's
// own pull error surfaces verbatim.
var distroImage = map[string]string{
	"alma":      "almalinux:latest",
	"alpine":    "alpine:latest",
	"arch":      "archlinux:latest",
	"centos":    "quay.io/centos/centos:stream9", // docker.io/library/centos is EOL
	"debian":    "debian:latest",
	"devuan":    "dyne/devuan:latest", // no library image
	"fedora":    "fedora:latest",
	"gentoo":    "gentoo/stage3:latest", // no library image
	"kali":      "kalilinux/kali-rolling:latest",
	"nixos":     "nixos/nix:latest", // Nix on a minimal base, not a full NixOS rootfs
	"openeuler": "openeuler/openeuler:latest",
	"opensuse":  "opensuse/leap:latest", // docker.io/library/opensuse is deprecated
	"oracle":    "oraclelinux:latest",
	"rocky":     "rockylinux:latest",
	"ubuntu":    "ubuntu:latest",
	"void":      "ghcr.io/void-linux/void-linux:latest-full-x86_64",
}

// Distros returns the supported distro ids, sorted.
func Distros() []string {
	out := make([]string, 0, len(distroImage))
	for k := range distroImage {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DistroID extracts the bare distro id from a spec, dropping any ":tag".
func DistroID(spec string) string {
	if i := strings.IndexByte(spec, ':'); i >= 0 {
		spec = spec[:i]
	}
	return strings.ToLower(strings.TrimSpace(spec))
}

// ResolveImage turns a distro spec ("ubuntu", "ubuntu:noble", "fedora:43") into
// the image reference to boot. An unknown distro is an error that lists the
// valid ids. A tag on the spec replaces the catalogue default tag.
func ResolveImage(spec string) (string, error) {
	name := spec
	tag := ""
	if i := strings.IndexByte(spec, ':'); i >= 0 {
		name, tag = spec[:i], spec[i+1:]
	}
	name = strings.ToLower(strings.TrimSpace(name))
	ref, ok := distroImage[name]
	if !ok {
		return "", fmt.Errorf("unknown distro %q; supported: %s", name, strings.Join(Distros(), ", "))
	}
	if tag != "" {
		ref = replaceTag(ref, tag)
	}
	return ref, nil
}

// replaceTag swaps the tag on an image reference, correctly leaving a registry
// host:port (a colon before the final path segment) untouched.
func replaceTag(ref, tag string) string {
	slash := strings.LastIndex(ref, "/")
	last := ref[slash+1:]
	if i := strings.LastIndex(last, ":"); i >= 0 {
		return ref[:slash+1] + last[:i] + ":" + tag
	}
	return ref + ":" + tag
}
