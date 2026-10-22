//go:build !linux

package imagescan

import "errors"

func setxattr(string, string, []byte) error { return errors.New("no file capabilities") }
