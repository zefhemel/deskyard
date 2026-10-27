package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func str(s string) *string { return &s }

func validRemotes() map[RemoteType]struct {
	r   Remote
	sec Secrets
} {
	return map[RemoteType]struct {
		r   Remote
		sec Secrets
	}{
		S3:     {Remote{Name: "bucket", Type: S3, Provider: "Minio", Endpoint: "http://minio:9000", AccessKeyID: "AKIA", Root: "backups/deskyard"}, Secrets{SecretAccessKey: str("s3-secret")}},
		B2:     {Remote{Name: "bee", Type: B2, KeyID: "key-id", Root: "my-bucket"}, Secrets{ApplicationKey: str("b2-secret")}},
		SFTP:   {Remote{Name: "nas", Type: SFTP, Host: "nas.local", User: "deskyard", Root: "/volume1/backups"}, Secrets{Password: str("sftp-secret")}},
		WebDAV: {Remote{Name: "cloud", Type: WebDAV, URL: "https://cloud.example/remote.php/dav/files/me", Vendor: "nextcloud", User: "me"}, Secrets{Password: str("dav-secret")}},
	}
}

func TestRemoteValidate(t *testing.T) {
	for typ, v := range validRemotes() {
		if err := v.r.Validate(v.sec, true); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	bad := map[string]struct {
		r   Remote
		sec Secrets
	}{
		"bad name":          {Remote{Name: "A", Type: B2, KeyID: "k", Root: "b"}, Secrets{ApplicationKey: str("x")}},
		"unknown type":      {Remote{Name: "xx", Type: "ftp"}, Secrets{}},
		"s3 no endpoint":    {Remote{Name: "xx", Type: S3, Provider: "Minio", AccessKeyID: "a", Root: "b"}, Secrets{SecretAccessKey: str("x")}},
		"s3 no bucket":      {Remote{Name: "xx", Type: S3, Provider: "AWS", AccessKeyID: "a"}, Secrets{SecretAccessKey: str("x")}},
		"s3 no secret":      {Remote{Name: "xx", Type: S3, Provider: "AWS", AccessKeyID: "a", Root: "b"}, Secrets{}},
		"s3 bad provider":   {Remote{Name: "xx", Type: S3, Provider: "Acme", AccessKeyID: "a", Root: "b"}, Secrets{SecretAccessKey: str("x")}},
		"s3 rooted bucket":  {Remote{Name: "xx", Type: S3, Provider: "AWS", AccessKeyID: "a", Root: "/b"}, Secrets{SecretAccessKey: str("x")}},
		"b2 no key":         {Remote{Name: "xx", Type: B2, KeyID: "k", Root: "b"}, Secrets{}},
		"sftp no host":      {Remote{Name: "xx", Type: SFTP, User: "u"}, Secrets{Password: str("p")}},
		"sftp no auth":      {Remote{Name: "xx", Type: SFTP, Host: "h", User: "u"}, Secrets{}},
		"sftp bad port":     {Remote{Name: "xx", Type: SFTP, Host: "h", User: "u", Port: 70000}, Secrets{Password: str("p")}},
		"webdav bad url":    {Remote{Name: "xx", Type: WebDAV, URL: "ftp://x"}, Secrets{}},
		"newline injection": {Remote{Name: "xx", Type: SFTP, Host: "h\ntype = local", User: "u"}, Secrets{Password: str("p")}},
		"dotdot root":       {Remote{Name: "xx", Type: SFTP, Host: "h", User: "u", Root: "a/../b"}, Secrets{Password: str("p")}},
		"other type fields": {Remote{Name: "xx", Type: B2, KeyID: "k", Root: "b", Host: "h"}, Secrets{ApplicationKey: str("x")}},
	}
	for name, v := range bad {
		if err := v.r.Validate(v.sec, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// On update a secret left out keeps the stored one.
	sftp := validRemotes()[SFTP].r
	if err := sftp.Validate(Secrets{}, false); err != nil {
		t.Errorf("update without secrets: %v", err)
	}
}

func TestRcloneConfigS3(t *testing.T) {
	v := validRemotes()[S3]
	conf, err := v.r.RcloneConfig(v.sec.stored(StoredSecrets{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[bucket]\n", "type = s3\n", "provider = Minio\n", "endpoint = http://minio:9000\n", "access_key_id = AKIA\n", "secret_access_key = s3-secret\n"} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}
}

func TestRcloneConfigB2(t *testing.T) {
	v := validRemotes()[B2]
	conf, err := v.r.RcloneConfig(v.sec.stored(StoredSecrets{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[bee]\n", "type = b2\n", "account = key-id\n", "key = b2-secret\n"} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}
}

func TestRcloneConfigSFTPObscuresPassword(t *testing.T) {
	v := validRemotes()[SFTP]
	v.r.HostKey = "nas.local ssh-ed25519 AAAAC3Nza"
	stored := v.sec.stored(StoredSecrets{})
	stored.PrivateKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n"
	conf, err := v.r.RcloneConfig(stored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(conf, "sftp-secret") {
		t.Fatalf("plain password in\n%s", conf)
	}
	for _, want := range []string{"type = sftp\n", "host = nas.local\n", "port = 22\n", "user = deskyard\n", "pass = ", `key_pem = -----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----` + "\n", "known_hosts_file = " + RemoteMount + "/known_hosts\n", "shell_type = none\n"} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}
	line := conf[strings.Index(conf, "pass = ")+len("pass = "):]
	line = line[:strings.IndexByte(line, '\n')]
	if plain, err := reveal(line); err != nil || plain != "sftp-secret" {
		t.Fatalf("obscured password %q reveals %q, %v", line, plain, err)
	}
}

func TestRcloneConfigWebDAV(t *testing.T) {
	v := validRemotes()[WebDAV]
	conf, err := v.r.RcloneConfig(v.sec.stored(StoredSecrets{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[cloud]\n", "type = webdav\n", "url = https://cloud.example/remote.php/dav/files/me\n", "vendor = nextcloud\n", "user = me\n", "pass = "} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "dav-secret") {
		t.Fatal("plain password in config")
	}
}

func TestObscureRoundTrip(t *testing.T) {
	for _, plain := range []string{"", "x", "a much longer password with spaces & symbols ✓"} {
		ob, err := Obscure(plain)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := reveal(ob); err != nil || got != plain {
			t.Errorf("%q: revealed %q, %v", plain, got, err)
		}
	}
	a, _ := Obscure("same")
	b, _ := Obscure("same")
	if a == b {
		t.Error("obscure uses no random IV")
	}
}

func TestRemotesStorePersistsAndKeepsSecrets(t *testing.T) {
	dir := t.TempDir()
	path, secret := filepath.Join(dir, "storage-remotes.json"), filepath.Join(dir, "secret")
	rs, err := LoadRemotes(path, secret)
	if err != nil {
		t.Fatal(err)
	}
	v := validRemotes()[SFTP]
	v.r.Created = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	if err := rs.Add(v.r, v.sec); err != nil {
		t.Fatal(err)
	}
	if err := rs.Add(v.r, v.sec); err == nil {
		t.Fatal("added a name twice")
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "sftp-secret") {
		t.Fatalf("secret in %s", b)
	}
	cfgDir := rs.ConfigDir("nas")
	for p, mode := range map[string]os.FileMode{cfgDir: 0o700 | os.ModeDir, filepath.Join(cfgDir, "rclone.conf"): 0o600, filepath.Join(cfgDir, "secrets.json"): 0o600} {
		st, err := os.Stat(p)
		if err != nil || st.Mode() != mode {
			t.Errorf("%s: %v %v", p, st.Mode(), err)
		}
	}
	again, err := LoadRemotes(path, secret)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := again.Get("nas")
	if !ok || got.Host != "nas.local" || !got.Created.Equal(v.r.Created) {
		t.Fatalf("reloaded %+v", got)
	}
	if set := again.SecretsSet("nas"); len(set) != 1 || set[0] != "password" {
		t.Fatalf("secrets set %v", set)
	}
	// An update without the password keeps it; the name and type are fixed.
	changed := got
	changed.User = "other"
	if err := again.Update("nas", changed, Secrets{}); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(cfgDir, "rclone.conf"))
	if !strings.Contains(string(conf), "user = other") || !strings.Contains(string(conf), "pass = ") {
		t.Fatalf("updated config:\n%s", conf)
	}
	changed.Type = WebDAV
	if err := again.Update("nas", changed, Secrets{}); err == nil {
		t.Fatal("changed the type")
	}
	if err := again.Delete("nas"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfgDir); !os.IsNotExist(err) {
		t.Fatal("secrets left behind")
	}
}

func TestRemotesStrictLoad(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field": `{"version":1,"remotes":[{"name":"nas","type":"sftp","host":"h","user":"u","created":"2026-10-27T09:00:00Z","color":"red"}]}`,
		"duplicate":     `{"version":1,"remotes":[{"name":"nas","type":"sftp","host":"h","user":"u","created":"2026-10-27T09:00:00Z"},{"name":"nas","type":"sftp","host":"h","user":"u","created":"2026-10-27T09:00:00Z"}]}`,
		"bad name":      `{"version":1,"remotes":[{"name":"NAS","type":"sftp","host":"h","user":"u","created":"2026-10-27T09:00:00Z"}]}`,
		"version":       `{"version":2,"remotes":[]}`,
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "storage-remotes.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRemotes(path, filepath.Join(dir, "secret")); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestSecretsNotMarshalable(t *testing.T) {
	if _, err := json.Marshal(Secrets{Password: str("x")}); err == nil {
		t.Fatal("secrets marshalled")
	}
}
