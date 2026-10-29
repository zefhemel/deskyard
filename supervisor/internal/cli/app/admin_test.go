package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/clitest"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// adminHarness signs in to a fake whose administrator routes answer from
// f.Answers.
func adminHarness(t *testing.T) (*harness, *clitest.Fake) {
	f := clitest.New(t, "alice")
	h := newHarness(t)
	h.save("srv", f, "alice")
	return h, f
}

// lastCall is the fake's last recorded call.
func lastCall(t *testing.T, f *clitest.Fake) clitest.Call {
	t.Helper()
	calls := f.Calls()
	if len(calls) == 0 {
		t.Fatal("no request reached the fake")
	}
	return calls[len(calls)-1]
}

// wantCall checks the last call's method, path, query and JSON body
// (compared as JSON; "" for no body).
func wantCall(t *testing.T, f *clitest.Fake, method, path, query, body string) {
	t.Helper()
	c := lastCall(t, f)
	if c.Method != method || c.Path != path || c.Query != query {
		t.Errorf("request %s %s?%s, want %s %s?%s", c.Method, c.Path, c.Query, method, path, query)
	}
	if body == "" {
		if c.Body != "" {
			t.Errorf("%s %s: body %s, want none", method, path, c.Body)
		}
		return
	}
	var got, want any
	if err := json.Unmarshal([]byte(c.Body), &got); err != nil {
		t.Errorf("%s %s: body %q isn't JSON: %v", method, path, c.Body, err)
		return
	}
	if err := json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatal(err)
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("%s %s: body %s, want %s", method, path, g, w)
	}
}

// wantColumns checks that a table's header names the columns that the
// command's description promises, in order.
func wantColumns(t *testing.T, name, stdout string) {
	t.Helper()
	desc := findCommand(name).Output.Desc
	_, cols, ok := strings.Cut(desc, "a table ")
	if !ok {
		t.Fatalf("%s: output %q names no table", name, desc)
	}
	cols, _, _ = strings.Cut(cols, ";")
	cols, _, _ = strings.Cut(cols, "(")
	header, _, _ := strings.Cut(stdout, "\n")
	got := strings.ReplaceAll(strings.Join(strings.Fields(header), " "), "-", " ")
	want := strings.ReplaceAll(strings.Join(strings.Fields(cols), " "), "-", " ")
	if got != want {
		t.Errorf("%s: header %q, its description promises %q", name, got, want)
	}
}

func fixedNow(t *testing.T, now time.Time) {
	old := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = old })
}

func TestAdminComputers(t *testing.T) {
	h, f := adminHarness(t)
	alice := map[string]any{"name": "alice", "state": "running", "power": "running", "level": "full", "image": "ghcr.io/x/cc:latest",
		"imageTitle": "Debian XFCE", "imageVersion": "2026.10.22", "error": "", "conflict": "",
		"settings": map[string]any{"memoryBytes": 2147483648, "nanoCpus": 2000000000, "volumes": []any{map[string]any{"name": "photos", "readOnly": true}}},
		"access":   []any{map[string]any{"username": "anna", "level": "full"}, map[string]any{"username": "bob", "level": "observe"}},
		"viewers":  []any{"anna"}}
	f.Answer("GET /api/computers", clitest.Answer{JSON: map[string]any{"computers": []any{alice}, "conflicts": []any{}}})
	f.Answer("GET /api/computers/alice", clitest.Answer{JSON: alice})

	r := h.run("computer", "list")
	if r.code != 0 || !strings.Contains(r.stdout, "alice") || !strings.Contains(r.stdout, "running") {
		t.Errorf("computer list: %+v", r)
	}
	wantCall(t, f, "GET", "/api/computers", "", "")
	r = h.run("computer", "list", "--json")
	var list struct {
		Computers []map[string]any `json:"computers"`
	}
	if json.Unmarshal([]byte(r.stdout), &list); len(list.Computers) != 1 || list.Computers[0]["name"] != "alice" {
		t.Errorf("computer list --json: %+v", r)
	}

	r = h.run("computer", "show", "alice")
	if r.code != 0 || !strings.Contains(r.stdout, "Debian XFCE") || !strings.Contains(r.stdout, "bob (observe)") || !strings.Contains(r.stdout, "photos (read-only)") {
		t.Errorf("computer show: %+v", r)
	}
	wantCall(t, f, "GET", "/api/computers/alice", "", "")
	if r = h.run("computer", "show", "alice", "--json"); !strings.Contains(r.stdout, `"imageTitle": "Debian XFCE"`) {
		t.Errorf("computer show --json: %+v", r)
	}

	// create waits for the job, printing its progress.
	f.Answer("POST /api/computers", clitest.Answer{Status: 202, JSON: map[string]string{"job": "j1"}})
	f.Answer("GET /api/jobs/j1", clitest.Answer{Events: []any{
		map[string]any{"status": "running", "message": "Pulling image", "progress": 0.5},
		map[string]any{"status": "done", "message": "Ready", "computer": "bob"}}})
	r = h.run("computer", "create", "bob", "--image", "ghcr.io/x/cc:latest", "--memory", "2G", "--cpus", "1.5",
		"--grant", "anna", "--grant", "carl=observe", "--volume", "photos:ro", "--volume", "music")
	if r.code != 0 || !strings.Contains(r.stderr, "Pulling image (50%)") || !strings.Contains(r.stdout, "Created bob") {
		t.Errorf("computer create: %+v", r)
	}
	calls := f.Calls()
	if len(calls) < 2 || calls[len(calls)-2].Path != "/api/computers" {
		t.Fatalf("calls %+v", calls)
	}
	f2 := calls[len(calls)-2]
	var body map[string]any
	json.Unmarshal([]byte(f2.Body), &body)
	g, _ := json.Marshal(body)
	if want := `{"access":[{"level":"full","username":"anna"},{"level":"observe","username":"carl"}],"image":"ghcr.io/x/cc:latest","memoryBytes":2147483648,"name":"bob","nanoCpus":1500000000,"volumes":[{"name":"photos","readOnly":true},{"name":"music","readOnly":false}]}`; string(g) != want {
		t.Errorf("create body %s, want %s", g, want)
	}
	wantCall(t, f, "GET", "/api/jobs/j1", "", "")

	f.Answer("GET /api/jobs/j2", clitest.Answer{Events: []any{map[string]any{"status": "failed", "message": "Pulling image", "error": "no such image"}}})
	f.Answer("POST /api/computers", clitest.Answer{Status: 202, JSON: map[string]string{"job": "j2"}})
	if r = h.run("computer", "create", "bob", "--image", "nope"); r.code != 1 || !strings.Contains(r.stderr, "no such image") {
		t.Errorf("failed create: %+v", r)
	}
	for _, args := range [][]string{{"computer", "create", "bob"}, {"computer", "create", "bob", "--image", "x", "--grant", "anna=admin"},
		{"computer", "create", "bob", "--image", "x", "--memory", "lots"}, {"computer", "create", "bob", "--image", "x", "--volume", "photos:rw"}} {
		if r := h.run(args...); r.code != 2 {
			t.Errorf("%v: exit %d, want 2 (%s)", args, r.code, r.stderr)
		}
	}

	for _, action := range []string{"start", "stop", "restart"} {
		f.Answer("POST /api/computers/alice/"+action, clitest.Answer{})
		if r := h.run("computer", action, "alice"); r.code != 0 {
			t.Errorf("computer %s: %+v", action, r)
		}
		wantCall(t, f, "POST", "/api/computers/alice/"+action, "", "")
	}

	f.Answer("DELETE /api/computers/alice", clitest.Answer{})
	if r = h.run("computer", "remove", "alice"); r.code != 0 {
		t.Errorf("computer remove: %+v", r)
	}
	wantCall(t, f, "DELETE", "/api/computers/alice", "", "")
	if r = h.run("computer", "remove", "alice", "--delete-files"); r.code != 0 {
		t.Errorf("computer remove --delete-files: %+v", r)
	}
	wantCall(t, f, "DELETE", "/api/computers/alice", "files=1", "")

	// access without assignments shows; with them it changes the listed
	// accounts and keeps the others.
	r = h.run("computer", "access", "alice")
	if r.code != 0 || !strings.Contains(r.stdout, "anna") || !strings.Contains(r.stdout, "observe") {
		t.Errorf("computer access: %+v", r)
	}
	f.Answer("PUT /api/computers/alice/access", clitest.Answer{})
	if r = h.run("computer", "access", "alice", "bob=full", "anna=none", "carl=observe"); r.code != 0 {
		t.Errorf("computer access set: %+v", r)
	}
	wantCall(t, f, "PUT", "/api/computers/alice/access", "", `{"grants":[{"username":"bob","level":"full"},{"username":"carl","level":"observe"}]}`)
	if r = h.run("computer", "access", "alice", "bob=admin"); r.code != 2 {
		t.Errorf("bad level: %+v", r)
	}
}

func TestAdminVolumes(t *testing.T) {
	h, f := adminHarness(t)
	f.Answer("GET /api/volumes", clitest.Answer{JSON: map[string]any{"volumes": []any{map[string]any{"name": "photos", "kind": "local", "folder": "volume/photos/",
		"created": "2026-10-01T10:00:00Z", "computers": []any{map[string]any{"name": "alice", "readOnly": true}}}}}})
	r := h.run("volume", "list")
	if r.code != 0 || !strings.Contains(r.stdout, "photos") || !strings.Contains(r.stdout, "alice (read-only)") {
		t.Errorf("volume list: %+v", r)
	}
	wantCall(t, f, "GET", "/api/volumes", "", "")
	if r = h.run("volume", "list", "--json"); !strings.Contains(r.stdout, `"folder": "volume/photos/"`) {
		t.Errorf("volume list --json: %+v", r)
	}
	f.Answer("POST /api/volumes", clitest.Answer{Status: 201, JSON: map[string]any{"volume": map[string]any{"name": "music", "folder": "volume/music/"}, "adopted": true}})
	if r = h.run("volume", "create", "music"); r.code != 0 || !strings.Contains(r.stdout, "music") || !strings.Contains(r.stdout, "existing folder") {
		t.Errorf("volume create: %+v", r)
	}
	wantCall(t, f, "POST", "/api/volumes", "", `{"name":"music"}`)
	f.Answer("DELETE /api/volumes/music", clitest.Answer{})
	if r = h.run("volume", "remove", "music"); r.code != 0 {
		t.Errorf("volume remove: %+v", r)
	}
	wantCall(t, f, "DELETE", "/api/volumes/music", "deleteFiles=false", "")
	if r = h.run("volume", "remove", "music", "--delete-files"); r.code != 0 {
		t.Errorf("volume remove --delete-files: %+v", r)
	}
	wantCall(t, f, "DELETE", "/api/volumes/music", "deleteFiles=true", "")
	f.Answer("DELETE /api/volumes/photos", clitest.Answer{Status: 409, JSON: map[string]any{"error": "photos is used by alice", "computers": []any{}}})
	if r = h.run("volume", "remove", "photos"); r.code != 1 || !strings.Contains(r.stderr, "photos is used by alice") {
		t.Errorf("volume remove in use: %+v", r)
	}
}

func TestAdminAccounts(t *testing.T) {
	h, f := adminHarness(t)
	fixedNow(t, time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	anna := map[string]any{"username": "anna", "kind": "token", "admin": false, "description": "", "grants": []any{
		map[string]any{"computer": "alice", "level": "full"}, map[string]any{"computer": "bob", "level": "observe"}},
		"sharedBy": nil, "expires": nil, "linkPending": true, "created": "2026-10-01T10:00:00Z", "lastLogin": nil}
	f.Answer("GET /api/accounts", clitest.Answer{JSON: []any{anna, map[string]any{"username": "zef", "kind": "password", "admin": true, "grants": []any{}}}})
	r := h.run("account", "list")
	if r.code != 0 || !strings.Contains(r.stdout, "anna") || !strings.Contains(r.stdout, "bob (observe)") || !strings.Contains(r.stdout, "admin") {
		t.Errorf("account list: %+v", r)
	}
	wantColumns(t, "account list", r.stdout)
	wantCall(t, f, "GET", "/api/accounts", "", "")
	if r = h.run("account", "list", "--json"); !strings.Contains(r.stdout, `"linkPending": true`) {
		t.Errorf("account list --json: %+v", r)
	}

	// A password from stdin makes a password account.
	f.Answer("POST /api/accounts", clitest.Answer{Status: 201, JSON: map[string]any{"username": "carl", "kind": "password"}})
	h.stdin = "s3cret pass\n"
	r = h.run("account", "create", "carl", "--password-stdin", "--admin", "--description", "Carl", "--grant", "alice", "--grant", "bob=observe")
	if r.code != 0 || strings.Contains(r.stdout, "s3cret") {
		t.Errorf("account create: %+v", r)
	}
	wantCall(t, f, "POST", "/api/accounts", "", `{"username":"carl","password":"s3cret pass","admin":true,"description":"Carl","grants":[{"computer":"alice","level":"full"},{"computer":"bob","level":"observe"}]}`)
	h.stdin = ""

	// Without one it is a token account, and the link is printed.
	f.Answer("POST /api/accounts/dana/link", clitest.Answer{JSON: map[string]string{"link": "https://h/link#dyl_1_x"}})
	r = h.run("account", "create", "dana", "--expires", "7d")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "https://h/link#dyl_1_x" {
		t.Errorf("token account create: %+v", r)
	}
	calls := f.Calls()
	if c := calls[len(calls)-2]; c.Path != "/api/accounts" || c.Body != `{"username":"dana","expires":"2026-11-03T12:00:00Z"}` {
		t.Errorf("token account body %+v", c)
	}
	wantCall(t, f, "POST", "/api/accounts/dana/link", "", "")
	if r = h.run("account", "create", "dana", "--expires", "2026-12-01T00:00:00+01:00"); r.code != 0 {
		t.Errorf("RFC 3339 expiry: %+v", r)
	}
	if c := f.Calls()[len(f.Calls())-2]; !strings.Contains(c.Body, `"expires":"2026-11-30T23:00:00Z"`) {
		t.Errorf("RFC 3339 expiry body %s", c.Body)
	}
	for _, bad := range []string{"soon", "-2h", "0d"} {
		if r = h.run("account", "create", "dana", "--expires", bad); r.code != 2 {
			t.Errorf("--expires %s: exit %d (%s)", bad, r.code, r.stderr)
		}
	}
	h.stdin = "\n"
	if r = h.run("account", "create", "carl", "--password-stdin"); r.code != 2 {
		t.Errorf("empty password: %+v", r)
	}
	h.stdin = ""

	// update sends only what was given; --grant changes the listed
	// computers and keeps the others.
	f.Answer("PATCH /api/accounts/anna", clitest.Answer{JSON: anna})
	if r = h.run("account", "update", "anna", "--grant", "bob=full", "--grant", "alice=none", "--grant", "carl=observe", "--expires", "never", "--no-admin"); r.code != 0 {
		t.Errorf("account update: %+v", r)
	}
	wantCall(t, f, "PATCH", "/api/accounts/anna", "", `{"admin":false,"grants":[{"computer":"bob","level":"full"},{"computer":"carl","level":"observe"}],"expires":null}`)
	if r = h.run("account", "update", "anna", "--description", ""); r.code != 0 {
		t.Errorf("account update description: %+v", r)
	}
	wantCall(t, f, "PATCH", "/api/accounts/anna", "", `{"description":""}`)
	if r = h.run("account", "update", "nobody", "--grant", "alice"); r.code != 1 || !strings.Contains(r.stderr, "no such account") {
		t.Errorf("update unknown: %+v", r)
	}
	if r = h.run("account", "update", "anna"); r.code != 2 {
		t.Errorf("update nothing: %+v", r)
	}

	f.Answer("DELETE /api/accounts/anna", clitest.Answer{})
	if r = h.run("account", "remove", "anna"); r.code != 0 {
		t.Errorf("account remove: %+v", r)
	}
	wantCall(t, f, "DELETE", "/api/accounts/anna", "", "")
	f.Answer("POST /api/accounts/anna/link", clitest.Answer{JSON: map[string]string{"link": "https://h/link#dyl_2_y"}})
	if r = h.run("account", "link", "anna"); r.code != 0 || strings.TrimSpace(r.stdout) != "https://h/link#dyl_2_y" {
		t.Errorf("account link: %+v", r)
	}
	wantCall(t, f, "POST", "/api/accounts/anna/link", "", "")
}

func TestAdminSharing(t *testing.T) {
	h, f := adminHarness(t)
	fixedNow(t, time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	f.Answer("GET /api/computers/alice/shares", clitest.Answer{JSON: []any{map[string]any{"username": "sanne", "computer": "alice", "level": "observe",
		"sharedBy": map[string]any{"username": "zef", "id": "a1"}, "expires": nil, "created": "2026-10-01T10:00:00Z", "lastLogin": nil, "lastUsed": "2026-10-26T10:00:00Z", "linkPending": true, "connected": false}}})
	r := h.run("share", "list", "alice")
	used := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC).Local().Format("2006-01-02 15:04")
	if r.code != 0 || !strings.Contains(r.stdout, "sanne") || !strings.Contains(r.stdout, "observe") || !strings.Contains(r.stdout, "zef") ||
		!strings.Contains(r.stdout, "LAST USED") || !strings.Contains(r.stdout, used) {
		t.Errorf("share list: %+v", r)
	}
	wantColumns(t, "share list", r.stdout)
	wantCall(t, f, "GET", "/api/computers/alice/shares", "", "")
	if r = h.run("share", "list", "alice", "--json"); !strings.Contains(r.stdout, `"username": "sanne"`) {
		t.Errorf("share list --json: %+v", r)
	}
	f.Answer("POST /api/computers/alice/shares", clitest.Answer{Status: 201, JSON: map[string]any{"share": map[string]any{"username": "sanne"}, "link": "https://h/link#dyl_3_z"}})
	r = h.run("share", "create", "alice", "sanne", "--observe", "--expires", "2h")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "https://h/link#dyl_3_z" || !strings.Contains(r.stderr, "once") {
		t.Errorf("share create: %+v", r)
	}
	wantCall(t, f, "POST", "/api/computers/alice/shares", "", `{"username":"sanne","level":"observe","expires":"2026-10-27T14:00:00Z"}`)
	if r = h.run("share", "create", "alice", "sanne"); r.code != 0 {
		t.Errorf("share create full: %+v", r)
	}
	wantCall(t, f, "POST", "/api/computers/alice/shares", "", `{"username":"sanne","level":"full"}`)
	f.Answer("POST /api/shares/sanne/link", clitest.Answer{JSON: map[string]string{"link": "https://h/link#dyl_4_w"}})
	if r = h.run("share", "link", "sanne"); r.code != 0 || strings.TrimSpace(r.stdout) != "https://h/link#dyl_4_w" {
		t.Errorf("share link: %+v", r)
	}
	wantCall(t, f, "POST", "/api/shares/sanne/link", "", "")
	f.Answer("DELETE /api/shares/sanne", clitest.Answer{})
	if r = h.run("share", "revoke", "sanne"); r.code != 0 {
		t.Errorf("share revoke: %+v", r)
	}
	wantCall(t, f, "DELETE", "/api/shares/sanne", "", "")
}

func TestAdminDevices(t *testing.T) {
	h, f := adminHarness(t)
	creds := []any{map[string]any{"id": "0a1b2c3d", "kind": "cli", "name": "deskyard CLI on laptop", "created": "2026-10-01T10:00:00Z", "lastUsed": nil, "current": true}}
	f.Answer("GET /api/me/credentials", clitest.Answer{JSON: creds})
	f.Answer("GET /api/accounts/anna/credentials", clitest.Answer{JSON: creds})
	r := h.run("device", "list")
	if r.code != 0 || !strings.Contains(r.stdout, "0a1b2c3d") || !strings.Contains(r.stdout, "deskyard CLI on laptop") {
		t.Errorf("device list: %+v", r)
	}
	wantColumns(t, "device list", r.stdout)
	wantCall(t, f, "GET", "/api/me/credentials", "", "")
	if r = h.run("device", "list", "--account", "anna", "--json"); !strings.Contains(r.stdout, `"current": true`) {
		t.Errorf("device list --account: %+v", r)
	}
	wantCall(t, f, "GET", "/api/accounts/anna/credentials", "", "")
	f.Answer("DELETE /api/accounts/anna/credentials/9f9f9f9f", clitest.Answer{})
	if r = h.run("device", "revoke", "9f9f9f9f", "--account", "anna"); r.code != 0 {
		t.Errorf("device revoke --account: %+v", r)
	}
	wantCall(t, f, "DELETE", "/api/accounts/anna/credentials/9f9f9f9f", "", "")
	// One's own goes through /api/me (the fake's built-in route).
	if r = h.run("device", "revoke", "0a1b2c3d"); r.code != 0 {
		t.Errorf("device revoke: %+v", r)
	}
	if got := f.Revoked(); len(got) != 1 || got[0] != "0a1b2c3d" {
		t.Errorf("revoked %v", got)
	}
}

func TestAdminCommandForbiddenShowsServerMessage(t *testing.T) {
	h, f := adminHarness(t)
	f.Answer("GET /api/accounts", clitest.Answer{Status: 403, JSON: map[string]string{"error": "administrators only"}})
	r := h.run("account", "list")
	if r.code != 1 || !strings.Contains(r.stderr, "administrators only") {
		t.Errorf("%+v", r)
	}
}

// A regular account gets only usernames from GET /api/accounts: the list
// shows those, without empty columns.
func TestAccountListForRegularAccount(t *testing.T) {
	h, f := adminHarness(t)
	f.Answer("GET /api/accounts", clitest.Answer{JSON: []any{map[string]any{"username": "anna"}, map[string]any{"username": "zef"}}})
	r := h.run("account", "list")
	if r.code != 0 || r.stdout != "USER\nanna\nzef\n" || !strings.Contains(r.stderr, "admin") {
		t.Errorf("account list: %+v", r)
	}
}

func TestAdminServerChoice(t *testing.T) {
	h, f := adminHarness(t)
	g := clitest.New(t, "bob")
	h.save("two", g, "bob")
	g.Answer("GET /api/volumes", clitest.Answer{JSON: map[string]any{"volumes": []any{}}})
	if r := h.run("volume", "list", "--server", "two"); r.code != 0 || len(f.Calls()) != 0 || len(g.Calls()) != 1 {
		t.Errorf("--server: %+v", r)
	}
	if r := h.run("-c", "two/bob", "volume", "list"); r.code != 0 || len(f.Calls()) != 0 || len(g.Calls()) != 2 {
		t.Errorf("-c SERVER/ENV: %+v", r)
	}
	if r := h.run("volume", "list", "--server", "nope"); r.code != 1 || !strings.Contains(r.stderr, `no server named "nope"`) {
		t.Errorf("--server nope: %+v", r)
	}
	if r := h.run("volume"); r.code != 2 || !strings.Contains(r.stderr, "list") {
		t.Errorf("volume: %+v", r)
	}
}

// Removals that delete files wait for the server, which works on them for
// longer than other requests may take: giving up would report a network
// failure for a removal still going on.
func TestRemovalsDeletingFilesWaitForTheServer(t *testing.T) {
	h, f := adminHarness(t)
	defer func(d time.Duration) { remote.Timeout = d }(remote.Timeout)
	remote.Timeout = 50 * time.Millisecond
	f.Answer("DELETE /api/computers/alice", clitest.Answer{Delay: 300 * time.Millisecond})
	if r := h.run("computer", "remove", "alice", "--delete-files"); r.code != 0 {
		t.Errorf("computer remove --delete-files: %+v", r)
	}
	f.Answer("DELETE /api/volumes/music", clitest.Answer{Delay: 300 * time.Millisecond})
	if r := h.run("volume", "remove", "music", "--delete-files"); r.code != 0 {
		t.Errorf("volume remove --delete-files: %+v", r)
	}
}
