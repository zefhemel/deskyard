package server

import (
	"strings"
	"testing"
)

func TestScreenAdapterTag(t *testing.T) {
	for _, tc := range []struct {
		page, want string
		supported  bool
	}{
		{`<head></head>`, additionsTag, true},
		{`<meta name="deskyard-client-contract" content="1">`, modernAdditionsTag, true},
		{`<meta name="deskyard-client-contract" content="2">`, "", false},
	} {
		got, err := screenAdapterTag([]byte(tc.page))
		if (err == nil) != tc.supported || got != tc.want {
			t.Fatalf("screenAdapterTag(%q) = %q, %v", tc.page, got, err)
		}
	}
	if !strings.Contains(modernAdditionsTag, `type="module"`) {
		t.Fatal("typed adapter must run before the client's following module scripts")
	}
}
