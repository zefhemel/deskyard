package environments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/store"
)

func (s *Service) tokenPath(name string) string {
	return filepath.Join(s.Layout.Secret, "proxy", name)
}

// proxyToken is the token the proxy sends to name's Caddy, kept in
// secret/proxy/<name> and made on first use.
func (s *Service) proxyToken(name string) (string, error) {
	b, err := os.ReadFile(s.tokenPath(name))
	if err == nil && len(bytes.TrimSpace(b)) > 0 {
		return string(bytes.TrimSpace(b)), nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	token := randomHex(32)
	if err := store.WriteFile(s.tokenPath(name), []byte(token+"\n")); err != nil {
		return "", err
	}
	return token, nil
}

// WaitReady waits until the environment's desktop answers through its Caddy.
func (s *Service) WaitReady(ctx context.Context, name string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	client := &http.Client{Timeout: 3 * time.Second}
	for {
		addr, token, err := s.Target(ctx, name)
		if err == nil {
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/", nil)
			req.Header.Set("X-Deskyard-Token", token)
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		} else if errors.Is(err, ErrStopped) {
			if c, _ := s.Get(ctx, name); c.Container.State == "exited" {
				return fmt.Errorf("%s stopped while starting (see its logs: docker logs %s)", name, containerName(name))
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s didn't come up within %s", name, limit)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Target is where the proxy reaches a running environment's port 8080, and the
// token it must send.
func (s *Service) Target(ctx context.Context, name string) (addr, token string, err error) {
	c, err := s.Get(ctx, name)
	if err != nil {
		return "", "", err
	}
	if !c.Container.Running() {
		return "", "", ErrStopped
	}
	token = c.Container.Env[TokenEnv]
	if c.Settings.VPNConnectionID != "" {
		if s.Networking == nil {
			return "", "", errVPNUnavailable
		}
		g, ok := s.Networking.target(name)
		if !ok {
			return "", "", errors.New("VPN gateway unavailable")
		}
		c.Container = g
	}
	port := strconv.Itoa(s.Cfg.DesktopPort)
	if ip := c.Container.IP[containerName(name)]; ip != "" {
		return net.JoinHostPort(ip, port), token, nil
	}
	return net.JoinHostPort(containerName(name), port), token, nil
}
