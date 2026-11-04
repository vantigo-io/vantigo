package google_test

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Test connection (test.go): its states, the scopes granted, missing and
// unexpected, the organisation, and Google's words kept out of everything.

// itoa is n in decimal.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestTest_UnconfiguredAsksGoogleNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := owner(t, h)
	result := testConnection(t, o)
	if result.Status != gen.GoogleConnectionTestStatusUnconfigured || !slices.Equal(result.Missing, everyScope) || len(result.Granted) != 0 {
		t.Errorf("the test before a key: %+v", result)
	}
	if n := len(h.g.Calls()); n != 0 {
		t.Errorf("the test made %d calls to Google", n)
	}
}

func TestTest_BeforeDelegationIsPending(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, ownerID := configured(t, h)
	result := testConnection(t, o)
	if result.Status != gen.GoogleConnectionTestStatusPendingDelegation || result.Reason == nil || *result.Reason != "not_delegated" ||
		!slices.Equal(result.Missing, everyScope) || len(result.Granted) != 0 || result.Customer != nil {
		t.Errorf("the test before delegation: %+v", result)
	}
	conn := getConnection(t, o)
	if conn.Status != gen.GoogleConnectionStatusPendingDelegation || conn.StatusReason == nil || *conn.StatusReason != "not_delegated" || conn.LastCheckedAt == nil {
		t.Errorf("the connection: %+v", conn)
	}
	// Every token was asked as the admin, one scope each, fresh.
	for _, c := range h.g.Calls() {
		if c.Host == googletest.TokenHost && (c.Subject != adminAddress || strings.Contains(c.Scope, " ")) {
			t.Errorf("a token asked as %q for %q", c.Subject, c.Scope)
		}
	}
	codes := modtest.One[[]string](t, h.Harness, `SELECT codes FROM google.events WHERE action = 'google.connection.tested' AND actor_user_id = $1`, ownerID)
	if len(codes) != 2+len(everyScope) || codes[0] != "pending_delegation" || codes[1] != "not_delegated" || codes[2] != "missing:"+googletest.ScopeCustomer {
		t.Errorf("the event's codes = %v", codes)
	}
}

func TestTest_DelegatedIsConnectedWithTheOrganisation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := configured(t, h)
	h.g.Delegate(saClientID, everyScope...)
	result := testConnection(t, o)
	if result.Status != gen.GoogleConnectionTestStatusConnected || result.Reason != nil || !slices.Equal(result.Granted, everyScope) ||
		len(result.Missing) != 0 || len(result.Unexpected) != 0 {
		t.Errorf("the test: %+v", result)
	}
	if !slices.Equal(result.Domains, []string{"nordlys.example", "nordlys.no"}) || result.Customer == nil || result.Customer.Id != customerID ||
		result.Customer.OrganisationName == nil || *result.Customer.OrganisationName != "Nordlys AS" ||
		result.Customer.PrimaryDomain == nil || *result.Customer.PrimaryDomain != "nordlys.no" {
		t.Errorf("the organisation: %+v %v", result.Customer, result.Domains)
	}
	conn := getConnection(t, o)
	if conn.Status != gen.GoogleConnectionStatusConnected || conn.CustomerId == nil || *conn.CustomerId != customerID ||
		!slices.Equal(conn.ScopesGranted, everyScope) || conn.Version != 2 || conn.LastCheckedAt == nil || !conn.LastCheckedAt.Equal(h.Now()) {
		t.Errorf("the connection: %+v", conn)
	}
	// The test's own tokens served the Directory's reads: six scopes, nine
	// probes, two reads.
	calls := h.g.Calls()
	if len(calls) != 17 {
		t.Errorf("%d calls, want 15 tokens and 2 reads: %+v", len(calls), calls)
	}
}

func TestTest_SomeMissingIsConnectedWithTheMissingAndUnexpectedListed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, ownerID := configured(t, h)
	h.g.Delegate(saClientID, googletest.ScopeCustomer, "https://mail.google.com/", "https://www.googleapis.com/auth/drive")
	result := testConnection(t, o)
	if result.Status != gen.GoogleConnectionTestStatusConnected || !slices.Equal(result.Missing, everyScope[1:]) ||
		!slices.Equal(result.Unexpected, []string{"https://mail.google.com/", "https://www.googleapis.com/auth/drive"}) {
		t.Errorf("the test: %+v", result)
	}
	conn := getConnection(t, o)
	if !slices.Equal(conn.ScopesMissing, everyScope[1:]) || len(conn.ScopesUnexpected) != 2 {
		t.Errorf("the connection: %+v", conn)
	}
	codes := modtest.One[[]string](t, h.Harness, `SELECT codes FROM google.events WHERE action = 'google.connection.tested' AND actor_user_id = $1`, ownerID)
	if codes[len(codes)-1] != "unexpected:https://www.googleapis.com/auth/drive" {
		t.Errorf("the event's codes = %v", codes)
	}
}

func TestTest_TheFailures(t *testing.T) {
	t.Parallel()
	first, _, _ := accounts()

	t.Run("the key refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		o, _ := configured(t, h)
		h.g.Delegate(saClientID, everyScope...)
		h.g.DeleteKey(first)
		result := testConnection(t, o)
		if result.Status != gen.GoogleConnectionTestStatusFailing || result.Reason == nil || *result.Reason != "credential_invalid" {
			t.Errorf("the test: %+v", result)
		}
		if conn := getConnection(t, o); conn.Status != gen.GoogleConnectionStatusFailing || conn.Credentials[0].LastFailedAt == nil {
			t.Errorf("the connection: %+v", conn)
		}
		// Refused again before it worked: recorded once.
		h.Advance(time.Minute)
		testConnection(t, o)
		if n := h.Count(t, `SELECT count(*) FROM google.events WHERE action = 'google.credential.refused'`); n != 1 {
			t.Errorf("%d google.credential.refused events, want 1", n)
		}
	})
	t.Run("an admin Google does not know", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		o, _ := configured(t, h)
		h.g.Delegate(saClientID, everyScope...)
		setAdmin(t, o, "nobody@nordlys.no")
		if result := testConnection(t, o); result.Status != gen.GoogleConnectionTestStatusFailing || *result.Reason != "admin_refused" {
			t.Errorf("the test: %+v", result)
		}
		// The subject was refused, not the key: nothing marks it.
		conn := getConnection(t, o)
		if conn.Status != gen.GoogleConnectionStatusFailing || conn.StatusReason == nil || *conn.StatusReason != "admin_refused" || conn.Credentials[0].LastFailedAt != nil {
			t.Errorf("the connection: %+v", conn)
		}
		if n := h.Count(t, `SELECT count(*) FROM google.events WHERE action = 'google.credential.refused'`); n != 0 {
			t.Errorf("%d google.credential.refused events, want none", n)
		}
	})
	t.Run("the admin refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		o, _ := configured(t, h)
		h.g.Delegate(saClientID, everyScope...)
		h.g.Configure(func(g *googletest.FakeGoogle) { g.AdminRefused = true })
		if result := testConnection(t, o); result.Status != gen.GoogleConnectionTestStatusFailing || *result.Reason != "admin_refused" {
			t.Errorf("the test: %+v", result)
		}
	})
	t.Run("Google unreachable", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		o, _ := configured(t, h)
		h.g.Delegate(saClientID, everyScope...)
		h.g.FailNext(http.StatusServiceUnavailable, http.StatusServiceUnavailable)
		if result := testConnection(t, o); result.Status != gen.GoogleConnectionTestStatusFailing || *result.Reason != "google_unreachable" {
			t.Errorf("the test: %+v", result)
		}
	})
	t.Run("one 5xx is retried", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		o, _ := configured(t, h)
		h.g.Delegate(saClientID, everyScope...)
		h.g.FailNext(http.StatusInternalServerError)
		if result := testConnection(t, o); result.Status != gen.GoogleConnectionTestStatusConnected {
			t.Errorf("the test: %+v", result)
		}
	})
}

func TestTest_AnotherSubjectGoogleDoesNotKnowMarksNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := configured(t, h)
	h.g.Delegate(saClientID, everyScope...)
	if result := testConnection(t, o); result.Status != gen.GoogleConnectionTestStatusConnected {
		t.Fatalf("the test: %+v", result)
	}
	// A person's call, as a later delivery makes, for an address Google
	// does not know: that person's problem, not the key's or the connection's.
	_, err := google.Token(context.Background(), h.Deps(), everyScope, "gone@nordlys.no")
	if !google.SubjectInvalid(err) {
		t.Fatalf("the token for an unknown person: %v, want the subject refused", err)
	}
	conn := getConnection(t, o)
	if conn.Status != gen.GoogleConnectionStatusConnected || conn.Credentials[0].LastFailedAt != nil {
		t.Errorf("the connection: %+v", conn)
	}
	if n := h.Count(t, `SELECT count(*) FROM google.events WHERE action = 'google.credential.refused'`); n != 0 {
		t.Errorf("%d google.credential.refused events, want none", n)
	}
}

func TestTest_TheProbesTokensAreNotKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := configured(t, h)
	drive := "https://www.googleapis.com/auth/drive"
	h.g.Delegate(saClientID, append(slices.Clone(everyScope), drive)...)
	if result := testConnection(t, o); !slices.Equal(result.Unexpected, []string{drive}) {
		t.Fatalf("the test: %+v", result)
	}
	sealed := modtest.One[[]byte](t, h.Harness, `SELECT sealed FROM google.credentials WHERE kind = 'service_account_key'`)
	if cached := google.CachedScopes(sealed); !slices.Equal(cached, slices.Sorted(slices.Values(everyScope))) {
		t.Errorf("the tokens kept: %v, want the needed scopes' alone", cached)
	}
}

func TestTest_AnAdminChangedDuringTheTestIsNotWritten(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := configured(t, h)
	h.g.Delegate(saClientID, everyScope...)
	// The admin changes while the test asks Google, as a PUT by another
	// Owner would.
	var once sync.Once
	h.g.Configure(func(g *googletest.FakeGoogle) {
		g.OnCall = func(googletest.Call) {
			once.Do(func() {
				h.Exec(t, `UPDATE google.connection SET admin_subject = 'other.admin@nordlys.no', status = 'unconfigured', version = version + 1 WHERE id = 1`)
			})
		}
	})
	if result := testConnection(t, o); result.Status != gen.GoogleConnectionTestStatusConnected || result.Customer == nil {
		t.Errorf("the test's answer: %+v, want what it found", result)
	}
	conn := getConnection(t, o)
	if conn.Status != gen.GoogleConnectionStatusUnconfigured || conn.LastCheckedAt != nil || len(conn.ScopesGranted) != 0 || conn.CustomerId != nil {
		t.Errorf("the connection: %+v, want the other admin's untouched", conn)
	}
	if n := h.Count(t, `SELECT count(*) FROM google.events WHERE action = 'google.connection.tested'`); n != 0 {
		t.Errorf("%d google.connection.tested events, want none", n)
	}
}

func TestTest_ALongOrganisationNameIsCut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := configured(t, h)
	h.g.Delegate(saClientID, everyScope...)
	h.g.Configure(func(g *googletest.FakeGoogle) { g.Organisation = strings.Repeat("Ø", 300) })
	result := testConnection(t, o)
	if result.Customer == nil || result.Customer.OrganisationName == nil || *result.Customer.OrganisationName != strings.Repeat("Ø", 256) {
		t.Errorf("the organisation: %+v", result.Customer)
	}
}

func TestTest_GooglesWordsAreNeverKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o, _ := configured(t, h)
	h.g.Delegate(saClientID, googletest.ScopeCustomer)
	h.g.Configure(func(g *googletest.FakeGoogle) { g.AdminRefused = true })
	r := o.Do(http.MethodPost, testPath, nil)
	words := googletest.New().Words
	if strings.Contains(string(r.Body), words) {
		t.Errorf("the answer carries Google's words")
	}
	if strings.Contains(h.Logs(), words) {
		t.Errorf("the log carries Google's words")
	}
	if strings.Contains(h.Logs(), adminAddress) {
		t.Errorf("the log carries the admin's address")
	}
	if n := h.Count(t, `SELECT count(*) FROM google.events WHERE array_to_string(codes, ' ') LIKE '%'||$1||'%'`, words); n != 0 {
		t.Errorf("an event carries Google's words")
	}
}
