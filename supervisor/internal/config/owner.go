package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
)

// UID and GID are who the supervisor runs as, and who owns its data folder.
// They are fixed: no setting changes them.
const UID, GID = 1000, 1000

const defaultSocket = "/var/run/docker.sock"

// ReownOnce gives the data folder to UID/GID if an earlier supervisor, which
// ran as root, made it. It does nothing once state/owner exists. It chowns
// the supervisor's own files recursively (the top-level JSON stores, secret/,
// state/, cache/) and the data folder, home/, volume/ and each direct child
// of home/ and volume/ without recursing: the contents of homes and volumes
// are left alone, since a computer re-owns its home when it starts.
//
// All of it goes through an os.Root on the data folder, so no path escapes
// it, and a home/, volume/, secret/, state/ or cache/ that is not a real
// directory (a planted symlink, say) is skipped. The marker is written last,
// through a chowned temporary file renamed into place, so a run that dies
// midway repeats harmlessly and never leaves a root-owned marker.
func ReownOnce(l Layout) error {
	root, err := os.OpenRoot(l.Data)
	if err != nil {
		return err
	}
	defer root.Close()
	return reownOnce(root, func(name string, uid, gid int) error { return root.Lchown(name, uid, gid) })
}

// reownOnce is ReownOnce with the chown injected, so tests run without root.
// Names passed to chown are relative to the root ("." is the data folder).
func reownOnce(root *os.Root, chown func(name string, uid, gid int) error) error {
	const marker = "state/owner"
	if _, err := root.Lstat(marker); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	own := func(name string) error { return chown(name, UID, GID) }
	realDir := func(name string) (bool, error) {
		fi, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return fi.IsDir(), nil
	}
	if fi, err := root.Lstat("state"); err == nil && !fi.IsDir() {
		return fmt.Errorf("%s/state is not a directory", root.Name())
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := root.MkdirAll("state", 0o700); err != nil {
		return err
	}
	if err := own("."); err != nil {
		return err
	}
	top, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, e := range top {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") {
			if err := own(e.Name()); err != nil {
				return err
			}
		}
	}
	for _, dir := range []string{"secret", "state", "cache"} {
		ok, err := realDir(dir)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		// WalkDir does not follow symlinked directories, and Lchown does not
		// follow a symlink it chowns.
		if err := fs.WalkDir(root.FS(), dir, func(name string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return own(name)
		}); err != nil {
			return err
		}
	}
	for _, dir := range []string{"home", "volume"} {
		ok, err := realDir(dir)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		children, err := fs.ReadDir(root.FS(), dir)
		if err != nil {
			return err
		}
		if err := own(dir); err != nil {
			return err
		}
		for _, c := range children {
			if err := own(dir + "/" + c.Name()); err != nil {
				return err
			}
		}
	}
	const tmp = "state/owner.tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "%d\n", UID)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = own(tmp)
	}
	if werr == nil {
		werr = root.Rename(tmp, marker)
	}
	if werr != nil {
		_ = root.Remove(tmp)
	}
	return werr
}

// SocketPath is the engine's unix socket for a DOCKER_HOST-style address ("" is
// the default socket), or ok false for a TCP or other host.
func SocketPath(engineHost string) (path string, ok bool) {
	if engineHost == "" {
		return defaultSocket, true
	}
	return strings.CutPrefix(engineHost, "unix://")
}

// SocketGroup is the group of the engine's unix socket, which the supervisor
// joins to use it. engineHost is a DOCKER_HOST-style address: "" is the
// default socket. For a TCP (or other) host there is no group and ok is
// false.
func SocketGroup(engineHost string) (gid int, ok bool, err error) {
	path, isUnix := SocketPath(engineHost)
	if !isUnix {
		return 0, false, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false, fmt.Errorf("container engine socket: %w", err)
	}
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, false, fmt.Errorf("container engine socket %s: no ownership information", path)
	}
	return int(st.Gid), true, nil
}
