package docker

import (
	"strings"
	"testing"
)

func TestReclaimCgroupRejectsOtherTargetsAndTraversal(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{"/docker/" + id, true},
		{"/system.slice/docker-" + id + ".scope\n", true},
		{"/docker/" + strings.Repeat("b", 64), false},
		{"/docker/" + id + "-other", false},
		{"/docker/" + id + "/../other", false},
		{"/docker/../" + id, false},
		{"/docker//" + id, false},
		{"/docker/" + id + "\n/docker/other", false},
		{"/", false}, {"", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			_, err := reclaimCgroup(tc.path, id)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
