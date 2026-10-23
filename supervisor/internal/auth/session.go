package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	CookieName = "deskyard_session"
	sessionTTL = 30 * 24 * time.Hour
)

// Sessions issues and reads signed session cookies carrying the username and
// the account's session epoch; bumping the epoch signs the account out.
type Sessions struct {
	Key    []byte
	Secure bool // HTTPS: mark cookies Secure
	Now    func() time.Time
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sessions) sign(payload string) string {
	m := hmac.New(sha256.New, s.Key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Sessions) Issue(w http.ResponseWriter, username string, epoch int) {
	id := make([]byte, 24)
	if _, err := rand.Read(id); err != nil {
		panic(err)
	}
	s.Refresh(w, SessionClaims{Username: username, Epoch: epoch, ID: base64.RawURLEncoding.EncodeToString(id)})
}

// Refresh preserves the browser login identity across sliding cookie renewal.
func (s *Sessions) Refresh(w http.ResponseWriter, c SessionClaims) {
	exp := s.now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%s|%d|%d|%s", c.Username, c.Epoch, exp, c.ID)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: s.Secure,
		SameSite: http.SameSiteStrictMode, Expires: time.Unix(exp, 0),
	})
}

func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.Secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

// SessionClaims contains verified identity and absolute cookie expiry.
type SessionClaims struct {
	Username string
	Epoch    int
	ID       string
	Expires  time.Time
	Refresh  bool
}

// Read preserves the existing username/epoch/refresh interface.
func (s *Sessions) Read(r *http.Request) (username string, epoch int, refresh, ok bool) {
	c, ok := s.Claims(r)
	return c.Username, c.Epoch, c.Refresh, ok
}

// Claims verifies the same signature and expiry as Read. Legacy cookies retain
// their signed fingerprint as identity when refreshed into the new format.
func (s *Sessions) Claims(r *http.Request) (SessionClaims, bool) {
	invalid := SessionClaims{}
	c, err := r.Cookie(CookieName)
	if err != nil {
		return invalid, false
	}
	enc, sig, found := strings.Cut(c.Value, ".")
	if !found {
		return invalid, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return invalid, false
	}
	payload := string(raw)
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return invalid, false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 3 && len(parts) != 4 {
		return invalid, false
	}
	epoch, e1 := strconv.Atoi(parts[1])
	exp, e2 := strconv.ParseInt(parts[2], 10, 64)
	if e1 != nil || e2 != nil || parts[0] == "" || s.now().Unix() >= exp {
		return invalid, false
	}
	id := "legacy-" + sig
	if len(parts) == 4 {
		id = parts[3]
		if id == "" {
			return invalid, false
		}
	}
	return SessionClaims{Username: parts[0], Epoch: epoch, ID: id, Expires: time.Unix(exp, 0), Refresh: len(parts) == 3 || time.Unix(exp, 0).Sub(s.now()) < sessionTTL-24*time.Hour}, true
}
