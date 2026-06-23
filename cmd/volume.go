package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"dcon/internal/dockerfmt"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

func randomName() string {
	// 32 bytes -> 64 hex chars, matching Docker's anonymous-volume id width.
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// matchVolumeFilters implements docker volume ls --filter (name/driver/label).
func matchVolumeFilters(v dockerfmt.Volume, driver string, filters []string) bool {
	for _, fl := range filters {
		kv := strings.SplitN(fl, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "name":
			if !strings.Contains(v.Configuration.Name, kv[1]) {
				return false
			}
		case "driver":
			if !strings.EqualFold(driver, kv[1]) {
				return false
			}
		case "label":
			lkv := strings.SplitN(kv[1], "=", 2)
			got, ok := v.Configuration.Labels[lkv[0]]
			if !ok || (len(lkv) == 2 && got != lkv[1]) {
				return false
			}
		}
	}
	return true
}

type volumeView struct {
	Name       string
	Driver     string
	Scope      string
	Mountpoint string
	Labels     string
}

func newVolumeGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:     "volume",
		Short:   "Manage volumes",
		Aliases: []string{"v"},
	}

	create := &cobra.Command{
		Use:   "create [OPTIONS] [VOLUME]",
		Short: "Create a volume",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := randomName()
			if len(args) == 1 {
				name = args[0]
			}
			cargs := []string{"volume", "create"}
			for _, l := range mustStringArray(cmd.Flags(), "label") {
				cargs = append(cargs, "--label", l)
			}
			for _, o := range mustStringArray(cmd.Flags(), "opt") {
				cargs = append(cargs, "--opt", o)
			}
			if s, _ := cmd.Flags().GetString("size"); s != "" {
				cargs = append(cargs, "--size", s)
			}
			if d, _ := cmd.Flags().GetString("driver"); d != "" && d != "local" {
				fmt.Fprintf(os.Stderr, "dcon: warning: driver %q ignored; backend volumes use the 'local' driver\n", d)
			}
			cargs = append(cargs, name)
			if _, err := runtime.CaptureSilent(cargs...); err != nil {
				return err
			}
			fmt.Println(name)
			return nil
		},
	}
	create.Flags().StringArrayP("label", "l", nil, "Set metadata for a volume")
	create.Flags().StringArrayP("opt", "o", nil, "Set driver specific options")
	create.Flags().StringP("driver", "d", "local", "Specify volume driver name")
	create.Flags().StringP("size", "s", "", "Volume size, e.g. 512M, 10G (backend extra)")
	create.Flags().String("name", "", "")
	_ = create.Flags().MarkHidden("name")

	ls := &cobra.Command{
		Use:     "ls [OPTIONS]",
		Aliases: []string{"list"},
		Short:   "List volumes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			quiet, _ := cmd.Flags().GetBool("quiet")
			format, _ := cmd.Flags().GetString("format")
			filters, _ := cmd.Flags().GetStringSlice("filter")
			var list []dockerfmt.Volume
			if err := runtime.CaptureJSON(&list, "volume", "list", "--format", "json"); err != nil {
				return err
			}
			views := make([]any, 0, len(list))
			for _, v := range list {
				drv := v.Configuration.Driver
				if drv == "" {
					drv = "local"
				}
				if !matchVolumeFilters(v, drv, filters) {
					continue
				}
				views = append(views, volumeView{
					Name:       v.Configuration.Name,
					Driver:     drv,
					Scope:      "local",
					Mountpoint: v.Configuration.Source,
					Labels:     labelsString(v.Configuration.Labels),
				})
			}
			def := dockerfmt.TableDef{
				Headers: []string{"DRIVER", "VOLUME NAME"},
				Row: func(v any) []string {
					vv := v.(volumeView)
					return []string{vv.Driver, vv.Name}
				},
				ID: func(v any) string { return v.(volumeView).Name },
			}
			return dockerfmt.Render(format, quiet, views, def)
		},
	}
	ls.Flags().BoolP("quiet", "q", false, "Only display volume names")
	ls.Flags().String("format", "", "Format output using a Go template or 'json'")
	ls.Flags().StringSliceP("filter", "f", nil, "Provide filter values")

	rm := &cobra.Command{
		Use:     "rm [OPTIONS] VOLUME [VOLUME...]",
		Aliases: []string{"remove"},
		Short:   "Remove one or more volumes",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runtime.Run(append([]string{"volume", "delete"}, args...)...)
		},
	}
	rm.Flags().BoolP("force", "f", false, "Force the removal of one or more volumes (no-op)")

	inspect := &cobra.Command{
		Use:   "inspect [OPTIONS] VOLUME [VOLUME...]",
		Short: "Display detailed information on one or more volumes",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runtime.Run(append([]string{"volume", "inspect"}, args...)...)
		},
	}

	prune := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove all unused local volumes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runtime.Run("volume", "prune")
		},
	}
	prune.Flags().BoolP("force", "f", false, "Do not prompt for confirmation (no-op)")
	prune.Flags().BoolP("all", "a", false, "Remove all unused volumes (no-op)")

	group.AddCommand(create, ls, rm, inspect, prune)
	return group
}
