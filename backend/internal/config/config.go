package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Address        string
	DataDir        string
	StaticDir      string
	MaxUploadBytes int64
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
