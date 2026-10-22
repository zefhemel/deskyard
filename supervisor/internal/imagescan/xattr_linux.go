package imagescan

import (
	"errors"

	"golang.org/x/sys/unix"
)

// capability returns a file's security.capability attribute, or nil.
func capability(name string) ([]byte, error) {
	buf := make([]byte, 256)
	n, err := unix.Lgetxattr(name, "security.capability", buf)
	switch {
	case errors.Is(err, unix.ENODATA), errors.Is(err, unix.ENOTSUP):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return buf[:n], nil
}
