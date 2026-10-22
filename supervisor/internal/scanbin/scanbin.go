// Package scanbin holds the image scanner (cmd/deskyard-image-scan), built
// static for Linux by `make scanner` into bin/ and embedded in the supervisor,
// which copies it into containers of the images it builds.
package scanbin

import (
	"embed"
	"fmt"
)

//go:embed all:bin
var bin embed.FS

// Binary returns the scanner for a Linux architecture (amd64, arm64).
func Binary(arch string) ([]byte, error) {
	b, err := bin.ReadFile("bin/deskyard-image-scan-linux-" + arch)
	if err != nil || len(b) == 0 {
		return nil, fmt.Errorf("the image scanner for linux/%s isn't built into this supervisor (run make -C supervisor scanner, then build the supervisor)", arch)
	}
	return b, nil
}
