package cmd

import (
	"testing"

	"dcon/internal/dockerfmt"

	"github.com/spf13/cobra"
)

// subcmd finds a direct subcommand by name (e.g. the "ls" under a group).
func subcmd(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("subcommand %q not found under %q", name, parent.Name())
	return nil
}

// TestFilterFlagsNotCommaSplit reproduces the StringSlice corruption: a single
// --filter value with a comma in a label value (e.g. label=team=a,b) was split
// into two bogus filters. The flag must be StringArray so each --filter is taken
// verbatim. Covers ps, images, and the volume/network ls commands.
func TestFilterFlagsNotCommaSplit(t *testing.T) {
	const raw = "label=team=a,b"
	check := func(t *testing.T, c *cobra.Command) {
		if err := c.ParseFlags([]string{"--filter", raw}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		got, err := c.Flags().GetStringArray("filter")
		if err != nil {
			t.Fatalf("filter is not a StringArray flag: %v", err)
		}
		if len(got) != 1 || got[0] != raw {
			t.Errorf("filter = %v, want exactly [%q] (comma must not split)", got, raw)
		}
	}
	t.Run("ps", func(t *testing.T) { check(t, newPsCmd()) })
	t.Run("images", func(t *testing.T) { check(t, newImagesCmd()) })
	t.Run("volume", func(t *testing.T) { check(t, subcmd(t, newVolumeGroupCmd(), "ls")) })
	t.Run("network", func(t *testing.T) { check(t, subcmd(t, newNetworkGroupCmd(), "ls")) })
}

// TestApplyFiltersCommaLabelValue confirms the downstream predicate matches a
// label whose value contains a comma, once the flag is no longer split.
func TestApplyFiltersCommaLabelValue(t *testing.T) {
	mk := func(id, team string) dockerfmt.Container {
		var c dockerfmt.Container
		c.ID = id
		c.Configuration.Labels = map[string]string{"team": team}
		return c
	}
	list := []dockerfmt.Container{mk("hit", "a,b"), mk("miss", "a")}
	out, err := applyFilters(list, []string{"label=team=a,b"})
	if err != nil || len(out) != 1 || out[0].ID != "hit" {
		t.Errorf("applyFilters by comma label value = %v (%v), want [hit]", out, err)
	}
}

// TestTrimLast covers docker's -n/--last semantics, including the regression
// where `ps -n 0` listed everything instead of showing none.
func TestTrimLast(t *testing.T) {
	mk := func(n int) []dockerfmt.Container {
		out := make([]dockerfmt.Container, n)
		for i := range out {
			out[i].ID = string(rune('a' + i))
		}
		return out
	}
	list := mk(3)
	if got := trimLast(list, -1); len(got) != 3 {
		t.Errorf("last=-1 (unset) should keep all; got %d", len(got))
	}
	if got := trimLast(list, 0); len(got) != 0 {
		t.Errorf("last=0 should show none; got %d", len(got))
	}
	if got := trimLast(list, 2); len(got) != 2 {
		t.Errorf("last=2 should keep 2; got %d", len(got))
	}
	if got := trimLast(list, 9); len(got) != 3 {
		t.Errorf("last>len should keep all; got %d", len(got))
	}
}

// TestImageRefFilter guards the positional `images REPO[:TAG|@DIGEST]` parsing:
// a registry-port colon must NOT be read as a tag (which hid all non-:latest
// tags), and a digest must filter the digest column, not the tag.
func TestImageRefFilter(t *testing.T) {
	cases := []struct {
		ref                       string
		wantRepo, wantTag, wantDg string
	}{
		{"alpine", "alpine", "", ""},
		{"alpine:3.18", "alpine", "3.18", ""},
		{"registry:5000/myimage", "registry:5000/myimage", "", ""},        // port, not a tag
		{"registry:5000/myimage:1.0", "registry:5000/myimage", "1.0", ""}, // real tag kept
		{"alpine@sha256:abc", "alpine", "", "sha256:abc"},                 // digest, not tag
		{"docker.io/library/alpine", "alpine", "", ""},                    // fully-qualified -> short
		{"docker.io/library/alpine:3.18", "alpine", "3.18", ""},
		{"docker.io/myorg/app", "myorg/app", "", ""},
	}
	for _, c := range cases {
		repo, tag, dg := imageRefFilter(c.ref)
		if repo != c.wantRepo || tag != c.wantTag || dg != c.wantDg {
			t.Errorf("imageRefFilter(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.ref, repo, tag, dg, c.wantRepo, c.wantTag, c.wantDg)
		}
	}
}

// TestHasStatusFilter reproduces the bug where `ps --filter status=exited`
// without -a fetched only running containers; a status= filter must force the
// all-states fetch.
func TestHasStatusFilter(t *testing.T) {
	if !hasStatusFilter([]string{"status=exited"}) {
		t.Error("status=exited should require all-states fetch")
	}
	if !hasStatusFilter([]string{"label=a=b", "status=running"}) {
		t.Error("status=running among others should be detected")
	}
	if hasStatusFilter([]string{"label=a=b", "name=x"}) {
		t.Error("no status filter -> false")
	}
}

// TestAncestorMatches reproduces the bug where `ps --filter ancestor=alpine`
// used a loose substring and matched superstrings like "myalpine".
func TestAncestorMatches(t *testing.T) {
	// matches: exact repo, repo:tag, full ref, docker.io short form
	for _, ref := range []string{"alpine:latest", "alpine:3.18", "docker.io/library/alpine:latest"} {
		if !ancestorMatches(ref, "alpine") {
			t.Errorf("ancestor=alpine should match image %q", ref)
		}
	}
	if !ancestorMatches("alpine:3.18", "alpine:3.18") {
		t.Error("ancestor=alpine:3.18 should match repo:tag")
	}
	// A fully-qualified ancestor filter must still match a shortened stored ref
	// (and vice-versa): both sides are normalized before comparison.
	if !ancestorMatches("alpine:latest", "docker.io/library/alpine") {
		t.Error("fully-qualified ancestor should match a shortened image ref")
	}
	if !ancestorMatches("docker.io/library/alpine:latest", "alpine") {
		t.Error("short ancestor should match a fully-qualified image ref")
	}
	// must NOT match superstring repos
	for _, ref := range []string{"myalpine:latest", "alpine-test:1", "notalpine:latest"} {
		if ancestorMatches(ref, "alpine") {
			t.Errorf("ancestor=alpine must NOT match %q (substring)", ref)
		}
	}
}

// TestValidateImageFilters confirms a malformed reference= glob errors loudly
// instead of silently hiding every image.
func TestValidateImageFilters(t *testing.T) {
	if err := validateImageFilters([]string{"reference=ngin[x"}); err == nil {
		t.Error("malformed reference pattern should error")
	}
	if err := validateImageFilters([]string{"reference=nginx*", "label=a=b"}); err != nil {
		t.Errorf("valid filters should not error: %v", err)
	}
}
