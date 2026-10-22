// deskyard-image-scan runs inside a throwaway container of an image the
// supervisor built, as root, and prints what could give the desktop's user
// root (see internal/imagescan). It is static and relies on nothing in the
// image. "deskyard-image-scan wait" only waits, to keep the container up.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/imagescan"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "wait" {
		time.Sleep(1 << 62) // removed by force when done
		return
	}
	r, err := imagescan.Scan("/", imagescan.Options{UserUID: 1000})
	if err != nil {
		fmt.Fprintln(os.Stderr, "deskyard-image-scan:", err)
		os.Exit(1)
	}
	if err := imagescan.Write(os.Stdout, r); err != nil {
		os.Exit(1)
	}
}
