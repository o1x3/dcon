package cmd

import (
	"strings"
	"testing"
)

func TestParseSystemStatus(t *testing.T) {
	out := `FIELD              VALUE
status             running
appRoot            /Users/x/Library/Application Support/com.apple.container/
installRoot        /usr/local/
logRoot            `
	m := parseSystemStatus(out)
	if m["status"] != "running" {
		t.Errorf("status = %q, want running", m["status"])
	}
	// Value with spaces is preserved.
	if !strings.HasPrefix(m["approot"], "/Users/x/Library/Application Support") {
		t.Errorf("appRoot = %q, want full path with spaces", m["approot"])
	}
	// A key with no value (logRoot) is skipped (single field).
	if _, ok := m["logroot"]; ok {
		t.Errorf("logRoot with no value should be skipped, got %q", m["logroot"])
	}
	if got := parseSystemStatus(""); len(got) != 0 {
		t.Errorf("empty input -> %v, want empty map", got)
	}
}

func TestRenderChecks(t *testing.T) {
	checks := []check{
		{name: "CLI", level: levelOK, detail: "v1.0"},
		{name: "Backend", level: levelFail, detail: "not running", hint: "dcon system start"},
		{name: "Kernel", level: levelWarn, detail: "missing", hint: "set a kernel"},
	}
	out, anyFail := renderChecks(checks)
	if !anyFail {
		t.Error("anyFail = false, want true (a check failed)")
	}
	for _, want := range []string{"✓", "✗", "!", "v1.0", "not running", "dcon system start", "set a kernel"} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q\n%s", want, out)
		}
	}
	// OK checks must not print a hint line.
	if strings.Contains(out, "v1.0\n      ↳") {
		t.Error("OK check should not render a hint")
	}

	// All-OK report does not fail.
	_, anyFail2 := renderChecks([]check{{name: "x", level: levelOK, detail: "ok"}})
	if anyFail2 {
		t.Error("all-OK renderChecks reported a failure")
	}
}

func TestCheckLevelSymbol(t *testing.T) {
	if levelOK.symbol() != "✓" || levelWarn.symbol() != "!" || levelFail.symbol() != "✗" {
		t.Errorf("symbols: ok=%q warn=%q fail=%q", levelOK.symbol(), levelWarn.symbol(), levelFail.symbol())
	}
}
