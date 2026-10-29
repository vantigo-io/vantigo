package entra_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The person's delegated token (Outlook connection design M3, M5): the
// refresh, revision-guarded and its race harmless; Microsoft's refusals to
// reconnect with their codes; the transient ones leaving the row alone; a
// continuous access evaluation claims challenge read as sign_in_again; and
// what a closing connection still allows. Against the fake authorization
// server, through a tap on the token endpoint for what the fake cannot
// answer.

// tokenTap stands before the fake and acts on the refresh_token grants
// alone: it answers the next queued statuses or transport errors in the
// fake's stead, and runs a hook once before the next refresh reaches the
// fake.
type tokenTap struct {
	next   http.RoundTripper
	mu     sync.Mutex
	fail   []int // a status, or 0 for a transport error
	before func()
}

func (w *tokenTap) RoundTrip(req *http.Request) (*http.Response, error) {
	refresh := false
	if req.URL.Host == graphtest.TokenHost && req.Body != nil {
		data, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		refresh = strings.Contains(string(data), "grant_type=refresh_token")
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(data))
	}
	w.mu.Lock()
	next := w.next
	w.mu.Unlock()
	if refresh {
		w.mu.Lock()
		var status *int
		if len(w.fail) > 0 {
			s := w.fail[0]
			w.fail = w.fail[1:]
			status = &s
		}
		hook := w.before
		w.before = nil
		w.mu.Unlock()
		switch {
		case status != nil && *status == 0:
			return nil, errors.New("tokentap: the connection was reset")
		case status != nil:
			return &http.Response{StatusCode: *status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		case hook != nil:
			hook()
		}
	}
	return next.RoundTrip(req)
}

func (w *tokenTap) failNext(statuses ...int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fail = append(w.fail, statuses...)
}

func (w *tokenTap) beforeNext(fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.before = fn
}

// tokenFixture is an Outlook fixture whose transport is a tokenTap over the
// fake, and Kari connected.
type tokenFixture struct {
	*outlookFixture
	tap    *tokenTap
	c      *modtest.Client
	userID uuid.UUID
	oid    uuid.UUID
}

const kari = "kari@nordlys.no"

func newTokenFixture(t *testing.T) *tokenFixture {
	t.Helper()
	tap := &tokenTap{}
	w := newFakeWorkforce()
	h, g := newTenantHarness(t, modtest.WithWorkforce(w), modtest.WithTransport(tap))
	tap.mu.Lock()
	tap.next = g
	tap.mu.Unlock()
	o := connected(t, h)
	setOutlookConnect(t, o, true)
	f := &tokenFixture{outlookFixture: &outlookFixture{h: h, g: g, o: o, w: w}, tap: tap}
	f.c, f.userID, f.oid = f.person(t, kari)
	if r := f.connectAs(t, f.c, f.oid, kari); r.Status != http.StatusOK {
		t.Fatalf("connect: %d %s", r.Status, r.Body)
	}
	return f
}

// token is OutlookToken for Kari's account.
func (f *tokenFixture) token(removal bool) (string, error) {
	return entra.OutlookToken(context.Background(), f.h.Deps(), f.oid, removal)
}

// refreshToken is the refresh token stored for oid, opened.
func (f *outlookFixture) refreshToken(t *testing.T, oid uuid.UUID) string {
	t.Helper()
	sealed := modtest.One[[]byte](t, f.h.Harness, `SELECT refresh_sealed FROM entra.outlook_connections WHERE object_id = $1`, oid)
	if sealed == nil {
		return ""
	}
	plain, err := f.h.Deps().Secrets.Open("entra/outlook-refresh", sealed)
	if err != nil {
		t.Fatal(err)
	}
	return string(plain)
}

// outlookRow is what a test reads of a connection.
type outlookRow struct {
	State    string
	Code     string
	Revision int64
}

func (f *outlookFixture) row(t *testing.T, oid uuid.UUID) outlookRow {
	t.Helper()
	var r outlookRow
	if err := f.h.Pool().QueryRow(context.Background(),
		`SELECT state, COALESCE(error_code, ''), revision FROM entra.outlook_connections WHERE object_id = $1`, oid).
		Scan(&r.State, &r.Code, &r.Revision); err != nil {
		t.Fatal(err)
	}
	return r
}

// validAtGraph reports whether Graph answers GET /me for token.
func (f *outlookFixture) validAtGraph(t *testing.T, token string) bool {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "https://"+graphtest.GraphHost+"/v1.0/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := f.g.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func TestOutlookToken_TheRefreshStoresTheNewTokenAndMovesTheRevision(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	first := f.refreshToken(t, f.oid)
	calls := f.g.RefreshCalls()

	tok, err := f.token(false)
	if err != nil || tok == "" {
		t.Fatalf("token: %q %v", tok, err)
	}
	if !f.validAtGraph(t, tok) {
		t.Error("the access token is not valid at Graph")
	}
	if n := f.g.RefreshCalls(); n != calls+1 {
		t.Errorf("%d refreshes, want 1", n-calls)
	}
	second := f.refreshToken(t, f.oid)
	if second == "" || second == first {
		t.Error("the refresh's new token is not stored")
	}
	if r := f.row(t, f.oid); r.State != "active" || r.Revision != 2 {
		t.Errorf("row = %+v, want active at revision 2", r)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1 AND refreshed_at = $2 AND used_at = $2`, f.oid, f.h.Now()); n != 1 {
		t.Error("refreshed_at and used_at are not the refresh's")
	}

	// Cached by the row's revision: the same token, no refresh.
	if again, err := f.token(false); err != nil || again != tok || f.g.RefreshCalls() != calls+1 {
		t.Errorf("a second token: same %v, %v, %d refreshes", again == tok, err, f.g.RefreshCalls()-calls)
	}
	// A revision moved is another key: refreshed again.
	f.h.Exec(t, `UPDATE entra.outlook_connections SET revision = revision + 1 WHERE object_id = $1`, f.oid)
	if again, err := f.token(false); err != nil || again == tok || f.g.RefreshCalls() != calls+2 {
		t.Errorf("after the revision moved: new %v, %v, %d refreshes", again != tok, err, f.g.RefreshCalls()-calls)
	}

	if id, err := entra.OutlookMe(context.Background(), f.h.Deps(), f.oid); err != nil || id != f.oid.String() {
		t.Errorf("GET /me: %q %v", id, err)
	}
	logs := f.h.Logs()
	for _, secret := range []string{first, second, tok} {
		if strings.Contains(logs, secret[:40]) {
			t.Error("the log carries a token")
		}
		if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE array_to_string(codes, ' ') LIKE '%' || $1 || '%'`, secret[:40]); n != 0 {
			t.Error("an event carries a token")
		}
	}
}

func TestOutlookToken_ALostRaceKeepsTheWinnersToken(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	var winner string
	f.tap.beforeNext(func() {
		// Another call refreshes the same token while this one's is on its
		// way, and stores first.
		var err error
		if winner, err = f.token(false); err != nil {
			t.Errorf("the winner: %v", err)
		}
	})
	loser, err := f.token(false)
	if err != nil {
		t.Fatalf("the loser: %v", err)
	}
	if winner == "" || loser == winner {
		t.Fatalf("winner %q, loser %q: want two access tokens", winner, loser)
	}
	if !f.validAtGraph(t, winner) || !f.validAtGraph(t, loser) {
		t.Error("both access tokens are valid")
	}
	if r := f.row(t, f.oid); r.State != "active" || r.Revision != 2 {
		t.Errorf("row = %+v, want active at revision 2: the winner's write alone", r)
	}
	calls := f.g.RefreshCalls()
	if again, err := f.token(false); err != nil || again != winner || f.g.RefreshCalls() != calls {
		t.Errorf("after the race: the winner's token %v, %v, %d refreshes", again == winner, err, f.g.RefreshCalls()-calls)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.reconnect_needed'`); n != 0 {
		t.Error("the race marked reconnect")
	}
}

func TestOutlookToken_AFailureOnAnOldRevisionAfterACompleteChangesNothing(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	f.g.RefuseRefresh(f.refreshToken(t, f.oid), "invalid_grant", 70008)
	var fresh string
	f.tap.beforeNext(func() {
		// The person reconnects while the old token's refresh is on its way.
		if r := f.connectAs(t, f.c, f.oid, kari); r.Status != http.StatusOK {
			t.Errorf("reconnect: %d %s", r.Status, r.Body)
		}
		fresh = f.refreshToken(t, f.oid)
	})
	_, err := f.token(false)
	if err == nil {
		t.Fatal("a refused refresh answered a token")
	}
	if reason, ok := entra.ConnectionNeeded(err); ok {
		t.Errorf("a refusal of a token since replaced answered connection needed (%s)", reason)
	}
	if r := f.row(t, f.oid); r.State != "active" || r.Code != "" || r.Revision != 2 {
		t.Errorf("row = %+v, want the reconnect's, active at revision 2", r)
	}
	if got := f.refreshToken(t, f.oid); got != fresh {
		t.Error("the reconnect's token was overwritten")
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.reconnect_needed'`); n != 0 {
		t.Error("a refusal of a replaced token was recorded")
	}
	if _, err := f.token(false); err != nil {
		t.Errorf("the next call: %v", err)
	}
}

func TestOutlookToken_RefusalsMoveTheRowToReconnect(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		error string
		codes []int
		want  string
	}{
		{"interaction_required", []int{50076}, "mfa"},
		{"invalid_grant", []int{53003}, "blocked"},
		{"invalid_grant", []int{700082}, "expired"},
	} {
		t.Run(c.want, func(t *testing.T) {
			t.Parallel()
			f := newTokenFixture(t)
			f.g.RefuseRefresh(f.refreshToken(t, f.oid), c.error, c.codes...)
			_, err := f.token(false)
			if reason, ok := entra.ConnectionNeeded(err); !ok || reason != c.want {
				t.Fatalf("token: %v, want connection needed %s", err, c.want)
			}
			if r := f.row(t, f.oid); r.State != "reconnect" || r.Code != c.want || r.Revision != 2 {
				t.Errorf("row = %+v", r)
			}
			if f.refreshToken(t, f.oid) == "" {
				t.Error("reconnect forgot the token")
			}
			if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.reconnect_needed' AND object_id = $1
			    AND upn = $2 AND codes = ARRAY[$3]::text[] AND actor_system = 'system:entra-outlook'`, f.oid, kari, c.want); n != 1 {
				t.Errorf("%d reconnect_needed events, want 1", n)
			}

			// Nothing asks Microsoft again until the person reconnects.
			calls := f.g.RefreshCalls()
			if _, err := f.token(false); func() bool { r, ok := entra.ConnectionNeeded(err); return !ok || r != c.want }() {
				t.Errorf("a second call: %v", err)
			}
			if f.g.RefreshCalls() != calls {
				t.Error("a connection in reconnect was refreshed")
			}
			if got := getOutlook(t, f.c); string(got.State) != "reconnect" || got.ErrorCode == nil || *got.ErrorCode != c.want {
				t.Errorf("the person's read: %+v", got)
			}
			if r := f.connectAs(t, f.c, f.oid, kari); r.Status != http.StatusOK {
				t.Fatalf("reconnect: %d", r.Status)
			}
			if r := f.row(t, f.oid); r.State != "active" || r.Code != "" {
				t.Errorf("after reconnecting: %+v", r)
			}
			if _, err := f.token(false); err != nil {
				t.Errorf("after reconnecting: %v", err)
			}
		})
	}
}

func TestOutlookToken_TransientFailuresLeaveTheRowActive(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	f.tap.failNext(http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusInternalServerError, 0)
	for _, name := range []string{"503", "429", "500", "a transport error"} {
		_, err := f.token(false)
		if !errors.Is(err, entra.ErrTenantUnreachable) {
			t.Errorf("%s: %v, want the tenant unreachable", name, err)
		}
		if reason, ok := entra.ConnectionNeeded(err); ok {
			t.Errorf("%s: connection needed (%s)", name, reason)
		}
	}
	f.g.RefuseRefresh(f.refreshToken(t, f.oid), "temporarily_unavailable")
	if _, err := f.token(false); !errors.Is(err, entra.ErrTenantUnreachable) {
		t.Errorf("temporarily_unavailable: %v", err)
	}
	if r := f.row(t, f.oid); r.State != "active" || r.Revision != 1 {
		t.Errorf("row = %+v, want untouched", r)
	}
	if _, err := f.token(false); err != nil {
		t.Errorf("once the tenant answers: %v", err)
	}
}

func TestOutlookToken_TheClientRefusedMarksTheCredential(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	f.g.RefuseRefresh(f.refreshToken(t, f.oid), "invalid_client", 7000215)
	_, err := f.token(false)
	if !errors.Is(err, entra.ErrCredentialInvalid) {
		t.Errorf("token: %v, want the credential invalid", err)
	}
	if reason, ok := entra.ConnectionNeeded(err); ok {
		t.Errorf("the application's refusal asked the person (%s)", reason)
	}
	if r := f.row(t, f.oid); r.State != "active" || r.Revision != 1 {
		t.Errorf("row = %+v, want untouched", r)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.credentials WHERE last_failed_at IS NOT NULL`); n != 1 {
		t.Error("the secret is not marked failed")
	}
}

func TestOutlookToken_StatesAndRemovals(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	ctx := context.Background()
	if _, err := entra.OutlookToken(ctx, f.h.Deps(), uuid.New(), false); func() bool { r, ok := entra.ConnectionNeeded(err); return !ok || r != "not_connected" }() {
		t.Errorf("no connection: %v", err)
	}

	if r := f.c.Do(http.MethodDelete, outlookConnectionPath, nil); r.Status != http.StatusNoContent {
		t.Fatalf("DELETE: %d", r.Status)
	}
	if _, err := f.token(false); func() bool { r, ok := entra.ConnectionNeeded(err); return !ok || r != "closing" }() {
		t.Errorf("closing, not a removal: %v", err)
	}
	if tok, err := f.token(true); err != nil || tok == "" {
		t.Errorf("closing, a removal: %q %v", tok, err)
	}
	f.h.Exec(t, `UPDATE entra.outlook_connections SET refresh_sealed = NULL, revision = revision + 1 WHERE object_id = $1`, f.oid)
	if _, err := f.token(true); func() bool { r, ok := entra.ConnectionNeeded(err); return !ok || r != "closing" }() {
		t.Errorf("closing without a token, a removal: %v", err)
	}
}

func TestOutlookToken_AnAccountNoLongerEnabledClosesOnUse(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	f.h.Exec(t, `UPDATE entra.accounts SET state = 'disabled' WHERE object_id = $1`, f.oid)
	calls := f.g.RefreshCalls()
	if _, err := f.token(false); func() bool { r, ok := entra.ConnectionNeeded(err); return !ok || r != "closing" }() {
		t.Errorf("token: %v, want connection needed closing", err)
	}
	if f.g.RefreshCalls() != calls {
		t.Error("an account no longer enabled was refreshed")
	}
	if r := f.row(t, f.oid); r.State != "closing" || r.Revision != 2 {
		t.Errorf("row = %+v, want closing", r)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.closed' AND object_id = $1 AND codes = '{disabled}' AND actor_system = 'system:entra-outlook'`, f.oid); n != 1 {
		t.Errorf("%d closed events, want 1", n)
	}
}

func TestOutlookToken_AClaimsChallengeAsksToSignInAgain(t *testing.T) {
	t.Parallel()
	f := newTokenFixture(t)
	ctx := context.Background()
	if _, err := entra.OutlookMe(ctx, f.h.Deps(), f.oid); err != nil {
		t.Fatalf("GET /me: %v", err)
	}
	f.g.ChallengeNext(f.oid.String())
	mark := len(f.g.Calls())
	_, err := entra.OutlookMe(ctx, f.h.Deps(), f.oid)
	if reason, ok := entra.ConnectionNeeded(err); !ok || reason != "sign_in_again" {
		t.Fatalf("GET /me challenged: %v, want connection needed sign_in_again", err)
	}
	me := 0
	for _, c := range f.g.Calls()[mark:] {
		if c.Host == graphtest.GraphHost && c.Path == "/v1.0/me" {
			me++
		}
	}
	if me != 1 {
		t.Errorf("%d calls to /me, want the one challenged and no retry", me)
	}
	if r := f.row(t, f.oid); r.State != "reconnect" || r.Code != "sign_in_again" {
		t.Errorf("row = %+v", r)
	}
	if n := f.h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.reconnect_needed' AND codes = '{sign_in_again}'`); n != 1 {
		t.Errorf("%d reconnect_needed events, want 1", n)
	}
}
