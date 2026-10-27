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
	if !cfg.InContainer {
		return errors.New("the supervisor runs only in a container: see the README for the docker run command")
	}
	// The root phase comes before the engine, the stores and the listener.
	if os.Getuid() == 0 {
		if err := rootPhase(cfg); err != nil {
			return err
		}
	}
	if os.Getuid() != config.UID || os.Getgid() != config.GID {
		return fmt.Errorf("the supervisor must run as UID:GID %d:%d (or start as root to drop to it), not %d:%d", config.UID, config.GID, os.Getuid(), os.Getgid())
	}
	if err := checkSocketAccess(cfg.EngineHost); err != nil {
		return err
	}
	// Before anything is written: an older layout is refused, not adopted.
	if err := config.CheckLegacy(cfg.DataDir); err != nil {
		return err
	}
	eng, err := docker.New(cfg.EngineHost)
	if err != nil {
		return fmt.Errorf("container engine: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := eng.Info(ctx); err != nil {
		return fmt.Errorf("can't reach the container engine (is Docker running, and may this user use its socket?): %w", err)
	}
	// Nor are an older supervisor's containers that use this data folder.
	if err := environments.New(eng, environments.Config{DataDir: cfg.DataDir}, log).CheckLegacyContainers(ctx); err != nil {
		return err
	}
	if err := cfg.Layout.Prepare(); err != nil {
		return err
	}
	instance, err := config.InstanceID(cfg.Layout)
	if err != nil {
		return err
	}

	selfID, err := os.Hostname()
	if err != nil {
		return err
	}

	accts, err := accounts.Load(cfg.DataDir)
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
		DataDir: cfg.DataDir, Subnet: cfg.Subnet.String(), SelfID: selfID,
	}, instance, log)
	if err != nil {
		return err
	}
	vpnStore, err := vpn.NewStore(filepath.Join(cfg.DataDir, "networks.json"), cfg.Layout.Secret)
	if err != nil {
		return err
	}
	vpnRuntime := vpn.NewManager(eng, vpnStore, cfg.Layout.State).ForInstance(instance)
	if err := comps.ConfigureNetworking(vpnStore, vpnRuntime, cfg.Layout.State); err != nil {
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
		Log:            log, Eng: eng, Environments: comps, Accounts: accts, Layout: cfg.Layout,
		UI: ui, Secure: cfg.Domain != "", DefaultImage: cfg.DefaultImage,
		ActivityPresence: cfg.ActivityPresence,
	})
	if err != nil {
		return err
	}
	srv.Run(ctx)

	if cfg.Domain != "" {
		log.Info("serving HTTPS", "domain", cfg.Domain, "data", cfg.DataDir, "instance", instance, "version", version)
		return tlsserve.Serve(ctx, srv, cfg.Domain, filepath.Join(cfg.Layout.Secret, "certmagic"), log)
	}
	hs := &http.Server{Addr: cfg.Listen, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("listening", "url", "http://"+cfg.Listen, "data", cfg.DataDir, "instance", instance, "version", version)
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

// checkSocketAccess explains a refused connection to the engine's unix
// socket: a process started without root (--user) must have the socket's
// group itself. A TCP engine host has no such check.
func checkSocketAccess(engineHost string) error {
	path, ok := config.SocketPath(engineHost)
	if !ok {
		return nil
	}
	c, err := net.DialTimeout("unix", path, 5*time.Second)
	if err == nil {
		return c.Close()
	}
	if !errors.Is(err, fs.ErrPermission) {
		return nil // missing or down: the engine check reports it
	}
	group := ""
	if gid, ok, gerr := config.SocketGroup(engineHost); gerr == nil && ok {
		group = fmt.Sprintf(" (group %d)", gid)
	}
	return fmt.Errorf("this user may not use the container engine socket %s%s: add its group to the container (--group-add), or start as root, which adds it automatically", path, group)
}

// rootPhase runs while the supervisor is still root: it fixes the ownership
// of a data folder an older, root supervisor made, finds the engine socket's
// group, and drops to UID 1000 with that group.
func rootPhase(cfg config.Config) error {
	if err := checkDataDir(cfg.DataDir); err != nil {
		return err
	}
	if err := config.ReownOnce(cfg.Layout); err != nil {
		return fmt.Errorf("giving %s to UID %d: %w", cfg.DataDir, config.UID, err)
	}
	gid, hasSocket, err := config.SocketGroup(cfg.EngineHost)
	if err != nil {
		return err
	}
	return dropPrivileges(gid, hasSocket)
}
