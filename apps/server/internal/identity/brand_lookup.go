package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The sign-in page's lookup of a brand by the domain of the typed address
// (brands design B25). It is keyed on the domain alone, so it cannot answer
// differently for an address that has an account, and it answers an
// unattached domain with the default brand in the same shape and status.

// brandLookupTTL is how long a by-domain answer may be cached (B25).
const brandLookupTTL = 300 * time.Second

// Results the lookup counts (B39).
const (
	lookupDefault   = "default"
	lookupMatched   = "matched"
	lookupUnmatched = "unmatched"
	lookupInvalid   = "invalid"
	lookupLimited   = "limited"
)

// lookupDomain is the normalised domain of a by-domain lookup, or false when
// it does not look like one: dotted labels of letters, digits and hyphens (not
// at either end), the last of two or more letters (or a punycode label).
func lookupDomain(raw string) (string, bool) {
	d, ok := normalizeBrandDomain(raw)
	if !ok {
		return "", false
	}
	labels := strings.Split(d, ".")
	for _, l := range labels {
		if l[0] == '-' || l[len(l)-1] == '-' {
			return "", false
		}
		for _, c := range l {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", false
			}
		}
	}
	tld := labels[len(labels)-1]
	if strings.HasPrefix(tld, "xn--") && len(tld) > 4 {
		return d, true
	}
	if len(tld) < 2 {
		return "", false
	}
	for _, c := range tld {
		if c < 'a' || c > 'z' {
			return "", false
		}
	}
	return d, true
}

// domainHash is what the logs keep of a typed domain (B39): never the domain.
func domainHash(domain string) string {
	sum := sha256.Sum256([]byte(domain))
	return hex.EncodeToString(sum[:8])
}

// brandForDomain is the brand of the first domain rule of a published brand
// that names domain, and whether there was one; else the default brand. One
// read of the live rules either way.
func (s *server) brandForDomain(ctx context.Context, domain string) (brandRow, brandVersion, bool, error) {
	def, err := s.defaultBrand(ctx)
	if err != nil {
		return brandRow{}, brandVersion{}, false, err
	}
	rules, err := s.q.ListLiveBrandRules(ctx)
	if err != nil {
		return brandRow{}, brandVersion{}, false, fmt.Errorf("identity: read the brand rules: %w", err)
	}
	for _, r := range rules {
		if r.Kind != ruleKindDomain || r.TargetRef != domain {
			continue
		}
		b, found, err := s.loadBrand(ctx, s.q, r.BrandID)
		if err != nil {
			return brandRow{}, brandVersion{}, false, err
		}
		if found && b.pub != nil {
			return b, *b.pub, true, nil
		}
	}
	return def, publishedOrBuiltin(def), false, nil
}

// observeBrandLookup counts and logs the by-domain lookups the router's rate
// limit refuses before the handler runs: a rising rate of them is the signal
// of enumeration probing (B39). It logs the hash of the domain, never the
// domain.
func (s *server) observeBrandLookup(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/brand/public") || !r.URL.Query().Has("domain") {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusTooManyRequests {
			s.metrics.publicLookups.Add(r.Context(), 1, resultAttr(lookupLimited))
			attrs := []any{}
			if d, ok := lookupDomain(r.URL.Query().Get("domain")); ok {
				attrs = append(attrs, "domain_hash", domainHash(d))
			}
			s.deps.Logger.InfoContext(r.Context(), "identity: a brand lookup by domain was rate limited", attrs...)
		}
	})
}

// statusRecorder remembers the status a handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
