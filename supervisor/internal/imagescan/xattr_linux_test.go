package imagescan

import "golang.org/x/sys/unix"

func setxattr(name, attr string, v []byte) error { return unix.Lsetxattr(name, attr, v, 0) }
