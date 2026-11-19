package identity_test

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type lookupBody struct {
	BrandID string `json:"brandId"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Matched *bool  `json:"matched"`
}

func lookup(c *client, domain string) *resp {
	return c.do(http.MethodGet, brandPublicPath+"?domain="+url.QueryEscape(domain), nil)
}

func lookupOf(t *testing.T, c *client, domain string) lookupBody {
	t.Helper()
	r := lookup(c, domain)
	if r.status != http.StatusOK {
		t.Fatalf("lookup %q: status %d body %s", domain, r.status, r.body)
	}
	var b lookupBody
	r.json(&b)
	return b
}

func TestBrandLookup_AnAttachedDomainAnswersItsBrandAndAnyOtherTheDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	nordlys := publishedBrand(t, owner, "Nordlys", tealPrimary)
	setRules(t, owner, ruleItem{BrandID: nordlys.ID, Kind: "domain", TargetRef: "nordlys.example"})
	anon := h.client(t)

	got := lookupOf(t, anon, "Nordlys.Example.")
	if got.BrandID != nordlys.ID || got.Name != "Nordlys" || got.Source != "rule" || got.Matched == nil || !*got.Matched {
		t.Errorf("attached domain = %+v", got)
	}
	other := lookupOf(t, anon, "elsewhere.example")
	if other.BrandID != defaultBrandID(t, owner) || other.Source != "default" || other.Matched == nil || *other.Matched {
		t.Errorf("unattached domain = %+v", other)
	}
	r := lookup(anon, "nordlys.example")
	if cc := r.header("Cache-Control"); cc != "public, max-age=300" {
		t.Errorf("Cache-Control = %q, want public, max-age=300", cc)
	}
	if v := r.header("Vary"); strings.Contains(strings.ToLower(v), "cookie") {
		t.Errorf("Vary = %q: the answer reads no cookie", v)
	}
	// Without a domain: the default brand, cached for a minute, as before.
	var plain lookupBody
	pr := anon.do(http.MethodGet, brandPublicPath, nil)
	pr.json(&plain)
	if plain.BrandID != defaultBrandID(t, owner) || pr.header("Cache-Control") != "public, max-age=60" || plain.Matched == nil || *plain.Matched {
		t.Errorf("no domain: %+v, Cache-Control %q", plain, pr.header("Cache-Control"))
	}
}

// The same domain answers the same bytes whether or not a person has an
// account on it, and whatever the client knew of the address (B25: it sends the domain and nothing else).
func TestBrandLookup_TheAnswerDoesNotDependOnAccountsOrOtherParameters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	nordlys := publishedBrand(t, owner, "Nordlys", tealPrimary)
	fjord := publishedBrand(t, owner, "Fjordkraft", amberPrimary)
	setRules(t, owner,
		ruleItem{BrandID: nordlys.ID, Kind: "domain", TargetRef: "nordlys.example"},
		ruleItem{BrandID: fjord.ID, Kind: "domain", TargetRef: "fjord.example"})
	h.personAt(t, "ola@nordlys.example") // an account on one attached domain, none on the other
	anon := h.client(t)

	withAccount := lookup(anon, "nordlys.example")
	again := lookup(anon, "nordlys.example")
	if withAccount.status != http.StatusOK || !bytes.Equal(withAccount.body, again.body) {
		t.Errorf("the same request twice differs:\n%s\n%s", withAccount.body, again.body)
	}
	noAccount := lookup(anon, "fjord.example")
	var a, b lookupBody
	withAccount.json(&a)
	noAccount.json(&b)
	if noAccount.status != withAccount.status || a.Source != b.Source || *a.Matched != *b.Matched ||
		noAccount.header("Cache-Control") != withAccount.header("Cache-Control") {
		t.Errorf("a domain with an account and one without answer differently: %+v / %+v", a, b)
	}
	// An unattached domain with an account answers like one without.
	h.personAt(t, "kari@plain.example")
	withUnattached, withoutUnattached := lookup(anon, "plain.example"), lookup(anon, "empty.example")
	if withUnattached.status != withoutUnattached.status || !bytes.Equal(withUnattached.body, withoutUnattached.body) {
		t.Errorf("an unattached domain answers by its accounts:\n%s\n%s", withUnattached.body, withoutUnattached.body)
	}
}

func TestBrandLookup_RefusesWhatIsNotADomain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	anon := h.client(t)
	for _, bad := range []string{
		"ola@nordlys.example", "@nordlys.example", "localhost", "nordlys", "a.b-", "-a.example", "nordlys.e", "nordlys.123",
		"a..example", "no rdlys.example", "nordlys.example/path", "*.example", "", strings.Repeat("a", 64) + ".example",
		strings.Repeat("a.", 130) + "example",
	} {
		r := lookup(anon, bad)
		if r.status != http.StatusBadRequest || r.code() != "domain_invalid" {
			t.Errorf("domain %q: status %d code %q body %s, want 400 domain_invalid", bad, r.status, r.code(), r.body)
		}
	}
	for _, good := range []string{"nordlys.example", "NORDLYS.NO", "a-b.c1.example.", "bücher.example", "nordlys.xn--p1ai"} {
		if r := lookup(anon, good); r.status != http.StatusOK {
			t.Errorf("domain %q: status %d body %s, want 200", good, r.status, r.body)
		}
	}
}

func TestBrandLookup_OnlyPublishedBrandsAnswer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	draft := createBrand(t, owner, "Utkast", tealPrimary)
	live := publishedBrand(t, owner, "Levende", amberPrimary)
	gone := publishedBrand(t, owner, "Borte", "#1d4ed8")
	setRules(t, owner,
		ruleItem{BrandID: live.ID, Kind: "domain", TargetRef: "levende.example"},
		ruleItem{BrandID: gone.ID, Kind: "domain", TargetRef: "borte.example"})
	if r := putRules(owner, getRules(t, owner).Revision,
		ruleItem{BrandID: live.ID, Kind: "domain", TargetRef: "levende.example"},
		ruleItem{BrandID: gone.ID, Kind: "domain", TargetRef: "borte.example"},
		ruleItem{BrandID: draft.ID, Kind: "domain", TargetRef: "utkast.example"}); r.status == http.StatusOK {
		// A rule may point at a brand not yet published; it matches nothing until it is (B20).
		if got := lookupOf(t, h.client(t), "utkast.example"); got.Source != "default" || *got.Matched {
			t.Errorf("a draft brand's domain = %+v, want the default", got)
		}
	}
	archiveBrand(t, owner, gone.ID)
	anon := h.client(t)
	if got := lookupOf(t, anon, "levende.example"); got.BrandID != live.ID || !*got.Matched {
		t.Errorf("published = %+v", got)
	}
	if got := lookupOf(t, anon, "borte.example"); got.BrandID == gone.ID || *got.Matched || got.Source != "default" {
		t.Errorf("an archived brand's rule answered: %+v", got)
	}
}

func TestBrandLookup_IsRateLimitedPerAddressAndLogsOnlyTheDomainHash(t *testing.T) {
	t.Parallel()
	reader := metric.NewManualReader()
	h := newHarness(t, withBrandMeter(metric.NewMeterProvider(metric.WithReader(reader))))
	owner, _ := h.bootstrapOwner(t)
	nordlys := publishedBrand(t, owner, "Nordlys", tealPrimary)
	setRules(t, owner, ruleItem{BrandID: nordlys.ID, Kind: "domain", TargetRef: "secretcustomer.example"})
	anon := h.client(t)
	for i := 1; i <= 30; i++ {
		if r := lookup(anon, "secretcustomer.example"); r.status != http.StatusOK {
			t.Fatalf("request %d: status %d", i, r.status)
		}
	}
	r := lookup(anon, "secretcustomer.example")
	if r.status != http.StatusTooManyRequests || r.header("Retry-After") == "" {
		t.Errorf("31st request: status %d Retry-After %q, want 429 with Retry-After", r.status, r.header("Retry-After"))
	}
	if r := lookup(h.client(t), "secretcustomer.example"); r.status != http.StatusOK {
		t.Errorf("another address: status %d, want 200", r.status)
	}

	logs := string(h.log.Bytes())
	if strings.Contains(logs, "secretcustomer") {
		t.Errorf("the typed domain reached the logs:\n%s", logs)
	}
	if !strings.Contains(logs, `"domain_hash"`) || !strings.Contains(logs, "rate limited") {
		t.Errorf("no hashed lookup line, or no rate-limit line, in the logs")
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	results := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if s, ok := m.Data.(metricdata.Sum[int64]); ok && m.Name == "vantigo.identity.brand.public_lookups" {
				for _, dp := range s.DataPoints {
					v, _ := dp.Attributes.Value("result")
					results[v.AsString()] += dp.Value
				}
			}
		}
	}
	if results["matched"] != 31 || results["limited"] != 1 {
		t.Errorf("lookup counters = %v, want matched 31 and limited 1", results)
	}
}
