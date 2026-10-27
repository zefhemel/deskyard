// Package config reads the supervisor's settings from the environment.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"time"
)

const DefaultImage = "ghcr.io/zefhemel/deskyard-debian-xfce:latest"

type Config struct {
	Listen        string // DESKYARD_LISTEN, default 0.0.0.0:7343
	Domain        string // DESKYARD_DOMAIN: HTTPS on 80/443 with automatic certificates
	DataDir       string // always DataDir (/data): definitions, state and environment homes
	Layout        Layout // the folders in DataDir
	EngineHost    string // DESKYARD_ENGINE_HOST, else DOCKER_HOST, else the default socket
	Subnet        netip.Prefix
	AdminUser     string
	AdminPassword string
	DefaultImage  string // DESKYARD_IMAGE: the catalog's first entry on first start
	InContainer   bool

	// ActivityPresence (DESKYARD_ACTIVITY_PRESENCE) is how long an agent
	// stays listed after its last action. A test knob, not for users.
	ActivityPresence time.Duration
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func Load() (Config, error) {
	c := Config{
		Listen:        env("DESKYARD_LISTEN", "0.0.0.0:7343"),
		Domain:        os.Getenv("DESKYARD_DOMAIN"),
		DataDir:       DataDir,
		EngineHost:    env("DESKYARD_ENGINE_HOST", os.Getenv("DOCKER_HOST")),
		AdminUser:     env("DESKYARD_ADMIN_USER", "admin"),
		AdminPassword: os.Getenv("DESKYARD_ADMIN_PASSWORD"),
		DefaultImage:  env("DESKYARD_IMAGE", DefaultImage),
		InContainer:   os.Getenv("DESKYARD_IN_CONTAINER") == "1",
	}
	if !c.InContainer {
		if _, err := os.Stat("/.dockerenv"); err == nil {
			c.InContainer = true
		}
	}
	if v := os.Getenv("DESKYARD_DATA_DIR"); v != "" && filepath.Clean(v) != DataDir {
		return c, fmt.Errorf("DESKYARD_DATA_DIR is no longer a setting: the data folder is always %s inside the container; mount the host folder there with -v %s:%s and drop DESKYARD_DATA_DIR", DataDir, v, DataDir)
	}
	var err error
	c.Layout = NewLayout(DataDir)
	c.ActivityPresence, err = time.ParseDuration(env("DESKYARD_ACTIVITY_PRESENCE", "5m"))
	if err != nil || c.ActivityPresence <= 0 {
		return c, fmt.Errorf("DESKYARD_ACTIVITY_PRESENCE must be a positive duration")
	}
	c.Subnet, err = netip.ParsePrefix(env("DESKYARD_SUBNET", "10.213.0.0/16"))
	if err != nil {
		return c, fmt.Errorf("DESKYARD_SUBNET: %w", err)
	}
	if c.Subnet.Bits() > 28 {
		return c, fmt.Errorf("DESKYARD_SUBNET must be /28 or larger")
	}
	return c, nil
}
