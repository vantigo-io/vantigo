package identity

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestWebhookURL_TheRulesOfW11(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{
		"https://hooks.example.com/in",
		"https://hooks.example.com:443/in?token=abc&x=1",
		"https://hooks.zapier.com/hooks/catch/123/abc/",
		"https://Hooks.Example.COM./in",
		"  https://hooks.example.com  ",
	} {
		if _, valid := parseWebhookURL(ok); !valid {
			t.Errorf("%q was refused", ok)
		}
	}
	for _, bad := range []string{
		"",
		"http://hooks.example.com/in",
		"HTTPS://hooks.example.com/in",
		"ftp://hooks.example.com",
		"https://user:pass@hooks.example.com/",
		"https://user@hooks.example.com/",
		"https://hooks.example.com/in#frag",
		"https://hooks.example.com:8443/in",
		"https://hooks.example.com:80/in",
		"https://127.0.0.1/in",
		"https://[::1]/in",
		"https://10.0.0.1/in",
		"https://localhost/in",
		"https://localhost./in",
		"https://app.localhost/in",
		"https://printer.local/in",
		"https://metadata.google.internal/in",
		"https://intranet/in",
		"https:hooks.example.com",
		"https://hooks.example.com/a b",
		"https://hooks.example.com/" + strings.Repeat("a", maxWebhookURLLength),
	} {
		if _, valid := parseWebhookURL(bad); valid {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// TestWebhookURL_TheHostIsCheckedAndStoredInASCII holds S1: the host is
// normalised by IDNA's lookup profile before any check, so a full-width or
// ideographic dot cannot hide a reserved suffix, and the URL kept is the
// ASCII form it will resolve as.
func TestWebhookURL_TheHostIsCheckedAndStoredInASCII(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"https://foo．internal/in", // a full-width full stop
		"https://foo。local/in",    // an ideographic full stop
		"https://LOCALHOST．/in",   // localhost, with a full-width trailing dot
		"https://app．localhost/in",
		"https://xn--/in", // punycode that does not decode
	} {
		if _, ok := parseWebhookURL(bad); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
	for raw, want := range map[string]string{
		"https://bücher.example/in?q=1":                  "https://xn--bcher-kva.example/in?q=1",
		"https://XN--BCHER-KVA.example/in":               "https://xn--bcher-kva.example/in",
		"https://Hooks.Example.COM./catch/T0K3N?token=x": "https://hooks.example.com/catch/T0K3N?token=x",
		"https://hooks.example.com:443/in":               "https://hooks.example.com/in",
		"https://hooks．example.com/in":                   "https://hooks.example.com/in",
	} {
		u, ok := parseWebhookURL(raw)
		if !ok {
			t.Errorf("%q was refused", raw)
			continue
		}
		if got := u.String(); got != want {
			t.Errorf("%q is kept as %q, want %q", raw, got, want)
		}
	}
}

func TestWebhookURL_TheSaveTimeResolveIsAdvice(t *testing.T) {
	t.Parallel()
	u, _ := parseWebhookURL("https://hooks.example.com/in")
	public := fixedResolver{addrs: []netip.Addr{netip.MustParseAddr("93.184.215.14"), netip.MustParseAddr("2606:2800:21f:cb07:6820:80da:af6b:8b2c")}}
	if err := checkWebhookHost(t.Context(), public, u); err != nil {
		t.Errorf("a public host: %v", err)
	}
	for name, r := range map[string]fixedResolver{
		"one private address": {addrs: []netip.Addr{netip.MustParseAddr("93.184.215.14"), netip.MustParseAddr("10.1.2.3")}},
		"the metadata":        {addrs: []netip.Addr{netip.MustParseAddr("169.254.169.254")}},
	} {
		var ref refusal
		if err := checkWebhookHost(t.Context(), r, u); !errors.As(err, &ref) || ref.body.Error.Code != "webhook_url_blocked" {
			t.Errorf("%s: %v, want webhook_url_blocked", name, err)
		}
	}
	for name, r := range map[string]fixedResolver{
		"no such host": {err: &net.DNSError{Err: "no such host", IsNotFound: true}},
		"no address":   {},
	} {
		var ref refusal
		if err := checkWebhookHost(t.Context(), r, u); !errors.As(err, &ref) || ref.body.Error.Code != "webhook_url_unresolvable" {
			t.Errorf("%s: %v, want webhook_url_unresolvable", name, err)
		}
	}
}

func TestWebhookURL_TheOriginHasNoPathOrQuery(t *testing.T) {
	t.Parallel()
	if got := webhookURLOrigin("https://hooks.slack.com/services/T000/B000/XXXX?token=y"); got != "https://hooks.slack.com" {
		t.Errorf("origin %q", got)
	}
}
