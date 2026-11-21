package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file is how Vantigo knows an OAuth client (OAuth connections design
// C6, C7, C9, A1, A2, A4): a client_id that is an https URL names a client
// metadata document, which Vantigo fetches through the guarded transport,
// holds to C6's rules and caches; and the redirect URIs it lists are the only
// ones a code is ever sent to.

const (
	maxOAuthClientIDLength    = 512
	maxOAuthRedirectURILength = 1024
	maxOAuthRedirectURIs      = 20
	// oauthDocumentMaxBytes is the most of a document read, counted after
	// decompression; reaching it is a refusal (A2).
	oauthDocumentMaxBytes = 16 << 10
	// oauthDocumentBudget is one fetch's whole budget (C6).
	oauthDocumentBudget = 5 * time.Second
	// The cache's bounds on a document's max-age, and its default (C7).
	oauthDocumentMinAge     = 5 * time.Minute
	oauthDocumentMaxAge     = 24 * time.Hour
	oauthDocumentDefaultAge = time.Hour
	// oauthFetchSpacing is how often one client_id may be fetched, in this
	// process (C7).
	oauthFetchSpacing = 10 * time.Second
	// oauthFetchTracked is the most client_ids the fetcher remembers trying
	// in the last minute; while it is full a new client_id is not fetched.
	oauthFetchTracked = 10000
	// oauthUserAgent is the fixed User-Agent of a fetch (A2).
	oauthUserAgent = "Vantigo"
	// oauthClientKindMetadata is a client identified by its metadata document.
	oauthClientKindMetadata = "metadata"
)

// oauthClient is a client Vantigo has verified: its client_id, the host its
// document was fetched from, the name it calls itself and the redirect URIs
// it registered.
type oauthClient struct {
	id           string
	host         string
	name         string
	redirectURIs []string
}

// errOAuthClient is a client_id, a document or a redirect URI refused: the
// consent page says so and never redirects (C13, A6). Its text is the
// reason, in words a person reads; it never quotes the document.
type errOAuthClient struct{ reason string }

func (e errOAuthClient) Error() string { return e.reason }

func clientRefused(reason string) error { return errOAuthClient{reason: reason} }

// oauthClientIDURL holds a client_id to C6's shape and answers it parsed: an
// https URL of at most 512 characters, its host in ASCII and no label in
// punycode (A2), port 443 or none, no userinfo, no fragment, a path other
// than "/" with no dot segment, and a host that is not this installation's.
func oauthClientIDURL(clientID string, ownHosts ...string) (*url.URL, error) {
	if clientID == "" || len(clientID) > maxOAuthClientIDLength {
		return nil, clientRefused("The client_id is missing or too long.")
	}
	if !isASCIIPrintable(clientID) {
		return nil, clientRefused("The client_id is not a URL Vantigo can fetch.")
	}
	u, err := url.Parse(clientID)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return nil, clientRefused("The client_id must be an https URL.")
	}
	if u.User != nil || u.Fragment != "" || strings.Contains(clientID, "#") {
		return nil, clientRefused("The client_id may not carry a user or a fragment.")
	}
	if p := u.Port(); p != "" && p != "443" {
		return nil, clientRefused("The client_id must be on port 443.")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || net.ParseIP(host) != nil || !validDNSName(host) {
		return nil, clientRefused("The client_id's host must be a DNS name.")
	}
	path := u.EscapedPath()
	if path == "" || path == "/" || hasDotSegment(path) {
		return nil, clientRefused("The client_id must name a document, with no dot segments.")
	}
	for _, own := range ownHosts {
		if own != "" && strings.EqualFold(host, own) {
			return nil, clientRefused("The client_id may not be on this installation's own host.")
		}
	}
	return u, nil
}

// validDNSName reports whether host is a DNS name of ASCII letters, digits
// and hyphens, at least two labels, none of them punycode: a host that
// decodes to other letters is refused rather than shown in a form that hides
// what it is (A2, A7).
func validDNSName(host string) bool {
	if len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || strings.HasPrefix(l, "xn--") || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// hasDotSegment reports whether an escaped path has a "." or ".." segment,
// spelled out or percent-encoded.
func hasDotSegment(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			return true
		}
		if decoded == "." || decoded == ".." {
			return true
		}
	}
	return false
}

func isASCIIPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	return true
}

// validRedirectURI holds a redirect URI to C9: https with a host, or http on
// 127.0.0.1, [::1] or localhost; no fragment; at most 1024 characters.
func validRedirectURI(raw string) bool {
	if raw == "" || len(raw) > maxOAuthRedirectURILength || !isASCIIPrintable(raw) || strings.Contains(raw, "#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Hostname() != ""
	case "http":
		return isLoopbackHost(u.Hostname())
	}
	return false
}

// isLoopbackHost is the three loopback hosts C9 admits over http.
func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// isLoopbackRedirect reports whether a valid redirect URI is an http
// loopback one, where a port is the client's to choose (C9).
func isLoopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "http" && isLoopbackHost(u.Hostname())
}

// redirectMatches compares a request's redirect_uri with a registered one
// (A4): both parsed; a non-loopback URI by string equality; an http loopback
// URI by its scheme, host, path and query, the port ignored, and a registered
// URI without a port matching any. localhost and 127.0.0.1 are different
// hosts.
func redirectMatches(registered, requested string) bool {
	if !validRedirectURI(requested) {
		return false
	}
	if !isLoopbackRedirect(registered) {
		return registered == requested
	}
	reg, err1 := url.Parse(registered)
	req, err2 := url.Parse(requested)
	if err1 != nil || err2 != nil || req.Scheme != "http" {
		return false
	}
	return reg.Hostname() == req.Hostname() && reg.EscapedPath() == req.EscapedPath() && reg.RawQuery == req.RawQuery
}

// oauthDocument is a client metadata document as C6 reads it; every other
// member is ignored, and nothing it links to is ever fetched or shown.
type oauthDocument struct {
	ClientID                          *string   `json:"client_id"`
	ClientName                        *string   `json:"client_name"`
	RedirectURIs                      *[]string `json:"redirect_uris"`
	TokenEndpointAuthMethod           *string   `json:"token_endpoint_auth_method"`
	TokenEndpointAuthMethodsSupported *[]string `json:"token_endpoint_auth_methods_supported"`
	GrantTypes                        *[]string `json:"grant_types"`
	ResponseTypes                     *[]string `json:"response_types"`
	ClientSecret                      any       `json:"client_secret"`
}

// acceptOAuthDocument holds a fetched document to C6 and A1 for clientID and
// answers the client it describes. The document's own members are trusted
// for nothing but its name and its redirect URIs.
func acceptOAuthDocument(clientID, host string, body []byte) (oauthClient, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
		return oauthClient{}, clientRefused("The client's metadata document is not a JSON object.")
	}
	if _, ok := raw["client_secret"]; ok {
		return oauthClient{}, clientRefused("The client's metadata document carries a secret.")
	}
	var doc oauthDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return oauthClient{}, clientRefused("The client's metadata document is not valid.")
	}
	if doc.ClientID == nil || *doc.ClientID != clientID {
		return oauthClient{}, clientRefused("The client's metadata document names another client_id.")
	}
	if doc.ClientName == nil || strings.TrimSpace(*doc.ClientName) == "" {
		return oauthClient{}, clientRefused("The client's metadata document has no client_name.")
	}
	if doc.RedirectURIs == nil || len(*doc.RedirectURIs) == 0 || len(*doc.RedirectURIs) > maxOAuthRedirectURIs {
		return oauthClient{}, clientRefused("The client's metadata document must list 1 to 20 redirect_uris.")
	}
	for _, uri := range *doc.RedirectURIs {
		if !validRedirectURI(uri) {
			return oauthClient{}, clientRefused("The client's metadata document lists a redirect URI Vantigo does not accept.")
		}
	}
	// A1: a list of supported methods decides when there is one, and must
	// offer none; otherwise the one method named must be none, or absent.
	if doc.TokenEndpointAuthMethodsSupported != nil {
		if !slices.Contains(*doc.TokenEndpointAuthMethodsSupported, "none") {
			return oauthClient{}, clientRefused("The client cannot connect as a public client (token_endpoint_auth_methods_supported).")
		}
	} else if doc.TokenEndpointAuthMethod != nil && *doc.TokenEndpointAuthMethod != "none" {
		return oauthClient{}, clientRefused("The client cannot connect as a public client (token_endpoint_auth_method).")
	}
	if doc.GrantTypes != nil && !slices.Contains(*doc.GrantTypes, "authorization_code") {
		return oauthClient{}, clientRefused("The client's metadata document does not use the authorization code grant.")
	}
	if doc.ResponseTypes != nil && !slices.Contains(*doc.ResponseTypes, "code") {
		return oauthClient{}, clientRefused("The client's metadata document does not use the code response type.")
	}
	return oauthClient{id: clientID, host: host, name: *doc.ClientName, redirectURIs: *doc.RedirectURIs}, nil
}

// oauthDisplayName is the name a client claims as the consent page shows it
// (C13): control and bidirectional characters stripped, spaces collapsed, cut
// at 80 characters.
func oauthDisplayName(name string) string {
	name = strings.ToValidUTF8(name, "")
	var b strings.Builder
	space := false
	for _, r := range name {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || unicode.Is(unicode.Cf, r):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteRune(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > 80 {
		out = string([]rune(out)[:80])
	}
	return out
}

// oauthNetwork is what a client document's fetch resolves, dials and trusts
// with, as webhookNetwork is the sender's: the zero value is production's,
// and a test swaps it (UseOAuthNetwork) so it reaches a fake host with a
// resolver and roots of its own. Every fetch still goes through
// netguard.DialContext.
type oauthNetwork = webhookNetwork

// UseOAuthNetwork makes every client metadata document fetch resolve names
// with resolver, dial with dial and trust tlsConfig, still through
// netguard.DialContext, which checks each resolved address. It is for tests
// that reach a fake client host, and must run before the module mounts.
func (a *Access) UseOAuthNetwork(resolver netguard.Resolver, dial func(ctx context.Context, network, address string) (net.Conn, error), tlsConfig *tls.Config) {
	a.oauthNet = &oauthNetwork{resolver: resolver, dial: dial, tls: tlsConfig}
}

// oauthFetcher fetches and caches client documents for one Access.
type oauthFetcher struct {
	client *http.Client
	mu     sync.Mutex
	// tried is when each client_id was last fetched in this process (C7).
	tried map[string]time.Time
}

// oauthFetcherFor is the one fetcher of a, built on first use over a's
// network.
func (a *Access) oauthFetcherFor() *oauthFetcher {
	a.oauthOnce.Do(func() {
		n := oauthNetwork{}
		if a.oauthNet != nil {
			n = *a.oauthNet
		}
		transport := webhookGuardedTransport(n)
		transport.ResponseHeaderTimeout = oauthDocumentBudget
		a.oauthFetch = &oauthFetcher{
			client: &http.Client{
				Transport: transport,
				Timeout:   oauthDocumentBudget,
				// No redirect is followed (C6): a 3xx is a refused client.
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			},
			tried: map[string]time.Time{},
		}
	})
	return a.oauthFetch
}

// oauthClientFor answers the client clientID names: from the cache while its
// document is fresh, else fetched, held to C6 and cached for its max-age. A
// failure is never cached, and a client_id is fetched at most once per 10
// seconds in this process. Any refusal is an errOAuthClient; only a failure
// to read or write the cache is another error.
func (a *Access) oauthClientFor(ctx context.Context, clientID string) (oauthClient, error) {
	u, err := oauthClientIDURL(clientID, a.cfg.AppHostname, a.cfg.PublicHostname)
	if err != nil {
		return oauthClient{}, err
	}
	host := strings.ToLower(u.Hostname())
	now := a.now()
	cached, err := a.q.GetOAuthClientDocument(ctx, store.GetOAuthClientDocumentParams{ClientID: clientID, Now: now})
	switch {
	case err == nil:
		if c, err := acceptOAuthDocument(clientID, host, cached); err == nil {
			return c, nil
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return oauthClient{}, fmt.Errorf("identity: read the client's document: %w", err)
	}

	f := a.oauthFetcherFor()
	f.mu.Lock()
	if last, ok := f.tried[clientID]; ok && now.Sub(last) < oauthFetchSpacing && now.Sub(last) >= 0 {
		f.mu.Unlock()
		return oauthClient{}, clientRefused("The client's metadata document was fetched moments ago and could not be used; try again shortly.")
	}
	for id, at := range f.tried {
		if now.Sub(at) > time.Minute {
			delete(f.tried, id)
		}
	}
	if len(f.tried) >= oauthFetchTracked {
		f.mu.Unlock()
		return oauthClient{}, clientRefused("Too many client documents are being fetched; try again shortly.")
	}
	f.tried[clientID] = now
	f.mu.Unlock()

	body, maxAge, err := f.fetch(ctx, u.String())
	if err != nil {
		return oauthClient{}, err
	}
	c, err := acceptOAuthDocument(clientID, host, body)
	if err != nil {
		return oauthClient{}, err
	}
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, body); err != nil {
		return oauthClient{}, clientRefused("The client's metadata document is not valid.")
	}
	if err := a.q.PutOAuthClientDocument(ctx, store.PutOAuthClientDocumentParams{
		ClientID: clientID, Document: compact.Bytes(), Now: now, ExpiresAt: now.Add(maxAge),
	}); err != nil {
		return oauthClient{}, fmt.Errorf("identity: keep the client's document: %w", err)
	}
	return c, nil
}

// fetch GETs a document (C6, A2): Accept: application/json, the fixed
// User-Agent, no redirect followed, 200 only, at most 16 KiB after
// decompression. It answers the body and the max-age to keep it for.
func (f *oauthFetcher) fetch(ctx context.Context, target string) ([]byte, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthDocumentBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, clientRefused("The client_id is not a URL Vantigo can fetch.")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", oauthUserAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, netguard.ErrDisallowed) {
			return nil, 0, clientRefused("The client's host resolves to an address Vantigo does not reach.")
		}
		return nil, 0, clientRefused("The client's metadata document could not be fetched.")
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, oauthDocumentMaxBytes))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, clientRefused("The client's metadata document answered " + strconv.Itoa(resp.StatusCode) + ", not 200.")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, oauthDocumentMaxBytes))
	if err != nil {
		return nil, 0, clientRefused("The client's metadata document could not be read.")
	}
	if len(body) >= oauthDocumentMaxBytes {
		return nil, 0, clientRefused("The client's metadata document is larger than 16 KiB.")
	}
	return body, documentMaxAge(resp.Header.Get("Cache-Control")), nil
}

// documentMaxAge is a document's Cache-Control max-age clamped to 5 minutes
// to 24 hours, one hour when it has none (C7).
func documentMaxAge(cacheControl string) time.Duration {
	for _, part := range strings.Split(cacheControl, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "max-age") {
			continue
		}
		secs, err := strconv.Atoi(strings.Trim(strings.TrimSpace(value), `"`))
		if err != nil || secs < 0 {
			break
		}
		return min(max(time.Duration(secs)*time.Second, oauthDocumentMinAge), oauthDocumentMaxAge)
	}
	return oauthDocumentDefaultAge
}
