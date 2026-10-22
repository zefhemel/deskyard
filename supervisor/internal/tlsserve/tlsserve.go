// Package tlsserve serves HTTPS with certificates from Let's Encrypt
// (CertMagic, Caddy's ACME library): :443 for the site, :80 for ACME
// challenges and redirects to HTTPS.
package tlsserve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/caddyserver/certmagic"
)

func Serve(ctx context.Context, h http.Handler, domain, storageDir string, log *slog.Logger) error {
	certmagic.DefaultACME.Agreed = true
	cfg := certmagic.NewDefault()
	cfg.Storage = &certmagic.FileStorage{Path: storageDir}
	if err := cfg.ManageAsync(ctx, []string{domain}); err != nil {
		return err
	}
	acme := certmagic.NewACMEIssuer(cfg, certmagic.DefaultACME)
	tlsConfig := cfg.TLSConfig()
	tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)

	redirect := acme.HTTPChallengeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+domain+r.URL.RequestURI(), http.StatusMovedPermanently)
	}))
	plain := &http.Server{Addr: ":80", Handler: redirect, ReadHeaderTimeout: 10 * time.Second}
	secure := &http.Server{Addr: ":443", Handler: h, TLSConfig: tlsConfig, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = plain.Shutdown(sctx)
		_ = secure.Shutdown(sctx)
	}()
	go func() {
		if err := plain.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP on :80", "err", err)
		}
	}()
	if err := secure.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
