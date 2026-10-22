package auth

import (
	"crypto/hmac"
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
	exp := s.now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%s|%d|%d", username, epoch, exp)
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

// Read returns the session's username and epoch, and whether it is valid and
// unexpired. refresh is true when the cookie is more than a day old, so the
// caller reissues it (a sliding 30 days).
func (s *Sessions) Read(r *http.Request) (username string, epoch int, refresh, ok bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return "", 0, false, false
	}
	enc, sig, found := strings.Cut(c.Value, ".")
	if !found {
		return "", 0, false, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", 0, false, false
	}
	payload := string(raw)
	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return "", 0, false, false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 3 {
		return "", 0, false, false
	}
	epoch, err1 := strconv.Atoi(parts[1])
	exp, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil {
		return "", 0, false, false
	}
	now := s.now()
	if now.Unix() > exp {
		return "", 0, false, false
	}
	refresh = time.Unix(exp, 0).Sub(now) < sessionTTL-24*time.Hour
	return parts[0], epoch, refresh, true
}
