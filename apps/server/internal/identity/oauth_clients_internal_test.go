package identity

import (
	"strings"
	"testing"
)

// The rules of delivery C that need no database: a registered client_id's
// shape, the allow-list's entries and C11's match.

func TestRegisteredClientID(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id := newRegisteredClientID()
		if !isRegisteredClientID(id) || len(id) != 28 {
			t.Fatalf("minted %q", id)
		}
		if seen[id] {
			t.Fatalf("minted %q twice", id)
		}
		seen[id] = true
	}
	for _, id := range []string{"", "vtc_", "vtc_" + strings.Repeat("a", 23), "vtc_" + strings.Repeat("a", 25), "vtc_" + strings.Repeat("a", 23) + "-",
		"vta_" + strings.Repeat("a", 24), "VTC_" + strings.Repeat("a", 24), "vtc_" + strings.Repeat("a", 23) + "é"} {
		if isRegisteredClientID(id) {
			t.Errorf("%q is a registered client_id", id)
		}
	}
	// A token never has a registered client_id's shape, nor the reverse.
	access, _ := newPrefixedToken(accessTokenPrefix)
	if isRegisteredClientID(access) {
		t.Error("an access token has a client_id's shape")
	}
	if _, ok := parsePrefixedToken(accessTokenPrefix, newRegisteredClientID()); ok {
		t.Error("a client_id parses as an access token")
	}
}

func TestNormalizeClientHosts(t *testing.T) {
	got, ok := normalizeClientHosts([]string{" Claude.AI", "chatgpt.com", "claude.ai", "my-app.example.co.uk"})
	if !ok || strings.Join(got, " ") != "chatgpt.com claude.ai my-app.example.co.uk" {
		t.Errorf("got %v %v", got, ok)
	}
	if got, ok := normalizeClientHosts(nil); !ok || len(got) != 0 {
		t.Errorf("empty: %v %v", got, ok)
	}
	for _, bad := range []string{"", "localhost", "*.claude.ai", ".claude.ai", "claude.ai.", "203.0.113.9", "[::1]", "claude.ai:443",
		"https://claude.ai", "xn--bcher-kva.example", "bücher.example", "claude ai", "-claude.ai", strings.Repeat("a", 64) + ".com"} {
		if _, ok := normalizeClientHosts([]string{bad}); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
	many := make([]string, 51)
	for i := range many {
		many[i] = "h" + strings.Repeat("x", i) + ".example.com"
	}
	if _, ok := normalizeClientHosts(many); ok {
		t.Error("51 hosts were accepted")
	}
	if _, ok := normalizeClientHosts(append(many[:50], many[0])); !ok {
		t.Error("50 hosts and a repeat were refused")
	}
}

func TestClientHostAllowed(t *testing.T) {
	list := []string{"claude.ai", "example.com"}
	for host, want := range map[string]bool{
		"claude.ai":         true,
		"CLAUDE.AI":         true,
		"api.claude.ai":     true,
		"a.b.example.com":   true,
		"notclaude.ai":      false,
		"claude.ai.evil.io": false,
		"chatgpt.com":       false,
		"ai":                false,
	} {
		if got := clientHostAllowed(list, host); got != want {
			t.Errorf("%s: %v, want %v", host, got, want)
		}
	}
	if !clientHostAllowed(nil, "anything.example") {
		t.Error("an empty list refused a host")
	}
}
