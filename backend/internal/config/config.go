package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address        string
	DataDir        string
	StaticDir      string
	MaxUploadBytes int64
	SessionTTL     time.Duration
	SessionIdleTTL time.Duration
	SecureCookies  bool
	PublicOrigin   string
}

func Load() (Config, error) {
	c := Config{
		Address:   value("FAMILYVAULT_ADDR", "127.0.0.1:8080"),
		DataDir:   value("FAMILYVAULT_DATA_DIR", "./data"),
		StaticDir: os.Getenv("FAMILYVAULT_STATIC_DIR"),
	}
	limit, err := strconv.ParseInt(value("FAMILYVAULT_MAX_UPLOAD_BYTES", "26214400"), 10, 64)
	if err != nil || limit < 1 || limit > 1<<30 {
		return c, fmt.Errorf("FAMILYVAULT_MAX_UPLOAD_BYTES must be between 1 and 1073741824")
	}
	c.MaxUploadBytes = limit
	c.SessionTTL, err = time.ParseDuration(value("FAMILYVAULT_SESSION_TTL", "8h"))
	if err != nil || c.SessionTTL < time.Minute || c.SessionTTL > 24*time.Hour {
		return c, fmt.Errorf("FAMILYVAULT_SESSION_TTL must be between 1m and 24h")
	}
	c.SessionIdleTTL, err = time.ParseDuration(value("FAMILYVAULT_SESSION_IDLE_TTL", "30m"))
	if err != nil || c.SessionIdleTTL < time.Minute || c.SessionIdleTTL > c.SessionTTL {
		return c, fmt.Errorf("FAMILYVAULT_SESSION_IDLE_TTL must be between 1m and the absolute session TTL")
	}
	c.SecureCookies, err = strconv.ParseBool(value("FAMILYVAULT_COOKIE_SECURE", "false"))
	if err != nil {
		return c, fmt.Errorf("FAMILYVAULT_COOKIE_SECURE must be true or false")
	}
	c.PublicOrigin = os.Getenv("FAMILYVAULT_PUBLIC_ORIGIN")
	if c.PublicOrigin != "" {
		origin, err := url.Parse(c.PublicOrigin)
		if err != nil || origin.Scheme != "https" || origin.Hostname() == "" ||
			origin.User != nil || c.PublicOrigin != "https://"+origin.Host ||
			strings.ContainsAny(origin.Host, "*%") || strings.HasSuffix(origin.Host, ":") {
			return c, fmt.Errorf("FAMILYVAULT_PUBLIC_ORIGIN must be an exact HTTPS origin without credentials, path, query or fragment")
		}
		if port := origin.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return c, fmt.Errorf("FAMILYVAULT_PUBLIC_ORIGIN port must be between 1 and 65535")
			}
		}
		if !c.SecureCookies {
			return c, fmt.Errorf("FAMILYVAULT_PUBLIC_ORIGIN requires FAMILYVAULT_COOKIE_SECURE=true")
		}
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil || port == "" {
		return c, fmt.Errorf("FAMILYVAULT_ADDR must be an IP:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return c, fmt.Errorf("port must be between 1 and 65535")
	}
	ip := net.ParseIP(host)
	// Docker requires a container-wide bind; Compose alone publishes its port on loopback.
	containerBind := host == "0.0.0.0" && os.Getenv("FAMILYVAULT_CONTAINER") == "1"
	if ip == nil || (!ip.IsLoopback() && !containerBind) {
		return c, fmt.Errorf("bind to a loopback IP; 0.0.0.0 requires FAMILYVAULT_CONTAINER=1 and loopback-only Docker publishing")
	}
	c.DataDir, err = filepath.Abs(c.DataDir)
	return c, err
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
