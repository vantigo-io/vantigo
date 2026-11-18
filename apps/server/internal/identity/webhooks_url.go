package identity

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file holds an endpoint's URL to its rules (outgoing webhooks design
// W11, W28). The save-time resolve is advice for a friendly answer, not the
// boundary: the sender's dial checks every address again on every attempt
// (webhooks_client.go).

const (
	maxWebhookURLLength = 2048
	// webhookResolveTimeout bounds the save-time lookup, so a slow resolver
	// never holds a request.
	webhookResolveTimeout = 5 * time.Second
)

// The words of a URL refused for its form, as the field error says.
const webhookURLMessage = "An https:// address of at most 2,048 characters with a host name, no user name or password, no fragment and no port but 443."

var (
	webhookURLBlocked      = refuse(http.StatusUnprocessableEntity, "webhook_url_blocked", "The address resolves to a private, loopback or reserved address, which Vantigo never sends to.", map[string][]string{"url": {"The address resolves to an address Vantigo never sends to."}})
	webhookURLUnresolvable = refuse(http.StatusUnprocessableEntity, "webhook_url_unresolvable", "The address's host name does not resolve.", map[string][]string{"url": {"The host name does not resolve."}})
)

// reservedWebhookHosts are the names W11 refuses outright, and the suffixes
// whose every name it refuses.
var (
	reservedWebhookHosts    = []string{"localhost"}
	reservedWebhookSuffixes = []string{".localhost", ".local", ".internal"}
)

// parseWebhookURL holds raw to W11's form: https, a host name that is not an
// IP literal, localhost or a name under .localhost, .local or .internal and
// has at least one dot, no userinfo, no fragment, port empty or 443, at most
// 2,048 characters. A query string is allowed (receivers carry tokens in
// it). It answers the URL as given, trimmed.
func parseWebhookURL(raw string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxWebhookURLLength || strings.ContainsAny(raw, "#\\ \t\r\n") {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.RawFragment != "" {
		return nil, false
	}
	if !strings.HasPrefix(raw, "https://") {
		return nil, false // a scheme written in capitals, which url.Parse lowers
	}
	if port := u.Port(); port != "" && port != "443" {
		return nil, false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || !strings.Contains(host, ".") || strings.HasPrefix(u.Host, "[") {
		return nil, false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil, false
	}
	for _, name := range reservedWebhookHosts {
		if host == name {
			return nil, false
		}
	}
	for _, suffix := range reservedWebhookSuffixes {
		if strings.HasSuffix(host, suffix) {
			return nil, false
		}
	}
	return u, true
}

// checkWebhookHost resolves u's host once and refuses it, as a save does:
// webhook_url_unresolvable when it has no address, webhook_url_blocked when
// any address is one netguard refuses.
func checkWebhookHost(ctx context.Context, resolver netguard.Resolver, u *url.URL) error {
	ctx, cancel := context.WithTimeout(ctx, webhookResolveTimeout)
	defer cancel()
	addrs, err := resolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(addrs) == 0 {
		return webhookURLUnresolvable
	}
	for _, addr := range addrs {
		if netguard.Disallowed(addr) {
			return webhookURLBlocked
		}
	}
	return nil
}

// webhookURLOrigin is the URL as the audit, notices and log lines carry it
// (W28): its scheme and host only, since Zapier, Slack and Make carry their
// token in the path and others in the query.
func webhookURLOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
