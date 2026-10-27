package staff_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// Workforce.DirectoryAccount (calendar Outlook sync design O3): the
// principal's own provider link, enabled or disabled, with an external id;
// nothing for anyone else, any other state or a bare context.

// directoryLink writes a person's provider link under 'entra' directly, in
// state, naming externalID (nil for none).
func directoryLink(t *testing.T, h *harness, personID int64, state string, externalID *string) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
		VALUES ($1, 'entra', $2, $3, true, $4, $4)`, personID, externalID, state, h.Now())
}

// linkedUser is a person linked to a fresh user, and that user.
func linkedUser(t *testing.T, h *harness, given string) (int64, uuid.UUID) {
	t.Helper()
	person := seedPerson(t, h, given, "Test")
	_, user := h.SignInUser(t)
	link(t, h, person, user)
	return person, user
}

func TestWorkforce_DirectoryAccount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	wf := workforceOf(h)
	ext := func(s string) *string { return &s }

	// Anne, through a request, and Bo, through AsUser, each with an enabled
	// link: each is answered their own, and only their own.
	anne := seedPerson(t, h, "Anne", "Aas")
	request, anneUser := calendarCaller(t, h.Harness)
	link(t, h, anne, anneUser)
	directoryLink(t, h, anne, "enabled", ext("anne-object-id"))
	bo, boUser := linkedUser(t, h, "Bo")
	directoryLink(t, h, bo, "enabled", ext("bo-object-id"))

	for name, tc := range map[string]struct {
		ctx  context.Context
		want string
	}{
		"a request's principal": {request, "anne-object-id"},
		"AsUser":                {contracts.AsUser(context.Background(), boUser), "bo-object-id"},
		"AsUser for Anne":       {contracts.AsUser(context.Background(), anneUser), "anne-object-id"},
	} {
		got, err := wf.DirectoryAccount(tc.ctx)
		if err != nil || got == nil || *got != (contracts.DirectoryAccount{Provider: "entra", ExternalID: tc.want, Enabled: true}) {
			t.Errorf("%s: %+v, %v; want entra %s enabled", name, got, err, tc.want)
		}
	}

	// A disabled provider link answers, not enabled — and so does a person
	// whose identity link the worker disabled (a leaver to clean up).
	cato, catoUser := linkedUser(t, h, "Cato")
	directoryLink(t, h, cato, "disabled", ext("cato-object-id"))
	h.Exec(t, `UPDATE staff.identity_links SET state = 'disabled', managed = true WHERE person_id = $1 AND provider = 'identity'`, cato)
	got, err := wf.DirectoryAccount(contracts.AsUser(context.Background(), catoUser))
	if err != nil || got == nil || *got != (contracts.DirectoryAccount{Provider: "entra", ExternalID: "cato-object-id"}) {
		t.Errorf("disabled: %+v, %v; want entra cato-object-id not enabled", got, err)
	}

	nothing := map[string]context.Context{
		"a bare context": context.Background(),
	}
	// A user with no person.
	_, stranger := h.SignInUser(t)
	nothing["a user with no person"] = contracts.AsUser(context.Background(), stranger)
	// A person with no provider link.
	_, plain := linkedUser(t, h, "Dina")
	nothing["a person with no provider link"] = contracts.AsUser(context.Background(), plain)
	// A provider link in any other state, or with no external id.
	for _, state := range []string{"preparing", "failed", "blocked", "deleted"} {
		p, u := linkedUser(t, h, "Eva")
		directoryLink(t, h, p, state, ext("object-"+state))
		nothing["a "+state+" link"] = contracts.AsUser(context.Background(), u)
	}
	nullP, nullU := linkedUser(t, h, "Finn")
	directoryLink(t, h, nullP, "enabled", nil)
	nothing["a link with no external id"] = contracts.AsUser(context.Background(), nullU)
	// An identity link blocked as someone else's account names no person of
	// the user's, so never that person's directory account.
	for _, reason := range []string{"mismatch", "account_not_managed"} {
		p, u := linkedUser(t, h, "Gro")
		directoryLink(t, h, p, "enabled", ext("object-"+reason))
		h.Exec(t, `UPDATE staff.identity_links SET state = 'blocked', managed = true, blocked_reason = $2
		           WHERE person_id = $1 AND provider = 'identity'`, p, reason)
		nothing["an identity link blocked "+reason] = contracts.AsUser(context.Background(), u)
	}
	for name, ctx := range nothing {
		if got, err := wf.DirectoryAccount(ctx); err != nil || got != nil {
			t.Errorf("%s: %+v, %v; want nothing", name, got, err)
		}
	}
}

// Two provider links answer the oldest, whatever the order they are read in.
func TestWorkforce_DirectoryAccountOfTwoProviders(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person, user := linkedUser(t, h, "Hans")
	first, second := "first-object-id", "second-object-id"
	directoryLink(t, h, person, "disabled", &first)
	h.Exec(t, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
		VALUES ($1, 'aaa', $2, 'enabled', true, $3, $3)`, person, second, h.Now())
	got, err := workforceOf(h).DirectoryAccount(contracts.AsUser(context.Background(), user))
	if err != nil || got == nil || *got != (contracts.DirectoryAccount{Provider: "entra", ExternalID: first}) {
		t.Errorf("two providers: %+v, %v; want the older entra link", got, err)
	}
}
