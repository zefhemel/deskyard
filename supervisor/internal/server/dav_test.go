package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
	"github.com/zefhemel/deskyard/supervisor/internal/environments"
)

// davServer is a test server whose environment alice has WebDAV on, with
// the volumes projects (writable), photos (read-only) and a host volume.
func davServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s, ts := newTestServer(t)
	now := time.Now().UTC()
	for _, v := range []environments.Volume{
		{Name: "projects", Created: now, Kind: environments.VolumeLocal},
		{Name: "photos", Created: now, Kind: environments.VolumeLocal},
		{Name: "outside", Created: now, Kind: environments.VolumeHost, HostPath: t.TempDir()},
	} {
		if err := s.Environments.Vols.Add(v); err != nil {
			t.Fatal(err)
		}
		if v.Kind == environments.VolumeLocal {
			if err := os.MkdirAll(s.Environments.Layout.VolumeOf(v.Name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.Environments.Defs.Update("alice", func(d *environments.Definition) error {
		d.Volumes = []environments.VolumeGrant{{Name: "projects"}, {Name: "photos", ReadOnly: true}, {Name: "outside", Path: "/srv/outside"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Environments.SetWebDAV("alice", true); err != nil {
		t.Fatal(err)
	}
	return s, ts
}

// dav makes a WebDAV request with Basic auth (none when user is "").
func dav(t *testing.T, ts *httptest.Server, method, path, user, password string, body string, header map[string]string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, ts.URL+path, r)
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func davBody(t *testing.T, ts *httptest.Server, method, path, user, password string, header map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, nil)
	req.SetBasicAuth(user, password)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// forgive clears failed attempts, so a test's deliberate failures don't
// throttle what follows.
func forgive(s *Server, usernames ...string) {
	s.Throttle.Succeed("ip:127.0.0.1")
	for _, u := range usernames {
		s.Throttle.Succeed("u:" + u)
	}
}

func TestDAVWhoMayConnect(t *testing.T) {
	s, ts := davServer(t)
	depth := map[string]string{"Depth": "1"}

	res := dav(t, ts, "PROPFIND", "/alice/dav/", "", "", "", depth)
	if res.StatusCode != 401 || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("without credentials: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if res := dav(t, ts, "PROPFIND", "/alice/dav/", "admin", "wrong", "", depth); res.StatusCode != 401 {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}
	// A browser session is not enough.
	if got := status(t, login(t, ts, "admin"), "GET", ts.URL+"/alice/dav/workspace/", ""); got != 401 {
		t.Fatalf("with a session: %d", got)
	}
	code, body := davBody(t, ts, "PROPFIND", "/alice/dav/", "admin", "pw", depth)
	if code != 207 || !strings.Contains(body, "/alice/dav/workspace/") || !strings.Contains(body, "/alice/dav/share/") || strings.Contains(body, "/alice/dav/srv/") {
		t.Fatalf("admin: %d %s", code, body)
	}
	code, body = davBody(t, ts, "PROPFIND", "/alice/dav/share/", "admin", "pw", depth)
	if code != 207 || !strings.Contains(body, "/alice/dav/share/photos/") || !strings.Contains(body, "/alice/dav/share/projects/") {
		t.Fatalf("share: %d %s", code, body)
	}

	// anna: nothing, then observe, then full.
	for level, want := range map[auth.Level]int{auth.LevelNone: 404, auth.LevelObserve: 404, auth.LevelFull: 207} {
		levels := map[string]auth.Level{}
		if level != auth.LevelNone {
			levels["anna"] = level
		}
		if err := s.Accounts.SetAccess("alice", levels); err != nil {
			t.Fatal(err)
		}
		if res := dav(t, ts, "PROPFIND", "/alice/dav/", "anna", "pw", "", depth); res.StatusCode != want {
			t.Fatalf("anna with %q: %d, want %d", level, res.StatusCode, want)
		}
	}
	// A token account with full use has no password to give.
	if _, err := s.Accounts.Create(accounts.NewAccount{Username: "sanne", Grants: []accounts.Grant{{Environment: "alice", Level: auth.LevelFull}}}); err != nil {
		t.Fatal(err)
	}
	if res := dav(t, ts, "PROPFIND", "/alice/dav/", "sanne", "", "", depth); res.StatusCode != 401 {
		t.Fatalf("token account: %d", res.StatusCode)
	}
	forgive(s, "admin", "sanne")
	// Switched off, or no such environment: 404.
	if err := s.Environments.SetWebDAV("alice", false); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/alice/dav/", "/nobody/dav/"} {
		if res := dav(t, ts, "PROPFIND", p, "admin", "pw", "", depth); res.StatusCode != 404 {
			t.Fatalf("%s: %d", p, res.StatusCode)
		}
	}
}

func TestDAVFiles(t *testing.T) {
	s, ts := davServer(t)
	home := s.Environments.Layout.HomeOf("alice")
	projects := s.Environments.Layout.VolumeOf("projects")
	write := func(path, content string) int {
		return dav(t, ts, "PUT", path, "admin", "pw", content, nil).StatusCode
	}

	if got := write("/alice/dav/workspace/page.html", "<script>alert(1)</script>"); got != 201 {
		t.Fatal(got)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "page.html")); string(b) != "<script>alert(1)</script>" {
		t.Fatalf("page.html = %q", b)
	}
	for _, method := range []string{"GET", "HEAD"} {
		res := dav(t, ts, method, "/alice/dav/workspace/page.html", "admin", "pw", "", nil)
		h := res.Header
		if res.StatusCode != 200 || h.Get("Content-Disposition") != "attachment" || h.Get("Content-Security-Policy") != "sandbox" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: %d %v", method, res.StatusCode, h)
		}
	}

	// Read-only and synthesized places refuse with 403.
	for _, p := range []string{"/alice/dav/share/photos/x.txt", "/alice/dav/share/x.txt", "/alice/dav/x.txt"} {
		if got := write(p, "x"); got != 403 {
			t.Fatalf("PUT %s: %d", p, got)
		}
	}
	for _, p := range []string{"/alice/dav/share/new", "/alice/dav/share/photos/new"} {
		if got := dav(t, ts, "MKCOL", p, "admin", "pw", "", nil).StatusCode; got != 403 {
			t.Fatalf("MKCOL %s: %d", p, got)
		}
	}
	if got := dav(t, ts, "DELETE", "/alice/dav/workspace", "admin", "pw", "", nil).StatusCode; got != 403 {
		t.Fatalf("DELETE workspace: %d", got)
	}
	if got := dav(t, ts, "MOVE", "/alice/dav/workspace/page.html", "admin", "pw", "", map[string]string{"Destination": ts.URL + "/alice/dav/share/photos/page.html"}).StatusCode; got != 403 {
		t.Fatalf("MOVE into read-only: %d", got)
	}

	// Moving between mounts copies, then removes the source.
	if got := dav(t, ts, "MOVE", "/alice/dav/workspace/page.html", "admin", "pw", "", map[string]string{"Destination": ts.URL + "/alice/dav/share/projects/page.html"}).StatusCode; got != 201 {
		t.Fatalf("MOVE across: %d", got)
	}
	if _, err := os.Stat(filepath.Join(projects, "page.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "page.html")); err == nil {
		t.Fatal("the source stayed")
	}

	// macOS metadata is accepted and dropped.
	if got := write("/alice/dav/workspace/._page.html", "attrs"); got != 201 {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(home, "._page.html")); err == nil {
		t.Fatal("._page.html was stored")
	}

}

func TestDAVPassword(t *testing.T) {
	s, ts := davServer(t)
	admin := login(t, ts, "admin")
	setPassword := func() string {
		t.Helper()
		req, _ := http.NewRequest("POST", ts.URL+"/api/me/webdav-password", nil)
		req.Header.Set("Origin", ts.URL)
		res, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out struct{ Password string }
		if res.StatusCode != 201 || json.NewDecoder(res.Body).Decode(&out) != nil || !strings.HasPrefix(out.Password, credentialPrefix) {
			t.Fatalf("set: %d %+v", res.StatusCode, out)
		}
		return out.Password
	}
	propfind := func(password string) int {
		t.Helper()
		return dav(t, ts, "PROPFIND", "/alice/dav/", "admin", password, "", map[string]string{"Depth": "0"}).StatusCode
	}

	first := setPassword()
	second := setPassword() // replaces the first
	if got := propfind(second); got != 207 {
		t.Fatalf("WebDAV password: %d", got)
	}
	if got := propfind(first); got != 401 {
		t.Fatalf("replaced password: %d", got)
	}
	if got := propfind("pw"); got != 401 {
		t.Fatalf("account password beside a WebDAV password: %d", got)
	}
	// It is listed with the devices, and opens nothing else.
	res, _ := admin.Get(ts.URL + "/api/me/credentials")
	var creds []credentialJSON
	_ = json.NewDecoder(res.Body).Decode(&creds)
	res.Body.Close()
	if len(creds) != 1 || creds[0].Kind != "webdav" {
		t.Fatalf("credentials: %+v", creds)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+second)
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 401 {
		t.Fatalf("WebDAV password as a bearer: %d", res.StatusCode)
	}
	forgive(s, "admin")

	// A CLI credential is no WebDAV password.
	a, _ := s.Accounts.Get("admin")
	_, cli, err := s.credentials.create(a, "cli", "deskyard CLI")
	if err != nil {
		t.Fatal(err)
	}
	if got := propfind(cli); got != 401 {
		t.Fatalf("CLI credential: %d", got)
	}
	forgive(s, "admin")

	// Removing it brings the account password back.
	if got := status(t, admin, "DELETE", ts.URL+"/api/me/webdav-password", ts.URL); got != 204 {
		t.Fatal(got)
	}
	if got := propfind("pw"); got != 207 {
		t.Fatalf("account password again: %d", got)
	}
	if got := propfind(second); got != 401 {
		t.Fatalf("removed password: %d", got)
	}
	forgive(s, "admin")

	// Signing out everywhere removes it.
	third := setPassword()
	if got := status(t, admin, "POST", ts.URL+"/api/me/sign-out-everywhere", ts.URL); got != 204 {
		t.Fatal(got)
	}
	if got := propfind(third); got != 401 {
		t.Fatalf("after signing out everywhere: %d", got)
	}
	forgive(s, "admin")

	// Accounts without a password can't have one.
	if _, err := s.Accounts.Create(accounts.NewAccount{Username: "sanne"}); err != nil {
		t.Fatal(err)
	}
	link := tokenLogin(t, s, ts, "sanne")
	req, _ = http.NewRequest("POST", ts.URL+"/api/me/webdav-password", nil)
	req.Header.Set("Origin", ts.URL)
	if res, _ := link.Do(req); res.StatusCode != 403 {
		t.Fatalf("token account: %d", res.StatusCode)
	}
}

func TestDAVPasswordChangeEndsTheRememberedOne(t *testing.T) {
	s, ts := davServer(t)
	propfind := func(password string) int {
		return dav(t, ts, "PROPFIND", "/alice/dav/", "admin", password, "", map[string]string{"Depth": "0"}).StatusCode
	}
	if got := propfind("pw"); got != 207 {
		t.Fatal(got)
	}
	if err := s.Accounts.SetPassword("admin", "new password"); err != nil {
		t.Fatal(err)
	}
	if got := propfind("pw"); got != 401 {
		t.Fatalf("the old password still works: %d", got)
	}
	if got := propfind("new password"); got != 207 {
		t.Fatal(got)
	}
}

func TestDAVSettingAPI(t *testing.T) {
	s, ts := davServer(t)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	if err := s.Accounts.SetAccess("alice", map[string]auth.Level{"anna": auth.LevelFull}); err != nil {
		t.Fatal(err)
	}
	put := func(c *http.Client, body string) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/environments/alice/webdav", strings.NewReader(body))
		req.Header.Set("Origin", ts.URL)
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := put(anna, `{"webdav":false}`); got != 403 {
		t.Fatal(got)
	}
	if got := put(admin, `{}`); got != 400 {
		t.Fatal(got)
	}
	if got := put(admin, `{"webdav":false}`); got != 204 {
		t.Fatal(got)
	}
	if def, _ := s.Environments.Defs.Get("alice"); def.WebDAV {
		t.Fatal("not saved")
	}
	if got := put(admin, `{"webdav":true}`); got != 204 {
		t.Fatal(got)
	}
	for client, want := range map[*http.Client]bool{admin: true, anna: true} {
		res, _ := client.Get(ts.URL + "/api/environments/alice")
		var c environmentJSON
		_ = json.NewDecoder(res.Body).Decode(&c)
		res.Body.Close()
		if c.WebDAV != want {
			t.Fatalf("webdav = %v", c.WebDAV)
		}
	}
}

// tokenLogin signs a browser in to the token account username directly.
func tokenLogin(t *testing.T, s *Server, ts *httptest.Server, username string) *http.Client {
	t.Helper()
	a, ok := s.Accounts.Get(username)
	if !ok {
		t.Fatal("no account", username)
	}
	rec := httptest.NewRecorder()
	s.issueSession(rec, a)
	jar := &cookieJar{}
	jar.SetCookies(nil, rec.Result().Cookies())
	return &http.Client{Jar: jar}
}

func TestDAVLocksAndOverwrites(t *testing.T) {
	s, ts := davServer(t)
	home := s.Environments.Layout.HomeOf("alice")
	projects := s.Environments.Layout.VolumeOf("projects")
	lockBody := `<?xml version="1.0" encoding="utf-8"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`

	// No locks on the tree's own folders or read-only places.
	for _, p := range []string{"/alice/dav/", "/alice/dav/workspace", "/alice/dav/share/photos/x"} {
		if got := dav(t, ts, "LOCK", p, "admin", "pw", lockBody, map[string]string{"Depth": "infinity"}).StatusCode; got != 403 {
			t.Fatalf("LOCK %s: %d", p, got)
		}
	}
	// A lock asked for forever lasts an hour.
	code, body := davBodyWith(t, ts, "LOCK", "/alice/dav/workspace/new.txt", lockBody, map[string]string{"Timeout": "Infinite"})
	if code != 200 && code != 201 || !strings.Contains(body, "Second-3600") {
		t.Fatalf("LOCK: %d %s", code, body)
	}

	// MOVE onto an existing file in another mount replaces it, leaving nothing behind.
	if err := os.WriteFile(filepath.Join(projects, "r.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "r.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dav(t, ts, "MOVE", "/alice/dav/workspace/r.txt", "admin", "pw", "", map[string]string{"Destination": ts.URL + "/alice/dav/share/projects/r.txt", "Overwrite": "T"}).StatusCode; got != 204 {
		t.Fatalf("MOVE: %d", got)
	}
	if b, _ := os.ReadFile(filepath.Join(projects, "r.txt")); string(b) != "new" {
		t.Fatalf("r.txt = %q", b)
	}
	if entries, _ := os.ReadDir(projects); len(entries) != 1 {
		t.Fatalf("projects holds %v", entries)
	}

	// A dropped macOS file can't replace a real one.
	if got := dav(t, ts, "MOVE", "/alice/dav/share/projects/._r.txt", "admin", "pw", "", map[string]string{"Destination": ts.URL + "/alice/dav/share/projects/r.txt", "Overwrite": "T"}).StatusCode; got != 403 {
		t.Fatalf("MOVE ._r.txt: %d", got)
	}
	if b, _ := os.ReadFile(filepath.Join(projects, "r.txt")); string(b) != "new" {
		t.Fatalf("r.txt = %q", b)
	}
}

func davBodyWith(t *testing.T, ts *httptest.Server, method, path, body string, header map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.SetBasicAuth("admin", "pw")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestDAVLockTimeouts(t *testing.T) {
	for header, long := range map[string]bool{"Second-60": false, "Second-3600": false, "Second-3601": true, "Infinite": true, "Infinite, Second-60": true, "Second-x": true, " Second-30 ": false} {
		if got := davLockTooLong(header); got != long {
			t.Errorf("%q: %v", header, got)
		}
	}
}
