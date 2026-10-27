package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
)

// rcloneKey is the fixed, published key rclone obscures passwords in its
// configuration with. Obscuring keeps them from casual view; it is not
// encryption, which is why rclone.conf stays in the owner-only secret/.
var rcloneKey = []byte{
	0x9c, 0x93, 0x5b, 0x48, 0x73, 0x0a, 0x55, 0x4d,
	0x6b, 0xfd, 0x7c, 0x63, 0xc8, 0x86, 0xa9, 0x2b,
	0xd3, 0x90, 0x19, 0x8e, 0xb8, 0x12, 0x8a, 0xfb,
	0xf4, 0xde, 0x16, 0x2b, 0x8b, 0x95, 0xf6, 0x38,
}

// Obscure is rclone obscure: AES-CTR with rclone's key and a random IV
// prefix, base64url without padding.
func Obscure(plain string) (string, error) {
	block, err := aes.NewCipher(rcloneKey)
	if err != nil {
		return "", err
	}
	out := make([]byte, aes.BlockSize+len(plain))
	if _, err := rand.Read(out[:aes.BlockSize]); err != nil {
		return "", err
	}
	cipher.NewCTR(block, out[:aes.BlockSize]).XORKeyStream(out[aes.BlockSize:], []byte(plain))
	return base64.RawURLEncoding.EncodeToString(out), nil
}
