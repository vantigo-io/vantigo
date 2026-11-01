package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The assistant grant is identity's side of the Assistant design S3. These
// tests run Check and Issue against the real tables, with the fixture's
// accounts in every state a rule distinguishes.

// grants is identity's AssistantGrants over the harness's Access.
func grants(h *harness) contracts.AssistantGrants {
	return identity.Module(h.access).AssistantGrants(module.Deps{})
}

// messageContext is the context of the person's own message request: a request
// carrying their cookie, attached the way the router attaches it.
func messageContext(token string) context.Context {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/messages", nil)
	req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
	return contracts.WithRequest(req.Context(), httptest.NewRecorder(), req)
}

// issue issues a grant for token's session, failing the test when refused.
func issue(t *testing.T, h *harness, token string, ttl time.Duration) uuid.UUID {
	t.Helper()
	id, err := grants(h).Issue(messageContext(token), uuid.New(), uuid.New(), ttl)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return id
}

// checkGrant runs Check for rule on an in-process request carrying grantID on
// its context, and cookie when it is not "" (which Check must ignore).
func checkGrant(t *testing.T, h *harness, grantID uuid.UUID, cookie, rule string) (contracts.Principal, outcome) {
	t.Helper()
	r, err := contracts.ParseRule(rule)
	if err != nil {
		t.Fatalf("rule %q: %v", rule, err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: cookie})
	}
	req = req.WithContext(contracts.WithAssistantGrant(req.Context(), grantID))
	p, err := h.access.Check(req, r)
	switch {
	case err == nil:
		return p, allowed
	case errors.Is(err, contracts.ErrUnauthenticated):
		return p, u401
	case errors.Is(err, contracts.ErrForbidden):
		return p, f403
	default:
		t.Fatalf("Check(%s): unexpected error %v", rule, err)
		return p, 0
	}
}

func TestAssistantGrant_AnswersTheSessionsOwnPrincipalWithVia(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	h := f.h

	// The rules a grant is held to, for every account whose session is live and
	// who is not locked out, must come out exactly as the session's own.
	rules := slices.DeleteFunc(slices.Clone(accessRules), func(rule string) bool {
		return rule == "scim" || rule == "policy:Owner" || rule == "policy:OwnerManagement" ||
			rule == "policy:Owner+OwnerManagement" || rule == "policy:SystemAdmin" ||
			rule == "policy:Owner+SystemAdmin" || rule == "policy:AuthorizationManagement"
	})
	for _, name := range []string{"ownerMFA", "ownerNoMFA", "ownerAdminMFA", "adminMFA", "adminNoMFA", "user", "userPermitted"} {
		t.Run(name, func(t *testing.T) {
			conversationID, runID := uuid.New(), uuid.New()
			id, err := grants(h).Issue(messageContext(f.tokens[name]), conversationID, runID, 5*time.Minute)
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}
			for _, rule := range rules {
				want, wantOutcome := check(t, h, f.tokens[name], rule)
				got, gotOutcome := checkGrant(t, h, id, "", rule)
				if gotOutcome != wantOutcome {
					t.Errorf("%s: grant answered %v, the session %v", rule, gotOutcome, wantOutcome)
					continue
				}
				if gotOutcome != allowed {
					continue
				}
				via := got.Via
				if via == nil || via.Kind != contracts.ViaAssistant || via.ConversationID != conversationID || via.RunID != runID {
					t.Errorf("%s: Via = %+v, want the assistant, the conversation and the run", rule, via)
				}
				got.Via = nil
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: principal = %+v, the session's is %+v", rule, got, want)
				}
				if want.Via != nil {
					t.Errorf("%s: the session's own principal has Via %+v, want nil", rule, want.Via)
				}
			}
		})
	}
}

func TestAssistantGrant_NeverSatisfiesTheOwnerTierOrSCIM(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	// The Owner who is also SystemAdmin with MFA satisfies every one of these
	// by session; by grant, none.
	token := f.tokens["ownerAdminMFA"]
	id := issue(t, f.h, token, time.Minute)
	for _, rule := range []string{
		"policy:Owner", "policy:OwnerManagement", "policy:Owner+OwnerManagement",
		"policy:SystemAdmin", "policy:Owner+SystemAdmin", "policy:AuthorizationManagement",
		"policy:ActiveAccount+Owner", "scim",
	} {
		if _, got := checkGrant(t, f.h, id, "", rule); got != f403 {
			t.Errorf("%s: grant answered %v, want ErrForbidden", rule, got)
		}
	}
	if _, got := check(t, f.h, token, "policy:Owner"); got != allowed {
		t.Fatalf("the session itself must satisfy policy:Owner for this test to mean anything, got %v", got)
	}

	// A valid SCIM bearer token beside the grant buys nothing either.
	r, _ := contracts.ParseRule("scim")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer identity-harness-scim-token")
	req = req.WithContext(contracts.WithAssistantGrant(req.Context(), id))
	if _, err := f.h.access.Check(req, r); !errors.Is(err, contracts.ErrForbidden) {
		t.Errorf("scim rule with a grant and a valid token: %v, want ErrForbidden", err)
	}
}

func TestAssistantGrant_EveryRefusal(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	h := f.h
	rule := "permission:identity:manage"

	t.Run("an unknown grant", func(t *testing.T) {
		if _, got := checkGrant(t, h, uuid.New(), "", "session"); got != u401 {
			t.Errorf("answered %v, want ErrUnauthenticated", got)
		}
		if p, got := checkGrant(t, h, uuid.New(), "", "anonymous"); got != allowed || p.UserID != uuid.Nil {
			t.Errorf("anonymous: %v %+v, want the caller with no session", got, p)
		}
	})
	t.Run("an expired grant", func(t *testing.T) {
		id := issue(t, h, f.tokens["userPermitted"], time.Minute)
		if _, got := checkGrant(t, h, id, "", rule); got != allowed {
			t.Fatalf("before expiry: %v", got)
		}
		h.advance(2 * time.Minute)
		if _, got := checkGrant(t, h, id, "", rule); got != u401 {
			t.Errorf("after expiry: %v, want ErrUnauthenticated", got)
		}
	})
	t.Run("a revoked grant", func(t *testing.T) {
		id := issue(t, h, f.tokens["userPermitted"], time.Hour)
		if err := grants(h).Revoke(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		if _, got := checkGrant(t, h, id, "", rule); got != u401 {
			t.Errorf("revoked: %v, want ErrUnauthenticated", got)
		}
		if err := grants(h).Revoke(context.Background(), id); err != nil {
			t.Errorf("revoking a revoked grant: %v, want no error", err)
		}
		if err := grants(h).Revoke(context.Background(), uuid.New()); err != nil {
			t.Errorf("revoking an unknown grant: %v, want no error", err)
		}
	})
	t.Run("every grant of a user revoked", func(t *testing.T) {
		a, b := issue(t, h, f.tokens["user"], time.Hour), issue(t, h, f.tokens["user"], time.Hour)
		other := issue(t, h, f.tokens["userPermitted"], time.Hour)
		if err := grants(h).RevokeForUser(context.Background(), f.users["user"]); err != nil {
			t.Fatal(err)
		}
		for _, id := range []uuid.UUID{a, b} {
			if _, got := checkGrant(t, h, id, "", "session"); got != u401 {
				t.Errorf("a revoked user's grant: %v, want ErrUnauthenticated", got)
			}
		}
		if _, got := checkGrant(t, h, other, "", "session"); got != allowed {
			t.Errorf("another user's grant: %v, want it untouched", got)
		}
	})
	t.Run("the session signed out", func(t *testing.T) {
		userID := h.seedUser(t, "signs-out@example.test", "a long enough password", identity.RoleUserID)
		c := h.signIn(t, userID, false)
		token := c.cookie(identity.SessionCookieName)
		id := issue(t, h, token, time.Hour)
		if _, got := checkGrant(t, h, id, "", "session"); got != allowed {
			t.Fatalf("before sign-out: %v", got)
		}
		if r := c.do(http.MethodPost, logoutPath, nil, origin(h.url)); r.status != http.StatusOK {
			t.Fatalf("logout: status %d", r.status)
		}
		if _, got := checkGrant(t, h, id, "", "session"); got != u401 {
			t.Errorf("after sign-out: %v, want ErrUnauthenticated: signing out ends the assistant at its next step", got)
		}
	})
	t.Run("the user disabled", func(t *testing.T) {
		userID := h.insertUser(t, "later-disabled@example.test", identity.RoleUserID)
		id := issue(t, h, h.session(t, userID, false), time.Hour)
		h.exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, userID)
		if _, got := checkGrant(t, h, id, "", "session"); got != u401 {
			t.Errorf("a disabled user: %v, want ErrUnauthenticated (the session is gone)", got)
		}
	})
	t.Run("the user locked out", func(t *testing.T) {
		userID := h.insertUser(t, "later-locked@example.test", identity.RoleUserID)
		id := issue(t, h, h.session(t, userID, false), time.Hour)
		if _, got := checkGrant(t, h, id, "", "session"); got != allowed {
			t.Fatalf("before the lockout: %v", got)
		}
		h.exec(t, `UPDATE identity.users SET lockout_end = $2 WHERE id = $1`, userID, h.now().Add(10*time.Minute))
		for _, r := range []string{"session", "policy:ActiveAccount", "permission:identity:manage"} {
			if _, got := checkGrant(t, h, id, "", r); got != f403 {
				t.Errorf("%s for a locked-out user: %v, want ErrForbidden", r, got)
			}
		}
		h.advance(11 * time.Minute)
		if _, got := checkGrant(t, h, id, "", "session"); got != allowed {
			t.Errorf("after the lockout ended: %v, want the grant to hold again", got)
		}
	})
	t.Run("the user loses the permission", func(t *testing.T) {
		userID := h.insertUser(t, "loses-role@example.test", identity.RoleUserID)
		role := h.insertRole(t, "Temporary", "identity:manage")
		h.exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, userID, role)
		id := issue(t, h, h.session(t, userID, false), time.Hour)
		if _, got := checkGrant(t, h, id, "", rule); got != allowed {
			t.Fatalf("holding the permission: %v", got)
		}
		h.exec(t, `DELETE FROM identity.user_roles WHERE user_id = $1 AND role_id = $2`, userID, role)
		if _, got := checkGrant(t, h, id, "", rule); got != f403 {
			t.Errorf("after losing it: %v, want ErrForbidden at the very next call", got)
		}
	})
	// Last: it moves the clock past every fixture session.
	t.Run("the session expired", func(t *testing.T) {
		userID := h.insertUser(t, "idle@example.test", identity.RoleUserID)
		token := h.session(t, userID, false)
		id := issue(t, h, token, time.Hour)
		h.advance(400 * 24 * time.Hour)
		if _, got := checkGrant(t, h, id, "", "session"); got != u401 {
			t.Errorf("after the session's absolute limit: %v, want ErrUnauthenticated", got)
		}
	})
}

func TestAssistantGrant_IsOnlyEverReadFromTheContext(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	h := f.h
	id := issue(t, h, f.tokens["userPermitted"], time.Hour)

	rule, _ := contracts.ParseRule("session")
	for name, build := range map[string]func(*http.Request){
		"a header":      func(r *http.Request) { r.Header.Set("X-Assistant-Grant", id.String()) },
		"authorization": func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+id.String()) },
		"a cookie":      func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "assistant_grant", Value: id.String()}) },
		"the session cookie": func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: id.String()})
		},
		"a query parameter": func(r *http.Request) { r.URL.RawQuery = "grant=" + id.String() },
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			build(req)
			if _, err := h.access.Check(req, rule); !errors.Is(err, contracts.ErrUnauthenticated) {
				t.Errorf("Check = %v, want ErrUnauthenticated: a grant is not a credential", err)
			}
		})
	}

	t.Run("a cookie beside a grant is ignored", func(t *testing.T) {
		p, got := checkGrant(t, h, id, f.tokens["ownerMFA"], "session")
		if got != allowed || p.UserID != f.users["userPermitted"] {
			t.Errorf("answered %v for %v, want the grant's user %v, not the cookie's", got, p.UserID, f.users["userPermitted"])
		}
	})
	t.Run("the nil grant is no grant", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(contracts.WithAssistantGrant(req.Context(), uuid.Nil))
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: f.tokens["userPermitted"]})
		if p, err := h.access.Check(req, rule); err != nil || p.Via != nil {
			t.Errorf("Check = %+v, %v, want the cookie's session with no Via", p, err)
		}
	})
}

func TestAssistantGrant_IssueNeedsASessionRequest(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	h := f.h
	g := grants(h)
	conversationID, runID := uuid.New(), uuid.New()

	cases := map[string]struct {
		ctx context.Context
		ttl time.Duration
		err error
	}{
		"no request":        {context.Background(), time.Minute, contracts.ErrNoSessionRequest},
		"an AsUser context": {contracts.AsUser(context.Background(), f.users["user"]), time.Minute, contracts.ErrNoSessionRequest},
		"a forged principal": {
			contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: f.users["user"], SessionID: uuid.New()}),
			time.Minute, contracts.ErrNoSessionRequest,
		},
		"a request without a cookie": {messageContext(""), time.Minute, contracts.ErrNoSessionRequest},
		"a request with a dead session": {
			messageContext(f.tokens["revoked"]), time.Minute, contracts.ErrNoSessionRequest,
		},
		"a disabled user": {messageContext(f.tokens["disabledUser"]), time.Minute, contracts.ErrNoSessionRequest},
		"a zero lifetime": {messageContext(f.tokens["user"]), 0, contracts.ErrInvalidGrantTTL},
		"a negative one":  {messageContext(f.tokens["user"]), -time.Minute, contracts.ErrInvalidGrantTTL},
		"too long a one":  {messageContext(f.tokens["user"]), contracts.MaxAssistantGrantTTL + time.Second, contracts.ErrInvalidGrantTTL},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if id, err := g.Issue(tc.ctx, conversationID, runID, tc.ttl); !errors.Is(err, tc.err) || id != uuid.Nil {
				t.Errorf("Issue = %v, %v, want %v", id, err, tc.err)
			}
		})
	}
	if n := h.count(t, `SELECT count(*) FROM identity.assistant_grants`); n != 0 {
		t.Errorf("%d grants exist after the refusals, want none", n)
	}

	t.Run("a request made through a grant cannot issue another", func(t *testing.T) {
		id := issue(t, h, f.tokens["user"], time.Hour)
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: f.tokens["user"]})
		req = req.WithContext(contracts.WithAssistantGrant(req.Context(), id))
		ctx := contracts.WithRequest(req.Context(), httptest.NewRecorder(), req)
		if _, err := g.Issue(ctx, conversationID, runID, time.Minute); !errors.Is(err, contracts.ErrNoSessionRequest) {
			t.Errorf("Issue = %v, want ErrNoSessionRequest", err)
		}
	})

	t.Run("the grant is bound to the request's session", func(t *testing.T) {
		token := f.tokens["userPermitted"]
		id := issue(t, h, token, 10*time.Minute)
		type row struct {
			UserID, SessionID, ConversationID, RunID uuid.UUID
			ExpiresAt                                time.Time
		}
		got := identityRow[row](t, h, `SELECT user_id, session_id, conversation_id, run_id, expires_at FROM identity.assistant_grants WHERE id = $1`, id)
		if got.UserID != f.users["userPermitted"] || got.SessionID != h.sessionID(t, token) {
			t.Errorf("grant = %+v, want the user and session of the request", got)
		}
		if want := h.now().Add(10 * time.Minute); !got.ExpiresAt.Equal(want) {
			t.Errorf("expires_at = %v, want %v", got.ExpiresAt, want)
		}
	})
}

func identityRow[T any](t *testing.T, h *harness, sql string, args ...any) T {
	t.Helper()
	var v T
	rv := reflect.ValueOf(&v).Elem()
	dest := make([]any, rv.NumField())
	for i := range dest {
		dest[i] = rv.Field(i).Addr().Interface()
	}
	if err := h.pool.QueryRow(context.Background(), sql, args...).Scan(dest...); err != nil {
		t.Fatalf("query: %v", err)
	}
	return v
}

func TestAssistantGrant_EndedGrantsArePurgedAndCascade(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	h := f.h
	token := f.tokens["user"]

	old := issue(t, h, token, time.Minute)
	h.advance(2 * time.Hour)
	// A day past its expiry, the old grant goes when the user's next is issued.
	h.advance(24 * time.Hour)
	// (The earlier session idled out meanwhile, so the user signs in again.)
	fresh := issue(t, h, h.session(t, f.users["user"], false), time.Minute)
	if n := h.count(t, `SELECT count(*) FROM identity.assistant_grants WHERE id = $1`, old); n != 0 {
		t.Errorf("the grant that ended over a day ago is still there")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.assistant_grants WHERE id = $1`, fresh); n != 1 {
		t.Errorf("the new grant is missing")
	}

	// Purging a session takes its grants with it.
	userID := h.insertUser(t, "cascade@example.test", identity.RoleUserID)
	sess := h.session(t, userID, false)
	issue(t, h, sess, time.Hour)
	h.exec(t, `DELETE FROM identity.sessions WHERE id = $1`, h.sessionID(t, sess))
	if n := h.count(t, `SELECT count(*) FROM identity.assistant_grants WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("%d grants outlive their session, want the cascade to remove them", n)
	}
	issue(t, h, h.session(t, userID, false), time.Hour)
	h.exec(t, `DELETE FROM identity.users WHERE id = $1`, userID)
	if n := h.count(t, `SELECT count(*) FROM identity.assistant_grants WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("%d grants outlive their user, want the cascade to remove them", n)
	}
}
