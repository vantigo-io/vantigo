package staff_test

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const assignableUsersPath = "/api/v1/staff/assignable-users"

func userPath(id int64) string { return personPath(id) + "/user" }

// linkThroughAPI links a person to a user the way HR does, through
// PUT /people/{id}/user, and fails the test unless it answered 200 — for a
// test whose subject is the real path. The matrix tests keep link's SQL seed.
func linkThroughAPI(t *testing.T, hr *modtest.Client, personID int64, userID uuid.UUID) gen.StaffPersonLink {
	t.Helper()
	var out gen.StaffPersonLink
	do(t, hr, http.MethodPut, userPath(personID), map[string]any{"userId": userID.String()}, http.StatusOK, &out)
	return out
}

// disableUser marks a user disabled in identity's own table, as an
// administrator's disable does.
func disableUser(t *testing.T, h *harness, id uuid.UUID) {
	t.Helper()
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, id)
}

// The self-service link's rules (design S24 as amended, §3.6): the user is
// read from the directory before anything is written — an unknown one is a
// field error, a disabled one user_inactive — and a user linked to another
// person is user_linked_elsewhere. A link is stored as the identity
// provider's, linked and not managed; linking again to the same user changes
// nothing, linking to another replaces it, and null unlinks. Each change is
// one event, and every directory read was made outside the transaction.
func TestLink_Rules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signInNamed(t, h, "Hanne HR", "staff:manage")
	first := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	second := createPerson(t, hr, newPerson("Ola", "Hansen"))
	_, kari := signInNamed(t, h, "Kari the user")
	_, other := signInNamed(t, h, "Kari's new account")
	_, disabled := signIn(t, h)
	disableUser(t, h, disabled)

	pr := refused(t, hr.Do(http.MethodPut, userPath(first.Id), map[string]any{"userId": uuid.New().String()}), http.StatusBadRequest, "")
	if len(pr.Errors["userId"]) == 0 {
		t.Errorf("an unknown user: errors = %v, want userId named", pr.Errors)
	}
	refused(t, hr.Do(http.MethodPut, userPath(first.Id), map[string]any{"userId": disabled.String()}), http.StatusBadRequest, "user_inactive")

	got := linkThroughAPI(t, hr, first.Id, kari)
	if !got.Linked || got.State == nil || *got.State != "linked" || got.Managed == nil || *got.Managed ||
		got.User == nil || got.User.Id != kari || got.User.DisplayName != "Kari the user" || !got.User.Active {
		t.Errorf("link = %+v, want linked, not managed, to Kari the user", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.identity_links WHERE person_id = $1 AND provider = 'identity'
		AND external_id = $2 AND state = 'linked' AND NOT managed`, first.Id, kari.String()); n != 1 {
		t.Errorf("stored links = %d, want the one, as the identity provider's, linked, not managed", n)
	}
	refused(t, hr.Do(http.MethodPut, userPath(second.Id), map[string]any{"userId": kari.String()}), http.StatusBadRequest, "user_linked_elsewhere")
	linkThroughAPI(t, hr, first.Id, kari) // the same user again changes nothing
	linkThroughAPI(t, hr, first.Id, other)

	var unlinked gen.StaffPersonLink
	do(t, hr, http.MethodPut, userPath(first.Id), map[string]any{"userId": nil}, http.StatusOK, &unlinked)
	if unlinked.Linked || unlinked.User != nil || unlinked.State != nil || unlinked.Managed != nil {
		t.Errorf("unlinked = %+v, want linked false and nothing else", unlinked)
	}
	do(t, hr, http.MethodPut, userPath(first.Id), map[string]any{"userId": nil}, http.StatusOK, nil) // nothing to unlink
	if n := h.Count(t, `SELECT count(*) FROM staff.identity_links WHERE person_id = $1`, first.Id); n != 0 {
		t.Errorf("links after the unlink = %d, want none", n)
	}
	linkThroughAPI(t, hr, second.Id, kari) // free again once its link is gone

	events := listEvents(t, hr, first.Id)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if want := []string{"user_unlinked", "user_linked", "user_linked", "person_created"}; !slices.Equal(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	relinked := fieldsOf(events[1])["user"].(map[string]any)
	if from := relinked["from"].(map[string]any); from["id"] != kari.String() || from["displayName"] != "Kari the user" ||
		relinked["to"].(map[string]any)["id"] != other.String() {
		t.Errorf("the relink = %v, want from Kari's user, named, to the new one", relinked)
	}
	if from := fieldsOf(events[0])["user"].(map[string]any)["from"].(map[string]any); from["id"] != other.String() || from["displayName"] != "Kari's new account" {
		t.Errorf("the unlink = %v, want from the new user, named as user_linked names one", from)
	}
	var reads int
	for _, call := range contractCalls.by(hrID) {
		if call.method == "Users.User" {
			reads++
		}
		if call.inTx {
			t.Errorf("%s was called inside a transaction", call.method)
		}
	}
	if reads == 0 {
		t.Error("no directory read was recorded")
	}
}

// The link is the "Account flag, links" row of design §5: the person
// themselves, staff:manage and staff:personal-data read it; a colleague and
// an outsider get the unknown id's 404. A person linked to nobody answers
// linked false.
func TestLink_ReadByAudience(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	seedEmployment(t, h, p.Id, 0)
	var none gen.StaffPersonLink
	do(t, hr, http.MethodGet, userPath(p.Id), nil, http.StatusOK, &none)
	if none.Linked || none.User != nil {
		t.Errorf("before the link = %+v, want linked false", none)
	}
	self, user := signInNamed(t, h, "Kari the user")
	linkThroughAPI(t, hr, p.Id, user)
	colleague, _ := signIn(t, h, "staff:view")
	personalData, _ := signIn(t, h, "staff:personal-data")
	outsider, _ := signIn(t, h)

	unknown := outsider.Do(http.MethodGet, userPath(p.Id+1000), nil)
	for _, c := range []struct {
		name   string
		client *modtest.Client
		want   int
	}{
		{"self", self, http.StatusOK},
		{"staff:manage", hr, http.StatusOK},
		{"staff:personal-data", personalData, http.StatusOK},
		{"colleague", colleague, http.StatusNotFound},
		{"outsider", outsider, http.StatusNotFound},
	} {
		r := c.client.Do(http.MethodGet, userPath(p.Id), nil)
		if r.Status != c.want {
			t.Errorf("%s: %d, want %d", c.name, r.Status, c.want)
			continue
		}
		if c.want == http.StatusNotFound && string(r.Body) != string(unknown.Body) {
			t.Errorf("%s: %q, want the unknown id's 404 %q", c.name, r.Body, unknown.Body)
		}
		if c.want == http.StatusOK {
			var got gen.StaffPersonLink
			r.JSON(&got)
			if !got.Linked || got.User == nil || got.User.Id != user || got.User.DisplayName != "Kari the user" {
				t.Errorf("%s reads %+v, want the link to Kari the user", c.name, got)
			}
		}
	}
}

// Linking and the picker are staff:manage's: the person themselves,
// staff:personal-data and a colleague are the uniform 403.
func TestLink_WritesAndThePickerAreStaffManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	self, user := signIn(t, h)
	link(t, h, p.Id, user)
	personalData, _ := signIn(t, h, "staff:personal-data")
	colleague, _ := signIn(t, h, "staff:view")
	for name, c := range map[string]*modtest.Client{"self": self, "staff:personal-data": personalData, "staff:view": colleague} {
		if r := c.Do(http.MethodPut, userPath(p.Id), map[string]any{"userId": nil}); r.Status != http.StatusForbidden {
			t.Errorf("%s PUT the link: %d, want 403", name, r.Status)
		}
		if r := c.Do(http.MethodGet, assignableUsersPath, nil); r.Status != http.StatusForbidden {
			t.Errorf("%s GET the picker: %d, want 403", name, r.Status)
		}
	}
}

// The picker is the user directory's search (plan reading 9, customers'
// precedent): active users only, narrowed by q, at most twenty.
func TestAssignableUsers_ActiveUsersCappedAtTwenty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	for i := range 22 {
		signInNamed(t, h, fmt.Sprintf("Picker %02d", i))
	}
	_, gone := signInNamed(t, h, "Picker gone")
	disableUser(t, h, gone)
	_, zelda := signInNamed(t, h, "Zelda Unique")

	var many []gen.StaffAssignableUser
	do(t, hr, http.MethodGet, assignableUsersPath+"?q=Picker", nil, http.StatusOK, &many)
	if len(many) != 20 {
		t.Errorf("q=Picker answers %d, want twenty", len(many))
	}
	for _, u := range many {
		if u.Id == gone {
			t.Error("a disabled user is offered")
		}
	}
	var one []gen.StaffAssignableUser
	do(t, hr, http.MethodGet, assignableUsersPath+"?q=zelda", nil, http.StatusOK, &one)
	if len(one) != 1 || one[0].Id != zelda || one[0].DisplayName != "Zelda Unique" {
		t.Errorf("q=zelda = %+v, want Zelda alone", one)
	}
}

// A body that does not name userId is no unlink: the decoder reads it as
// null, so the handler refuses it naming userId, and the link stands with no
// event written.
func TestLink_ABodyWithoutUserIdUnlinksNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	_, user := signIn(t, h)
	linkThroughAPI(t, hr, p.Id, user)
	for _, body := range []string{`{}`, `{"other":1}`} {
		pr := refused(t, hr.Do(http.MethodPut, userPath(p.Id), nil, modtest.RawBody("application/json", []byte(body))), http.StatusBadRequest, "")
		if len(pr.Errors["userId"]) == 0 {
			t.Errorf("%s: errors = %v, want userId named", body, pr.Errors)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.identity_links WHERE person_id = $1 AND external_id = $2`, p.Id, user.String()); n != 1 {
		t.Errorf("links = %d, want the one untouched", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.events WHERE person_id = $1 AND kind = 'user_unlinked'`, p.Id); n != 0 {
		t.Errorf("user_unlinked events = %d, want none", n)
	}
}

// preparingLink writes the accounts worker's preparing link directly: the
// identity provider, no user yet (00051), managed. A test whose subject is how
// a reader takes a link naming nobody needs no worker to write it.
func preparingLink(t *testing.T, h *harness, personID int64) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO staff.identity_links (person_id, provider, external_id, state, managed, created_at, updated_at)
		VALUES ($1, 'identity', NULL, 'preparing', true, $2, $2)`, personID, h.Now())
}

// A link naming no user — the accounts worker's preparing link, before
// identity has created the user (plan M3) — is a person linked to nobody to
// every reader of the link: the link itself reads linked false, the person
// and the personal-data export carry no user, and no signed-in user is the
// person through it.
func TestLink_APreparingLinkNamesNobody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage", "staff:personal-data")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	preparingLink(t, h, p.Id)

	var got gen.StaffPersonLink
	do(t, hr, http.MethodGet, userPath(p.Id), nil, http.StatusOK, &got)
	if got.Linked || got.User != nil {
		t.Errorf("a preparing link reads %+v, want linked false and no user", got)
	}
	var person gen.StaffPerson
	do(t, hr, http.MethodGet, personPath(p.Id), nil, http.StatusOK, &person)
	if person.User != nil {
		t.Errorf("the person with a preparing link names user %+v, want none", person.User)
	}
	export := exportOf(t, hr.Do(http.MethodGet, personalDataPath(p.Id), nil))
	if export.Link.Linked || export.Link.User != nil {
		t.Errorf("the export's link = %+v, want linked false and no user", export.Link)
	}
	nobody, _ := signIn(t, h)
	if r := nobody.Do(http.MethodGet, "/api/v1/staff/me", nil); r.Status != http.StatusNotFound {
		t.Errorf("an unlinked user's /me: %d %s, want the 404 of a user who is nobody", r.Status, r.Body)
	}
}

// The self-service link keeps away from the accounts worker's (plan
// pre-flight B3, reading 8): a user identity reports Staff-managed is
// link_managed whether it is the user to link or the one linked now, asked
// before the transaction; a link in one of the worker's states is
// link_managed under the lock, for a relink and an unlink alike; identity
// unable to say is accounts_unavailable; and an unmanaged self-service link
// is linked and unlinked as before. A refusal changes nothing.
func TestLink_AManagedAccountIsNotLinkedByHand(t *testing.T) {
	t.Parallel()
	fake := newFakeAccounts()
	h := newHarness(t, withAccounts(fake))
	hr, _ := signIn(t, h, "staff:manage")
	_, managed := signIn(t, h)
	_, plain := signIn(t, h)
	_, other := signIn(t, h)
	fake.setUser(contracts.AccountRef{UserID: managed, State: contracts.AccountStateActive, Managed: true})
	fake.setUser(contracts.AccountRef{UserID: plain, State: contracts.AccountStateActive})

	kari := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	refused(t, hr.Do(http.MethodPut, userPath(kari.Id), map[string]any{"userId": managed}), http.StatusConflict, "link_managed")
	if n := h.Count(t, `SELECT count(*) FROM staff.identity_links WHERE person_id = $1`, kari.Id); n != 0 {
		t.Errorf("a refused link wrote %d links", n)
	}

	// A worker-state link: identity knows nothing of its user, the lock's
	// check refuses.
	ola := createPerson(t, hr, newPerson("Ola", "Hansen"))
	managedLink(t, h, ola.Id, uuid.New(), "pending")
	refused(t, hr.Do(http.MethodPut, userPath(ola.Id), map[string]any{"userId": other}), http.StatusConflict, "link_managed")
	refused(t, hr.Do(http.MethodPut, userPath(ola.Id), map[string]any{"userId": nil}), http.StatusConflict, "link_managed")
	if n := h.Count(t, `SELECT count(*) FROM staff.identity_links WHERE person_id = $1 AND state = 'pending'`, ola.Id); n != 1 {
		t.Errorf("the worker's link after the refusals: %d, want it kept", n)
	}

	// An adopted user behind HR's link: the unlink is the worker's too.
	per := createPerson(t, hr, newPerson("Per", "Olsen"))
	link(t, h, per.Id, managed)
	refused(t, hr.Do(http.MethodPut, userPath(per.Id), map[string]any{"userId": nil}), http.StatusConflict, "link_managed")
	refused(t, hr.Do(http.MethodPut, userPath(per.Id), map[string]any{"userId": other}), http.StatusConflict, "link_managed")

	// An unmanaged self-service link is HR's.
	linkThroughAPI(t, hr, kari.Id, plain)
	do(t, hr, http.MethodPut, userPath(kari.Id), map[string]any{"userId": nil}, http.StatusOK, nil)

	fake.set(func(f *fakeAccounts) { f.accountErr = errors.New("identity is down") })
	refused(t, hr.Do(http.MethodPut, userPath(kari.Id), map[string]any{"userId": plain}), http.StatusServiceUnavailable, "accounts_unavailable")
	if n := h.Count(t, `SELECT count(*) FROM staff.identity_links WHERE person_id = $1`, kari.Id); n != 0 {
		t.Errorf("a link identity could not judge wrote %d links", n)
	}
}
