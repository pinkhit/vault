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

func TestAuthenticationSettings(t *testing.T) {
	for _, test := range []struct{ key, value string }{
		{"FAMILYVAULT_SESSION_TTL", "0s"},
		{"FAMILYVAULT_SESSION_TTL", "48h"},
		{"FAMILYVAULT_SESSION_IDLE_TTL", "0s"},
		{"FAMILYVAULT_SESSION_IDLE_TTL", "9h"},
		{"FAMILYVAULT_COOKIE_SECURE", "invalid"},
	} {
		t.Run(test.key+test.value, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid authentication setting accepted")
			}
		})
	}
}
