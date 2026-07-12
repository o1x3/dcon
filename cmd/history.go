package cmd

import (
	"encoding/json"
	"strings"

	"dcon/internal/dockerfmt"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

// formatCreatedBy renders a layer's created_by for the CREATED BY column
// exactly like docker: tabs become spaces (a raw tab would corrupt the
// tabwriter row), then the value is ellipsis-truncated to 45 display columns
// (44 + "…") unless noTrunc. Docker does NOT strip the /bin/sh -c wrappers.
func formatCreatedBy(raw string, noTrunc bool) string {
	cb := strings.ReplaceAll(raw, "\t", " ")
	if !noTrunc {
		cb = dockerfmt.Ellipsis(cb, 45)
	}
	return cb
}

// ociHistory mirrors the OCI image-config history entries that container
// embeds under variants[].config.history.
type ociHistory struct {
	Created    string `json:"created"`
	CreatedBy  string `json:"created_by"`
	Comment    string `json:"comment"`
	EmptyLayer bool   `json:"empty_layer"`
}

type inspectImageRaw struct {
	ID            string `json:"id"`
	Configuration struct {
		Descriptor dockerfmt.Descriptor `json:"descriptor"`
	} `json:"configuration"`
	Variants []struct {
		Platform dockerfmt.Platform `json:"platform"`
		Config   struct {
			History []ociHistory `json:"history"`
		} `json:"config"`
	} `json:"variants"`
}

// history returns the layer history of the preferred variant — the same
// linux/GOARCH-first selection imageSizeBytes uses — instead of blindly
// reading Variants[0], which on a multi-platform image could describe a
// foreign architecture.
func (img inspectImageRaw) history() []ociHistory {
	if len(img.Variants) == 0 {
		return nil
	}
	plats := make([]dockerfmt.Platform, len(img.Variants))
	for i, v := range img.Variants {
		plats[i] = v.Platform
	}
	vi := preferredVariantIdx(plats)
	if vi < 0 {
		vi = 0
	}
	return img.Variants[vi].Config.History
}

type historyView struct {
	ID           string
	CreatedSince string
	CreatedBy    string
	Size         string
	Comment      string
}

// buildHistoryViews renders the layer list newest-first, the way docker does:
// the newest row carries the image's ID (12 chars, or full with --no-trunc)
// and every other layer shows <missing> — which also makes `history -q`
// return a usable image id.
func buildHistoryViews(imageID string, hist []ociHistory, noTrunc bool) []any {
	views := make([]any, 0, len(hist))
	for i := len(hist) - 1; i >= 0; i-- {
		h := hist[i]
		id := "<missing>"
		if i == len(hist)-1 && imageID != "" {
			if noTrunc {
				id = imageID
			} else {
				id = dockerfmt.ShortID(imageID)
			}
		}
		views = append(views, historyView{
			ID:           id,
			CreatedSince: dockerfmt.RelativeAgo(h.Created),
			CreatedBy:    formatCreatedBy(h.CreatedBy, noTrunc),
			// Per-layer size is not present in the OCI config and is
			// unrecoverable from the backend; use a non-numeric sentinel.
			Size:    "unknown",
			Comment: h.Comment,
		})
	}
	return views
}

func newHistoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history [OPTIONS] IMAGE",
		Short: "Show the history of an image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			noTrunc, _ := cmd.Flags().GetBool("no-trunc")
			quiet, _ := cmd.Flags().GetBool("quiet")
			format, _ := cmd.Flags().GetString("format")

			out, err := runtime.CaptureSilent("image", "inspect", args[0])
			if err != nil {
				return err
			}
			var imgs []inspectImageRaw
			if err := json.Unmarshal([]byte(out), &imgs); err != nil {
				return err
			}
			var hist []ociHistory
			imageID := ""
			if len(imgs) > 0 {
				imageID = imgs[0].ID
				if imageID == "" { // older inspect payloads: fall back to the config digest
					imageID = imgs[0].Configuration.Descriptor.Digest
				}
				hist = imgs[0].history()
			}

			views := buildHistoryViews(imageID, hist, noTrunc)
			def := dockerfmt.TableDef{
				Headers: []string{"IMAGE", "CREATED", "CREATED BY", "SIZE", "COMMENT"},
				Row: func(v any) []string {
					h := v.(historyView)
					return []string{h.ID, h.CreatedSince, h.CreatedBy, h.Size, h.Comment}
				},
				ID:           func(v any) string { return v.(historyView).ID },
				FieldHeaders: map[string]string{".ID": "IMAGE"},
			}
			return dockerfmt.Render(format, quiet, views, def)
		},
	}
	cmd.Flags().Bool("no-trunc", false, "Don't truncate output")
	cmd.Flags().BoolP("quiet", "q", false, "Only show image IDs")
	cmd.Flags().String("format", "", "Format output using a Go template")
	// long-only: -H is taken by the root --host persistent flag
	cmd.Flags().Bool("human", true, "Print sizes and dates in human readable format")
	_ = cmd.Flags().MarkHidden("human")
	return cmd
}
