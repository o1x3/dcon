package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"

	"dcon/internal/dockerfmt"
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
}

func imageSizeBytes(img dockerfmt.Image) int64 {
	// Prefer the host-platform linux variant (closest to `docker images` SIZE),
	// else the first linux variant. Reflects summed compressed blob sizes — the
	// only size the backend exposes — so it won't exactly equal docker's value.
	var fallback int64
	for _, v := range img.Variants {
		if v.Platform.OS != "linux" {
			continue
		}
		if v.Platform.Architecture == goruntime.GOARCH {
			return v.Size
		}
		if fallback == 0 {
			fallback = v.Size
		}
	}
	return fallback
}

func buildImageView(img dockerfmt.Image, noTrunc bool) imageView {
	repo, tag := dockerfmt.SplitRepoTag(dockerfmt.ShortImage(img.Configuration.Name))
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
	return imageView{
		Repository:   repo,
		Tag:          tag,
		ID:           id,
		Digest:       digest,
		CreatedSince: dockerfmt.RelativeAgo(img.Configuration.CreationDate),
		CreatedAt:    img.Configuration.CreationDate,
		Size:         dockerfmt.HumanSize(float64(imageSizeBytes(img))),
		Platform:     plat,
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
	f.StringSliceP("filter", "f", nil, "Filter output based on conditions provided")
	f.Bool("digests", false, "Show digests")
	f.Bool("tree", false, "List multi-platform images as a tree (unsupported; falls back to flat list)")
}

func runImages(cmd *cobra.Command, args []string) error {
	quiet, _ := cmd.Flags().GetBool("quiet")
	noTrunc, _ := cmd.Flags().GetBool("no-trunc")
	format, _ := cmd.Flags().GetString("format")
	digests, _ := cmd.Flags().GetBool("digests")
	filters, _ := cmd.Flags().GetStringSlice("filter")
	if cmd.Flags().Changed("tree") {
		fmt.Fprintln(os.Stderr, "dcon: warning: --tree is not supported by the backend; showing a flat list")
	}

	list, err := getImages()
	if err != nil {
		return err
	}

	// positional REPO[:TAG] filter
	var repoFilter, tagFilter string
	if len(args) == 1 {
		repoFilter, tagFilter = dockerfmt.SplitRepoTag(args[0])
		if !strings.Contains(args[0], ":") {
			tagFilter = ""
		}
	}

	views := make([]any, 0, len(list))
	for _, img := range list {
		v := buildImageView(img, noTrunc)
		if repoFilter != "" && v.Repository != repoFilter && !strings.HasSuffix(v.Repository, "/"+repoFilter) {
			continue
		}
		if tagFilter != "" && v.Tag != tagFilter {
			continue
		}
		if !matchImageFilters(v, filters) {
			continue
		}
		views = append(views, v)
	}
	sort.SliceStable(views, func(i, j int) bool {
		return views[i].(imageView).Repository < views[j].(imageView).Repository
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
		Headers: headers,
		Row:     rowFn,
		ID:      func(v any) string { return v.(imageView).ID },
	}
	return dockerfmt.Render(format, quiet, views, def)
}

func matchImageFilters(v imageView, filters []string) bool {
	for _, fl := range filters {
		kv := strings.SplitN(fl, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "reference":
			// glob match against both the tagged form and the bare repo
			// (docker treats reference=nginx as matching any tag).
			full := v.Repository + ":" + v.Tag
			m1, _ := filepath.Match(kv[1], full)
			m2, _ := filepath.Match(kv[1], v.Repository)
			if !m1 && !m2 {
				return false
			}
		case "dangling":
			// All listed images are tagged here, so dangling=true matches none.
			if kv[1] == "true" {
				return false
			}
		default:
			fmt.Fprintf(os.Stderr, "dcon: warning: image filter %q is not supported and was ignored\n", kv[0])
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
			return runtime.Run(cargs...)
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
			cargs = append(cargs, args[0])
			return runtime.Run(cargs...)
		},
	}
	cmd.Flags().String("platform", "", "Push a platform-specific manifest")
	cmd.Flags().String("arch", "", "Push a platform-specific manifest by architecture")
	cmd.Flags().String("os", "", "Push a platform-specific manifest by OS")
	cmd.Flags().String("scheme", "", "Registry scheme: http, https, or auto")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress verbose output")
	cmd.Flags().BoolP("all-tags", "a", false, "")
	_ = cmd.Flags().MarkHidden("all-tags")
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
			return runtime.Run(cargs...)
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
			return runtime.Run("image", "tag", args[0], args[1])
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
			raw, err := inspectRaw("image", args)
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
		newImageInspectCmd(), newImageLoadCmd(), newImageSaveCmd(), newImagePruneCmd(), newHistoryCmd())
	return group
}
