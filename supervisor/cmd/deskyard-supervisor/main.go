// Command deskyard-supervisor runs desktops and shares them: one binary
// with its web UI built in. Configuration comes from DESKYARD_* variables.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/desktopcli"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/engine/docker"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
	"github.com/zefhemel/deskyard/supervisor/internal/server"
	"github.com/zefhemel/deskyard/supervisor/internal/tlsserve"
	"github.com/zefhemel/deskyard/supervisor/internal/vpn"
	"github.com/zefhemel/deskyard/supervisor/internal/web"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(version)
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Print(usage)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()}))
	if err := run(log); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

const usage = `deskyard-supervisor: runs desktops (Linux desktops in containers) and shares them in the browser.

Configuration (environment variables):
  DESKYARD_LISTEN          address for HTTP (default 0.0.0.0:7343)
  DESKYARD_DOMAIN          serve HTTPS on :443 (and :80) for this domain, with automatic certificates
  DESKYARD_DATA_DIR        no longer a setting: mount the data folder at /data
  DESKYARD_ENGINE_HOST     container engine socket (default DOCKER_HOST, else the platform default)
  DESKYARD_SUBNET          address pool for the desktops' networks (default 10.213.0.0/16)
  DESKYARD_ADMIN_USER      the first admin's username (default admin)
  DESKYARD_ADMIN_PASSWORD  the first admin's password (default: generated and printed once)
  DESKYARD_IMAGE           the first image in the catalog (default ghcr.io/zefhemel/deskyard-debian-xfce:latest)
  DESKYARD_LOG             debug, info (default), warn
`

func logLevel() slog.Level {
	switch strings.ToLower(os.Getenv("DESKYARD_LOG")) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Before anything is written: /data must be a mounted folder, and an
	// older layout is refused, not adopted.
	if err := checkDataDir(config.DataDir); err != nil {
		return err
	}
	if err := config.CheckLegacy(config.DataDir); err != nil {
		return err
	}
	dockerEng, err := docker.New(cfg.EngineHost)
	if err != nil {
		return fmt.Errorf("container engine: %w", err)
	}
	var eng engine.Engine = dockerEng
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := eng.Info(ctx); err != nil {
		return engineError(cfg.EngineHost, err)
	}
	// The engine resolves mounts on the host, so find where /data lives there.
	// The container ID is the hostname, which a user's --hostname or host
	// networking would change: then the supervisor can't find itself and says
	// so. Outside a container it never finds itself, and stops here, before
	// the root phase changes anything.
	selfID, err := os.Hostname()
	if err != nil {
		return err
	}
	self, err := eng.Inspect(ctx, selfID)
	if err != nil {
		return fmt.Errorf("can't find the supervisor's own container %q: it runs only in a container (see the README for the docker run command), whose hostname is the container ID (so don't set --hostname or use --network host): %w", selfID, err)
	}
	host, err := dataHostPath(self)
	if err != nil {
		return err
	}
	// The root phase comes before the stores and the listener.
	if os.Getuid() == 0 {
		if err := rootPhase(cfg); err != nil {
			return err
		}
	}
	if os.Getuid() != config.UID || os.Getgid() != config.GID {
		return fmt.Errorf("the supervisor must run as UID:GID %d:%d (or start as root to drop to it), not %d:%d", config.UID, config.GID, os.Getuid(), os.Getgid())
	}
	// Only the engine translates mounts; everything else keeps the /data
	// layout.
	layout := cfg.Layout.WithHost(host)
	eng = engine.WithDataFolder(eng, layout.Data, layout.Host())
	// Nor are an older supervisor's containers that use this data folder.
	if err := environments.New(eng, environments.Config{Layout: layout}, log).CheckLegacyContainers(ctx); err != nil {
		return err
	}
	if err := layout.Prepare(); err != nil {
		return err
	}
	if err := desktopcli.Publish(desktopcli.Source, layout.State); err != nil {
		return err
	}
	instance, err := config.InstanceID(layout)
	if err != nil {
		return err
	}

	accts, err := accounts.Load(layout.Data)
	if err != nil {
		return err
	}
	generated, err := accts.EnsureAdmin(cfg.AdminUser, cfg.AdminPassword)
	if err != nil {
		return err
	}
	if generated != "" {
		fmt.Fprintf(os.Stderr, "\n  First start: an admin account was created.\n  Username: %s\n  Password: %s\n  (Shown once. Change it after signing in.)\n\n", cfg.AdminUser, generated)
	}

	comps, err := environments.Open(eng, environments.Config{
		Layout: layout, Subnet: cfg.Subnet.String(), SelfID: selfID, DesktopPort: environments.DesktopPort,
	}, instance, log)
	if err != nil {
		return err
	}
	vpnStore, err := vpn.NewStore(filepath.Join(layout.Data, "networks.json"), layout.Secret)
	if err != nil {
		return err
	}
	vpnRuntime := vpn.NewManager(eng, vpnStore, layout.State).ForInstance(instance)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			cleanup, cancel := context.WithTimeout(ctx, 20*time.Second)
			vpnRuntime.RetryRetirements(cleanup)
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	if err := comps.ConfigureNetworking(vpnStore, vpnRuntime, layout.State); err != nil {
		return err
	}
	if err := comps.Reconcile(ctx); err != nil {
		log.Warn("reconciling environments", "err", err)
	}

	ui, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}
	srv, err := server.New(server.Options{
		StartupContext: ctx,
		Log:            log, Eng: eng, Environments: comps, Accounts: accts, Layout: layout,
		UI: ui, Secure: cfg.Domain != "", DefaultImage: cfg.DefaultImage,
		ActivityPresence: cfg.ActivityPresence,
	})
	if err != nil {
		return err
	}
	srv.Run(ctx)

	if cfg.Domain != "" {
		log.Info("serving HTTPS", "domain", cfg.Domain, "data", layout.Data, "instance", instance, "version", version)
		return tlsserve.Serve(ctx, srv, cfg.Domain, filepath.Join(layout.Secret, "certmagic"), log)
	}
	hs := &http.Server{Addr: cfg.Listen, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("listening", "url", "http://"+cfg.Listen, "data", layout.Data, "instance", instance, "version", version)
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// checkDataDir requires the data folder to exist as a directory: a bind
// mount at /data, which the engine creates on the host if need be.
func checkDataDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory: mount the data folder with -v <host folder>:/data", dir)
	}
	return nil
}

// engineError is err, from the engine's first call, explained. A process
// started without root (--user) must have the unix socket's group itself:
// when the socket refuses it, say so.
func engineError(engineHost string, err error) error {
	if path, ok := config.SocketPath(engineHost); ok {
		if c, dialErr := net.DialTimeout("unix", path, 5*time.Second); dialErr == nil {
			_ = c.Close()
		} else if errors.Is(dialErr, fs.ErrPermission) {
			group := ""
			if gid, ok, gerr := config.SocketGroup(engineHost); gerr == nil && ok {
				group = fmt.Sprintf(" (group %d)", gid)
			}
			return fmt.Errorf("this user may not use the container engine socket %s%s: add its group to the container (--group-add), or start as root, which adds it automatically", path, group)
		}
	}
	return fmt.Errorf("can't reach the container engine (is Docker running?): %w", err)
}

// rootPhase runs while the supervisor is still root: it fixes the ownership
// of a data folder an older, root supervisor made, finds the engine socket's
// group, and drops to UID 1000 with that group.
func rootPhase(cfg config.Config) error {
	if err := config.ReownOnce(cfg.Layout); err != nil {
		return fmt.Errorf("giving %s to UID %d: %w", cfg.Layout.Data, config.UID, err)
	}
	gid, hasSocket, err := config.SocketGroup(cfg.EngineHost)
	if err != nil {
		return err
	}
	return dropPrivileges(gid, hasSocket)
}
