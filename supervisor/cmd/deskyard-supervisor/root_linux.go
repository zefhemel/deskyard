//go:build linux

package main

import (
	"fmt"
	"syscall"

	"github.com/zefhemel/deskyard/supervisor/internal/config"
)

// dropPrivileges leaves root for UID/GID 1000 with the engine socket's group
// as the only supplementary group (none without a socket). Groups go first
// and the UID last: after setuid the process may no longer change either.
func dropPrivileges(socketGID int, hasSocket bool) error {
	groups := []int{}
	if hasSocket {
		groups = append(groups, socketGID)
	}
	if err := syscall.Setgroups(groups); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(config.GID); err != nil {
		return fmt.Errorf("setgid: %w", err)
	}
	if err := syscall.Setuid(config.UID); err != nil {
		return fmt.Errorf("setuid: %w", err)
	}
	return nil
}
