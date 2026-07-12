package cmd

import (
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"dcon/internal/dockerfmt"
)

// --- history ---------------------------------------------------------------

// TestFormatCreatedByTabSanitized reproduces the table corruption: a raw tab
// in created_by is a tabwriter cell separator and shifted every column right.
// Docker replaces tabs with spaces before rendering.
func TestFormatCreatedByTabSanitized(t *testing.T) {
	got := formatCreatedBy("RUN /bin/sh -c\tapk add curl", false)
	if strings.Contains(got, "\t") {
		t.Errorf("tab must be sanitized to a space: %q", got)
	}
	if got != "RUN /bin/sh -c apk add curl" {
		t.Errorf("formatCreatedBy = %q", got)
	}
	// no-trunc still sanitizes tabs but never truncates
	long := strings.Repeat("a\tb", 40)
	if out := formatCreatedBy(long, true); strings.Contains(out, "\t") || len(out) != len(long) {
		t.Errorf("no-trunc must sanitize tabs without truncating: %q", out)
	}
}

// TestBuildHistoryViewsImageID reproduces the `<missing>`-everywhere bug:
// docker shows the image ID (12 chars; full with --no-trunc) on the newest
// row, which is also what `history -q` prints.
func TestBuildHistoryViewsImageID(t *testing.T) {
	hist := []ociHistory{
		{Created: "2024-01-01T00:00:00Z", CreatedBy: "ADD file:abc"}, // oldest
		{Created: "2024-01-02T00:00:00Z", CreatedBy: "CMD [\"sh\"]"}, // newest
	}
	const fullID = "sha256:0123456789abcdef0123456789abcdef"

	views := buildHistoryViews(fullID, hist, false)
	if len(views) != 2 {
		t.Fatalf("want 2 rows, got %d", len(views))
	}
	if id := views[0].(historyView).ID; id != "0123456789ab" {
		t.Errorf("newest row must carry the short image id, got %q", id)
	}
	if id := views[1].(historyView).ID; id != "<missing>" {
		t.Errorf("older rows stay <missing>, got %q", id)
	}

	// --no-trunc keeps the full id
	views = buildHistoryViews(fullID, hist, true)
	if id := views[0].(historyView).ID; id != fullID {
		t.Errorf("no-trunc must keep the full id, got %q", id)
	}

	// no id available: everything stays <missing> (old behavior, no panic)
	views = buildHistoryViews("", hist, false)
	if id := views[0].(historyView).ID; id != "<missing>" {
		t.Errorf("missing image id must stay <missing>, got %q", id)
	}
}

// TestInspectImageRawPreferredVariant reproduces the Variants[0] blind read:
// history must pick the same linux/GOARCH-first variant imageSizeBytes uses.
func TestInspectImageRawPreferredVariant(t *testing.T) {
	var img inspectImageRaw
	img.Variants = make([]struct {
		Platform dockerfmt.Platform `json:"platform"`
		Config   struct {
			History []ociHistory `json:"history"`
		} `json:"config"`
	}, 2)
	img.Variants[0].Platform = dockerfmt.Platform{OS: "linux", Architecture: "otherarch"}
	img.Variants[0].Config.History = []ociHistory{{CreatedBy: "foreign"}}
	img.Variants[1].Platform = dockerfmt.Platform{OS: "linux", Architecture: goruntime.GOARCH}
	img.Variants[1].Config.History = []ociHistory{{CreatedBy: "native"}}

	hist := img.history()
	if len(hist) != 1 || hist[0].CreatedBy != "native" {
		t.Errorf("history must come from the linux/%s variant, got %+v", goruntime.GOARCH, hist)
	}
}

func TestPreferredVariantIdx(t *testing.T) {
	native := dockerfmt.Platform{OS: "linux", Architecture: goruntime.GOARCH}
	foreign := dockerfmt.Platform{OS: "linux", Architecture: "otherarch"}
	windows := dockerfmt.Platform{OS: "windows", Architecture: goruntime.GOARCH}
	cases := []struct {
		plats []dockerfmt.Platform
		want  int
	}{
		{[]dockerfmt.Platform{foreign, native}, 1},  // host arch wins
		{[]dockerfmt.Platform{windows, foreign}, 1}, // first linux fallback
		{[]dockerfmt.Platform{windows}, -1},         // no linux variant
		{nil, -1},
	}
	for i, c := range cases {
		if got := preferredVariantIdx(c.plats); got != c.want {
			t.Errorf("case %d: preferredVariantIdx = %d, want %d", i, got, c.want)
		}
	}
}

// --- images ----------------------------------------------------------------

// TestBuildImageViewDigestPinnedTag reproduces the digest-in-TAG bug: a
// digest-pinned image (repo@sha256:…) must show TAG <none> like docker, not
// the raw digest.
func TestBuildImageViewDigestPinnedTag(t *testing.T) {
	var img dockerfmt.Image
	img.ID = "sha256:aabbccddeeff00112233"
	img.Configuration.Name = "docker.io/library/alpine@sha256:deadbeef"
	v := buildImageView(img, false)
	if v.Tag != "<none>" {
		t.Errorf("digest-pinned TAG = %q, want <none>", v.Tag)
	}
	if v.Repository != "alpine" {
		t.Errorf("digest-pinned REPOSITORY = %q, want alpine", v.Repository)
	}
}

// TestBuildImageViewCreatedAtFormatted reproduces the raw-RFC3339 leak:
// docker's .CreatedAt is "2006-01-02 15:04:05 -0700 MST" (same as ps).
func TestBuildImageViewCreatedAtFormatted(t *testing.T) {
	var img dockerfmt.Image
	img.Configuration.Name = "alpine:latest"
	img.Configuration.CreationDate = "2024-03-01T12:30:45Z"
	v := buildImageView(img, false)
	want, _ := time.Parse(time.RFC3339, "2024-03-01T12:30:45Z")
	if v.CreatedAt != want.Format("2006-01-02 15:04:05 -0700 MST") {
		t.Errorf("CreatedAt = %q", v.CreatedAt)
	}
	if !v.created.Equal(want) {
		t.Errorf("parsed creation time must be kept for filters, got %v", v.created)
	}
}

// TestRefPatternMatch reproduces the positional over-match: `dcon images
// alpine` must not list ghcr.io/foo/alpine (the old HasSuffix clause did),
// while wildcards keep working path-component aware.
func TestRefPatternMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"alpine", "alpine", true},
		{"alpine", "ghcr.io/foo/alpine", false}, // the old suffix over-match
		{"alpine", "myalpine", false},
		{"alpine*", "alpine", true},
		{"alpine*", "alpine-slim", true},
		{"alpine*", "ghcr.io/foo/alpine", false}, // * must not cross /
		{"ghcr.io/foo/*", "ghcr.io/foo/alpine", true},
		{"3.1?", "3.18", true},
	}
	for _, c := range cases {
		if got := refPatternMatch(c.pattern, c.s); got != c.want {
			t.Errorf("refPatternMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func mkImage(id, name, created string) dockerfmt.Image {
	var img dockerfmt.Image
	img.ID = id
	img.Configuration.Name = name
	img.Configuration.CreationDate = created
	return img
}

// TestImagesBeforeSinceFilters covers `images --filter before=/since=`: pure
// CreationDate comparisons against the named image, erroring when the
// reference image does not exist.
func TestImagesBeforeSinceFilters(t *testing.T) {
	list := []dockerfmt.Image{
		mkImage("sha256:aaa1", "docker.io/library/old:1", "2024-01-01T00:00:00Z"),
		mkImage("sha256:bbb2", "docker.io/library/mid:1", "2024-02-01T00:00:00Z"),
		mkImage("sha256:ccc3", "docker.io/library/new:1", "2024-03-01T00:00:00Z"),
	}
	beforeT, sinceT, err := resolveImageTimeFilters(list, []string{"before=mid:1"})
	if err != nil {
		t.Fatalf("resolve before: %v", err)
	}
	if !sinceT.IsZero() {
		t.Errorf("since should stay zero")
	}
	keep := func(i int, filters []string) bool {
		return matchImageFilters(buildImageView(list[i], false), filters, beforeT, sinceT)
	}
	if !keep(0, []string{"before=mid:1"}) || keep(1, []string{"before=mid:1"}) || keep(2, []string{"before=mid:1"}) {
		t.Error("before=mid must keep only strictly older images")
	}

	_, sinceT, err = resolveImageTimeFilters(list, []string{"since=mid:1"})
	if err != nil {
		t.Fatalf("resolve since: %v", err)
	}
	if keep(0, []string{"since=mid:1"}) || keep(1, []string{"since=mid:1"}) || !keep(2, []string{"since=mid:1"}) {
		t.Error("since=mid must keep only strictly newer images")
	}

	// reference by ID prefix works; a missing image errors like docker
	if _, _, err := resolveImageTimeFilters(list, []string{"before=bbb2"}); err != nil {
		t.Errorf("id-prefix reference should resolve: %v", err)
	}
	if _, _, err := resolveImageTimeFilters(list, []string{"before=nosuch"}); err == nil {
		t.Error("unknown reference image must error")
	}
}

// --- ps --------------------------------------------------------------------

// TestDockerStateMapping reproduces the backend-vocabulary leak in
// `ps --format '{{.State}}'`: docker's enum only.
func TestDockerStateMapping(t *testing.T) {
	cases := map[string]string{
		"running":  "running",
		"stopped":  "exited",
		"stopping": "removing",
		"":         "created",
		"unknown":  "created",
	}
	for in, want := range cases {
		if got := dockerState(in); got != want {
			t.Errorf("dockerState(%q) = %q, want %q", in, got, want)
		}
		var c dockerfmt.Container
		c.Status.State = in
		if got := buildPsView(c, false).State; got != want {
			t.Errorf("psView.State for %q = %q, want %q", in, got, want)
		}
	}
}

func mkFilterContainer(id, state, created string) dockerfmt.Container {
	var c dockerfmt.Container
	c.ID = id
	c.Status.State = state
	c.Configuration.CreationDate = created
	return c
}

// TestApplyFiltersNetworkPublishVolume covers the client-side implementations
// of docker's network=, publish=, and volume= ps filters.
func TestApplyFiltersNetworkPublishVolume(t *testing.T) {
	a := mkFilterContainer("a", "running", "2024-01-01T00:00:00Z")
	a.Configuration.Networks = []dockerfmt.AttachmentConf{{Network: "backend"}}
	a.Configuration.Ports = []dockerfmt.PublishPort{{HostPort: 8080, ContainerPort: 80, Proto: "tcp", Count: 1}}
	a.Configuration.Mounts = []dockerfmt.Filesystem{{Source: "appdata", Destination: "/data"}}
	b := mkFilterContainer("b", "running", "2024-01-02T00:00:00Z")
	b.Status.Networks = []dockerfmt.Attachment{{Network: "frontend"}}
	b.Configuration.Ports = []dockerfmt.PublishPort{{HostPort: 9000, ContainerPort: 9000, Proto: "udp", Count: 3}}
	list := []dockerfmt.Container{a, b}

	cases := []struct {
		filter string
		want   []string
	}{
		{"network=backend", []string{"a"}},
		{"network=frontend", []string{"b"}}, // runtime attachment counts too
		{"network=nosuch", nil},
		{"publish=80", []string{"a"}}, // container port, default proto tcp
		{"publish=80/tcp", []string{"a"}},
		{"publish=80/udp", nil},             // proto mismatch
		{"publish=9001/udp", []string{"b"}}, // inside the Count range 9000-9002
		{"publish=70-90", []string{"a"}},    // range form
		{"volume=appdata", []string{"a"}},   // volume name
		{"volume=/data", []string{"a"}},     // destination path
		{"volume=nosuch", nil},
	}
	for _, c := range cases {
		got, err := applyFilters(list, []string{c.filter})
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.filter, err)
			continue
		}
		var ids []string
		for _, x := range got {
			ids = append(ids, x.ID)
		}
		if len(ids) != len(c.want) || (len(c.want) > 0 && ids[0] != c.want[0]) {
			t.Errorf("%s: got %v, want %v", c.filter, ids, c.want)
		}
	}

	// malformed publish value errors instead of silently matching everything
	if _, err := applyFilters(list, []string{"publish=http"}); err == nil {
		t.Error("publish=http must error")
	}
}

// TestApplyFiltersBeforeSince covers before=/since= CreationDate comparisons
// against the named container, erroring for an unknown reference.
func TestApplyFiltersBeforeSince(t *testing.T) {
	list := []dockerfmt.Container{
		mkFilterContainer("old1", "running", "2024-01-01T00:00:00Z"),
		mkFilterContainer("mid1", "running", "2024-02-01T00:00:00Z"),
		mkFilterContainer("new1", "running", "2024-03-01T00:00:00Z"),
	}
	got, err := applyFilters(list, []string{"before=mid1"})
	if err != nil || len(got) != 1 || got[0].ID != "old1" {
		t.Errorf("before=mid1 = %v (%v), want [old1]", got, err)
	}
	got, err = applyFilters(list, []string{"since=mid1"})
	if err != nil || len(got) != 1 || got[0].ID != "new1" {
		t.Errorf("since=mid1 = %v (%v), want [new1]", got, err)
	}
	// ID prefix resolves; unknown reference errors like docker
	if _, err := applyFilters(list, []string{"since=mid"}); err != nil {
		t.Errorf("id-prefix reference should resolve: %v", err)
	}
	if _, err := applyFilters(list, []string{"before=nosuch"}); err == nil {
		t.Error("before=nosuch must error")
	}
}

// TestApplyFiltersNameRegex locks docker's name semantics: an unanchored
// regex (substring by default, ^…$ anchors honored), erroring on an invalid
// pattern instead of silently matching everything.
func TestApplyFiltersNameRegex(t *testing.T) {
	list := []dockerfmt.Container{
		mkFilterContainer("web", "running", "2024-01-01T00:00:00Z"),
		mkFilterContainer("web-2", "running", "2024-01-01T00:00:00Z"),
	}
	got, err := applyFilters(list, []string{"name=web"})
	if err != nil || len(got) != 2 {
		t.Errorf("substring regex should match both: %v (%v)", got, err)
	}
	got, err = applyFilters(list, []string{"name=^web$"})
	if err != nil || len(got) != 1 || got[0].ID != "web" {
		t.Errorf("anchored regex should match exactly one: %v (%v)", got, err)
	}
	if _, err := applyFilters(list, []string{"name=["}); err == nil {
		t.Error("invalid regex must error")
	}
}

// TestApplyFiltersInvalidStatusErrors reproduces the silent match-all: a bad
// status value fed into `dcon rm $(dcon ps -aq --filter status=exted)` used to
// select EVERY container. Docker errors; so do we.
func TestApplyFiltersInvalidStatusErrors(t *testing.T) {
	list := []dockerfmt.Container{mkFilterContainer("a", "running", "2024-01-01T00:00:00Z")}
	if _, err := applyFilters(list, []string{"status=exted"}); err == nil {
		t.Error("invalid status value must error")
	}
	for _, ok := range []string{"status=exited", "status=running", "status=stopped"} {
		if _, err := applyFilters(list, []string{ok}); err != nil {
			t.Errorf("%s should be accepted: %v", ok, err)
		}
	}
}

// TestApplyFiltersUnsupportedKeyFailsSafe: recognized docker keys the backend
// cannot answer (exited=, health=) must match NOTHING (never everything), so
// destructive pipelines fail safe.
func TestApplyFiltersUnsupportedKeyFailsSafe(t *testing.T) {
	list := []dockerfmt.Container{
		mkFilterContainer("a", "stopped", "2024-01-01T00:00:00Z"),
		mkFilterContainer("b", "stopped", "2024-01-01T00:00:00Z"),
	}
	for _, fl := range []string{"exited=0", "health=healthy"} {
		got, err := applyFilters(list, []string{fl})
		if err != nil {
			t.Errorf("%s: unexpected error %v", fl, err)
		}
		if len(got) != 0 {
			t.Errorf("%s must exclude all containers, got %v", fl, got)
		}
	}
	// a truly unknown key is warned but stays match-all (docker compat shim)
	got, err := applyFilters(list, []string{"frobnicate=1"})
	if err != nil || len(got) != 2 {
		t.Errorf("unknown key should warn and match all: %v (%v)", got, err)
	}
}

func TestHasTimeFilter(t *testing.T) {
	if !hasTimeFilter([]string{"before=x"}) || !hasTimeFilter([]string{"label=a", "since=y"}) {
		t.Error("before=/since= must be detected")
	}
	if hasTimeFilter([]string{"status=running", "name=x"}) {
		t.Error("no time filter -> false")
	}
}
