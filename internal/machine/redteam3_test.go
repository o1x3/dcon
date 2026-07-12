package machine

import "testing"

// TestValidateNameAllowList pins the switch from the old " \t/:@" blocklist to
// docker's name allow-list: control characters and ANSI escapes used to pass
// straight into labels and styled TTY echo.
func TestValidateNameAllowList(t *testing.T) {
	valid := []string{"ubuntu", "Dev2", "my-machine", "work_box", "a.b-c_d", "9lives"}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) unexpected error: %v", name, err)
		}
	}
	invalid := []string{
		"a\x1b[31mred",  // ANSI escape (terminal injection via ls/echo)
		"a\nb",          // newline
		"a\x00b",        // NUL
		"bell\ab",       // control char
		"-leading-dash", // would parse as a flag downstream
		".hidden",       // must start alphanumeric
		"_under",        // must start alphanumeric
		"café",          // non-ASCII
		"a b",           // space (still rejected)
		"a/b", "a:b", "a@b",
	}
	for _, name := range invalid {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) should have errored", name)
		}
	}
}
