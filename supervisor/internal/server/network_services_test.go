package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestTailscaleServiceWritesRequireAdmin(t *testing.T) {
	_, ts := newTestServer(t)
	user := login(t, ts, "anna")
	req, _ := http.NewRequest("PUT", ts.URL+"/api/environments/alice/network/services", strings.NewReader(`{"services":[]}`))
	req.Header.Set("Origin", ts.URL)
	resp, err := user.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("non-admin service write: %d", resp.StatusCode)
	}
}
