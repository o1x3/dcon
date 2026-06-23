package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"dcon/internal/dockerfmt"
)

func captureOut(t *testing.T, f func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	f()
	w.Close()
	os.Stdout = orig
	return <-done
}

func TestRenderStatsComputesCPUAndMem(t *testing.T) {
	prev := []dockerfmt.Stats{{ID: "c1", CPUUsageUsec: 0}}
	cur := []dockerfmt.Stats{{
		ID: "c1", CPUUsageUsec: 1_000_000, // 1s of CPU over 1s dt = 100%
		MemoryUsageBytes: 50_000_000, MemoryLimitBytes: 100_000_000,
		NetworkRxBytes: 1000, NetworkTxBytes: 2000, NumProcesses: 7,
	}}
	out := captureOut(t, func() {
		if err := renderStats(cur, prev, 1.0, "", false, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "100.00%") {
		t.Errorf("expected CPU 100.00%%, got:\n%s", out)
	}
	if !strings.Contains(out, "50.00%") {
		t.Errorf("expected MEM 50.00%%, got:\n%s", out)
	}
	if !strings.Contains(out, "CONTAINER ID") {
		t.Errorf("missing header:\n%s", out)
	}
}

func TestRenderStatsPlaceholderRows(t *testing.T) {
	out := captureOut(t, func() {
		_ = renderStats(nil, nil, 1.0, "", false, []string{"stopped1"})
	})
	if !strings.Contains(out, "stopped1") || !strings.Contains(out, "--") {
		t.Errorf("placeholder row missing:\n%s", out)
	}
}

func TestStatIDTrunc(t *testing.T) {
	if statID("0123456789abcdef", false) != "0123456789ab" {
		t.Error("should truncate to 12")
	}
	if statID("0123456789abcdef", true) != "0123456789abcdef" {
		t.Error("no-trunc keeps full")
	}
}
