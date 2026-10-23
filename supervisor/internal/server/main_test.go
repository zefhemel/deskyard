package server

import (
	"os"
	"testing"

	"github.com/zefhemel/deskyard/supervisor/internal/auth"
)

// Cheap password hashes: see auth.UseTestParams.
func TestMain(m *testing.M) {
	auth.UseTestParams()
	os.Exit(m.Run())
}
