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
	if err := s.Accounts.SetAccess("bob", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	put := func(client *http.Client, body, origin string) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/environments/bob/memory-policy", strings.NewReader(body))
		req.Header.Set("Origin", origin)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if got := put(anna, `{"memoryReclaim":false}`, ts.URL); got != 403 {
		t.Fatal(got)
	}
	if got := put(admin, `{"memoryReclaim":false}`, "https://elsewhere.example"); got != 403 {
		t.Fatal(got)
	}
	for _, bad := range []string{`{}`, `{"memoryReclaim":null}`, `{"memoryReclaim":"false"}`} {
		if got := put(admin, bad, ts.URL); got != 400 {
			t.Fatal(bad, got)
		}
	}
	for _, value := range []bool{false, true} {
		body := `{"memoryReclaim":false}`
		if value {
			body = `{"memoryReclaim":true}`
		}
		if got := put(admin, body, ts.URL); got != 204 {
			t.Fatal(got)
		}
		res, err := admin.Get(ts.URL + "/api/environments/bob")
		if err != nil {
			t.Fatal(err)
		}
		var c environmentJSON
		err = json.NewDecoder(res.Body).Decode(&c)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if c.MemoryReclaim != value || c.State != "running" {
			t.Fatalf("%+v", c)
		}
		if def, ok := s.Environments.Defs.Get("bob"); !ok || def.MemoryReclaim != value || def.Revision != 1 {
			t.Fatal("preference not saved in the definition", def)
		}
	}
	if e.calls.Load() != 0 || len(s.Environments.Busy()) != 0 {
		t.Fatal("setting operated on desktop")
	}
}
