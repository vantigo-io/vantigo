package staff_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

func passPath(id int64) string { return accountPath(id) + "/pass" }

// entraProvider is the fake provider in the slot under the entra key.
func entraProvider() *modtest.FakeIdentityProvisioner {
	return modtest.NewFakeIdentityProvisioner(contracts.AccountKindEntra, "Microsoft Entra ID")
}

// providerLink writes the person's link under provider directly, in state,
// naming externalID, with a pass issued and not yet shown when withPass —
// the accounts worker's row (Task 4a's), which the Account tab and the pass
// need only to read.
func providerLink(t *testing.T, h *harness, personID int64, provider, externalID, state string, withPass bool) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, external_upn,
			pass_issued_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, true, 'kari.nordmann@fake.example', CASE WHEN $5 THEN $6::timestamptz END, $6, $6)`,
		personID, provider, externalID, state, withPass, h.Now())
}

// issuedPass puts an enabled account holding number in the fake's directory
// with a pass waiting to be shown, and answers its external id.
func issuedPass(t *testing.T, fake *modtest.FakeIdentityProvisioner, number string) string {
	t.Helper()
	account := fake.SetAccount(number, contracts.ExternalAccount{State: "disabled"})
	if _, err := fake.Activate(context.Background(), contracts.Actor{System: contracts.SystemActorStaffAccounts},
		account.ExternalID, contracts.ExternalSpec{EmployeeNumber: number}); err != nil {
		t.Fatalf("activate the fake's account: %v", err)
	}
	return account.ExternalID
}

// The pass (Entra design D7) is shown once: staff:manage asks, the provider
// answers it with the caller as the actor, on no transaction; the response
// is no-store, the link's pass is cleared, and pass_revealed names the
// viewer and the provider — never the pass. A second ask is pass_gone.
func TestAccountPass_ShownOnce(t *testing.T) {
	t.Parallel()
	fake := entraProvider()
	h := newHarness(t, modtest.WithIdentityProvisioners(fake))
	hr, hrID := signIn(t, h, "staff:manage")
	viewer, _ := signIn(t, h, "staff:view", "staff:personal-data")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	external := issuedPass(t, fake, p.EmployeeNumber)
	providerLink(t, h, p.Id, "entra", external, "enabled", true)

	if got := getAccount(t, hr, p.Id); got.ProviderLink == nil || !got.ProviderLink.HasPass || got.ProviderLink.PassIssuedAt == nil {
		t.Fatalf("before: providerLink = %+v, want a pass waiting", got.ProviderLink)
	}
	if r := viewer.Do(http.MethodPost, passPath(p.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("staff:personal-data asked for the pass: %d, want 403", r.Status)
	}

	r := hr.Do(http.MethodPost, passPath(p.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("show the pass: %d %s, want 200", r.Status, r.Body)
	}
	if cc := r.Header("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var pass gen.StaffAccountPass
	r.JSON(&pass)
	if pass.Pass == "" || pass.ExpiresAt.IsZero() {
		t.Errorf("pass = %+v, want the value and its expiry", pass)
	}
	calls := fake.Calls()
	if last := calls[len(calls)-1]; last.Method != "RevealPass" || last.Actor != hrID.String() || last.ExternalID != external {
		t.Errorf("the provider was called %+v, want RevealPass by HR on the account", last)
	}
	for _, c := range contractCalls.by(hrID) {
		if c.inTx {
			t.Errorf("%s was called inside a transaction", c.method)
		}
	}
	if got := getAccount(t, hr, p.Id); got.ProviderLink == nil || got.ProviderLink.HasPass || got.ProviderLink.PassIssuedAt != nil {
		t.Errorf("after: providerLink = %+v, want no pass waiting", got.ProviderLink)
	}

	payload := modtest.One[string](t, h.Harness, `
		SELECT payload::text FROM staff.events WHERE person_id = $1 AND kind = 'pass_revealed'`, p.Id)
	actor := modtest.One[string](t, h.Harness, `
		SELECT actor_user_id::text FROM staff.events WHERE person_id = $1 AND kind = 'pass_revealed'`, p.Id)
	if payload != `{"provider": "entra"}` || actor != hrID.String() {
		t.Errorf("pass_revealed = %s by %s, want the provider alone, by HR", payload, actor)
	}
	if n := modtest.One[int64](t, h.Harness, `SELECT count(*) FROM staff.events WHERE payload::text LIKE '%' || $1 || '%'`, pass.Pass); n != 0 {
		t.Errorf("%d events carry the pass, want none", n)
	}

	again := hr.Do(http.MethodPost, passPath(p.Id), nil)
	refused(t, again, http.StatusConflict, "pass_gone")
	if strings.Contains(string(again.Body), pass.Pass) {
		t.Errorf("the refusal carries the pass: %s", again.Body)
	}
}

// No link naming an account is the person's 404, as an unknown person is;
// the provider failing, or no enabled module offering it, is a 503 and
// leaves the pass waiting.
func TestAccountPass_TheRefusals(t *testing.T) {
	t.Parallel()
	fake := entraProvider()
	h := newHarness(t, modtest.WithIdentityProvisioners(fake))
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))

	if r := hr.Do(http.MethodPost, passPath(p.Id+100000), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown person: %d, want 404", r.Status)
	}
	if r := hr.Do(http.MethodPost, passPath(p.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a person with no provider link: %d, want 404", r.Status)
	}

	external := issuedPass(t, fake, p.EmployeeNumber)
	providerLink(t, h, p.Id, "entra", external, "enabled", true)
	fake.SetStatus(contracts.ProviderFailing, "credential_invalid")
	refused(t, hr.Do(http.MethodPost, passPath(p.Id), nil), http.StatusServiceUnavailable, "accounts_unavailable")
	if got := getAccount(t, hr, p.Id); got.ProviderLink == nil || !got.ProviderLink.HasPass {
		t.Errorf("after the provider failed: %+v, want the pass still waiting", got.ProviderLink)
	}

	bare := newHarness(t)
	hr2, _ := signIn(t, bare, "staff:manage")
	q := createPerson(t, hr2, newPerson("Ola", "Hansen"))
	bare.Exec(t, `UPDATE staff.people SET account_kind = 'entra' WHERE id = $1`, q.Id)
	providerLink(t, bare, q.Id, "entra", uuid.NewString(), "enabled", true)
	refused(t, hr2.Do(http.MethodPost, passPath(q.Id), nil), http.StatusServiceUnavailable, "accounts_unavailable")
}
