package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestMemoryPolicyAPI(t *testing.T) {
	e := runningReclaimEngine()
	s, ts := newTestServerWith(t, e)
	admin := login(t, ts, "admin")
	anna := login(t, ts, "anna")
	if err := s.Accounts.SetAccess("id-bob", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	put := func(client *http.Client, body, origin string) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/desktops/bob/memory-policy", strings.NewReader(body))
		req.Header.Set("Origin", origin)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if got := put(anna, `{"autoReclaim":false}`, ts.URL); got != 403 {
		t.Fatal(got)
	}
	if got := put(admin, `{"autoReclaim":false}`, "https://elsewhere.example"); got != 403 {
		t.Fatal(got)
	}
	for _, bad := range []string{`{}`, `{"autoReclaim":null}`, `{"autoReclaim":"false"}`} {
		if got := put(admin, bad, ts.URL); got != 400 {
			t.Fatal(bad, got)
		}
	}
	for _, value := range []bool{false, true} {
		body := `{"autoReclaim":false}`
		if value {
			body = `{"autoReclaim":true}`
		}
		if got := put(admin, body, ts.URL); got != 204 {
			t.Fatal(got)
		}
		res, err := admin.Get(ts.URL + "/api/desktops/bob")
		if err != nil {
			t.Fatal(err)
		}
		var c desktopJSON
		err = json.NewDecoder(res.Body).Decode(&c)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if c.AutoReclaim != value || c.State != "running" {
			t.Fatalf("%+v", c)
		}
		p, err := loadMemoryPolicy(s.memoryPolicy.path)
		if err != nil || p.enabled("id-bob") != value {
			t.Fatal("preference not saved", err)
		}
	}
	if e.calls.Load() != 0 || len(s.Desktops.Busy()) != 0 {
		t.Fatal("setting operated on desktop")
	}
}
