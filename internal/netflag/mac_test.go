package netflag

import "testing"

func TestWithMAC(t *testing.T) {
	cases := []struct {
		net, mac, want string
		err            bool
	}{
		{"", "", "", false},
		{"default", "", "", false},
		{"bridge", "", "", false},
		{"mynet", "", "mynet", false},
		{"", "02:42:ac:11:00:02", "default,mac=02:42:ac:11:00:02", false},
		{"  ", "  02:42:ac:11:00:02  ", "default,mac=02:42:ac:11:00:02", false},
		{"bridge", "02:42:ac:11:00:02", "default,mac=02:42:ac:11:00:02", false},
		{"default", "02:42:AC:11:00:02", "default,mac=02:42:AC:11:00:02", false},
		{"mynet", "02:42:ac:11:00:02", "mynet,mac=02:42:ac:11:00:02", false},
		{"mynet", "02-42-ac-11-00-02", "mynet,mac=02-42-ac-11-00-02", false},
		{"default,mtu=1500", "02:42:ac:11:00:02", "default,mtu=1500,mac=02:42:ac:11:00:02", false},
		{"default,mac=aa:bb:cc:dd:ee:ff", "", "default,mac=aa:bb:cc:dd:ee:ff", false},
		{"default,MAC=aa:bb:cc:dd:ee:ff", "", "default,MAC=aa:bb:cc:dd:ee:ff", false},
		{"default, mac=aa:bb:cc:dd:ee:ff", "", "default, mac=aa:bb:cc:dd:ee:ff", false},
		{"default,mac=aa:bb:cc:dd:ee:ff", "02:42:ac:11:00:02", "", true},
		{"", "zz:zz:zz:zz:zz:zz", "", true},
		{"", "02:42:ac:11:00", "", true},
		{"", "02:42:ac:11:00:02:99", "", true},
		{"", "02:42:ac-11:00:02", "", true},
		{"", "2:42:ac:11:00:02", "", true},
		{"", "0242ac110002", "", true},
		{"", "not-a-mac", "", true},
	}
	for _, tc := range cases {
		got, err := WithMAC(tc.net, tc.mac)
		if tc.err {
			if err == nil {
				t.Errorf("WithMAC(%q,%q) expected error", tc.net, tc.mac)
			}
			continue
		}
		if err != nil {
			t.Errorf("WithMAC(%q,%q): %v", tc.net, tc.mac, err)
			continue
		}
		if got != tc.want {
			t.Errorf("WithMAC(%q,%q)=%q, want %q", tc.net, tc.mac, got, tc.want)
		}
	}
}

func TestAttachMAC(t *testing.T) {
	got, err := AttachMAC("proj_default", "02:42:ac:11:00:02")
	if err != nil {
		t.Fatal(err)
	}
	if got != "proj_default,mac=02:42:ac:11:00:02" {
		t.Errorf("AttachMAC = %q", got)
	}
	// Empty MAC is a no-op.
	got, err = AttachMAC("mynet", "")
	if err != nil || got != "mynet" {
		t.Errorf("AttachMAC no-op: got %q err=%v", got, err)
	}
	// Empty network + MAC → default,mac=.
	got, err = AttachMAC("", "02:42:ac:11:00:02")
	if err != nil || got != "default,mac=02:42:ac:11:00:02" {
		t.Errorf("AttachMAC empty net: got %q err=%v", got, err)
	}
	if _, err := AttachMAC("n,mac=aa:bb:cc:dd:ee:ff", "02:42:ac:11:00:02"); err == nil {
		t.Error("AttachMAC should conflict when mac= already present")
	}
	if _, err := AttachMAC("n", "bad"); err == nil {
		t.Error("AttachMAC should reject invalid MAC")
	}
}

func TestValidateMAC(t *testing.T) {
	ok := []string{
		"02:42:ac:11:00:02",
		"02-42-AC-11-00-02",
		"ff:ff:ff:ff:ff:ff",
		"00:00:00:00:00:00",
	}
	for _, m := range ok {
		if err := ValidateMAC(m); err != nil {
			t.Errorf("ValidateMAC(%q): %v", m, err)
		}
	}
	bad := []string{
		"",
		"02:42:ac:11:00",
		"02:42:ac:11:00:02:03",
		"02:42:ac-11:00:02",
		"2:42:ac:11:00:02",
		"02:42:ac:11:00:0g",
		"0242ac110002",
		"02:42:ac:11:00:02 ",
		" 02:42:ac:11:00:02",
	}
	for _, m := range bad {
		if err := ValidateMAC(m); err == nil {
			t.Errorf("ValidateMAC(%q) expected error", m)
		}
	}
}

func TestHasMAC(t *testing.T) {
	if !HasMAC("default,mac=aa:bb:cc:dd:ee:ff") {
		t.Error("expected HasMAC true")
	}
	if !HasMAC("default, MAC=aa:bb:cc:dd:ee:ff") {
		t.Error("expected HasMAC true for spaced/uppercase key")
	}
	if HasMAC("default,mtu=1500") {
		t.Error("expected HasMAC false")
	}
	if HasMAC("") {
		t.Error("expected HasMAC false for empty")
	}
	// "macaroni=1" must not match the mac= prefix check incorrectly —
	// HasMAC looks for mac= specifically.
	if HasMAC("default,macaroni=1") {
		t.Error("macaroni= must not count as mac=")
	}
}
