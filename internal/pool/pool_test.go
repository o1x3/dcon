package pool

import (
	"testing"
)

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
