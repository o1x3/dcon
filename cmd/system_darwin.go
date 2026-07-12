package cmd

import "golang.org/x/sys/unix"

// hostMemTotal returns the host's physical memory in bytes (docker info's
// MemTotal), best effort: 0 when the sysctl fails.
func hostMemTotal() int64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(n)
}

// hostKernelVersion reports the host (Darwin) kernel release, best effort.
// The Linux guest kernel is per-VM and not cheaply queryable from the backend,
// so the host value — prefixed to make its origin unambiguous — stands in for
// docker's KernelVersion.
func hostKernelVersion() string {
	rel, err := unix.Sysctl("kern.osrelease")
	if err != nil {
		return ""
	}
	return "Darwin " + rel
}
