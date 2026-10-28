package config

import "testing"

func TestLoad(t *testing.T) {
	for _, tc := range []struct {
		name, addr, container, max string
		valid                      bool
	}{
		{"defaults", "", "", "", true},
		{"loopback", "127.0.0.1:8080", "", "", true},
		{"ipv6", "[::1]:8080", "", "", true},
		{"public", "0.0.0.0:8080", "", "", false},
		{"container", "0.0.0.0:8080", "1", "", true},
		{"lan", "192.168.1.5:8080", "1", "", false},
		{"bad limit", "", "", "bad", false},
		{"zero limit", "", "", "0", false},
		{"huge limit", "", "", "1073741825", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAMILYVAULT_ADDR", tc.addr)
			t.Setenv("FAMILYVAULT_CONTAINER", tc.container)
			t.Setenv("FAMILYVAULT_MAX_UPLOAD_BYTES", tc.max)
			_, err := Load()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}
