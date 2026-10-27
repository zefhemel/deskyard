//go:build !linux

package main

import "errors"

func dropPrivileges(socketGID int, hasSocket bool) error {
	return errors.New("the supervisor runs only in a Linux container")
}
