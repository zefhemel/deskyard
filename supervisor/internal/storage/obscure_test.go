package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
)

// reveal undoes Obscure, as rclone reveal does.
func reveal(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	if len(b) < aes.BlockSize {
		return "", errors.New("too short")
	}
	block, err := aes.NewCipher(rcloneKey)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(b)-aes.BlockSize)
	cipher.NewCTR(block, b[:aes.BlockSize]).XORKeyStream(out, b[aes.BlockSize:])
	return string(out), nil
}
