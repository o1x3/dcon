package pool

import (
	"testing"
)

// TestInvalidateImage reproduces the stale-image bug: after `dcon pull alpine`
// (or tag/rmi/build -t), a warm member booted from the OLD image must not stay
// claimable, or `run --rm alpine …` execs into a VM running outdated bits.
// InvalidateImage must atomically drop every available member for the ref
// (normalized), leave other images' members alone, and clear the ref's
// unwarmable negative-cache entry (the image changed, so the old verdict is
// void).
func TestInvalidateImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// DestroyAsync spawns a detached `container rm`; point it at a no-op
	// binary so the test never touches a real backend.
	t.Setenv("DCON_CONTAINER_BIN", "/usr/bin/true")

	seed := func() {
		for _, m := range []Member{
			{ID: "a1", Image: NormalizeRef("alpine"), BootedAt: 1},
			{ID: "a2", Image: NormalizeRef("alpine:latest"), BootedAt: 2},
			{ID: "p1", Image: NormalizeRef("python:3.12"), BootedAt: 3},
		} {
			if err := Add(m); err != nil {
				t.Fatalf("seed Add: %v", err)
			}
		}
	}

	seed()
	MarkUnwarmable("alpine")

	// Invalidate by the un-normalized ref: both alpine members must go
	// (normalization), python must survive.
	InvalidateImage("alpine")
	if d := AvailableDepth("alpine"); d != 0 {
		t.Errorf("after InvalidateImage(alpine): depth = %d, want 0", d)
	}
	if d := AvailableDepth("python:3.12"); d != 1 {
		t.Errorf("python member must survive alpine invalidation; depth = %d", d)
	}
	if Unwarmable("alpine") {
		t.Error("InvalidateImage must clear the ref's unwarmable entry")
	}
	// A claim right after invalidation must miss (the stale VM is gone from
	// the state file even though its async teardown may still be in flight).
	if _, ok := Claim("alpine"); ok {
		t.Error("Claim(alpine) after invalidation must miss")
	}

	// Empty ref = invalidate everything (rmi --all).
	InvalidateImage("")
	if got := List(); len(got) != 0 {
		t.Errorf("InvalidateImage(\"\") must empty the pool; got %v", got)
	}
}
