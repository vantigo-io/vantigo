package meta_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/meta/metatest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Every Graph call carries an appsecret_proof: the hex HMAC-SHA256 of the
// access token under the app secret.
func TestGraphCalls_CarryTheAppSecretProof(t *testing.T) {
	g := metatest.NewFakeGraph()
	g.AddPage("5550001", "Nordlys", "page-token-proof", "MESSAGING")
	h := newHarness(t, modtest.WithTransport(g))
	o, _ := owner(t, h)
	configure(t, o)
	if r := o.Do(http.MethodPost, pagesPath, map[string]any{"accessToken": "page-token-proof"}); r.Status != http.StatusCreated {
		t.Fatalf("add the Page: %d %s", r.Status, r.Body)
	}
	calls := g.Calls()
	if len(calls) == 0 {
		t.Fatal("no Graph call was made")
	}
	want := signature(appSecret, []byte("page-token-proof"))[len("sha256="):]
	for _, c := range calls {
		if got := c.Query.Get("appsecret_proof"); got != want {
			t.Errorf("%s %s: appsecret_proof = %q, want %q", c.Method, c.Path, got, want)
		}
	}
}
