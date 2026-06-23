package cmd

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"dcon/internal/pool"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// warmAllowed is the set of `run` flags (including inherited persistent flags)
// that can be faithfully reproduced by `container exec` into a pre-booted VM.
// If a run sets ANY flag outside this set, it is not warm-eligible and takes
// the normal cold path — this allow-list is conservative on purpose so new
// flags never silently change semantics on the fast path.
var warmAllowed = map[string]bool{
	// required / ephemeral
	"rm": true,
	// process options exec supports directly
	"interactive": true, "tty": true,
	"env": true, "env-file": true, "workdir": true,
	"user": true, "uid": true, "gid": true, "ulimit": true,
	// no-ops on the warm path (image already resident / cosmetic)
	"pull": true, "detach-keys": true,
	// global persistent flags with no effect on execution
	"debug": true, "host": true, "context": true, "log-level": true, "config": true,
}

// warmEligible reports whether this run can be served from the warm pool: it
// needs an explicit command, --rm semantics (we destroy the VM after), no
// detach, and only flags that exec can honor.
func warmEligible(cmd *cobra.Command, args []string) bool {
	if len(args) < 2 { // need IMAGE + at least one command token
		return false
	}
	f := cmd.Flags()
	if rm, _ := f.GetBool("rm"); !rm {
		return false
	}
	if d, _ := f.GetBool("detach"); d {
		return false
	}
	eligible := true
	f.Visit(func(fl *pflag.Flag) {
		if !warmAllowed[fl.Name] {
			eligible = false
		}
	})
	return eligible
}

// warmExecArgs renders the `container exec ...` argument list for a warm run,
// translating the exec-compatible run flags onto the claimed member.
func warmExecArgs(cmd *cobra.Command, id string, command []string) []string {
	f := cmd.Flags()
	out := []string{"exec"}
	if v, _ := f.GetBool("interactive"); v {
		out = append(out, "--interactive")
	}
	if v, _ := f.GetBool("tty"); v {
		out = append(out, "--tty")
	}
	if v, _ := f.GetString("workdir"); v != "" {
		out = append(out, "--workdir", v)
	}
	if v, _ := f.GetString("user"); v != "" {
		out = append(out, "--user", v)
	}
	if v, _ := f.GetString("uid"); v != "" {
		out = append(out, "--uid", v)
	}
	if v, _ := f.GetString("gid"); v != "" {
		out = append(out, "--gid", v)
	}
	for _, e := range mustStringArray(f, "env") {
		out = append(out, "--env", e)
	}
	for _, e := range mustStringArray(f, "env-file") {
		out = append(out, "--env-file", e)
	}
	for _, l := range mustStringArray(f, "ulimit") {
		out = append(out, "--ulimit", l)
	}
	out = append(out, id)
	return append(out, command...)
}

// tryWarmRun attempts to serve a `run` from the warm pool. It returns
// handled=false when the run should fall through to the normal cold path
// (ineligible, pooling disabled, empty pool, or a claimed VM turned out dead);
// handled=true means the workload ran (err carries its exit status).
func tryWarmRun(cmd *cobra.Command, args []string) (handled bool, err error) {
	if pool.Disabled() || !warmEligible(cmd, args) {
		// Even when not eligible to *use* the pool, auto mode may pre-warm so a
		// later eligible run is fast.
		maybeAutoPrime(args)
		return false, nil
	}
	image := args[0]
	command := args[1:]

	m, ok := pool.Claim(image)
	if !ok {
		// Pool miss: run cold, but prime for next time if auto is on.
		maybeAutoPrime(args)
		return false, nil
	}

	runErr := runtime.Run(warmExecArgs(cmd, m.ID, command)...)
	if runErr != nil && !pool.IsRunning(m.ID) {
		// The member was gone before our command could run — fall back to a
		// genuine cold run so the user still gets their result. (A command that
		// deliberately halts its own VM is the rare exception; such runs may be
		// retried cold.)
		pool.DestroyAsync(m.ID)
		pool.Replenish(image) // keep depth for next time
		return false, nil
	}

	// Workload ran (runErr, if any, is its real exit status). Retire the
	// single-use VM off the critical path and top the pool back up.
	pool.DestroyAsync(m.ID)
	pool.Replenish(image)
	return true, runErr
}

// maybeAutoPrime spawns a detached background warm-up when DCON_WARM=auto and
// the run at least targets an image with a command (so priming is useful).
func maybeAutoPrime(args []string) {
	if !pool.AutoEnabled() || len(args) < 1 {
		return
	}
	pool.Replenish(args[0])
}

// --- `dcon warm` command group ------------------------------------------------

func newWarmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "warm [OPTIONS] IMAGE",
		Short: "Pre-boot single-use microVMs so `run` starts in ~70 ms instead of cold-booting",
		Long: `Pre-boot one or more single-use microVMs for an image and keep them idle.

A later 'dcon run --rm IMAGE COMMAND' that needs no bind mounts, ports, or
resource limits is served by exec-ing into a pre-booted VM (~70 ms) instead of
cold-booting a fresh one (~700 ms). Each warm VM is handed out exactly once and
then destroyed, so isolation is identical to a normal run — the boot cost is
just paid here, ahead of time.

Each idle warm VM costs roughly ~35 MB of host RAM until claimed or pruned.

  dcon warm alpine               # pre-boot 1 warm alpine VM
  dcon warm -n 3 python:3.12     # keep 3 warm python VMs ready
  dcon warm ls                   # show the pool
  dcon warm prune                # tear the whole pool down

Set DCON_WARM=auto to have dcon self-prime the pool after eligible runs.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			image := args[0]
			n, _ := cmd.Flags().GetInt("number")
			if n < 1 {
				n = 1
			}
			quiet, _ := cmd.Flags().GetBool("quiet")

			// Ensure the image is resident first so each boot is a pure VM start,
			// not a pull. Best effort — if it's missing the boot will pull anyway.
			if !quiet {
				fmt.Fprintf(os.Stderr, "pre-warming %d microVM(s) for %s …\n", n, image)
			}
			booted := 0
			for i := 0; i < n; i++ {
				m, err := pool.Boot(image)
				if err != nil {
					if !quiet {
						fmt.Fprintf(os.Stderr, "dcon: warm boot failed: %v\n", err)
					}
					if booted == 0 {
						return err
					}
					break
				}
				booted++
				if !quiet {
					fmt.Fprintf(os.Stderr, "  warmed %s (%s)\n", short(m.ID), m.Image)
				}
			}
			if !quiet {
				fmt.Fprintf(os.Stderr, "%d warm VM(s) ready for %s\n", booted, image)
			}
			return nil
		},
	}
	cmd.Flags().IntP("number", "n", 1, "Number of warm VMs to pre-boot")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress progress output (used by background priming)")

	cmd.AddCommand(newWarmLsCmd(), newWarmPruneCmd())
	return cmd
}

func newWarmLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list", "ps"},
		Short:   "List warm pool members",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			members := pool.List()
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "CONTAINER ID\tIMAGE\tAGE\tSTATE")
			now := time.Now().Unix()
			for _, m := range members {
				state := "ready"
				if !pool.IsRunning(m.ID) {
					state = "dead"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", short(m.ID), m.Image, age(now-m.BootedAt), state)
			}
			if len(members) == 0 {
				fmt.Fprintln(tw, "(pool empty)\t\t\t")
			}
			return tw.Flush()
		},
	}
}

func newWarmPruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune [IMAGE]",
		Short: "Tear down warm pool VMs (all, or for one image)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image := ""
			if len(args) == 1 {
				image = args[0]
			}
			n, err := pool.PruneOrphans(image)
			if err != nil {
				return err
			}
			fmt.Printf("Removed %d warm VM(s)\n", n)
			return nil
		},
	}
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func age(secs int64) string {
	switch {
	case secs < 0:
		return "0s"
	case secs < 60:
		return strconv.FormatInt(secs, 10) + "s"
	case secs < 3600:
		return strconv.FormatInt(secs/60, 10) + "m"
	default:
		return strconv.FormatInt(secs/3600, 10) + "h"
	}
}
