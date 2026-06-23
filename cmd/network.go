package cmd

import (
	"fmt"
	"os"
	"strings"

	"dcon/internal/dockerfmt"
	"dcon/internal/runtime"

	"github.com/spf13/cobra"
)

type networkView struct {
	ID     string
	Name   string
	Driver string
	Scope  string
	Subnet string
}

func driverForMode(mode string) string {
	switch mode {
	case "nat":
		return "bridge"
	case "hostOnly":
		return "host"
	default:
		return mode
	}
}

// matchNetworkFilters implements docker network ls --filter (name/id/driver/scope/label).
func matchNetworkFilters(n dockerfmt.Network, filters []string) bool {
	for _, fl := range filters {
		kv := strings.SplitN(fl, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "name":
			if !strings.Contains(n.Configuration.Name, kv[1]) {
				return false
			}
		case "id":
			if !strings.HasPrefix(n.ID, kv[1]) {
				return false
			}
		case "driver":
			if !strings.EqualFold(driverForMode(n.Configuration.Mode), kv[1]) {
				return false
			}
		case "scope":
			if !strings.EqualFold("local", kv[1]) {
				return false
			}
		case "label":
			lkv := strings.SplitN(kv[1], "=", 2)
			got, ok := n.Configuration.Labels[lkv[0]]
			if !ok || (len(lkv) == 2 && got != lkv[1]) {
				return false
			}
		}
	}
	return true
}

func newNetworkGroupCmd() *cobra.Command {
	group := &cobra.Command{
		Use:     "network",
		Short:   "Manage networks",
		Aliases: []string{"n"},
	}

	create := &cobra.Command{
		Use:   "create [OPTIONS] NETWORK",
		Short: "Create a network",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cargs := []string{"network", "create"}
			if v, _ := cmd.Flags().GetBool("internal"); v {
				cargs = append(cargs, "--internal")
			}
			for _, l := range mustStringArray(cmd.Flags(), "label") {
				cargs = append(cargs, "--label", l)
			}
			for _, o := range mustStringArray(cmd.Flags(), "opt") {
				cargs = append(cargs, "--option", o)
			}
			if s, _ := cmd.Flags().GetString("subnet"); s != "" {
				cargs = append(cargs, "--subnet", s)
			}
			if p, _ := cmd.Flags().GetString("plugin"); p != "" {
				cargs = append(cargs, "--plugin", p)
			}
			for _, name := range []string{"gateway", "ip-range", "aux-address"} {
				if cmd.Flags().Changed(name) {
					fmt.Fprintf(os.Stderr, "dcon: warning: --%s is not supported by the backend and was ignored\n", name)
				}
			}
			if d, _ := cmd.Flags().GetString("driver"); d != "" && d != "bridge" && d != "nat" {
				fmt.Fprintf(os.Stderr, "dcon: warning: driver %q is not supported by the backend and was ignored\n", d)
			}
			cargs = append(cargs, args[0])
			if _, err := runtime.CaptureSilent(cargs...); err != nil {
				return err
			}
			fmt.Println(args[0])
			return nil
		},
	}
	create.Flags().StringP("driver", "d", "bridge", "Driver to manage the network")
	create.Flags().Bool("internal", false, "Restrict external access to the network")
	create.Flags().StringArrayP("label", "l", nil, "Set metadata on a network")
	create.Flags().StringArrayP("opt", "o", nil, "Set driver specific options")
	create.Flags().String("subnet", "", "Subnet in CIDR format")
	create.Flags().String("plugin", "", "Network plugin (backend extra)")
	create.Flags().String("gateway", "", "IPv4 or IPv6 Gateway (unsupported)")
	create.Flags().String("ip-range", "", "Allocate IPs from a sub-range (unsupported)")
	create.Flags().String("aux-address", "", "Auxiliary IPv4/IPv6 addresses (unsupported)")
	create.Flags().Bool("ipv6", false, "Enable IPv6 networking")

	ls := &cobra.Command{
		Use:     "ls [OPTIONS]",
		Aliases: []string{"list"},
		Short:   "List networks",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			quiet, _ := cmd.Flags().GetBool("quiet")
			noTrunc, _ := cmd.Flags().GetBool("no-trunc")
			format, _ := cmd.Flags().GetString("format")
			filters, _ := cmd.Flags().GetStringSlice("filter")
			var list []dockerfmt.Network
			if err := runtime.CaptureJSON(&list, "network", "list", "--format", "json"); err != nil {
				return err
			}
			views := make([]any, 0, len(list))
			for _, n := range list {
				id := n.ID
				if !noTrunc {
					id = dockerfmt.ShortID(id)
				}
				subnet := n.Status.IPv4Subnet
				if subnet == "" {
					subnet = n.Configuration.IPv4Subnet
				}
				if !matchNetworkFilters(n, filters) {
					continue
				}
				views = append(views, networkView{
					ID:     id,
					Name:   n.Configuration.Name,
					Driver: driverForMode(n.Configuration.Mode),
					Scope:  "local",
					Subnet: subnet,
				})
			}
			def := dockerfmt.TableDef{
				Headers: []string{"NETWORK ID", "NAME", "DRIVER", "SCOPE"},
				Row: func(v any) []string {
					nv := v.(networkView)
					return []string{nv.ID, nv.Name, nv.Driver, nv.Scope}
				},
				ID: func(v any) string { return v.(networkView).ID },
			}
			return dockerfmt.Render(format, quiet, views, def)
		},
	}
	ls.Flags().BoolP("quiet", "q", false, "Only display network IDs")
	ls.Flags().Bool("no-trunc", false, "Do not truncate the output")
	ls.Flags().String("format", "", "Format output using a Go template or 'json'")
	ls.Flags().StringSliceP("filter", "f", nil, "Provide filter values")

	rm := &cobra.Command{
		Use:     "rm NETWORK [NETWORK...]",
		Aliases: []string{"remove"},
		Short:   "Remove one or more networks",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runtime.Run(append([]string{"network", "delete"}, args...)...)
		},
	}

	inspect := &cobra.Command{
		Use:   "inspect [OPTIONS] NETWORK [NETWORK...]",
		Short: "Display detailed information on one or more networks",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runtime.Run(append([]string{"network", "inspect"}, args...)...)
		},
	}

	prune := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove all unused networks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runtime.Run("network", "prune")
		},
	}
	prune.Flags().BoolP("force", "f", false, "Do not prompt for confirmation (no-op)")

	connect := &cobra.Command{
		Use:   "connect NETWORK CONTAINER",
		Short: "Connect a container to a network",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("network connect is not supported: attach networks at creation with `dcon run --network`")
		},
	}
	disconnect := &cobra.Command{
		Use:   "disconnect NETWORK CONTAINER",
		Short: "Disconnect a container from a network",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("network disconnect is not supported by the backend")
		},
	}

	group.AddCommand(create, ls, rm, inspect, prune, connect, disconnect)
	return group
}
