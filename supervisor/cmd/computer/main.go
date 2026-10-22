// Command computer uses a Deskyard computer: desktop actions, shell, files
// and the shared browser, inside the computer or remotely through the
// supervisor's agent API. Run `computer guide` to get started.
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
