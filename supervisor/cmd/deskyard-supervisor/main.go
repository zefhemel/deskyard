// Command deskyard-supervisor runs desktops and shares them: one binary
// with its web UI built in. Configuration comes from DESKYARD_* variables.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/config"
	"github.com/zefhemel/deskyard/supervisor/internal/desktops"
	"github.com/zefhemel/deskyard/supervisor/internal/engine"
	"github.com/zefhemel/deskyard/supervisor/internal/engine/docker"
	"github.com/zefhemel/deskyard/supervisor/internal/server"
	"github.com/zefhemel/deskyard/supervisor/internal/tlsserve"
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
  DESKYARD_LISTEN          address for HTTP (default 127.0.0.1:8080)
  DESKYARD_DOMAIN          serve HTTPS on :443 (and :80) for this domain, with automatic certificates
  DESKYARD_DATA_DIR        workspace folders and state (default ~/deskyard; required in a container)
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
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
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

	selfID := ""
	if cfg.InContainer {
		if selfID, err = os.Hostname(); err != nil {
			return err
		}
		if err := checkSamePath(ctx, eng, cfg.DataDir); err != nil {
			return err
		}
	}

	accts, err := accounts.Load(cfg.StateDir)
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

	comps := desktops.New(eng, desktops.Config{
		DataDir: cfg.DataDir, Subnet: cfg.Subnet.String(), InContainer: cfg.InContainer, SelfID: selfID,
		LocalEngine: desktops.LocalEngine(cfg.EngineHost),
	}, log)
	if err := comps.Reconcile(ctx); err != nil {
		log.Warn("reconciling desktops (networks, gone cameras)", "err", err)
	}

	ui, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}
	srv, err := server.New(server.Options{
		Log: log, Eng: eng, Desktops: comps, Accounts: accts, StateDir: cfg.StateDir,
		UI: ui, Secure: cfg.Domain != "", DefaultImage: cfg.DefaultImage,
		ActivityPresence: cfg.ActivityPresence,
	})
	if err != nil {
		return err
	}
	srv.Run(ctx)

	if cfg.Domain != "" {
		log.Info("serving HTTPS", "domain", cfg.Domain, "data", cfg.DataDir, "version", version)
		return tlsserve.Serve(ctx, srv, cfg.Domain, filepath.Join(cfg.StateDir, "certmagic"), log)
	}
	hs := &http.Server{Addr: cfg.Listen, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("listening", "url", "http://"+cfg.Listen, "data", cfg.DataDir, "version", version)
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// checkSamePath verifies that the data folder is mounted at the same path in
// the supervisor's container as on the host (bind-mount paths given to the
// engine are host paths): a helper container mounts that path and must see
// a marker the supervisor just wrote.
func checkSamePath(ctx context.Context, eng engine.Engine, dataDir string) error {
	token := fmt.Sprintf("%d", time.Now().UnixNano())
	marker := filepath.Join(dataDir, ".deskyard", "same-path-check")
	if err := os.WriteFile(marker, []byte(token), 0o600); err != nil {
		return err
	}
	defer os.Remove(marker)
	const img = "alpine:3.22"
	if _, err := eng.ImageInspect(ctx, img); errors.Is(err, engine.ErrNotFound) {
		if err := eng.Pull(ctx, img, nil); err != nil {
			return err
		}
	}
	name := "deskyard-pathcheck-" + token[len(token)-6:]
	if _, err := eng.Create(ctx, engine.ContainerSpec{
		Name: name, Image: img, Cmd: []string{"sleep", "30"},
		Mounts: []engine.Mount{{Source: dataDir, Target: "/check"}},
	}); err != nil {
		return err
	}
	defer func() { _ = eng.Remove(context.WithoutCancel(ctx), name) }()
	if err := eng.Start(ctx, name); err != nil {
		return err
	}
	out, _, _, err := engine.Output(ctx, eng, name, engine.ExecSpec{Cmd: []string{"cat", "/check/.deskyard/same-path-check"}})
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != token {
		return fmt.Errorf("DESKYARD_DATA_DIR (%s) must be mounted into the supervisor's container at the same path as on the host (-v %s:%s)", dataDir, dataDir, dataDir)
	}
	return nil
}
