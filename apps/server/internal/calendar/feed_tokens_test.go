package calendar_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The calendar addresses' paths.
const (
	feedAddressesPath = "/api/v1/calendar/feed"
	feedFilePrefix    = "/api/v1/calendar/feed/"
)

func feedVariantPath(variant string) string { return feedFilePrefix + variant }

// newAddress creates or resets c's address of variant, failing unless 200.
func newAddress(t *testing.T, c *modtest.Client, variant string) gen.FeedAddress {
	t.Helper()
	var a gen.FeedAddress
	do(t, c, http.MethodPost, feedVariantPath(variant), nil, http.StatusOK, &a)
	return a
}

// addresses is c's calendar addresses, failing unless 200.
func addresses(t *testing.T, c *modtest.Client) gen.FeedAddresses {
	t.Helper()
	var out gen.FeedAddresses
	do(t, c, http.MethodGet, feedAddressesPath, nil, http.StatusOK, &out)
	return out
}

// pathOf is an address's path, which a client of the harness requests.
func pathOf(t *testing.T, address string) string {
	t.Helper()
	u, err := url.Parse(address)
	if err != nil {
		t.Fatalf("address %q: %v", address, err)
	}
	return u.Path
}

// tokenIn is an address's token: its last segment without .ics.
func tokenIn(t *testing.T, address string) string {
	t.Helper()
	file := address[strings.LastIndex(address, "/")+1:]
	token, ok := strings.CutSuffix(file, ".ics")
	if !ok || len(token) != 43 {
		t.Fatalf("address %q does not end in a 43-character token and .ics", address)
	}
	return token
}

func TestFeedAddress_CreateAnswersAnAddressUnderAppURLWhoseHashIsStoredAndNotTheToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, me := signIn(t, h)

	a := newAddress(t, c, "full")
	token := tokenIn(t, a.Address)
	if !strings.HasPrefix(a.Address, "http://modules.example.com/api/v1/calendar/feed/") ||
		a.WebcalAddress != "webcal://"+strings.TrimPrefix(a.Address, "http://") {
		t.Errorf("address %q, webcal %q", a.Address, a.WebcalAddress)
	}
	if a.Variant != gen.FeedVariantFull || !a.CreatedAt.Equal(h.Now()) || a.LastUsedAt != nil {
		t.Errorf("the address = %+v", a)
	}
	sum := sha256.Sum256([]byte(token))
	if n := h.Count(t, `SELECT count(*) FROM calendar.feed_tokens WHERE user_id = $1 AND variant = 'full' AND token_sha256 = $2`,
		me, hex.EncodeToString(sum[:])); n != 1 {
		t.Errorf("rows holding the token's hash = %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM calendar.feed_tokens WHERE token_sha256 = $1 OR address_ciphertext LIKE '%' || $1 || '%'`, token); n != 0 {
		t.Errorf("the token itself is in the table (%d rows)", n)
	}

	got := addresses(t, c)
	if got.Public || len(got.Addresses) != 1 || got.Addresses[0].Address != a.Address || got.Addresses[0].LastUsedAt != nil {
		t.Errorf("GET /feed = %+v", got)
	}

	// A read records itself.
	h.Advance(30 * time.Second)
	do(t, h.Client(t), http.MethodGet, pathOf(t, a.Address), nil, http.StatusOK, nil)
	got = addresses(t, c)
	if got.Addresses[0].LastUsedAt == nil || !got.Addresses[0].LastUsedAt.Equal(h.Now()) {
		t.Errorf("lastUsedAt = %v, want %v", got.Addresses[0].LastUsedAt, h.Now())
	}
}

func TestFeedAddress_ASecondPostResetsItAndTheOldAddressIs404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	anonymous := h.Client(t)

	old := newAddress(t, c, "full")
	do(t, anonymous, http.MethodGet, pathOf(t, old.Address), nil, http.StatusOK, nil)
	h.Advance(30 * time.Second)
	reset := newAddress(t, c, "full")
	if reset.Address == old.Address || !reset.CreatedAt.Equal(h.Now()) || reset.LastUsedAt != nil {
		t.Errorf("the reset = %+v, the old %q", reset, old.Address)
	}
	do(t, anonymous, http.MethodGet, pathOf(t, old.Address), nil, http.StatusNotFound, nil)
	do(t, anonymous, http.MethodGet, pathOf(t, reset.Address), nil, http.StatusOK, nil)

	busy := newAddress(t, c, "busy")
	got := addresses(t, c)
	if len(got.Addresses) != 2 || got.Addresses[0].Variant != gen.FeedVariantFull || got.Addresses[1].Variant != gen.FeedVariantBusy ||
		got.Addresses[0].Address != reset.Address || got.Addresses[1].Address != busy.Address {
		t.Errorf("GET /feed = %+v, want full then busy", got.Addresses)
	}
}

func TestFeedAddress_DeleteTurnsItOffAndTheAddressIs404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	other, _ := signIn(t, h)
	a := newAddress(t, c, "busy")

	// Another person's DELETE is of their own rows, and they have none.
	do(t, other, http.MethodDelete, feedVariantPath("busy"), nil, http.StatusNotFound, nil)
	do(t, h.Client(t), http.MethodGet, pathOf(t, a.Address), nil, http.StatusOK, nil)

	do(t, c, http.MethodDelete, feedVariantPath("busy"), nil, http.StatusNoContent, nil)
	do(t, h.Client(t), http.MethodGet, pathOf(t, a.Address), nil, http.StatusNotFound, nil)
	do(t, c, http.MethodDelete, feedVariantPath("busy"), nil, http.StatusNotFound, nil)
	if got := addresses(t, c); len(got.Addresses) != 0 {
		t.Errorf("GET /feed after the delete = %+v", got.Addresses)
	}
}

func TestFeedAddress_IsOnPublicBaseURLWhenItIsSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("PUBLIC_BASE_URL", "https://pay.example.com"))
	c, _ := signIn(t, h)
	a := newAddress(t, c, "full")
	if !strings.HasPrefix(a.Address, "https://pay.example.com/api/v1/calendar/feed/") ||
		a.WebcalAddress != "webcal://"+strings.TrimPrefix(a.Address, "https://") {
		t.Errorf("address %q, webcal %q", a.Address, a.WebcalAddress)
	}
	if got := addresses(t, c); !got.Public {
		t.Error("GET /feed: public = false under PUBLIC_BASE_URL")
	}
	do(t, h.Client(t), http.MethodGet, pathOf(t, a.Address), nil, http.StatusOK, nil)
}

func TestFeedAddress_OneWhoseSealedCopyCannotBeOpenedIsLeftOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, me := signIn(t, h)
	newAddress(t, c, "full")
	busy := newAddress(t, c, "busy")
	h.Exec(t, `UPDATE calendar.feed_tokens SET address_ciphertext = 'not-sealed' WHERE user_id = $1 AND variant = 'full'`, me)
	got := addresses(t, c)
	if len(got.Addresses) != 1 || got.Addresses[0].Address != busy.Address {
		t.Errorf("GET /feed = %+v, want the busy address alone", got.Addresses)
	}
	if !strings.Contains(h.Logs(), "a calendar address could not be opened") {
		t.Error("the unopenable address was not logged")
	}
}

func TestFeed_IsThe404ForATokenNobodyHoldsAFileWithoutIcsAndAVariant(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	a := newAddress(t, c, "full")
	token := tokenIn(t, a.Address)
	anonymous := h.Client(t)
	for _, path := range []string{
		feedFilePrefix + strings.Repeat("A", 43) + ".ics", // nobody holds it
		feedFilePrefix + token,                            // no suffix
		feedFilePrefix + token + ".ICS",
		feedFilePrefix + ".ics",
		feedFilePrefix + "full",
	} {
		r := anonymous.Do(http.MethodGet, path, nil)
		if r.Status != http.StatusNotFound || len(r.Body) != 0 {
			t.Errorf("GET %s = %d %q, want the bare 404", path, r.Status, r.Body)
		}
	}
	// A signed-in caller is answered by the token, not the session.
	do(t, c, http.MethodGet, pathOf(t, a.Address), nil, http.StatusOK, nil)
}

func TestFeed_The121stRequestFromOneAddressIs429(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	anonymous := h.Client(t)
	path := feedFilePrefix + strings.Repeat("B", 43) + ".ics"
	for i := 0; i < 120; i++ {
		do(t, anonymous, http.MethodGet, path, nil, http.StatusNotFound, nil)
	}
	if r := anonymous.Do(http.MethodGet, path, nil); r.Status != http.StatusTooManyRequests {
		t.Errorf("the 121st request = %d, want 429", r.Status)
	}
	do(t, h.Client(t), http.MethodGet, path, nil, http.StatusNotFound, nil)
}
