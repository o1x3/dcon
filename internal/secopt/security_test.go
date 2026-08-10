package secopt

import (
	"strings"
	"testing"
)

func TestTranslateSystempathsUnconfined(t *testing.T) {
	r := Translate([]string{"systempaths=unconfined"})
	if len(r.MaskedPaths) != 1 || r.MaskedPaths[0] != "NONE" {
		t.Fatalf("MaskedPaths = %v, want [NONE]", r.MaskedPaths)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", r.Warnings)
	}
}

func TestTranslateCaseInsensitive(t *testing.T) {
	r := Translate([]string{"SystemPaths=Unconfined"})
	if len(r.MaskedPaths) != 1 || r.MaskedPaths[0] != "NONE" {
		t.Fatalf("MaskedPaths = %v", r.MaskedPaths)
	}
}

func TestTranslateMixed(t *testing.T) {
	r := Translate([]string{
		"seccomp=unconfined",
		"systempaths=unconfined",
		"apparmor=unconfined",
		"no-new-privileges",
	})
	if len(r.MaskedPaths) != 1 || r.MaskedPaths[0] != "NONE" {
		t.Fatalf("MaskedPaths = %v", r.MaskedPaths)
	}
	if len(r.Warnings) != 3 {
		t.Fatalf("want 3 warnings, got %v", r.Warnings)
	}
	for _, w := range r.Warnings {
		if !strings.Contains(w, "not supported") {
			t.Errorf("warning: %q", w)
		}
		if strings.Contains(strings.ToLower(w), "systempaths") {
			t.Errorf("systempaths should not be warned: %q", w)
		}
	}
}

func TestTranslateEmpty(t *testing.T) {
	r := Translate(nil)
	if len(r.MaskedPaths) != 0 || len(r.Warnings) != 0 {
		t.Fatalf("empty: %+v", r)
	}
	r = Translate([]string{"", "  "})
	if len(r.MaskedPaths) != 0 || len(r.Warnings) != 0 {
		t.Fatalf("blank: %+v", r)
	}
}
