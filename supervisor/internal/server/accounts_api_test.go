package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/accounts"
	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

func TestAccountLinkForTokenAccount(t *testing.T) {
	_, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	code, body := send(t, admin, "POST", ts.URL+"/api/accounts", ts.URL,
		`{"username":"tess","description":"Build agent","grants":[{"computer":"alice","level":"full"}]}`)
	var a accountJSON
	_ = json.Unmarshal([]byte(body), &a)
	if code != 201 || a.Kind != "token" || a.Description != "Build agent" || a.LinkPending || a.SharedBy != nil ||
		len(a.Grants) != 1 || a.Grants[0] != (accounts.Grant{Computer: "alice", Level: auth.LevelFull}) {
		t.Fatalf("create token account: %d %s", code, body)
	}
	code, body = send(t, admin, "POST", ts.URL+"/api/accounts/tess/link", ts.URL, "")
	var res struct{ Link string }
	_ = json.Unmarshal([]byte(body), &res)
	_, linkCode, _ := strings.Cut(res.Link, "#")
	if code != 200 || !strings.HasPrefix(res.Link, ts.URL+"/link#dyl_") {
		t.Fatalf("account link: %d %s", code, body)
	}
	var list []accountJSON
	getJSON(t, admin, ts.URL+"/api/accounts", &list)
	for _, a := range list {
		if a.Username == "tess" && !a.LinkPending {
			t.Fatalf("listed without its link: %+v", a)
		}
		if a.Username == "admin" && a.Kind != "password" {
			t.Fatalf("admin listed as %+v", a)
		}
	}
	code, body = redeemCLI(t, ts, linkCode)
	var redeemed struct{ Credential string }
	_ = json.Unmarshal([]byte(body), &redeemed)
	if code != 200 {
		t.Fatalf("redeem: %d %s", code, body)
	}
	code, body = sendBearer(t, "GET", ts.URL+"/api/me", redeemed.Credential, "")
	if code != 200 || body != `{"username":"tess","admin":false,"kind":"token","share":false}`+"\n" {
		t.Fatalf("token account's me: %d %s", code, body)
	}
	if code, body := sendBearer(t, "POST", ts.URL+"/api/me/password", redeemed.Credential, `{"current":"","new":"pw"}`); code != 400 || !strings.Contains(body, "token accounts have no password") {
		t.Fatalf("own password of a token account: %d %s", code, body)
	}
	if code, body := send(t, admin, "POST", ts.URL+"/api/accounts/anna/link", ts.URL, ""); code != 400 || !strings.Contains(body, "password accounts sign in with their password") {
		t.Fatalf("link for a password account: %d %s", code, body)
	}
	if code, _ := send(t, admin, "POST", ts.URL+"/api/accounts/nobody/link", ts.URL, ""); code != 404 {
		t.Fatalf("link for nobody: %d", code)
	}
	if code, _ := send(t, login(t, ts, "anna"), "POST", ts.URL+"/api/accounts/tess/link", ts.URL, ""); code != 403 {
		t.Fatalf("link by a regular account: %d", code)
	}
	// Token accounts have no password to set.
	if code, body := send(t, admin, "PATCH", ts.URL+"/api/accounts/tess", ts.URL, `{"password":"pw"}`); code != 400 || !strings.Contains(body, "token accounts have no password") {
		t.Fatalf("password for a token account: %d %s", code, body)
	}
}

func TestAccountsAPIValidation(t *testing.T) {
	s, ts := newTestServer(t)
	admin := login(t, ts, "admin")
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for _, req := range []string{
		`{"username":"x1","grants":[{"computer":"ghost","level":"full"}]}`,
		`{"username":"x1","grants":[{"computer":"alice","level":"write"}]}`,
		`{"username":"x1","grants":[{"computer":"alice","level":"full"},{"computer":"alice","level":"observe"}]}`,
		`{"username":"x1","expires":"2001-01-01T00:00:00Z"}`,
		`{"username":"x1","password":"pw","admin":true,"expires":"` + future + `"}`,
		`{"username":"x1","description":"` + strings.Repeat("d", 201) + `"}`,
	} {
		if code, body := send(t, admin, "POST", ts.URL+"/api/accounts", ts.URL, req); code != 400 {
			t.Errorf("create %s: %d %s", req, code, body)
		}
	}
	// A token admin may expire; a password account may too.
	for _, req := range []string{`{"username":"x2","admin":true,"expires":"` + future + `"}`, `{"username":"x3","password":"pw","expires":"` + future + `"}`} {
		if code, body := send(t, admin, "POST", ts.URL+"/api/accounts", ts.URL, req); code != 201 {
			t.Errorf("create %s: %d %s", req, code, body)
		}
	}
	// PATCH: description, grants with levels, expiry and its removal.
	code, body := send(t, admin, "PATCH", ts.URL+"/api/accounts/x3", ts.URL,
		`{"description":"Temp","grants":[{"computer":"alice","level":"observe"}],"expires":null}`)
	if a, _ := s.Accounts.Get("x3"); code != 200 || a.Description != "Temp" || a.Expires != nil || a.Level("alice") != auth.LevelObserve {
		t.Fatalf("patch: %d %s %+v", code, body, a)
	}
	if code, _ := send(t, admin, "PATCH", ts.URL+"/api/accounts/x3", ts.URL, `{"expires":"`+future+`"}`); code != 200 {
		t.Fatalf("patch expiry: %d", code)
	}
	if a, _ := s.Accounts.Get("x3"); a.Expires == nil {
		t.Fatal("the expiry wasn't set")
	}
	// Omitted fields keep their value.
	if code, _ := send(t, admin, "PATCH", ts.URL+"/api/accounts/x3", ts.URL, `{}`); code != 200 {
		t.Fatalf("empty patch: %d", code)
	}
	if a, _ := s.Accounts.Get("x3"); a.Expires == nil || a.Description != "Temp" || len(a.Grants) != 1 {
		t.Fatalf("an empty patch changed %+v", a)
	}
	if code, _ := send(t, admin, "PATCH", ts.URL+"/api/accounts/x3", ts.URL, `{"admin":true}`); code != 400 {
		t.Fatalf("making an expiring password account an admin: %d", code)
	}
	if code, _ := send(t, admin, "PATCH", ts.URL+"/api/accounts/x3", ts.URL, `{"password":""}`); code != 400 {
		t.Fatalf("empty password: %d", code)
	}
	if code, _ := send(t, admin, "PATCH", ts.URL+"/api/accounts/nobody", ts.URL, `{"description":"x"}`); code != 404 {
		t.Fatalf("patch nobody: %d", code)
	}

	// A share's access is set when it is shared.
	adminAccount := accountNamed(t, s, "admin")
	createShare(t, s, adminAccount, "sanne", auth.LevelObserve)
	for _, req := range []string{`{"admin":true}`, `{"grants":[]}`} {
		if code, body := send(t, admin, "PATCH", ts.URL+"/api/accounts/sanne", ts.URL, req); code != 400 || !strings.Contains(body, "a share's access is set when it is shared") {
			t.Errorf("patch share %s: %d %s", req, code, body)
		}
	}
	var list []accountJSON
	getJSON(t, admin, ts.URL+"/api/accounts", &list)
	for _, a := range list {
		if a.Username == "sanne" && (a.SharedBy == nil || a.SharedBy.Username != "admin" || !a.LinkPending || a.Kind != "token") {
			t.Fatalf("share listed as %+v", a)
		}
	}
	// The last admin with a password stays.
	if code, _ := send(t, admin, "PATCH", ts.URL+"/api/accounts/admin", ts.URL, `{"admin":false}`); code != 409 {
		t.Fatalf("demoting the last password admin: %d", code)
	}
}

func TestAccessEndpointLevels(t *testing.T) {
	s, ts := newTestServer(t)
	createBob(t, s)
	adminAccount := accountNamed(t, s, "admin")
	createShare(t, s, adminAccount, "sanne", auth.LevelObserve)
	admin := login(t, ts, "admin")
	put := func(body string) (int, string) {
		return send(t, admin, "PUT", ts.URL+"/api/computers/alice/access", ts.URL, body)
	}
	if code, body := put(`{"grants":[{"username":"anna","level":"observe"},{"username":"bob","level":"full"}]}`); code != 204 {
		t.Fatalf("set access: %d %s", code, body)
	}
	if a, b := accountNamed(t, s, "anna"), accountNamed(t, s, "bob"); a.Level("alice") != auth.LevelObserve || b.Level("alice") != auth.LevelFull {
		t.Fatalf("levels: anna %q bob %q", a.Level("alice"), b.Level("alice"))
	}
	_, body := get(t, admin, ts.URL+"/api/computers/alice")
	if !strings.Contains(body, `"access":[{"username":"anna","level":"observe"},{"username":"bob","level":"full"}]`) {
		t.Fatalf("computer access: %s", body)
	}
	for _, req := range []string{
		`{"grants":[{"username":"nobody","level":"full"}]}`,
		`{"grants":[{"username":"anna","level":"write"}]}`,
		`{"grants":[{"username":"anna","level":"full"},{"username":"anna","level":"observe"}]}`,
		`{"grants":[{"username":"sanne","level":"full"}]}`,
	} {
		if code, body := put(req); code != 400 {
			t.Errorf("set access %s: %d %s", req, code, body)
		}
	}
	// Exactly the listed accounts keep a grant; shares aren't affected.
	if code, _ := put(`{"grants":[{"username":"bob","level":"observe"}]}`); code != 204 {
		t.Fatalf("set access: %d", code)
	}
	if a, b := accountNamed(t, s, "anna"), accountNamed(t, s, "bob"); a.Level("alice") != auth.LevelNone || b.Level("alice") != auth.LevelObserve {
		t.Fatalf("levels: anna %q bob %q", a.Level("alice"), b.Level("alice"))
	}
	if sh, ok := s.Accounts.Get("sanne"); !ok || sh.Level("alice") != auth.LevelObserve {
		t.Fatal("setting access changed a share")
	}
	if code, _ := send(t, login(t, ts, "anna"), "PUT", ts.URL+"/api/computers/alice/access", ts.URL, `{"grants":[]}`); code == 204 {
		t.Fatal("a regular account set access")
	}
}

func TestOwnCredentialsListAndRevoke(t *testing.T) {
	s, ts := newTestServer(t)
	bob := createBob(t, s)
	first, firstBearer := issueCredential(t, s, bob)
	second, secondBearer := issueCredential(t, s, bob)
	annaCred, _ := issueCredential(t, s, accountNamed(t, s, "anna"))

	code, body := sendBearer(t, "GET", ts.URL+"/api/me/credentials", firstBearer, "")
	var list []credentialJSON
	_ = json.Unmarshal([]byte(body), &list)
	if code != 200 || len(list) != 2 {
		t.Fatalf("own credentials: %d %s", code, body)
	}
	for _, c := range list {
		if c.Kind != "cli" || c.Name != "deskyard CLI on test" || c.Current != (c.ID == first.ID) {
			t.Fatalf("credential %+v", c)
		}
	}
	if strings.Contains(body, "hash") || strings.Contains(body, strings.TrimPrefix(firstBearer, "dyc_"+first.ID+"_")) {
		t.Fatalf("the list holds secrets: %s", body)
	}
	if code, _ := sendBearer(t, "DELETE", ts.URL+"/api/me/credentials/"+annaCred.ID, firstBearer, ""); code != 404 {
		t.Fatalf("deleting another account's credential: %d", code)
	}
	if len(s.credentials.list(annaCred.AccountID)) != 1 {
		t.Fatal("bob deleted anna's credential")
	}
	if code, _ := sendBearer(t, "DELETE", ts.URL+"/api/me/credentials/"+second.ID, firstBearer, ""); code != 204 {
		t.Fatalf("deleting own credential: %d", code)
	}
	if code, _ := sendBearer(t, "GET", ts.URL+"/api/me", secondBearer, ""); code != 401 {
		t.Fatalf("deleted credential: %d", code)
	}
	// deskyard logout revokes the credential making the request.
	if code, _ := sendBearer(t, "DELETE", ts.URL+"/api/me/credentials/"+first.ID, firstBearer, ""); code != 204 {
		t.Fatalf("deleting the requesting credential: %d", code)
	}
	if code, _ := sendBearer(t, "GET", ts.URL+"/api/me", firstBearer, ""); code != 401 {
		t.Fatalf("self-revoked credential: %d", code)
	}

	// Admins see and delete any account's credentials.
	third, _ := issueCredential(t, s, bob)
	admin := login(t, ts, "admin")
	list = nil
	if code := getJSON(t, admin, ts.URL+"/api/accounts/bob/credentials", &list); code != 200 || len(list) != 1 || list[0].ID != third.ID || list[0].Current {
		t.Fatalf("bob's credentials for the admin: %d %+v", code, list)
	}
	if code := getJSON(t, admin, ts.URL+"/api/accounts/nobody/credentials", nil); code != 404 {
		t.Fatalf("nobody's credentials: %d", code)
	}
	if code, _ := send(t, admin, "DELETE", ts.URL+"/api/accounts/bob/credentials/"+annaCred.ID, ts.URL, ""); code != 404 {
		t.Fatalf("deleting anna's credential as bob's: %d", code)
	}
	if code := getJSON(t, login(t, ts, "anna"), ts.URL+"/api/accounts/bob/credentials", nil); code != 403 {
		t.Fatalf("bob's credentials for anna: %d", code)
	}
	if code, _ := send(t, admin, "DELETE", ts.URL+"/api/accounts/bob/credentials/"+third.ID, ts.URL, ""); code != 204 {
		t.Fatalf("admin deleting bob's credential: %d", code)
	}
	if len(s.credentials.list(bob.ID)) != 0 {
		t.Fatal("bob kept a deleted credential")
	}
}

func TestMeShowsKind(t *testing.T) {
	_, ts := newTestServer(t)
	_, body := get(t, login(t, ts, "admin"), ts.URL+"/api/me")
	if body != `{"username":"admin","admin":true,"kind":"password","share":false}`+"\n" {
		t.Fatalf("me: %s", body)
	}
}

// A non-admin lists the usernames of the invited accounts and of its own
// shares; a share, an outsider, lists only itself.
func TestAccountListForNonAdmins(t *testing.T) {
	s, ts := newTestServer(t)
	bob := createBob(t, s)
	if err := s.Accounts.SetAccess("alice", fullAccess("bob")); err != nil {
		t.Fatal(err)
	}
	createShare(t, s, bob, "sanne", auth.LevelObserve)
	guest, _ := createShare(t, s, bob, "secretguest", auth.LevelObserve)
	_, bearer := issueCredential(t, s, guest)
	names := func(body string) string {
		var list []struct{ Username string }
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		var out []string
		for _, a := range list {
			out = append(out, a.Username)
		}
		return strings.Join(out, ",")
	}
	for _, tc := range []struct{ who, want string }{{"anna", "admin,anna,bob"}, {"bob", "admin,anna,bob,sanne,secretguest"}} {
		code, body := send(t, login(t, ts, tc.who), "GET", ts.URL+"/api/accounts", "", "")
		if code != 200 || names(body) != tc.want {
			t.Errorf("%s lists %d %s, want %s", tc.who, code, body, tc.want)
		}
	}
	if code, body := sendBearer(t, "GET", ts.URL+"/api/accounts", bearer, ""); code != 200 || names(body) != "secretguest" {
		t.Errorf("a share lists %d %s", code, body)
	}
}
