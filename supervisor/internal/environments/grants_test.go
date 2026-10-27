package environments

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateGrantPath(t *testing.T) {
	for _, p := range []string{"/share/a", "/mnt/photos", "/home/user/x", "/data", "/srv/my files", "/library"} {
		if err := ValidateGrantPath(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for p, want := range map[string]string{
		"":              "absolute",
		"relative":      "absolute",
		"/share/../etc": "clean",
		"/share/":       "clean",
		"/":             "whole",
		"/workspace":    "home",
		"/workspace/x":  "home",
		"/usr/local/x":  "/usr",
		"/lib64":        "/lib64",
		"/lib":          "/lib",
		"/proc":         "/proc",
		"/var/lib":      "/var",
		"/opt/x":        "/opt",
		"/etc":          "/etc",
		"/a\x00b":       "absolute",
	} {
		err := ValidateGrantPath(p)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error mentioning %q", p, err, want)
		}
	}
}

func TestValidateVolumeGrantsPaths(t *testing.T) {
	if err := ValidateVolumeGrants([]VolumeGrant{{Name: "aa", Path: "/a"}, {Name: "aa", Path: "/b"}}); err == nil {
		t.Error("accepted a volume granted twice")
	}
	if err := ValidateVolumeGrants([]VolumeGrant{{Name: "aa", Path: "/a"}, {Name: "bb", Path: "/a/b"}}); err == nil {
		t.Error("accepted nested grant paths")
	}
	if err := ValidateVolumeGrants([]VolumeGrant{{Name: "aa", Path: "/data"}, {Name: "bb", Path: "/data"}}); err == nil {
		t.Error("accepted equal grant paths")
	}
	if err := ValidateVolumeGrants([]VolumeGrant{{Name: "aa", Path: "/ab"}, {Name: "bb", Path: "/a"}}); err != nil {
		t.Errorf("/ab and /a don't nest: %v", err)
	}
	if err := ValidateVolumeGrants([]VolumeGrant{{Name: "aa", Path: "/etc/x"}}); err == nil {
		t.Error("accepted a system path")
	}
	var many []VolumeGrant
	for i := range 40 {
		many = append(many, VolumeGrant{Name: fmt.Sprintf("v%02d", i)})
	}
	if err := ValidateVolumeGrants(many); err != nil {
		t.Errorf("40 grants at default paths: %v", err)
	}
}

func TestGrantPathDefault(t *testing.T) {
	if got := GrantPath(VolumeGrant{Name: "photos"}); got != "/share/photos" {
		t.Errorf("default: %s", got)
	}
	if got := GrantPath(VolumeGrant{Name: "photos", Path: "/mnt/p"}); got != "/mnt/p" {
		t.Errorf("explicit: %s", got)
	}
}
