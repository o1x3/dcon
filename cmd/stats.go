package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"dcon/internal/dockerfmt"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

type statsView struct {
	Container string
	Name      string
	ID        string
	CPUPerc   string
	MemUsage  string
	MemPerc   string
	NetIO     string
	BlockIO   string
	PIDs      string
}

func sampleStats(ids []string) ([]dockerfmt.Stats, error) {
	args := []string{"stats", "--no-stream", "--format", "json"}
	args = append(args, ids...)
	var s []dockerfmt.Stats
	if err := runtime.CaptureJSON(&s, args...); err != nil {
		return nil, err
	}
	return s, nil
}

func statID(id string, noTrunc bool) string {
	if noTrunc {
		return id
	}
	return dockerfmt.ShortID(id)
}

func renderStats(cur, prev []dockerfmt.Stats, dt float64, format string, noTrunc bool, extraIDs []string) error {
	prevByID := map[string]dockerfmt.Stats{}
	for _, p := range prev {
		prevByID[p.ID] = p
	}
	views := make([]any, 0, len(cur)+len(extraIDs))
	for _, s := range cur {
		cpu := "--"
		if p, ok := prevByID[s.ID]; ok && dt > 0 && s.CPUUsageUsec >= p.CPUUsageUsec {
			pct := float64(s.CPUUsageUsec-p.CPUUsageUsec) / (dt * 1e6) * 100
			cpu = fmt.Sprintf("%.2f%%", pct)
		}
		memPerc := "--"
		if s.MemoryLimitBytes > 0 {
			memPerc = fmt.Sprintf("%.2f%%", float64(s.MemoryUsageBytes)/float64(s.MemoryLimitBytes)*100)
		}
		views = append(views, statsView{
			Container: statID(s.ID, noTrunc),
			Name:      s.ID,
			ID:        statID(s.ID, noTrunc),
			CPUPerc:   cpu,
			MemUsage:  fmt.Sprintf("%s / %s", dockerfmt.HumanSizeBytes(s.MemoryUsageBytes), dockerfmt.HumanSizeBytes(s.MemoryLimitBytes)),
			MemPerc:   memPerc,
			NetIO:     fmt.Sprintf("%s / %s", dockerfmt.HumanSizeBytes(s.NetworkRxBytes), dockerfmt.HumanSizeBytes(s.NetworkTxBytes)),
			BlockIO:   fmt.Sprintf("%s / %s", dockerfmt.HumanSizeBytes(s.BlockReadBytes), dockerfmt.HumanSizeBytes(s.BlockWriteBytes)),
			PIDs:      fmt.Sprint(s.NumProcesses),
		})
	}
	// --all: non-running containers appear as placeholder "--" rows.
	for _, id := range extraIDs {
		views = append(views, statsView{
			Container: statID(id, noTrunc), Name: id, ID: statID(id, noTrunc),
			CPUPerc: "--", MemUsage: "-- / --", MemPerc: "--", NetIO: "-- / --", BlockIO: "-- / --", PIDs: "--",
		})
	}
	def := dockerfmt.TableDef{
		Headers: []string{"CONTAINER ID", "NAME", "CPU %", "MEM USAGE / LIMIT", "MEM %", "NET I/O", "BLOCK I/O", "PIDS"},
		Row: func(v any) []string {
			s := v.(statsView)
			return []string{s.ID, s.Name, s.CPUPerc, s.MemUsage, s.MemPerc, s.NetIO, s.BlockIO, s.PIDs}
		},
		ID: func(v any) string { return v.(statsView).ID },
	}
	return dockerfmt.Render(format, false, views, def)
}

func newStatsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats [OPTIONS] [CONTAINER...]",
		Short: "Display a live stream of container(s) resource usage statistics",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			noStream, _ := cmd.Flags().GetBool("no-stream")
			noTrunc, _ := cmd.Flags().GetBool("no-trunc")
			all, _ := cmd.Flags().GetBool("all")
			format, _ := cmd.Flags().GetString("format")

			// extraIDs: with --all and no explicit ids, also show non-running
			// containers as placeholder rows.
			extraIDs := func(cur []dockerfmt.Stats) []string {
				if !all || len(args) > 0 {
					return nil
				}
				inSample := map[string]bool{}
				for _, s := range cur {
					inSample[s.ID] = true
				}
				cs, _ := getContainers(true)
				var ids []string
				for _, c := range cs {
					if !inSample[c.ID] {
						ids = append(ids, c.ID)
					}
				}
				return ids
			}

			streaming := !noStream
			tableMode := format == "" || strings.HasPrefix(format, "table")
			prevT := time.Now()
			prev, err := sampleStats(args)
			if err != nil {
				return err
			}
			interval := 1.5
			for {
				time.Sleep(time.Duration(interval * float64(time.Second)))
				cur, err := sampleStats(args)
				if err != nil {
					return err
				}
				dt := time.Since(prevT).Seconds()
				// Clear the screen only for a live table on a real terminal — never
				// when stdout is a pipe/file, so piped output stays free of raw ANSI.
				if streaming && tableMode && isTerminal(os.Stdout) {
					fmt.Print("\033[H\033[2J")
				}
				if err := renderStats(cur, prev, dt, format, noTrunc, extraIDs(cur)); err != nil {
					return err
				}
				if noStream {
					return nil
				}
				prev = cur
				prevT = time.Now()
			}
		},
	}
	cmd.Flags().Bool("no-stream", false, "Disable streaming stats and only pull the first result")
	cmd.Flags().String("format", "", "Format output using a Go template or 'json'")
	cmd.Flags().BoolP("all", "a", false, "Show all containers (default shows just running)")
	cmd.Flags().Bool("no-trunc", false, "Do not truncate output")
	return cmd
}
