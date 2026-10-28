package api

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"time"
)

// middleware applies the local-development boundary and request limits to every route.
func (a *API) middleware(next http.Handler) http.Handler {
	slots := make(chan struct{}, 8)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var token [12]byte
		if _, err := rand.Read(token[:]); err != nil {
			problem(w, http.StatusInternalServerError, "request could not be initialized")
			return
		}
		requestID := hex.EncodeToString(token[:])
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; "+
				"img-src 'self' data:; connect-src 'self'; object-src 'none'; "+
				"base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		defer func() {
			if recover() != nil {
				a.Log.Error("request panic", "request_id", requestID)
				problem(w, http.StatusInternalServerError, "internal server error")
			}
			// Avoid logging filenames, query strings, request bodies or filesystem paths.
			a.Log.Info("request completed",
				"request_id", requestID,
				"method", r.Method,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		}()
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			problem(w, http.StatusForbidden, "localhost access only")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			problem(w, http.StatusForbidden, "cross-site access is not allowed")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if origin != "http://"+r.Host {
				problem(w, http.StatusForbidden, "origin is not allowed")
				return
			}
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			problem(w, http.StatusServiceUnavailable, "server is busy; try again shortly")
			return
		}
		next.ServeHTTP(w, r)
	})
}
