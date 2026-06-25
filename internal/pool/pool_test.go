package pool

import (
	"os"
	"path/filepath"
	"testing"
)

// TestClaimMissOnSaveFailure reproduces the isolation bug where Claim ignored a
// state-write failure and still returned ok=true: the popped member stayed in
// pool.json on disk, so a concurrent/next Claim could hand out the SAME live VM
// (two runs exec'ing into one microVM). On a persist failure Claim must report a
// miss (so the caller cold-runs) and leave the member available.
func TestClaimMissOnSaveFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	if err := Add(Member{ID: "vm1", Image: NormalizeRef("alpine")}); err != nil {
		t.Fatalf("seed Add: %v", err)
	}
	p, err := statePath()
	if err != nil {
		t.Fatal(err)
	}
	// A directory at the temp-file path makes save()'s os.WriteFile fail
	// deterministically for every user (EISDIR) — unlike a chmod, which root
	// bypasses. save() writes pool.json.tmp then renames it into place.
	tmp := p + ".tmp"
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp) //nolint:errcheck // restore for temp-dir cleanup

	if _, ok := Claim("alpine"); ok {
		t.Error("Claim must report a miss when the state write fails")
	}
	// Restore and confirm the member was NOT lost (still claimable later).
	if err := os.RemoveAll(tmp); err != nil {
		t.Fatal(err)
	}
	if got := AvailableDepth("alpine"); got != 1 {
		t.Errorf("member should remain available after a failed claim; depth=%d", got)
	}
}

func TestNormalizeRef(t *testing.T) {
	cases := map[string]string{
		"alpine":                          "alpine:latest",
		"alpine:latest":                   "alpine:latest",
		"alpine:3.20":                     "alpine:3.20",
		"python":                          "python:latest",
		"docker.io/library/alpine":        "docker.io/library/alpine:latest",
		"docker.io/library/alpine:3.20":   "docker.io/library/alpine:3.20",
		"localhost:5000/img":              "localhost:5000/img:latest", // colon is a port, not a tag
		"localhost:5000/img:v2":           "localhost:5000/img:v2",     // explicit tag kept
		"registry.io:5000/team/app":       "registry.io:5000/team/app:latest",
		"alpine@sha256:abc":               "alpine@sha256:abc", // digest pin untouched
		"registry.io/app@sha256:deadbeef": "registry.io/app@sha256:deadbeef",
	}
	for in, want := range cases {
		if got := NormalizeRef(in); got != want {
			t.Errorf("NormalizeRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPartitionStale verifies the reap policy splits members by boot time. The
// real fix it guards is atomicity: ReapStale now removes stale members inside
// the state lock (via this helper) instead of List→forget→Destroy, so a
// concurrent Claim and a reap can never both own — and the reaper can never
// destroy — a VM a live run just claimed.
func TestPartitionStale(t *testing.T) {
	members := []Member{
		{ID: "old1", BootedAt: 100},
		{ID: "fresh", BootedAt: 1000},
		{ID: "old2", BootedAt: 200},
	}
	stale, kept := partitionStale(members, 500)
	if len(stale) != 2 || stale[0].ID != "old1" || stale[1].ID != "old2" {
		t.Errorf("stale = %v, want [old1 old2]", stale)
	}
	if len(kept) != 1 || kept[0].ID != "fresh" {
		t.Errorf("kept = %v, want [fresh]", kept)
	}
	// Nothing stale: all kept, none reaped.
	stale, kept = partitionStale(members, 0)
	if len(stale) != 0 || len(kept) != 3 {
		t.Errorf("cutoff 0: stale=%v kept=%v, want none stale", stale, kept)
	}
}

func TestEnvKnobs(t *testing.T) {
	// AutoEnabled
	for _, v := range []string{"auto", "1", "on", "true", "yes", "AUTO", " on "} {
		t.Setenv("DCON_WARM", v)
		if !AutoEnabled() {
			t.Errorf("AutoEnabled() with DCON_WARM=%q = false, want true", v)
		}
		if Disabled() {
			t.Errorf("Disabled() with DCON_WARM=%q = true, want false", v)
		}
	}
	// Disabled
	for _, v := range []string{"off", "0", "no", "false", "OFF"} {
		t.Setenv("DCON_WARM", v)
		if !Disabled() {
			t.Errorf("Disabled() with DCON_WARM=%q = false, want true", v)
		}
		if AutoEnabled() {
			t.Errorf("AutoEnabled() with DCON_WARM=%q = true, want false", v)
		}
	}
	// Unset / neutral: neither auto nor disabled.
	t.Setenv("DCON_WARM", "")
	if AutoEnabled() || Disabled() {
		t.Errorf("empty DCON_WARM: AutoEnabled=%v Disabled=%v, want both false", AutoEnabled(), Disabled())
	}
}

func TestTargetDepthClamping(t *testing.T) {
	cases := map[string]int{
		"":     1, // default
		"3":    3,
		"0":    1, // clamped up
		"-5":   1, // clamped up
		"99":   8, // clamped down
		"junk": 1, // unparseable -> default
	}
	for in, want := range cases {
		t.Setenv("DCON_WARM_DEPTH", in)
		if got := TargetDepth(); got != want {
			t.Errorf("TargetDepth() with DCON_WARM_DEPTH=%q = %d, want %d", in, got, want)
		}
	}
}

func TestWarmCommand(t *testing.T) {
	cases := []struct {
		name    string
		member  Member
		userCmd []string
		want    []string
	}{
		{
			name:    "entrypoint + user args (args replace cmd)",
			member:  Member{Entrypoint: []string{"/app"}, Cmd: []string{"--default"}},
			userCmd: []string{"serve", "--port=80"},
			want:    []string{"/app", "serve", "--port=80"},
		},
		{
			name:   "entrypoint + image cmd (no user args)",
			member: Member{Entrypoint: []string{"/app"}, Cmd: []string{"--default"}},
			want:   []string{"/app", "--default"},
		},
		{
			name:    "no entrypoint, user args",
			member:  Member{Cmd: []string{"/bin/sh"}},
			userCmd: []string{"echo", "hi"},
			want:    []string{"echo", "hi"},
		},
		{
			name:   "no entrypoint, image cmd",
			member: Member{Cmd: []string{"/bin/sh"}},
			want:   []string{"/bin/sh"},
		},
		{
			name:   "nothing to run -> nil (caller falls back to cold)",
			member: Member{},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WarmCommand(tc.member, tc.userCmd)
			if len(got) != len(tc.want) {
				t.Fatalf("WarmCommand = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("WarmCommand = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestTTL(t *testing.T) {
	cases := map[string]int{
		"":     600, // default 10m
		"0":    0,   // disabled
		"30":   30,
		"-5":   600, // negative ignored -> default
		"junk": 600, // unparseable -> default
	}
	for in, wantSecs := range cases {
		t.Setenv("DCON_WARM_TTL", in)
		if got := TTL().Seconds(); int(got) != wantSecs {
			t.Errorf("TTL() with DCON_WARM_TTL=%q = %vs, want %ds", in, got, wantSecs)
		}
	}
}

// TestStateRoundTrip exercises Add/Claim/List/AvailableDepth against a private
// state dir (redirected via HOME) without touching the container backend.
func TestStateRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // redirect os.UserConfigDir
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if got := List(); len(got) != 0 {
		t.Fatalf("fresh pool List() = %v, want empty", got)
	}
	if _, ok := Claim("alpine"); ok {
		t.Fatal("Claim on empty pool returned ok=true")
	}

	if err := Add(Member{ID: "id-a", Image: NormalizeRef("alpine"), BootedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := Add(Member{ID: "id-b", Image: NormalizeRef("alpine"), BootedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if err := Add(Member{ID: "id-c", Image: NormalizeRef("python:3.12"), BootedAt: 3}); err != nil {
		t.Fatal(err)
	}

	if d := AvailableDepth("alpine"); d != 2 {
		t.Errorf("AvailableDepth(alpine) = %d, want 2", d)
	}
	if d := AvailableDepth("alpine:latest"); d != 2 {
		t.Errorf("AvailableDepth(alpine:latest) = %d, want 2 (normalization)", d)
	}

	// Claim pops exactly one matching member and removes it from state.
	m, ok := Claim("alpine")
	if !ok || m.ID != "id-a" {
		t.Fatalf("Claim(alpine) = %v,%v, want id-a,true (FIFO)", m, ok)
	}
	if d := AvailableDepth("alpine"); d != 1 {
		t.Errorf("after claim AvailableDepth(alpine) = %d, want 1", d)
	}

	// Wrong image must not be served by another image's members.
	if _, ok := Claim("nginx"); ok {
		t.Error("Claim(nginx) returned ok=true with no nginx members")
	}

	// The python member is independent.
	if d := AvailableDepth("python:3.12"); d != 1 {
		t.Errorf("AvailableDepth(python) = %d, want 1", d)
	}
	if len(List()) != 2 {
		t.Errorf("List() = %d members, want 2 after one claim", len(List()))
	}
}
