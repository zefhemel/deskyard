//go:build engine

package docker

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/engine"
)

// Docker's refusal of an overlapping subnet is engine.ErrSubnetTaken, which
// the environments service retries with another subnet.
func TestCreateNetworkReportsTakenSubnet(t *testing.T) {
	d, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	suffix := strings.ToLower(rand.Text()[:8])
	first, second := "deskyard-test-net-"+suffix, "deskyard-test-net2-"+suffix
	// A /28 in a range Deskyard's pools don't use.
	const subnet = "10.254.254.0/28"
	if err := d.CreateNetwork(ctx, first, subnet, nil); err != nil {
		t.Fatal(err)
	}
	defer d.RemoveNetwork(context.Background(), first)
	err = d.CreateNetwork(ctx, second, subnet, nil)
	if err == nil {
		d.RemoveNetwork(context.Background(), second)
		t.Fatal("an overlapping network was created")
	}
	if !errors.Is(err, engine.ErrSubnetTaken) {
		t.Fatalf("%v is not ErrSubnetTaken", err)
	}
}
