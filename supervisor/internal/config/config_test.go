package config

import (
	"strings"
	"testing"
)

func TestLoadRefusesOtherDataDir(t *testing.T) {
	t.Setenv("DESKYARD_DATA_DIR", "/srv/deskyard")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "-v /srv/deskyard:/data") {
		t.Fatalf("got %v, want an error naming -v /srv/deskyard:/data", err)
	}
	for _, v := range []string{"/data", ""} {
		t.Setenv("DESKYARD_DATA_DIR", v)
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if c.DataDir != "/data" || c.Layout.Data != "/data" {
			t.Errorf("DataDir = %q, Layout.Data = %q", c.DataDir, c.Layout.Data)
		}
	}
}

func TestLoadListensOnAllInterfacesByDefault(t *testing.T) {
	t.Setenv("DESKYARD_LISTEN", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "0.0.0.0:7343" {
		t.Errorf("Listen = %q", c.Listen)
	}
}
