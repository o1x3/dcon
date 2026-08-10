package buildssh

import (
	"strings"
	"testing"
)

func TestIsDefaultAgent(t *testing.T) {
	yes := []string{"default", "id=default", " default ", "id=default,", ",default"}
	for _, s := range yes {
		if !IsDefaultAgent(s) {
			t.Errorf("IsDefaultAgent(%q) = false, want true", s)
		}
	}
	no := []string{
		"", " ", "myagent", "id=myagent",
		"default=/home/me/.ssh/id_rsa",
		"id=default,src=/tmp/key",
		"id=default,src=/tmp/key,mode=0600",
		"src=/tmp/key",
	}
	for _, s := range no {
		if IsDefaultAgent(s) {
			t.Errorf("IsDefaultAgent(%q) = true, want false", s)
		}
	}
}

func TestTranslate(t *testing.T) {
	enable, warns := Translate([]string{"default"})
	if !enable || len(warns) != 0 {
		t.Fatalf("default: enable=%v warns=%v", enable, warns)
	}

	enable, warns = Translate([]string{"id=default", "default"})
	if !enable || len(warns) != 0 {
		t.Fatalf("dup default: enable=%v warns=%v", enable, warns)
	}

	enable, warns = Translate([]string{"id=foo", "default=/tmp/k"})
	if enable {
		t.Fatal("non-default specs must not enable")
	}
	if len(warns) != 2 {
		t.Fatalf("want 2 warnings, got %v", warns)
	}
	for _, w := range warns {
		if !strings.Contains(w, "not supported") {
			t.Errorf("warning text: %q", w)
		}
	}

	enable, warns = Translate([]string{"id=foo", "default"})
	if !enable {
		t.Fatal("mixed: default should still enable")
	}
	if len(warns) != 1 {
		t.Fatalf("want 1 warning for id=foo, got %v", warns)
	}

	enable, warns = Translate(nil)
	if enable || len(warns) != 0 {
		t.Fatalf("nil: enable=%v warns=%v", enable, warns)
	}
	enable, warns = Translate([]string{"", "  "})
	if enable || len(warns) != 0 {
		t.Fatalf("blank: enable=%v warns=%v", enable, warns)
	}
}
