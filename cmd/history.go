package cmd

import (
	"encoding/json"
	"strings"

	"dcon/internal/dockerfmt"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

// ociHistory mirrors the OCI image-config history entries that container
// embeds under variants[].config.history.
type ociHistory struct {
	Created    string `json:"created"`
	CreatedBy  string `json:"created_by"`
	Comment    string `json:"comment"`
	EmptyLayer bool   `json:"empty_layer"`
}

type inspectImageRaw struct {
	Variants []struct {
		Config struct {
			History []ociHistory `json:"history"`
		} `json:"config"`
	} `json:"variants"`
}

type historyView struct {
	ID           string
	CreatedSince string
	CreatedBy    string
	Size         string
	Comment      string
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
			if len(imgs) > 0 && len(imgs[0].Variants) > 0 {
				hist = imgs[0].Variants[0].Config.History
			}

			views := make([]any, 0, len(hist))
			// Docker lists newest layer first.
			for i := len(hist) - 1; i >= 0; i-- {
				h := hist[i]
				cb := strings.TrimPrefix(h.CreatedBy, "/bin/sh -c #(nop) ")
				cb = strings.TrimSpace(strings.TrimPrefix(cb, "/bin/sh -c"))
				if !noTrunc && len(cb) > 45 {
					cb = cb[:42] + "..."
				}
				views = append(views, historyView{
					ID:           "<missing>",
					CreatedSince: dockerfmt.RelativeAgo(h.Created),
					CreatedBy:    cb,
					// Per-layer size is not present in the OCI config and is
					// unrecoverable from the backend; use a non-numeric sentinel.
					Size:    "unknown",
					Comment: h.Comment,
				})
			}
			def := dockerfmt.TableDef{
				Headers: []string{"IMAGE", "CREATED", "CREATED BY", "SIZE", "COMMENT"},
				Row: func(v any) []string {
					h := v.(historyView)
					return []string{h.ID, h.CreatedSince, h.CreatedBy, h.Size, h.Comment}
				},
				ID: func(v any) string { return v.(historyView).ID },
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
