//go:build !unix

package computers

// DiskSpace is unknown off Unix, where the supervisor doesn't run.
func DiskSpace(string) (free, size uint64, ok bool) { return 0, 0, false }
