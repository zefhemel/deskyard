package server

import (
	"strings"
	"testing"
)

func TestParseMiseTools(t *testing.T) {
	out := `{
	  "python": [{"version":"3.13.1","install_path":"/opt/mise/installs/python/3.13.1","installed":true}],
	  "node": [
	    {"version":"22.1.0","install_path":"/workspace/.local/share/mise/installs/node/22.1.0","installed":true},
	    {"version":"24.0.0","install_path":"/opt/mise/installs/node/24.0.0"},
	    {"version":"20.0.0","install_path":"/workspace/x","installed":false},
	    {"version":"19","install_path":"/opt/miseevil/node"},
	    {"version":"","install_path":"/x"},
	    "junk", 7
	  ],
	  "bad": "not a list",
	  "worse": null
	}`
	got, err := parseMiseTools([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []Tool{
		{"node", "22.1.0", "environment"},
		{"node", "24.0.0", "image"},
		{"node", "19", "environment"},
		{"python", "3.13.1", "image"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %v want %v", i, got[i], want[i])
		}
	}
}

func TestParseMiseToolsOddInput(t *testing.T) {
	for _, in := range []string{``, `[]`, `null`, `"x"`, `{`} {
		if got, err := parseMiseTools([]byte(in)); err == nil && len(got) != 0 {
			t.Errorf("%q: %v", in, got)
		}
	}
	long := `{"` + strings.Repeat("a", 1000) + `":[{"version":"1","install_path":"/x"}]}`
	got, _ := parseMiseTools([]byte(long))
	if len(got) != 1 || len(got[0].Tool) > maxToolText {
		t.Errorf("not clipped: %v", got)
	}
}
