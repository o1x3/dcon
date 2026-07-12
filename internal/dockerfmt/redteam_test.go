package dockerfmt

import (
	"strings"
	"testing"
	"time"
)

// TestHumanDurationWeeksMonthsBoundary locks the go-units/docker boundary:
// weeks->months crosses at 24*30*2 (1440h / 60 days), verified against the
// canonical docker/go-units duration.go. So <60d is "N weeks", >=60d is months.
func TestHumanDurationWeeksMonthsBoundary(t *testing.T) {
	if got := HumanDuration(50 * 24 * time.Hour); got != "7 weeks" { // 1200h < 1440h
		t.Errorf("HumanDuration(50d) = %q, want %q", got, "7 weeks")
	}
	if got := HumanDuration(70 * 24 * time.Hour); got != "2 months" { // 1680h >= 1440h
		t.Errorf("HumanDuration(70d) = %q, want %q", got, "2 months")
	}
	if got := HumanDuration(100 * 24 * time.Hour); got != "3 months" { // 2400h
		t.Errorf("HumanDuration(100d) = %q, want %q", got, "3 months")
	}
}

// TestRenderTableFieldHeaderOverride locks the round-2 fix: a custom
// `table {{.ID}}` format derives its header from def.FieldHeaders when set, so
// images/network/history print their own ID header instead of "CONTAINER ID".
// (captureStdout is defined in render_test.go.)
func TestRenderTableFieldHeaderOverride(t *testing.T) {
	def := TableDef{
		Headers:      []string{"IMAGE ID"},
		Row:          func(v any) []string { return []string{"x"} },
		ID:           func(v any) string { return "x" },
		FieldHeaders: map[string]string{".ID": "IMAGE ID"},
	}
	out := captureStdout(t, func() {
		if err := Render("table {{.ID}}", false, nil, def); err != nil { // empty views: header only
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "IMAGE ID") {
		t.Errorf("expected overridden header 'IMAGE ID', got:\n%s", out)
	}
	if strings.Contains(out, "CONTAINER ID") {
		t.Errorf("global default 'CONTAINER ID' leaked despite override:\n%s", out)
	}

	// Without an override, the global default still applies.
	plain := TableDef{Headers: []string{"CONTAINER ID"}, Row: def.Row, ID: def.ID}
	out2 := captureStdout(t, func() {
		if err := Render("table {{.ID}}", false, nil, plain); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out2, "CONTAINER ID") {
		t.Errorf("expected global default 'CONTAINER ID', got:\n%s", out2)
	}
}

// TestHumanSizeWithPrecision locks the precision-3 decimal formatter used by
// `docker images` SIZE and `docker stats` NET/BLOCK I/O (the default HumanSize
// uses precision 4, printing one digit too many for those columns).
func TestHumanSizeWithPrecision(t *testing.T) {
	cases := []struct {
		n    float64
		p    int
		want string
	}{
		{13256, 3, "13.3kB"},
		{1234000000, 3, "1.23GB"},
		{12345678, 3, "12.3MB"},
		{512, 3, "512B"},
		{0, 3, "0B"},
	}
	for _, tc := range cases {
		if got := HumanSizeWithPrecision(tc.n, tc.p); got != tc.want {
			t.Errorf("HumanSizeWithPrecision(%v, %d) = %q, want %q", tc.n, tc.p, got, tc.want)
		}
	}
}

// TestHumanSizeBinary locks the binary IEC formatter used by `docker stats` for
// the MEM USAGE / LIMIT column (KiB/MiB/GiB, base 1024), distinct from the
// decimal SI units docker uses elsewhere.
func TestHumanSizeBinary(t *testing.T) {
	cases := []struct {
		n    float64
		want string
	}{
		{536870912, "512MiB"}, // 512 * 1024^2
		{8589934592, "8GiB"},  // 8 * 1024^3
		{1024, "1KiB"},
		{512, "512B"},
	}
	for _, tc := range cases {
		if got := HumanSizeBinary(tc.n); got != tc.want {
			t.Errorf("HumanSizeBinary(%v) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
