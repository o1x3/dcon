//go:build !darwin

package cmd

// dcon's backend (Apple container) is macOS-only; these stubs exist so the
// package still compiles on other platforms (CI runs the unit tests on Linux).
func hostMemTotal() int64       { return 0 }
func hostKernelVersion() string { return "" }
