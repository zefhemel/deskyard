package vpn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTailscaleClientEnrollment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/oauth/token":
			r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_secret") != "private" {
				t.Error("incorrect OAuth grant")
			}
			w.Write([]byte(`{"access_token":"access","expires_in":3600}`))
		case "/api/v2/tailnet/home/keys":
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Error("missing bearer")
			}
			var v struct {
				ExpirySeconds int `json:"expirySeconds"`
				Capabilities  struct {
					Devices struct {
						Create struct {
							Reusable, Ephemeral, Preauthorized bool
							Tags                               []string
						} `json:"create"`
					} `json:"devices"`
				} `json:"capabilities"`
			}
			json.NewDecoder(r.Body).Decode(&v)
			if v.ExpirySeconds != 600 || v.Capabilities.Devices.Create.Reusable || v.Capabilities.Devices.Create.Ephemeral || !v.Capabilities.Devices.Create.Preauthorized || len(v.Capabilities.Devices.Create.Tags) != 1 {
				t.Errorf("unsafe enrollment: %+v", v)
			}
			w.Write([]byte(`{"key":"tskey-auth-oneuse"}`))
		case "/api/v2/device/node123":
			if r.Method != "DELETE" {
				t.Error("not delete")
			}
			w.WriteHeader(204)
		default:
			t.Error("unexpected path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := tailscaleClient{baseURL: server.URL, clientID: "id", secret: "private", tailnet: "home", http: server.Client()}
	key, err := c.EnrollKey(context.Background(), []string{"tag:deskyard"})
	if err != nil || key != "tskey-auth-oneuse" {
		t.Fatalf("key: %q %v", key, err)
	}
	if err = c.DeleteDevice(context.Background(), "node123"); err != nil {
		t.Fatal(err)
	}
}
func TestTailscaleClientSanitizesFailures(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				w.Write([]byte("private provider payload"))
			}))
			defer server.Close()
			c := tailscaleClient{baseURL: server.URL, clientID: "id", secret: "private", tailnet: "home", http: server.Client()}
			_, err := c.EnrollKey(context.Background(), []string{"tag:desk"})
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal("unsafe API error", err)
			}
		})
	}
}

func TestTailscaleClientExplainsRejectedDeviceTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/oauth/token" {
			w.Write([]byte(`{"access_token":"access"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"requested tags [tag:deskyard] are invalid or not permitted"}`))
	}))
	defer server.Close()
	c := tailscaleClient{baseURL: server.URL, clientID: "id", secret: "private", tailnet: "home", http: server.Client()}
	_, err := c.EnrollKey(context.Background(), []string{"tag:deskyard"})
	if err == nil {
		t.Fatal("rejected tags accepted")
	}
	if !strings.Contains(err.Error(), "tailnet access policy") || !strings.Contains(err.Error(), "OAuth client") {
		t.Fatalf("tag rejection is not actionable: %v", err)
	}
}
