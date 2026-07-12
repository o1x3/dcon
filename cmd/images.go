package cmd

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"dcon/internal/dockerfmt"
	"dcon/internal/pool"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

type imageView struct {
	Repository   string
	Tag          string
	ID           string
	Digest       string
	CreatedSince string
	CreatedAt    string
	Size         string
	Platform     string

	// created keeps the parsed creation time (unexported: invisible to
	// templates and json) for before=/since= filtering.
	created time.Time
}

// preferredVariantIdx picks the image variant closest to docker's notion of
// "the" image on this host: the linux/GOARCH variant, else the first linux
// variant, else -1. Shared by images (SIZE) and history (layer list) so both
// read the same variant.
func preferredVariantIdx(platforms []dockerfmt.Platform) int {
	fallback := -1
	for i, p := range platforms {
		if p.OS != "linux" {
			continue
		}
		if p.Architecture == goruntime.GOARCH {
			return i
		}
		if fallback < 0 {
			fallback = i
		}
	}
	return fallback
}

func imageSizeBytes(img dockerfmt.Image) int64 {
	// Prefer the host-platform linux variant (closest to `docker images` SIZE),
	// else the first linux variant. Reflects summed compressed blob sizes — the
	// only size the backend exposes — so it won't exactly equal docker's value.
	plats := make([]dockerfmt.Platform, len(img.Variants))
	for i, v := range img.Variants {
		plats[i] = v.Platform
	}
	if i := preferredVariantIdx(plats); i >= 0 {
		return img.Variants[i].Size
	}
	return 0
}

func buildImageView(img dockerfmt.Image, noTrunc bool) imageView {
	name := dockerfmt.ShortImage(img.Configuration.Name)
	repo, tag := dockerfmt.SplitRepoTag(name)
	// A digest-pinned reference (repo@sha256:…) has no tag; docker prints
	// <none>, not the raw digest, in the TAG column. SplitRepoTag keeps
	// returning the digest for ancestor matching.
	if strings.Contains(name, "@") {
		tag = "<none>"
	}
	id := img.ID
	// DIGEST shows the full sha256:<hex> like `docker images --digests`.
	digest := img.Configuration.Descriptor.Digest
	if !noTrunc {
		id = dockerfmt.ShortID(id)
	}
	plat := ""
	if len(img.Variants) > 0 {
		p := img.Variants[0].Platform
		plat = p.OS + "/" + p.Architecture
		if p.Variant != "" {
			plat += "/" + p.Variant
		}
	}
	// Docker's .CreatedAt is a formatted local time, not raw RFC3339 (same
	// rendering ps uses); the parsed value is kept for time-based filters.
	createdAt := img.Configuration.CreationDate
	var created time.Time
	if t, ok := dockerfmt.ParseTime(createdAt); ok {
		created = t
		createdAt = t.Format("2006-01-02 15:04:05 -0700 MST")
	}
	return imageView{
		Repository:   repo,
		Tag:          tag,
		ID:           id,
		Digest:       digest,
		CreatedSince: dockerfmt.RelativeAgo(img.Configuration.CreationDate),
		CreatedAt:    createdAt,
		// docker images SIZE uses 3 significant figures (HumanSizeWithPrecision),
		// not the default 4 — e.g. "13.3kB", not "13.26kB".
		Size:     dockerfmt.HumanSizeWithPrecision(float64(imageSizeBytes(img)), 3),
		Platform: plat,
		created:  created,
	}
}

func getImages() ([]dockerfmt.Image, error) {
	var list []dockerfmt.Image
	if err := runtime.CaptureJSON(&list, "image", "list", "--format", "json"); err != nil {
		return nil, err
	}
	return list, nil
}

func newImagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "images [OPTIONS] [REPOSITORY[:TAG]]",
		Short: "List images",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runImages,
	}
	addImagesFlags(cmd)
	return cmd
}

func addImagesFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.BoolP("all", "a", false, "Show all images (default hides intermediate images)")
	f.BoolP("quiet", "q", false, "Only show image IDs")
	f.Bool("no-trunc", false, "Don't truncate output")
	f.String("format", "", "Format output using a Go template or 'json'")
	// StringArray, not StringSlice: a label-value filter may contain commas.
	f.StringArrayP("filter", "f", nil, "Filter output based on conditions provided")
	f.Bool("digests", false, "Show digests")
	f.Bool("tree", false, "List multi-platform images as a tree (unsupported; falls back to flat list)")
}

func runImages(cmd *cobra.Command, args []string) error {
	quiet, _ := cmd.Flags().GetBool("quiet")
	noTrunc, _ := cmd.Flags().GetBool("no-trunc")
	format, _ := cmd.Flags().GetString("format")
	digests, _ := cmd.Flags().GetBool("digests")
	filters, _ := cmd.Flags().GetStringArray("filter")
	if cmd.Flags().Changed("tree") {
		fmt.Fprintln(os.Stderr, "dcon: warning: --tree is not supported by the backend; showing a flat list")
	}
	if err := validateImageFilters(filters); err != nil {
		return err
	}

	list, err := getImages()
	if err != nil {
		return err
	}

	// positional REPO[:TAG|@DIGEST] filter
	var repoFilter, tagFilter, digestFilter string
	if len(args) == 1 {
		repoFilter, tagFilter, digestFilter = imageRefFilter(args[0])
	}

	beforeT, sinceT, err := resolveImageTimeFilters(list, filters)
	if err != nil {
		return err
	}

	views := make([]any, 0, len(list))
	for _, img := range list {
		v := buildImageView(img, noTrunc)
		if repoFilter != "" && !refPatternMatch(repoFilter, v.Repository) {
			continue
		}
		if tagFilter != "" && !refPatternMatch(tagFilter, v.Tag) {
			continue
		}
		if digestFilter != "" && !strings.HasPrefix(v.Digest, digestFilter) {
			continue
		}
		if !matchImageFilters(v, filters, beforeT, sinceT) {
			continue
		}
		views = append(views, v)
	}
	// Sort newest first, matching `docker images` (daemon returns created-time
	// descending; the CLI does no alphabetical re-sort). Mirrors `dcon ps`.
	sort.SliceStable(views, func(i, j int) bool {
		return views[i].(imageView).created.After(views[j].(imageView).created)
	})

	headers := []string{"REPOSITORY", "TAG", "IMAGE ID", "CREATED", "SIZE"}
	rowFn := func(v any) []string {
		im := v.(imageView)
		return []string{im.Repository, im.Tag, im.ID, im.CreatedSince, im.Size}
	}
	if digests {
		headers = []string{"REPOSITORY", "TAG", "DIGEST", "IMAGE ID", "CREATED", "SIZE"}
		rowFn = func(v any) []string {
			im := v.(imageView)
			return []string{im.Repository, im.Tag, im.Digest, im.ID, im.CreatedSince, im.Size}
		}
	}
	def := dockerfmt.TableDef{
		Headers:      headers,
		Row:          rowFn,
		ID:           func(v any) string { return v.(imageView).ID },
		FieldHeaders: map[string]string{".ID": "IMAGE ID"},
	}
	return dockerfmt.Render(format, quiet, views, def)
}

// imageRefFilter splits a positional `images REPO[:TAG|@DIGEST]` argument into
// repo, tag, and digest filters. A tag filter is set ONLY when the ref has an
// explicit tag — a colon after the last slash. A registry-port colon like
// registry:5000/img is not a tag, so it must not pin tag=latest and hide every
// other tag of that repo. A digest ref filters by the digest column, never by
// the human tag (which is never a digest), which otherwise yields an empty list.
func imageRefFilter(ref string) (repo, tag, digest string) {
	// Normalize like buildImageView (which stores Repository via ShortImage), so a
	// fully-qualified docker.io/library/alpine matches the stored "alpine".
	ref = dockerfmt.ShortImage(ref)
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		return ref[:i], "", ref[i+1:]
	}
	repo, t := dockerfmt.SplitRepoTag(ref)
	if j := strings.LastIndex(ref, ":"); j >= 0 && !strings.Contains(ref[j:], "/") {
		tag = t // a real tag separator (after the last slash) was present
	}
	return repo, tag, ""
}

// validateImageFilters rejects malformed reference= glob patterns up front
// (mirroring docker, which errors on a bad filter) so a pattern like
// "ngin[x" surfaces an error instead of silently matching nothing and
// returning an empty image list.
func validateImageFilters(filters []string) error {
	for _, fl := range filters {
		kv := strings.SplitN(fl, "=", 2)
		if len(kv) == 2 && kv[0] == "reference" {
			if _, err := filepath.Match(kv[1], ""); err != nil {
				return fmt.Errorf("invalid filter 'reference=%s': %v", kv[1], err)
			}
		}
	}
	return nil
}

// refPatternMatch implements docker's positional `images REPO[:TAG]`
// semantics: an exact familiar-name match, or — when the pattern carries
// wildcards — a path.Match, whose `*` never crosses `/`, keeping the match
// path-component aware (so `alpine` or `alpine*` cannot match
// ghcr.io/foo/alpine the way the old HasSuffix clause did).
func refPatternMatch(pattern, s string) bool {
	if strings.ContainsAny(pattern, "*?[") {
		ok, err := path.Match(pattern, s)
		return err == nil && ok
	}
	return s == pattern
}

// resolveImageTimeFilters resolves --filter before=/since= references to the
// named image's creation time (zero when the filter is absent). Docker errors
// when the reference image does not exist; so do we. With repeated values the
// last one wins, matching the daemon's WalkValues behavior.
func resolveImageTimeFilters(list []dockerfmt.Image, filters []string) (before, since time.Time, err error) {
	for _, fl := range filters {
		kv := strings.SplitN(fl, "=", 2)
		if len(kv) != 2 || (kv[0] != "before" && kv[0] != "since") {
			continue
		}
		img, ok := findImage(list, kv[1])
		if !ok {
			return before, since, fmt.Errorf("no such image: %s", kv[1])
		}
		t, ok := dockerfmt.ParseTime(img.Configuration.CreationDate)
		if !ok {
			return before, since, fmt.Errorf("invalid filter '%s=%s': image has no creation time", kv[0], kv[1])
		}
		if kv[0] == "before" {
			before = t
		} else {
			since = t
		}
	}
	return before, since, nil
}

// findImage locates an image by familiar name (repo or repo:tag) or by ID
// (full, or any unambiguous sha prefix).
func findImage(list []dockerfmt.Image, ref string) (dockerfmt.Image, bool) {
	want := dockerfmt.ShortImage(ref)
	for _, img := range list {
		name := dockerfmt.ShortImage(img.Configuration.Name)
		repo, _ := dockerfmt.SplitRepoTag(name)
		if want == name || want == repo {
			return img, true
		}
		id := strings.TrimPrefix(img.ID, "sha256:")
		if ref == img.ID || (ref != "" && strings.HasPrefix(id, strings.TrimPrefix(ref, "sha256:"))) {
			return img, true
		}
	}
	return dockerfmt.Image{}, false
}

func matchImageFilters(v imageView, filters []string, beforeT, sinceT time.Time) bool {
	var refs []string
	for _, fl := range filters {
		kv := strings.SplitN(fl, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "reference":
			refs = append(refs, kv[1])
		case "dangling":
			// All listed images are tagged here, so dangling=true matches none.
			if kv[1] == "true" {
				return false
			}
		case "before":
			if v.created.IsZero() || !v.created.Before(beforeT) {
				return false
			}
		case "since":
			if v.created.IsZero() || !v.created.After(sinceT) {
				return false
			}
		default:
			fmt.Fprintf(os.Stderr, "dcon: warning: image filter %q is not supported and was ignored\n", kv[0])
		}
	}
	// Multiple reference= patterns are OR-combined (union), matching docker —
	// keep the image if it matches ANY pattern (against the tagged form or the
	// bare repo, since docker treats reference=nginx as matching any tag).
	if len(refs) > 0 {
		full := v.Repository + ":" + v.Tag
		matched := false
		for _, pat := range refs {
			m1, _ := filepath.Match(pat, full)
			m2, _ := filepath.Match(pat, v.Repository)
			if m1 || m2 {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func newPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull [OPTIONS] NAME[:TAG|@DIGEST]",
		Short: "Download an image from a registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"image", "pull"}
			if p, _ := cmd.Flags().GetString("platform"); p != "" {
				cargs = append(cargs, "--platform", p)
			}
			if a, _ := cmd.Flags().GetString("arch"); a != "" {
				cargs = append(cargs, "--arch", a)
			}
			if o, _ := cmd.Flags().GetString("os"); o != "" {
				cargs = append(cargs, "--os", o)
			}
			if q, _ := cmd.Flags().GetBool("quiet"); q {
				cargs = append(cargs, "--progress", "none")
			}
			if s, _ := cmd.Flags().GetString("scheme"); s != "" {
				cargs = append(cargs, "--scheme", s)
			}
			// Layer-download concurrency: the backend defaults to 3, which leaves
			// throughput on the table for multi-layer images. dcon defaults to 8
			// (the empirical knee — gains past it are marginal) and honors an
			// explicit value or the DCON_PULL_CONCURRENCY env override.
			cargs = append(cargs, "--max-concurrent-downloads", strconv.Itoa(pullConcurrency(cmd)))
			if cmd.Flags().Changed("all-tags") {
				fmt.Fprintln(os.Stderr, "dcon: warning: -a/--all-tags is not supported by the backend and was ignored")
			}
			cargs = append(cargs, args[0])
			if err := runtime.Run(cargs...); err != nil {
				return err
			}
			// The ref may now point at a different image: retire any warm pool
			// members booted from the old one so `run --rm` can't exec into a
			// stale VM. (Teardown is detached; this is off the hot path.)
			pool.InvalidateImage(args[0])
			return nil
		},
	}
	cmd.Flags().String("platform", "", "Set platform if server is multi-platform capable")
	cmd.Flags().String("arch", "", "Target architecture (backend extra)")
	cmd.Flags().String("os", "", "Target OS (backend extra)")
	cmd.Flags().String("scheme", "", "Registry scheme: http, https, or auto")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress verbose output")
	cmd.Flags().BoolP("all-tags", "a", false, "Download all tagged images (unsupported)")
	cmd.Flags().Int("max-concurrent-downloads", 0, "Max concurrent layer downloads (0 = dcon default of 8)")
	cmd.Flags().Bool("disable-content-trust", true, "Skip image verification (no-op; backend has no content trust)")
	return cmd
}

// pullConcurrency resolves the layer-download concurrency for a pull: an
// explicit --max-concurrent-downloads wins, then DCON_PULL_CONCURRENCY, then
// dcon's default of 8. The result is clamped to a sane 1..32.
func pullConcurrency(cmd *cobra.Command) int {
	n := 8
	if v := os.Getenv("DCON_PULL_CONCURRENCY"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}
	if cmd.Flags().Changed("max-concurrent-downloads") {
		if v, _ := cmd.Flags().GetInt("max-concurrent-downloads"); v > 0 {
			n = v
		}
	}
	if n < 1 {
		n = 1
	}
	if n > 32 {
		n = 32
	}
	return n
}

// repoTagRefs returns the stored references of every local image whose repo
// matches repo (docker `push -a` semantics: every tag of that repo). Digest
// pins are skipped — they are not tags. Pure, so `push --all-tags` target
// selection is unit-testable without a backend.
func repoTagRefs(list []dockerfmt.Image, repo string) []string {
	want := dockerfmt.ShortImage(repo)
	var refs []string
	for _, img := range list {
		name := img.Configuration.Name
		short := dockerfmt.ShortImage(name)
		if strings.Contains(short, "@") {
			continue // digest pin, not a tag
		}
		r, _ := dockerfmt.SplitRepoTag(short)
		if r == want {
			refs = append(refs, name)
		}
	}
	return refs
}

func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push [OPTIONS] NAME[:TAG]",
		Short: "Upload an image to a registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"image", "push"}
			if p, _ := cmd.Flags().GetString("platform"); p != "" {
				cargs = append(cargs, "--platform", p)
			}
			if a, _ := cmd.Flags().GetString("arch"); a != "" {
				cargs = append(cargs, "--arch", a)
			}
			if o, _ := cmd.Flags().GetString("os"); o != "" {
				cargs = append(cargs, "--os", o)
			}
			if s, _ := cmd.Flags().GetString("scheme"); s != "" {
				cargs = append(cargs, "--scheme", s)
			}
			if q, _ := cmd.Flags().GetBool("quiet"); q {
				cargs = append(cargs, "--progress", "none")
			}
			if all, _ := cmd.Flags().GetBool("all-tags"); all {
				// docker rejects an explicit tag/digest with -a (repo only). A
				// colon in the last path segment is a tag; earlier colons are
				// registry ports.
				if i := strings.LastIndex(args[0], ":"); strings.Contains(args[0], "@") ||
					(i >= 0 && !strings.Contains(args[0][i:], "/")) {
					return fmt.Errorf("tag can't be used with --all-tags/-a")
				}
				imgs, err := getImages()
				if err != nil {
					return err
				}
				refs := repoTagRefs(imgs, args[0])
				if len(refs) == 0 {
					return fmt.Errorf("An image does not exist locally with the tag: %s", args[0])
				}
				var firstErr error
				for _, ref := range refs {
					if err := runtime.Run(append(append([]string{}, cargs...), ref)...); err != nil && firstErr == nil {
						firstErr = err
					}
				}
				return firstErr
			}
			cargs = append(cargs, args[0])
			return runtime.Run(cargs...)
		},
	}
	cmd.Flags().String("platform", "", "Push a platform-specific manifest")
	cmd.Flags().String("arch", "", "Push a platform-specific manifest by architecture")
	cmd.Flags().String("os", "", "Push a platform-specific manifest by OS")
	cmd.Flags().String("scheme", "", "Registry scheme: http, https, or auto")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress verbose output")
	cmd.Flags().BoolP("all-tags", "a", false, "Push all tags of an image to the repository")
	cmd.Flags().Bool("disable-content-trust", true, "Skip image signing (no-op; backend has no content trust)")
	return cmd
}

func newRmiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rmi [OPTIONS] IMAGE [IMAGE...]",
		Short: "Remove one or more images",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all && len(args) > 0 {
				return fmt.Errorf("--all cannot be combined with image arguments")
			}
			if !all && len(args) == 0 {
				return fmt.Errorf("requires at least 1 image argument, or --all")
			}
			cargs := []string{"image", "delete"}
			if f, _ := cmd.Flags().GetBool("force"); f {
				cargs = append(cargs, "--force")
			}
			if all {
				cargs = append(cargs, "--all")
			} else {
				cargs = append(cargs, args...)
			}
			if err := runtime.Run(cargs...); err != nil {
				return err
			}
			// Deleted refs must not keep serving warm VMs booted from them.
			if all {
				pool.InvalidateImage("")
			} else {
				for _, ref := range args {
					pool.InvalidateImage(ref)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Force removal of the image")
	cmd.Flags().BoolP("all", "a", false, "Delete all images (backend extra)")
	cmd.Flags().Bool("no-prune", false, "Do not delete untagged parents (no-op)")
	return cmd
}

func newTagCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tag SOURCE_IMAGE[:TAG] TARGET_IMAGE[:TAG]",
		Short: "Create a tag TARGET_IMAGE that refers to SOURCE_IMAGE",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runtime.Run("image", "tag", args[0], args[1]); err != nil {
				return err
			}
			// TARGET now points at SOURCE's image: warm members booted from the
			// old TARGET are stale.
			pool.InvalidateImage(args[1])
			return nil
		},
	}
}

func newImageLoadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "load [OPTIONS]",
		Short: "Load an image from a tar archive or STDIN",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"image", "load"}
			if i, _ := cmd.Flags().GetString("input"); i != "" {
				cargs = append(cargs, "--input", i)
			}
			if f, _ := cmd.Flags().GetBool("force"); f {
				cargs = append(cargs, "--force")
			}
			return runtime.Run(cargs...)
		},
	}
	cmd.Flags().StringP("input", "i", "", "Read from tar archive file, instead of STDIN")
	cmd.Flags().BoolP("force", "f", false, "Load even if the archive contains invalid files")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress the load output (no-op)")
	_ = cmd.Flags().MarkHidden("quiet")
	return cmd
}

func newImageSaveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "save [OPTIONS] IMAGE [IMAGE...]",
		Short: "Save one or more images to a tar archive (streamed to STDOUT by default)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"image", "save"}
			if o, _ := cmd.Flags().GetString("output"); o != "" {
				cargs = append(cargs, "--output", o)
			}
			if p, _ := cmd.Flags().GetString("platform"); p != "" {
				cargs = append(cargs, "--platform", p)
			}
			cargs = append(cargs, args...)
			return runtime.Run(cargs...)
		},
	}
	cmd.Flags().StringP("output", "o", "", "Write to a file, instead of STDOUT")
	cmd.Flags().String("platform", "", "Save a platform-specific image")
	return cmd
}

func newImagePruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove unused images",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"image", "prune"}
			if a, _ := cmd.Flags().GetBool("all"); a {
				cargs = append(cargs, "--all")
			}
			return runtime.Run(cargs...)
		},
	}
	cmd.Flags().BoolP("all", "a", false, "Remove all unused images, not just dangling ones")
	cmd.Flags().BoolP("force", "f", false, "Do not prompt for confirmation (no-op)")
	cmd.Flags().StringSlice("filter", nil, "Provide filter values (no-op)")
	return cmd
}

func newImageInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect [OPTIONS] IMAGE [IMAGE...]",
		Short: "Display detailed information on one or more images",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, _ := cmd.Flags().GetString("format")
			raw, _, err := inspectRaw("image", args)
			if err != nil {
				return err
			}
			return renderInspect(raw, format)
		},
	}
	cmd.Flags().StringP("format", "f", "", "Format output using a Go template or 'json'")
	return cmd
}

// image management group: `dcon image <subcommand>`
func newImageGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:     "image",
		Short:   "Manage images",
		Aliases: []string{"i"},
	}
	ls := newImagesCmd()
	ls.Use = "ls [OPTIONS] [REPOSITORY[:TAG]]"
	ls.Aliases = []string{"list"}
	rm := newRmiCmd()
	rm.Use = "rm [OPTIONS] IMAGE [IMAGE...]"
	rm.Aliases = []string{"remove", "delete"}
	group.AddCommand(ls, rm, newPullCmd(), newPushCmd(), newTagCmd(),
		newImageInspectCmd(), newImageLoadCmd(), newImageSaveCmd(), newImagePruneCmd(), newHistoryCmd(),
		// Mirrors the top-level `import` stub so `docker image import` probes get
		// the same specific message instead of cobra's "unknown command".
		stub("import [OPTIONS] file|URL|- [REPOSITORY[:TAG]]",
			"Import the contents from a tarball to create a filesystem image",
			"import is not supported: the backend loads OCI archives, not raw filesystem tarballs — use `dcon load` for an OCI archive, or build from a Dockerfile"))
	return group
}
