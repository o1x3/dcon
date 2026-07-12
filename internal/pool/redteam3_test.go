package pool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// redirectState points the pool state at a fresh temp dir.
func redirectState(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

// fakePoolBackend writes an executable script and points DCON_CONTAINER_BIN at
// it so pool functions that shell out stay backend-free in CI.
func fakePoolBackend(t *testing.T, script string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "container")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DCON_CONTAINER_BIN", p)
}

// TestWithLockReadOnlySkipsRewrite guards the dirty flag: read-only callers
// (List, AvailableDepth, a Claim miss) must not rewrite pool.json. The state
// file is seeded with compact JSON; a rewrite would re-indent it, so
// byte-identical content proves the write+rename was skipped.
func TestWithLockReadOnlySkipsRewrite(t *testing.T) {
	redirectState(t)
	seed := []byte(`{"members":[{"id":"vm1","image":"alpine:latest","entrypoint":null,"cmd":null,"bootedAt":1}]}`)
	p, err := statePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, seed, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := List(); len(got) != 1 || got[0].ID != "vm1" {
		t.Fatalf("List = %v, want the seeded member", got)
	}
	if d := AvailableDepth("alpine"); d != 1 {
		t.Fatalf("AvailableDepth = %d, want 1", d)
	}
	if _, ok := Claim("nginx"); ok {
		t.Fatal("Claim(nginx) should miss")
	}

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(seed) {
		t.Errorf("read-only calls rewrote pool.json:\n before %q\n after  %q", seed, after)
	}

	// A mutating call must still persist.
	if err := Add(Member{ID: "vm2", Image: NormalizeRef("alpine")}); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(p)
	if !strings.Contains(string(after), "vm2") {
		t.Error("Add did not persist to pool.json")
	}
}

// TestUnwarmableNegativeCache covers the DCON_WARM=auto doomed-boot loop fix:
// a failed warm boot marks the image unwarmable for the backoff window, a
// stale mark expires, and a later successful boot clears it.
func TestUnwarmableNegativeCache(t *testing.T) {
	redirectState(t)

	if Unwarmable("gcr.io/distroless/static") {
		t.Fatal("fresh state: nothing should be unwarmable")
	}
	MarkUnwarmable("gcr.io/distroless/static")
	if !Unwarmable("gcr.io/distroless/static") {
		t.Error("image must be unwarmable right after MarkUnwarmable")
	}
	if !Unwarmable("gcr.io/distroless/static:latest") {
		t.Error("negative cache must key by normalized ref")
	}
	if Unwarmable("alpine") {
		t.Error("other images must be unaffected")
	}

	// clearUnwarmable (the successful-Boot path) drops the entry.
	clearUnwarmable(NormalizeRef("gcr.io/distroless/static"))
	if Unwarmable("gcr.io/distroless/static") {
		t.Error("entry must be gone after clearUnwarmable")
	}

	// An entry older than the backoff no longer blocks.
	p, _ := statePath()
	old := state{Unwarmable: map[string]int64{"old:latest": time.Now().Add(-2 * unwarmableBackoff).Unix()}}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if Unwarmable("old") {
		t.Error("expired negative-cache entry must not block replenishment")
	}
}

// TestLeakedPoolIDs pins the reconcile policy: only pool-labeled containers
// that the state file does not know about AND that are older than the cutoff
// are reaped — a just-claimed member (young, absent from state) survives.
func TestLeakedPoolIDs(t *testing.T) {
	mkRow := func(id, label, created string) backendRow {
		var r backendRow
		r.ID = id
		r.Configuration.Labels = map[string]string{}
		if label != "" {
			r.Configuration.Labels[LabelPool] = label
		}
		r.Configuration.CreationDate = created
		return r
	}
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []backendRow{
		mkRow("leaked-old", "1", "2025-12-01T00:00:00Z"),      // reap
		mkRow("tracked-old", "1", "2025-12-01T00:00:00Z"),     // in state: keep
		mkRow("claimed-young", "1", "2026-01-01T09:00:00Z"),   // young: keep (likely mid-exec)
		mkRow("user-container", "", "2025-12-01T00:00:00Z"),   // no pool label: never touch
		mkRow("bad-date", "1", "not-a-time"),                  // unparseable: keep (fail safe)
		mkRow("leaked-nano", "1", "2025-12-01T00:00:00.123Z"), // fractional seconds parse too
	}
	known := map[string]bool{"tracked-old": true}
	got := leakedPoolIDs(rows, known, cutoff)
	want := []string{"leaked-old", "leaked-nano"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("leakedPoolIDs = %v, want %v", got, want)
	}
}

// TestAcquireReplenishLock verifies the per-image flock that stops K
// concurrent pool-miss runs from booting K members: the second acquisition
// for the same image fails while the first is held, an unrelated image is
// independent, and release makes the lock available again.
func TestAcquireReplenishLock(t *testing.T) {
	redirectState(t)

	rel1, ok := AcquireReplenishLock("alpine")
	if !ok {
		t.Fatal("first acquisition must succeed")
	}
	if _, ok := AcquireReplenishLock("alpine:latest"); ok {
		t.Error("second acquisition for the same (normalized) image must fail while held")
	}
	if rel2, ok := AcquireReplenishLock("python:3.12"); !ok {
		t.Error("a different image must have an independent lock")
	} else {
		rel2()
	}
	rel1()
	rel3, ok := AcquireReplenishLock("alpine")
	if !ok {
		t.Error("acquisition must succeed again after release")
	} else {
		rel3()
	}
}

// TestPruneOrphansKeepsStateOnDestroyFailure reproduces the prune bug where a
// failed Destroy still forgot the member and swept the state file: the VM kept
// running but vanished from `warm ls`. Now the entry survives and the failure
// is reported.
func TestPruneOrphansKeepsStateOnDestroyFailure(t *testing.T) {
	redirectState(t)
	fakePoolBackend(t, `case "$1" in
ls) printf '[{"id":"vm1","configuration":{"labels":{"dcon.pool":"1","dcon.pool.image":"alpine:latest"},"creationDate":"2020-01-01T00:00:00Z"}}]' ;;
rm) echo "cannot remove vm1: busy" >&2; exit 1 ;;
esac`)
	if err := Add(Member{ID: "vm1", Image: NormalizeRef("alpine")}); err != nil {
		t.Fatal(err)
	}

	n, err := PruneOrphans("")
	if n != 0 {
		t.Errorf("n = %d, want 0 (nothing was actually removed)", n)
	}
	if err == nil || !strings.Contains(err.Error(), "vm1") {
		t.Errorf("err = %v, want per-VM failure mentioning vm1", err)
	}
	if got := List(); len(got) != 1 || got[0].ID != "vm1" {
		t.Errorf("state after failed prune = %v; the live VM must stay tracked", got)
	}
}

// TestPruneOrphansForgetsOnSuccess is the happy-path counterpart: a successful
// destroy is counted, forgotten, and the sweep clears the image's entries.
func TestPruneOrphansForgetsOnSuccess(t *testing.T) {
	redirectState(t)
	fakePoolBackend(t, `case "$1" in
ls) printf '[{"id":"vm1","configuration":{"labels":{"dcon.pool":"1","dcon.pool.image":"alpine:latest"},"creationDate":"2020-01-01T00:00:00Z"}}]' ;;
rm) exit 0 ;;
esac`)
	if err := Add(Member{ID: "vm1", Image: NormalizeRef("alpine")}); err != nil {
		t.Fatal(err)
	}
	n, err := PruneOrphans("")
	if err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	if n != 1 {
		t.Errorf("n = %d, want 1", n)
	}
	if got := List(); len(got) != 0 {
		t.Errorf("state after prune = %v, want empty", got)
	}
}
