package machine

import "testing"

// redirectState points the state dir at temp dirs so the test never touches the
// real ~/Library/Application Support/dcon (HOME for darwin's UserConfigDir,
// XDG_CONFIG_HOME for Linux's).
func redirectState(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestDefaultLifecycle(t *testing.T) {
	redirectState(t)

	if got := Default(); got != "" {
		t.Errorf("fresh Default() = %q, want empty", got)
	}

	// First machine auto-becomes default.
	if err := SetDefaultIfUnset("a"); err != nil {
		t.Fatal(err)
	}
	if got := Default(); got != "a" {
		t.Errorf("after first SetDefaultIfUnset, Default() = %q, want a", got)
	}

	// A later auto-set does not override an existing default.
	if err := SetDefaultIfUnset("b"); err != nil {
		t.Fatal(err)
	}
	if got := Default(); got != "a" {
		t.Errorf("SetDefaultIfUnset clobbered existing default: %q", got)
	}

	// Explicit set wins and persists across reload.
	if err := SetDefault("b"); err != nil {
		t.Fatal(err)
	}
	if got := Default(); got != "b" {
		t.Errorf("after SetDefault, Default() = %q, want b", got)
	}

	// Clearing a non-matching name is a no-op.
	if err := ClearDefaultIf("a"); err != nil {
		t.Fatal(err)
	}
	if got := Default(); got != "b" {
		t.Errorf("ClearDefaultIf(non-matching) changed default to %q", got)
	}

	// Clearing the matching name resets it.
	if err := ClearDefaultIf("b"); err != nil {
		t.Fatal(err)
	}
	if got := Default(); got != "" {
		t.Errorf("ClearDefaultIf(matching) left default = %q", got)
	}
}
