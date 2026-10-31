package vpn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailscaleConnectionCredentials(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "networks.json"), filepath.Join(dir, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	var in Input
	if err = json.Unmarshal([]byte(`{"name":"Home tailnet","provider":"tailscale","tailscale":{"tailnet":"example.ts.net","clientId":"client123","clientSecret":"private-oauth-secret","tags":["tag:deskyard"]}}`), &in); err != nil {
		t.Fatal(err)
	}
	c, err := s.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), "private-oauth-secret") {
		t.Fatal("secret in DTO")
	}
	metadata, _ := os.ReadFile(filepath.Join(dir, "networks.json"))
	if strings.Contains(string(metadata), "private-oauth-secret") {
		t.Fatal("secret in metadata")
	}
	var edit Input
	json.Unmarshal([]byte(`{"name":"Renamed","provider":"tailscale","tailscale":{"tailnet":"example.ts.net","clientId":"client123","tags":["tag:deskyard"]}}`), &edit)
	c, err = s.Update(c.ID, edit)
	if err != nil || c.Revision != 2 {
		t.Fatalf("retained credentials: %+v %v", c, err)
	}
	files, err := s.SecretFiles(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := os.ReadFile(files.Password)
	if string(secret) != "private-oauth-secret" {
		t.Fatal("omission erased secret")
	}
	if _, err = NewStore(filepath.Join(dir, "networks.json"), filepath.Join(dir, "secret")); err != nil {
		t.Fatal("reload:", err)
	}
	edit.Provider = "nordvpn"
	edit.Country = "Germany"
	if _, err = s.Update(c.ID, edit); err == nil {
		t.Fatal("provider mutation accepted")
	}
}
func TestTailscaleRejectsConflictingInput(t *testing.T) {
	for _, body := range []string{
		`{"name":"TS","provider":"tailscale","country":"Germany","tailscale":{"tailnet":"x","clientId":"id","clientSecret":"secret","tags":["tag:desk"]}}`,
		`{"name":"TS","provider":"tailscale","tailscale":{"tailnet":"x","clientId":"id","clientSecret":"secret","tags":[]}}`,
		`{"name":"TS","provider":"tailscale","tailscale":{"tailnet":"../x","clientId":"id","clientSecret":"secret","tags":["tag:desk"]}}`,
	} {
		var in Input
		json.Unmarshal([]byte(body), &in)
		if _, err := normalizeInput(in, true); err == nil {
			t.Fatal("accepted invalid provider config")
		}
	}
}
