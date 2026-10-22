// Package config reads the supervisor's settings from the environment.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
)

const DefaultImage = "ghcr.io/zefhemel/container-computer:latest"

type Config struct {
	Listen        string // SUPERVISOR_LISTEN, default 127.0.0.1:8080
	Domain        string // SUPERVISOR_DOMAIN: HTTPS on 80/443 with automatic certificates
	DataDir       string // SUPERVISOR_DATA_DIR: workspace folders, and .supervisor/ for state
	StateDir      string // DataDir/.supervisor
	EngineHost    string // SUPERVISOR_ENGINE_HOST, else DOCKER_HOST, else the default socket
	Subnet        netip.Prefix
	AdminUser     string
	AdminPassword string
	DefaultImage  string // SUPERVISOR_IMAGE: the catalog's first entry on first start
	InContainer   bool
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func Load() (Config, error) {
	c := Config{
		Listen:        env("SUPERVISOR_LISTEN", "127.0.0.1:8080"),
		Domain:        os.Getenv("SUPERVISOR_DOMAIN"),
		DataDir:       os.Getenv("SUPERVISOR_DATA_DIR"),
		EngineHost:    env("SUPERVISOR_ENGINE_HOST", os.Getenv("DOCKER_HOST")),
		AdminUser:     env("SUPERVISOR_ADMIN_USER", "admin"),
		AdminPassword: os.Getenv("SUPERVISOR_ADMIN_PASSWORD"),
		DefaultImage:  env("SUPERVISOR_IMAGE", DefaultImage),
		InContainer:   os.Getenv("SUPERVISOR_IN_CONTAINER") == "1",
	}
	if !c.InContainer {
		if _, err := os.Stat("/.dockerenv"); err == nil {
			c.InContainer = true
		}
	}
	if c.DataDir == "" {
		if c.InContainer {
			return c, fmt.Errorf("SUPERVISOR_DATA_DIR is required in a container (mount it at the same path as on the host)")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return c, err
		}
		c.DataDir = filepath.Join(home, "computers")
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return c, err
	}
	c.DataDir = abs
	c.StateDir = filepath.Join(c.DataDir, ".supervisor")
	c.Subnet, err = netip.ParsePrefix(env("SUPERVISOR_SUBNET", "10.213.0.0/16"))
	if err != nil {
		return c, fmt.Errorf("SUPERVISOR_SUBNET: %w", err)
	}
	if c.Subnet.Bits() > 28 {
		return c, fmt.Errorf("SUPERVISOR_SUBNET must be /28 or larger")
	}
	return c, nil
}
