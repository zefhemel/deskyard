package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTerminalClaimsIdentity(t *testing.T) {
	now := time.Now()
	s := Sessions{Key: []byte("secret"), Now: func() time.Time { return now }}
	issue := func() *http.Request {
		w := httptest.NewRecorder()
		s.Issue(w, "anna", 3)
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(w.Result().Cookies()[0])
		return r
	}
	r := issue()
	c, ok := s.Claims(r)
	if !ok || c.Username != "anna" || c.Epoch != 3 || c.ID == "" || c.Expires.IsZero() {
		t.Fatalf("claims: %+v %v", c, ok)
	}
	other, _ := s.Claims(issue())
	if c.ID == other.ID {
		t.Fatal("separate logins share identity")
	}
	now = now.Add(48 * time.Hour)
	w := httptest.NewRecorder()
	s.Refresh(w, c)
	rr := httptest.NewRequest("GET", "/", nil)
	rr.AddCookie(w.Result().Cookies()[0])
	fresh, ok := s.Claims(rr)
	if !ok || fresh.ID != c.ID || !fresh.Expires.After(c.Expires) {
		t.Fatal("refresh lost identity")
	}
	now = c.Expires
	if _, ok := s.Claims(r); ok {
		t.Fatal("accepted expired cookie")
	}
}

func TestTerminalLegacyClaimsMigration(t *testing.T) {
	s := Sessions{Key: []byte("secret")}
	exp := time.Now().Add(time.Hour).Unix()
	payload := fmt.Sprintf("anna|3|%d", exp)
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.sign(payload)})
	c, ok := s.Claims(r)
	if !ok || !c.Refresh {
		t.Fatal("legacy claims rejected")
	}
	w := httptest.NewRecorder()
	s.Refresh(w, c)
	next := httptest.NewRequest("GET", "/", nil)
	next.AddCookie(w.Result().Cookies()[0])
	fresh, ok := s.Claims(next)
	if !ok || fresh.ID != c.ID {
		t.Fatal("legacy active connection lost identity")
	}
}
