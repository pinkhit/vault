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
			t.Setenv("SAFEVAULT_ADDR", tc.addr)
			t.Setenv("SAFEVAULT_CONTAINER", tc.container)
			t.Setenv("SAFEVAULT_MAX_UPLOAD_BYTES", tc.max)
			_, err := Load()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}

func TestAuthenticationSettings(t *testing.T) {
	for _, test := range []struct{ key, value string }{
		{"SAFEVAULT_SESSION_TTL", "0s"},
		{"SAFEVAULT_SESSION_TTL", "48h"},
		{"SAFEVAULT_SESSION_IDLE_TTL", "0s"},
		{"SAFEVAULT_SESSION_IDLE_TTL", "9h"},
		{"SAFEVAULT_COOKIE_SECURE", "invalid"},
	} {
		t.Run(test.key+test.value, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid authentication setting accepted")
			}
		})
	}
}

func TestPublicOriginRequiresExactHTTPSAndSecureCookies(t *testing.T) {
	for _, test := range []struct {
		origin string
		secure string
		valid  bool
	}{
		{"", "false", true},
		{"https://vault.example.ts.net", "true", true},
		{"https://vault.example.ts.net:8443", "true", true},
		{"https://vault.example.ts.net", "false", false},
		{"http://vault.example.ts.net", "true", false},
		{"https://vault.example.ts.net/", "true", false},
		{"https://vault.example.ts.net/api", "true", false},
		{"https://vault.example.ts.net?", "true", false},
		{"https://vault.example.ts.net#", "true", false},
		{"https://user@vault.example.ts.net", "true", false},
		{"https://*.ts.net", "true", false},
		{"https://vault.example.ts.net:", "true", false},
		{"https://vault.example.ts.net:0", "true", false},
		{"https://vault.example.ts.net:65536", "true", false},
		{"https://vault.example.ts.net:bad", "true", false},
		{"https://", "true", false},
		{" https://vault.example.ts.net", "true", false},
	} {
		t.Run(test.origin+"/secure="+test.secure, func(t *testing.T) {
			t.Setenv("SAFEVAULT_PUBLIC_ORIGIN", test.origin)
			t.Setenv("SAFEVAULT_COOKIE_SECURE", test.secure)
			config, err := Load()
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t error=%v", test.valid, err)
			}
			if err == nil && config.PublicOrigin != test.origin {
				t.Fatal("origin changed silently")
			}
		})
	}
}
