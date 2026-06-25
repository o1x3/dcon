package machine

import "testing"

func TestResolveImageAllDistros(t *testing.T) {
	for _, d := range Distros() {
		ref, err := ResolveImage(d)
		if err != nil {
			t.Errorf("ResolveImage(%q) errored: %v", d, err)
		}
		if ref == "" {
			t.Errorf("ResolveImage(%q) returned empty ref", d)
		}
	}
}

func TestResolveImageTags(t *testing.T) {
	cases := map[string]string{
		"ubuntu":          "ubuntu:latest",
		"ubuntu:noble":    "ubuntu:noble",
		"UBUNTU":          "ubuntu:latest", // case-insensitive
		"fedora:43":       "fedora:43",
		"centos":          "quay.io/centos/centos:stream9",
		"centos:stream10": "quay.io/centos/centos:stream10", // tag swap keeps registry host
		"alpine":          "alpine:latest",
	}
	for spec, want := range cases {
		got, err := ResolveImage(spec)
		if err != nil {
			t.Errorf("ResolveImage(%q) errored: %v", spec, err)
			continue
		}
		if got != want {
			t.Errorf("ResolveImage(%q) = %q, want %q", spec, got, want)
		}
	}
}

func TestResolveImageUnknown(t *testing.T) {
	if _, err := ResolveImage("frobix"); err == nil {
		t.Error("ResolveImage(frobix) should error for an unknown distro")
	}
}

func TestDistroID(t *testing.T) {
	for in, want := range map[string]string{
		"ubuntu":       "ubuntu",
		"ubuntu:noble": "ubuntu",
		"UBUNTU:Jammy": "ubuntu",
		"  fedora ":    "fedora",
	} {
		if got := DistroID(in); got != want {
			t.Errorf("DistroID(%q) = %q, want %q", in, got, want)
		}
	}
}
