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

func driverForMode(mode string) string { return dockerfmt.NetworkDriver(mode) }

// matchNetworkFilters implements docker network ls --filter (name/id/driver/scope/label).
// Repeated values of the same key are OR-combined and distinct keys AND-combined,
// matching Docker (and dcon's ps/volume filters); labels AND.
func matchNetworkFilters(n dockerfmt.Network, filters []string) bool {
	byKey := map[string][]string{}
	var labels []string
	for _, fl := range filters {
		kv := strings.SplitN(fl, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if kv[0] == "label" {
			labels = append(labels, kv[1])
			continue
		}
		byKey[kv[0]] = append(byKey[kv[0]], kv[1])
	}
	match := func(key, val string) bool {
		switch key {
		case "name":
			return strings.Contains(n.Configuration.Name, val)
		case "id":
			return strings.HasPrefix(n.ID, val)
		case "driver":
			return strings.EqualFold(driverForMode(n.Configuration.Mode), val)
		case "scope":
			return strings.EqualFold("local", val)
		}
		return true // unknown key: ignored
	}
	for key, vals := range byKey {
		matched := false
		for _, val := range vals {
			if match(key, val) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, lv := range labels {
		lkv := strings.SplitN(lv, "=", 2)
		got, ok := n.Configuration.Labels[lkv[0]]
		if !ok || (len(lkv) == 2 && got != lkv[1]) {
			return false
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
			// Docker's --subnet is repeatable (dual-stack: one IPv4 + one IPv6
			// pool, or several IPv4 pools); the backend splits them into --subnet /
			// --subnet-v6. Route each value by family. A scalar flag would keep only
			// the last value and silently drop the rest.
			subnets := mustStringArray(cmd.Flags(), "subnet")
			hasV6 := false
			for _, s := range subnets {
				if strings.Contains(s, ":") {
					cargs = append(cargs, "--subnet-v6", s)
					hasV6 = true
				} else {
					cargs = append(cargs, "--subnet", s)
				}
			}
			if v6, _ := cmd.Flags().GetBool("ipv6"); v6 && !hasV6 {
				fmt.Fprintln(os.Stderr, "dcon: warning: --ipv6 has no effect without an IPv6 --subnet (e.g. --subnet 2001:db8::/64)")
			}
			if p, _ := cmd.Flags().GetString("plugin"); p != "" {
				cargs = append(cargs, "--plugin", p)
			}
			for _, name := range []string{"gateway", "ip-range", "aux-address", "attachable", "scope", "ingress", "config-only", "config-from"} {
				if cmd.Flags().Changed(name) {
					fmt.Fprintf(os.Stderr, "dcon: warning: --%s is not supported by the backend and was ignored\n", name)
				}
			}
			if d, _ := cmd.Flags().GetString("driver"); d != "" && d != "bridge" && d != "nat" {
				fmt.Fprintf(os.Stderr, "dcon: warning: driver %q is not supported by the backend and was ignored\n", d)
			}
			cargs = append(cargs, args[0])
			out, err := runtime.CaptureSilent(cargs...)
			if err != nil {
				return err
			}
			// docker `network create` prints the new network's ID. Echo the
			// backend's output (the id) when it is a single bare token; otherwise
			// fall back to the name so we never print a backend status line.
			printed := strings.TrimSpace(out)
			if printed == "" || strings.ContainsAny(printed, " \t\n") {
				printed = args[0]
			}
			fmt.Println(printed)
			return nil
		},
	}
	create.Flags().StringP("driver", "d", "bridge", "Driver to manage the network")
	create.Flags().Bool("internal", false, "Restrict external access to the network")
	create.Flags().StringArrayP("label", "l", nil, "Set metadata on a network")
	create.Flags().StringArrayP("opt", "o", nil, "Set driver specific options")
	create.Flags().StringArray("subnet", nil, "Subnet in CIDR format (repeatable for dual-stack)")
	create.Flags().String("plugin", "", "Network plugin (backend extra)")
	create.Flags().String("gateway", "", "IPv4 or IPv6 Gateway (unsupported)")
	create.Flags().String("ip-range", "", "Allocate IPs from a sub-range (unsupported)")
	create.Flags().String("aux-address", "", "Auxiliary IPv4/IPv6 addresses (unsupported)")
	create.Flags().Bool("ipv6", false, "Enable IPv6 networking (provide an IPv6 --subnet)")
	create.Flags().Bool("attachable", false, "Enable manual container attachment (unsupported)")
	create.Flags().String("scope", "", "Control the network's scope (unsupported)")
	create.Flags().Bool("ingress", false, "Create swarm routing-mesh network (unsupported)")
	create.Flags().Bool("config-only", false, "Create a configuration only network (unsupported)")
	create.Flags().String("config-from", "", "The network from which to copy the configuration (unsupported)")

	ls := &cobra.Command{
		Use:     "ls [OPTIONS]",
		Aliases: []string{"list"},
		Short:   "List networks",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			quiet, _ := cmd.Flags().GetBool("quiet")
			noTrunc, _ := cmd.Flags().GetBool("no-trunc")
			format, _ := cmd.Flags().GetString("format")
			filters, _ := cmd.Flags().GetStringArray("filter")
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
				ID:           func(v any) string { return v.(networkView).ID },
				FieldHeaders: map[string]string{".ID": "NETWORK ID"},
			}
			return dockerfmt.Render(format, quiet, views, def)
		},
	}
	ls.Flags().BoolP("quiet", "q", false, "Only display network IDs")
	ls.Flags().Bool("no-trunc", false, "Do not truncate the output")
	ls.Flags().String("format", "", "Format output using a Go template or 'json'")
	// StringArray, not StringSlice: a label-value filter may contain commas.
	ls.Flags().StringArrayP("filter", "f", nil, "Provide filter values")

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
			if cmd.Flags().Changed("verbose") {
				fmt.Fprintln(os.Stderr, "dcon: warning: --verbose is not supported by the backend and was ignored")
			}
			format, _ := cmd.Flags().GetString("format")
			if format == "" {
				return runtime.Run(append([]string{"network", "inspect"}, args...)...)
			}
			raw, err := runtime.CaptureSilent(append([]string{"network", "inspect"}, args...)...)
			if err != nil {
				return err
			}
			// Templates execute against docker-shaped views ({{.Name}}, {{.Id}},
			// {{.Driver}}, {{.IPAM.Config}}, …), like `docker network inspect -f`.
			return renderInspectTyped("network", raw, format)
		},
	}
	inspect.Flags().StringP("format", "f", "", "Format output using a Go template or 'json'")
	inspect.Flags().BoolP("verbose", "v", false, "Verbose output for diagnostics (unsupported)")

	prune := &cobra.Command{
		Use:   "prune [OPTIONS]",
		Short: "Remove all unused networks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("filter") {
				fmt.Fprintln(os.Stderr, "dcon: warning: --filter is not supported by the backend and was ignored")
			}
			return runtime.Run("network", "prune")
		},
	}
	prune.Flags().BoolP("force", "f", false, "Do not prompt for confirmation (no-op)")
	prune.Flags().String("filter", "", "Provide filter values (unsupported)")

	connect := &cobra.Command{
		Use:   "connect [OPTIONS] NETWORK CONTAINER",
		Short: "Connect a container to a network",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("network connect is not supported: attach networks at creation with `dcon run --network`")
		},
	}
	connect.Flags().String("ip", "", "IPv4 address (unsupported)")
	connect.Flags().String("ip6", "", "IPv6 address (unsupported)")
	connect.Flags().StringSlice("alias", nil, "Add network-scoped alias for the container (unsupported)")
	connect.Flags().StringSlice("link", nil, "Add link to another container (unsupported)")
	disconnect := &cobra.Command{
		Use:   "disconnect [OPTIONS] NETWORK CONTAINER",
		Short: "Disconnect a container from a network",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("network disconnect is not supported by the backend")
		},
	}
	disconnect.Flags().BoolP("force", "f", false, "Force the container to disconnect from a network (unsupported)")

	group.AddCommand(create, ls, rm, inspect, prune, connect, disconnect)
	return group
}
