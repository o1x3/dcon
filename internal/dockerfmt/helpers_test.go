package dockerfmt

import "testing"

func TestSplitRepoTag(t *testing.T) {
	cases := []struct {
		in, repo, tag string
	}{
		{"alpine", "alpine", "latest"},
		{"alpine:3.20", "alpine", "3.20"},
		{"docker.io/library/nginx:1.27", "docker.io/library/nginx", "1.27"},
		{"registry:5000/img", "registry:5000/img", "latest"},
		{"registry:5000/img:v2", "registry:5000/img", "v2"},
		{"repo@sha256:abc", "repo", "sha256:abc"},
	}
	for _, c := range cases {
		repo, tag := SplitRepoTag(c.in)
		if repo != c.repo || tag != c.tag {
			t.Errorf("SplitRepoTag(%q) = (%q,%q); want (%q,%q)", c.in, repo, tag, c.repo, c.tag)
		}
	}
}

func TestShortID(t *testing.T) {
	if got := ShortID("sha256:0123456789abcdef0000"); got != "0123456789ab" {
		t.Errorf("ShortID = %q", got)
	}
	if got := ShortID("abc"); got != "abc" {
		t.Errorf("ShortID short = %q", got)
	}
}

func TestShortImage(t *testing.T) {
	if got := ShortImage("docker.io/library/alpine:latest"); got != "alpine:latest" {
		t.Errorf("ShortImage = %q", got)
	}
	if got := ShortImage("ghcr.io/foo/bar:1"); got != "ghcr.io/foo/bar:1" {
		t.Errorf("ShortImage non-dockerhub = %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	// Values verified against docker/go-units (4 significant figures).
	cases := map[float64]string{
		0:          "0B",
		512:        "512B",
		1023:       "1.023kB",
		999600:     "999.6kB",
		4180000:    "4.18MB",
		11710000:   "11.71MB",
		1234000000: "1.234GB",
		1500000000: "1.5GB",
	}
	for in, want := range cases {
		if got := HumanSize(in); got != want {
			t.Errorf("HumanSize(%v) = %q; want %q", in, got, want)
		}
	}
}

func TestTruncCommandRuneSafe(t *testing.T) {
	// 25 multibyte runes; must cut at 20 runes without splitting a rune.
	long := []string{"日本語日本語日本語日本語日本語日本語日本語日本語日"}
	out := TruncCommand(long, false)
	inner := out[1 : len(out)-1] // strip quotes
	if n := len([]rune(inner)); n != 20 {
		t.Errorf("expected 20 runes, got %d (%q)", n, inner)
	}
}

func TestTruncCommand(t *testing.T) {
	if got := TruncCommand([]string{"sleep", "300"}, false); got != `"sleep 300"` {
		t.Errorf("TruncCommand = %q", got)
	}
	long := TruncCommand([]string{"this-is-a-very-long-command-indeed"}, false)
	if len(long) != 22 { // 20 chars + 2 quotes
		t.Errorf("TruncCommand truncation len = %d (%q)", len(long), long)
	}
}
