package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Bearer secrets (login links, client credentials) have the form
// <prefix><id>_<secret>: the id finds the record, and only a SHA-256 of the
// secret is stored (the secret is random, so a slow hash adds nothing).

// RandomID is n random hex digits.
func RandomID(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// HashSecret is the hash of a bearer secret's secret part that is stored.
func HashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// NewSecret makes a bearer secret with the given prefix (such as "dyc_"):
// an 8-hex-digit id and 32 random bytes, and the hash to store.
func NewSecret(prefix string) (id, bearer, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	secret := base64.RawURLEncoding.EncodeToString(b)
	id = RandomID(8)
	return id, prefix + id + "_" + secret, HashSecret(secret)
}

// ParseBearer splits a bearer secret of the given prefix into its id and
// secret. The id never holds "_"; the secret may.
func ParseBearer(prefix, s string) (id, secret string, ok bool) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return "", "", false
	}
	id, secret, ok = strings.Cut(rest, "_")
	if !ok || id == "" || secret == "" {
		return "", "", false
	}
	return id, secret, true
}
