package staff_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

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
