package staff_test

import (
	"net/http"
	"testing"
)

// HR repairs a link the worker left account_not_managed (the final
// re-check's M-b, the coordinator's option b): the link gives no Self, and
// PUT /people/{id}/user may unlink it, or set it to a user — the same or
// another — as an ordinary self-service link (linked, not managed, the
// worker's columns reset), which gives the user the person's Self again. A
// mismatch link stays refused link_managed.
func TestAccounts_HRRepairsANotManagedLink(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		unlink bool
	}{{"relinked to the same user", false}, {"unlinked", true}} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			hr, _ := signIn(t, h, "staff:manage")
			p := createPerson(t, hr, newPerson("Re", "Pareres"))
			self, user := signIn(t, h)
			managedLink(t, h, p.Id, user, "active")
			h.Exec(t, `UPDATE staff.identity_links SET blocked_reason = 'account_not_managed', last_error = 'accounts_unavailable', attempts = 2
				WHERE person_id = $1`, p.Id)
			refused(t, self.Do(http.MethodGet, mePath, nil), http.StatusNotFound, "no_person")
			body := map[string]any{"userId": nil}
			if !c.unlink {
				body["userId"] = user.String()
			}
			do(t, hr, http.MethodPut, personPath(p.Id)+"/user", body, http.StatusOK, nil)
			l, ok := linkOf(t, h, p.Id)
			if c.unlink {
				if ok {
					t.Errorf("after the unlink the link = %+v, want none", l)
				}
				return
			}
			if !ok || l.State != "linked" || l.Managed || l.Reason != "" || l.LastError != "" || l.Attempts != 0 || l.User != user.String() {
				t.Errorf("after the relink the link = %+v, want linked, not managed, the worker's columns reset", l)
			}
			if r := self.Do(http.MethodGet, mePath, nil); r.Status != http.StatusOK {
				t.Errorf("GET /me after the relink: %d, want the person's own 200", r.Status)
			}
		})
	}
	t.Run("a mismatch link stays refused", func(t *testing.T) {
		h := newHarness(t)
		hr, _ := signIn(t, h, "staff:manage")
		p := createPerson(t, hr, newPerson("Mis", "Match"))
		_, user := signIn(t, h)
		managedLink(t, h, p.Id, user, "blocked")
		h.Exec(t, `UPDATE staff.identity_links SET blocked_reason = 'mismatch' WHERE person_id = $1`, p.Id)
		refused(t, hr.Do(http.MethodPut, personPath(p.Id)+"/user", map[string]any{"userId": user.String()}), http.StatusConflict, "link_managed")
		refused(t, hr.Do(http.MethodPut, personPath(p.Id)+"/user", map[string]any{"userId": nil}), http.StatusConflict, "link_managed")
	})
}
