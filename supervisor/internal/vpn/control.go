package vpn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

func prepareRuntime(config runtimeConfig, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	key := hex.EncodeToString(token)
	// Unmatched routes retain Gluetun's deny-by-default role, including settings
	// and every mutating endpoint. The shared desktop cannot read these mounts.
	auth := "[[roles]]\nname = \"deskyard-observer\"\nroutes = [\"GET /v1/vpn/status\", \"GET /v1/publicip/ip\"]\nauth = \"apikey\"\napikey = \"" + key + "\"\n"
	for name, data := range map[string]string{"auth.toml": auth, "post-rules.txt": config.Firewall.text(), "verify.sh": config.Firewall.verification() + " && " + config.Firewall.verifyRoutes()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) ready(ctx context.Context, id string, policy firewallPolicy) error {
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if m.probe(readyCtx, id, policy.verification()+" && "+policy.installRoutes()+" && "+policy.verifyRoutes()+" && ("+controlProbe+")") == nil {
			return nil
		}
		select {
		case <-readyCtx.Done():
			return errors.New("VPN firewall/control readiness failed")
		case <-ticker.C:
		}
	}
}

const controlProbe = `key=$(sed -n 's/^apikey = "\(.*\)"$/\1/p' /gluetun/auth/config.toml); test -n "$key" && wget -qO- -T 2 --header "X-API-Key: $key" http://127.0.0.1:8000/v1/vpn/status >/dev/null`

func (m *Manager) probe(ctx context.Context, id, command string) error {
	_, _, code, err := engine.Output(ctx, m.eng, id, engine.ExecSpec{User: "root", Cmd: []string{"sh", "-ec", command}})
	if err != nil || code != 0 {
		return errors.New("VPN managed probe failed")
	}
	return nil
}
