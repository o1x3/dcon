package cmd

import (
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// TestTopPassesDashedPsOptions reproduces the bug where `top web -ef` aborted
// with "unknown shorthand flag: 'e'" because cobra parsed the ps options as
// flags of the top command. With SetInterspersed(false) they pass through.
func TestTopPassesDashedPsOptions(t *testing.T) {
	cmd := newTopCmd()
	var got []string
	cmd.RunE = func(c *cobra.Command, args []string) error { got = args; return nil }
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"web", "-ef"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("`top web -ef` must not error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"web", "-ef"}) {
		t.Errorf("args reaching RunE = %v, want [web -ef]", got)
	}
}

// TestFormatCreatedByRuneSafe reproduces the byte-truncation bug: slicing
// created_by at byte 42 could split a multibyte UTF-8 rune, emitting invalid
// UTF-8. Truncation must be rune-safe.
func TestFormatCreatedByRuneSafe(t *testing.T) {
	// Leading ASCII + 3-byte runes so byte 42 lands mid-rune.
	in := "x" + strings.Repeat("あ", 50)
	out := formatCreatedBy(in, false)
	if !utf8.ValidString(out) {
		t.Errorf("rune-unsafe truncation produced invalid UTF-8: %q", out)
	}
	if r := []rune(out); len(r) != 45 || string(r[len(r)-3:]) != "..." {
		t.Errorf("want 42 runes + '...'; got %d runes (%q)", len(r), out)
	}
	if got := formatCreatedBy("/bin/sh -c #(nop) CMD [\"x\"]", false); got != `CMD ["x"]` {
		t.Errorf("prefix strip wrong: %q", got)
	}
	if got := formatCreatedBy("/bin/sh -c apk add curl", false); got != "apk add curl" {
		t.Errorf("shell prefix strip wrong: %q", got)
	}
}

// TestCpIsContainerRef reproduces the bug where a local path containing a colon
// (./my:file.txt) was misclassified as a CONTAINER:PATH reference.
func TestCpIsContainerRef(t *testing.T) {
	for _, p := range []string{"./my:file.txt", "../x:y", "/abs:path", "/abs/path", "plainfile", ":leading"} {
		if cpIsContainerRef(p) {
			t.Errorf("%q should be treated as a local path", p)
		}
	}
	for _, p := range []string{"web:/tmp", "mycontainer:/var/log", "abc123:/x"} {
		if !cpIsContainerRef(p) {
			t.Errorf("%q should be treated as a container ref", p)
		}
	}
}

// TestSystemPrunePlan covers the prune step plan that the error-propagating
// loop runs (the bug fixed alongside it was that every step's error was
// discarded and the command always exited 0).
func TestSystemPrunePlan(t *testing.T) {
	base := systemPrunePlan(false, false)
	if len(base) != 3 {
		t.Fatalf("base plan has %d steps, want 3", len(base))
	}
	if reflect.DeepEqual(base[1].args, []string{"image", "prune", "--all"}) {
		t.Error("default prune must not pass --all to image prune")
	}
	full := systemPrunePlan(true, true)
	if len(full) != 4 {
		t.Fatalf("all+volumes plan has %d steps, want 4", len(full))
	}
	if !reflect.DeepEqual(full[1].args, []string{"image", "prune", "--all"}) {
		t.Errorf("--all should add --all to image prune; got %v", full[1].args)
	}
	if !reflect.DeepEqual(full[3].args, []string{"volume", "prune"}) {
		t.Errorf("--volumes should append a volume prune; got %v", full[3].args)
	}
}
