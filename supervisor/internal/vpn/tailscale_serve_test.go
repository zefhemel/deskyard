package vpn

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestServiceRuleValidation(t *testing.T) {
	for _, rules := range [][]ServiceRule{
		{{LocalPort: 8080, ListenPort: 443, Audience: "public"}},
		{{LocalPort: 3000, ListenPort: 80, Audience: "public"}},
		{{LocalPort: 3000, ListenPort: 443, Audience: "private"}, {LocalPort: 4000, ListenPort: 443, Audience: "public"}},
		{{LocalPort: 0, ListenPort: 443, Audience: "private"}},
		{{LocalPort: 3000, ListenPort: 443, Audience: "everyone"}},
	} {
		if _, err := NormalizeServiceRules(rules); err == nil {
			t.Fatal("unsafe rules accepted", rules)
		}
	}
	rules, err := NormalizeServiceRules([]ServiceRule{{LocalPort: 3000, ListenPort: 443, Audience: "private"}})
	if err != nil || len(rules) != 1 {
		t.Fatal(err)
	}
}
func TestServeConfigAudiences(t *testing.T) {
	rules := []ServiceRule{{LocalPort: 3000, ListenPort: 443, Audience: "private"}, {LocalPort: 4000, ListenPort: 8443, Audience: "public"}}
	c, err := buildServeConfig("alice.home.ts.net", rules)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	text := string(b)
	if strings.Contains(text, `"alice.home.ts.net:443":true`) || !strings.Contains(text, `"alice.home.ts.net:8443":true`) || !strings.Contains(text, "http://127.0.0.1:3000") {
		t.Fatal("wrong audience/backend", text)
	}
	if _, err = buildServeConfig("host\nmalicious", rules); err == nil {
		t.Fatal("invalid hostname accepted")
	}
}

// The double models only the external tailscaled boundary; configuration and
// observed status pass through the production reconciler and JSON parser.
