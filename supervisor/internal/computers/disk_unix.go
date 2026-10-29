//go:build unix

package computers

import "golang.org/x/sys/unix"

// DiskSpace is the space left to unprivileged users and the size of the
// filesystem holding path; ok is false when it can't be read.
func DiskSpace(path string) (free, size uint64, ok bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize), true
}
