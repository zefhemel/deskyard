// Command computer uses a Deskyard desktop: screen actions, shell, files
// and the shared browser, inside the desktop or remotely through the
// supervisor's agent API. Run `deskyard guide` to get started.
package main

import (
	"context"
	"os"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/app"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	os.Exit(app.New(version).Run(context.Background(), os.Args[1:]))
}
