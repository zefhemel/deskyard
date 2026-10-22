//go:build engine

package docker

import (
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/engine/enginetest"
)

func TestDocker(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, d)
}
