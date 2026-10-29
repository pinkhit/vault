package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/pinkhit/vault/backend/internal/auth"
)

func TestHTTPSOriginBoundaryIgnoresForwardedHeaders(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	a.PublicOrigin = "https://vault.example.ts.net"
	a.Auth.SecureCookies = true
	for _, test := range []struct {
		name, host, origin, method, path, site string
		status                                 int
	}{
		{"configured", "vault.example.ts.net", a.PublicOrigin, "GET", "/api/health", "same-origin", 200},
		{"navigation", "vault.example.ts.net", "", "GET", "/api/health", "", 200},
		{"auth still required", "vault.example.ts.net", a.PublicOrigin, "GET", "/api/documents", "", 401},
		{"wrong host", "evil.example", a.PublicOrigin, "GET", "/api/health", "", 403},
		{"host suffix", "vault.example.ts.net.evil.example", a.PublicOrigin, "GET", "/api/health", "", 403},
		{"wrong port", "vault.example.ts.net:8443", a.PublicOrigin, "GET", "/api/health", "", 403},
		{"wrong scheme", "vault.example.ts.net", "http://vault.example.ts.net", "GET", "/api/health", "", 403},
		{"foreign origin", "vault.example.ts.net", "https://evil.example", "GET", "/api/health", "", 403},
		{"null origin", "vault.example.ts.net", "null", "GET", "/api/health", "", 403},
		{"cross-site", "vault.example.ts.net", a.PublicOrigin, "GET", "/api/health", "cross-site", 403},
		{"local health", "127.0.0.1:8080", "", "GET", "/api/health", "", 200},
		{"local head health", "localhost:8080", "", "HEAD", "/api/health", "", 200},
		{"local UI", "localhost:8080", "", "GET", "/", "", 403},
		{"local API", "127.0.0.1:8080", "", "GET", "/api/documents", "", 403},
		{"local login", "localhost:8080", a.PublicOrigin, "POST", "/api/auth/login", "", 403},
		{"missing login origin", "vault.example.ts.net", "", "POST", "/api/auth/login", "", 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, "http://localhost"+test.path, nil)
			r.Host = test.host
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Sec-Fetch-Site", test.site)
			// Even plausible proxy and identity headers cannot change the boundary or sign in.
			r.Header.Set("X-Forwarded-Host", "vault.example.ts.net")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("Forwarded", "host=vault.example.ts.net;proto=https")
			r.Header.Set("Tailscale-User-Login", fixtureActor.Username)
			w := httptest.NewRecorder()
			a.Handler("").ServeHTTP(w, r)
			requireStatus(t, w, test.status)
		})
	}
}

func TestTLSProxySessionCSRFAndDocumentAuthorization(t *testing.T) {
	a, _ := newApp(t, t.TempDir(), 1024)
	private := uploadAs(t, a, fixtureIdentity(), "private-https.txt")
	reader := createMember(t, a, "https-reader")
	a.Auth.SecureCookies = true
	upstream := httptest.NewUnstartedServer(nil)
	target, err := url.Parse("http://" + upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate Serve: TLS terminates at the proxy; its HTTP upstream preserves Host.
	proxy := httptest.NewUnstartedServer(httputil.NewSingleHostReverseProxy(target))
	// httptest's certificate covers example.com. Use that DNS origin because
	// browsers/Go deliberately permit Secure cookies over HTTP on loopback names.
	a.PublicOrigin = "https://example.com:" + strconv.Itoa(proxy.Listener.Addr().(*net.TCPAddr).Port)
	upstream.Config.Handler = a.Handler("")
	upstream.Start()
	defer upstream.Close()
	proxy.StartTLS()
	defer proxy.Close()
	client := proxy.Client()
	client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
	}
	client.Jar, err = cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, csrf string) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, a.PublicOrigin+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Origin", a.PublicOrigin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	login := request("POST", "/api/auth/login", `{"username":"https-reader","password":"fictional-member-password"}`, "")
	if login.StatusCode != 200 {
		t.Fatal("HTTPS login", login.StatusCode)
	}
	cookies := login.Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Name != "__Host-safevault_session" || cookies[0].Domain != "" || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe HTTPS session cookie")
	}
	var session auth.Session
	if err := json.NewDecoder(login.Body).Decode(&session); err != nil || session.User.ID != reader.ID || session.CSRFToken == "" {
		t.Fatal("session", err)
	}
	plainURL := "http://example.com:" + strconv.Itoa(upstream.Listener.Addr().(*net.TCPAddr).Port)
	plain, err := http.NewRequest("GET", plainURL+"/api/auth/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	plain.Host = strings.TrimPrefix(a.PublicOrigin, "https://")
	response, err := client.Do(plain)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("Secure session cookie was sent over HTTP", response.StatusCode)
	}
	for _, test := range []struct {
		method, path, csrf string
		status             int
	}{
		{"GET", "/api/auth/session", "", 200},
		{"GET", "/api/documents/" + private.ID + "/download", "", 404},
		{"DELETE", "/api/documents/" + private.ID, session.CSRFToken, 404},
		{"POST", "/api/auth/logout", "", 403},
		{"POST", "/api/auth/logout", "forged", 403},
		{"POST", "/api/auth/logout", session.CSRFToken, 204},
		{"GET", "/api/auth/session", "", 401},
	} {
		if response := request(test.method, test.path, "", test.csrf); response.StatusCode != test.status {
			t.Fatalf("%s %s: got %d want %d", test.method, test.path, response.StatusCode, test.status)
		}
	}
}
