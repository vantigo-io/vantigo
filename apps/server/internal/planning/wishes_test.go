package planning_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

const (
	myWishesPath = "/api/v1/planning/me/wishes"
	wishesPath   = "/api/v1/planning/wishes"
)

func myWishPath(id int64) string { return myWishesPath + "/" + strconv.FormatInt(id, 10) }
func wishPath(id int64) string   { return wishesPath + "/" + strconv.FormatInt(id, 10) }

// wisher signs in a user holding permissions beside planning:access, linked
// to personID, and answers their client and id.
func wisher(t *testing.T, h *harness, personID int64, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, user := signIn(t, h, permissions...)
	h.workforce.link(user, personID)
	return c, user
}

// wishBody is a wish's body: off the active project from 1 October 2026,
// with what fields replace (nil removes one).
func wishBody(fields map[string]any) map[string]any {
	body := map[string]any{"kind": "off", "projectId": projectActive, "fromDate": "2026-10-01"}
	maps.Copy(body, fields)
	for k, v := range body {
		if v == nil {
			delete(body, k)
		}
	}
	return body
}

// fileWish files body as c's wish, failing unless it is 201.
func fileWish(t *testing.T, c *modtest.Client, body map[string]any) gen.PlanningWish {
	t.Helper()
	res := c.Do(http.MethodPost, myWishesPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /me/wishes %v = %d %s, want 201", body, res.Status, res.Body)
	}
	var out gen.PlanningWish
	res.JSON(&out)
	return out
}

// seedOpenWish writes one open wish straight into planning.wishes, created at
// the harness clock moved by offset, and answers its id.
func seedOpenWish(t *testing.T, h *harness, personID int64, kind string, projectID *int32, from string, offset time.Duration) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `INSERT INTO planning.wishes (person_id, kind, project_id, from_date, created_at)
VALUES ($1, $2, $3, $4::date, $5) RETURNING id`, personID, kind, projectID, date(from), h.Now().Add(offset))
}

// wishEventKinds is the kinds of the events about wish id, in order.
func wishEventKinds(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return modtest.One[[]string](t, h.Harness,
		`SELECT coalesce(array_agg(kind ORDER BY id), '{}') FROM planning.events WHERE entity_kind = 'wish' AND entity_id = $1`, id)
}

// wishEventPayload is the payload of the last event of kind about wish id.
func wishEventPayload(t *testing.T, h *harness, id int64, kind string) map[string]any {
	t.Helper()
	raw := modtest.One[string](t, h.Harness,
		`SELECT payload::text FROM planning.events WHERE entity_kind = 'wish' AND entity_id = $1 AND kind = $2 ORDER BY id DESC LIMIT 1`, id, kind)
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("payload %s: %v", raw, err)
	}
	return out
}

// listWishes is GET /wishes with query, as c.
func listWishes(t *testing.T, c *modtest.Client, query string) []gen.PlanningWish {
	t.Helper()
	var out gen.PlanningWishList
	getJSON(t, c, wishesPath+query, http.StatusOK, &out)
	return out.Wishes
}

// wishIDs is the ids of wishes, in order.
func wishIDs(wishes []gen.PlanningWish) []int64 {
	ids := make([]int64, len(wishes))
	for i, w := range wishes {
		ids[i] = w.Id
	}
	return ids
}

// TestWishes_NoPerson is S21's no_person: a caller Staff answers no person
// for can neither file nor withdraw a wish.
func TestWishes_NoPerson(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h)
	wantCode(t, problemOf(t, c.Do(http.MethodPost, myWishesPath, wishBody(map[string]any{"kind": "new", "projectId": nil})), http.StatusNotFound), "no_person")
	wantCode(t, problemOf(t, c.Do(http.MethodDelete, myWishPath(1001), nil), http.StatusNotFound), "no_person")
}

// TestWishes_FieldErrors is S21's field rules: a kind, a project for off
// and none for new, a project the directory resolves, a date on or after
// today, and no property the contract does not name — each a field error
// under "Invalid wish", nothing written.
func TestWishes_FieldErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := wisher(t, h, personKari)
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-09-01", to: "2026-10-30", load: 50})

	for _, tc := range []struct {
		name   string
		fields map[string]any
		field  string
	}{
		{"off without a project", map[string]any{"projectId": nil}, "projectId"},
		{"new with a project", map[string]any{"kind": "new"}, "projectId"},
		{"an unknown kind", map[string]any{"kind": "maybe"}, "kind"},
		{"an unknown project", map[string]any{"projectId": projectUnknown}, "projectId"},
		{"yesterday", map[string]any{"fromDate": "2026-09-11"}, "fromDate"},
		{"a property not taken", map[string]any{"note": "Bort herfra"}, "note"},
	} {
		p := problemOf(t, c.Do(http.MethodPost, myWishesPath, wishBody(tc.fields)), http.StatusBadRequest)
		if p.Errors == nil || len((*p.Errors)[tc.field]) == 0 || p.Title == nil || *p.Title != "Invalid wish" {
			t.Errorf("%s = %+v, want a field error on %s", tc.name, p, tc.field)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.wishes`); n != 0 {
		t.Errorf("%d wishes written by refusals, want none", n)
	}
	// Today itself is on or after today.
	fileWish(t, c, wishBody(map[string]any{"fromDate": "2026-09-12"}))
}

// TestWishes_NoBooking is S21's wish_no_booking: an off wish needs a
// confirmed row of the person on the project ending today or later — not
// none, not a tentative one, not one that ended yesterday, not a
// colleague's; a row ending today is enough.
func TestWishes_NoBooking(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := wisher(t, h, personKari)
	seedAllocation(t, h, seedRow{person: personKari, project: projectPlanned, from: "2026-10-05", to: "2026-10-30", load: 50, status: "tentative"})
	seedAllocation(t, h, seedRow{person: personKari, project: projectCompleted, from: "2026-08-03", to: "2026-09-11", load: 50})
	seedAllocation(t, h, seedRow{person: personOla, project: projectActive, from: "2026-09-01", to: "2026-10-30", load: 50})
	seedAllocation(t, h, seedRow{person: personKari, project: projectInternal, from: "2026-09-01", to: "2026-09-12", load: 20})

	for _, project := range []int32{projectActive, projectPlanned, projectCompleted} {
		res := c.Do(http.MethodPost, myWishesPath, wishBody(map[string]any{"projectId": project}))
		wantCode(t, problemOf(t, res, http.StatusConflict), "wish_no_booking")
	}
	w := fileWish(t, c, wishBody(map[string]any{"projectId": projectInternal}))
	if w.Project == nil || w.Project.Id != projectInternal {
		t.Errorf("the wish off the row ending today = %+v", w)
	}
}

// TestWishes_LimitAndExists is S21's wish_limit at the sixth open wish and
// wish_exists on a second off wish on one project.
func TestWishes_LimitAndExists(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := wisher(t, h, personKari)
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-09-01", to: "2026-10-30", load: 50})

	fileWish(t, c, wishBody(nil))
	wantCode(t, problemOf(t, c.Do(http.MethodPost, myWishesPath, wishBody(map[string]any{"fromDate": "2026-11-01"})), http.StatusConflict), "wish_exists")
	for range 4 {
		fileWish(t, c, wishBody(map[string]any{"kind": "new", "projectId": nil}))
	}
	wantCode(t, problemOf(t, c.Do(http.MethodPost, myWishesPath, wishBody(map[string]any{"kind": "new", "projectId": nil})), http.StatusConflict), "wish_limit")
	if n := h.Count(t, `SELECT count(*) FROM planning.wishes WHERE person_id = $1`, personKari); n != 5 {
		t.Errorf("%d open wishes, want five", n)
	}
}

// TestWishes_Create is S21's two kinds: each answered with the person and
// the project named, unseen, and recorded as wish_created filed under the
// person and the project, the payload the kind, the project id and the
// date alone.
func TestWishes_Create(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := wisher(t, h, personKari)
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-09-01", to: "2026-10-30", load: 50})

	off := fileWish(t, c, wishBody(nil))
	if off.Kind != gen.Off || off.Person.Id != personKari || off.Person.DisplayName == nil || *off.Person.DisplayName != "Kari Nordmann" ||
		off.Project == nil || off.Project.Code == nil || *off.Project.Code != "KVEM1000" || off.FromDate.String() != "2026-10-01" ||
		off.SeenAt != nil || !off.CreatedAt.Equal(h.Now()) {
		t.Errorf("the off wish = %+v", off)
	}
	fresh := fileWish(t, c, wishBody(map[string]any{"kind": "new", "projectId": nil, "fromDate": "2027-01-01"}))
	if fresh.Kind != gen.New || fresh.Project != nil {
		t.Errorf("the new wish = %+v", fresh)
	}

	if kinds := wishEventKinds(t, h, off.Id); len(kinds) != 1 || kinds[0] != "wish_created" {
		t.Errorf("the off wish's events = %v", kinds)
	}
	if p := wishEventPayload(t, h, off.Id, "wish_created"); len(p) != 3 || p["kind"] != "off" || p["projectId"] != float64(projectActive) || p["fromDate"] != "2026-10-01" {
		t.Errorf("the off wish's payload = %v", p)
	}
	if p := wishEventPayload(t, h, fresh.Id, "wish_created"); len(p) != 2 || p["kind"] != "new" || p["fromDate"] != "2027-01-01" {
		t.Errorf("the new wish's payload = %v", p)
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.events WHERE entity_kind = 'wish' AND entity_id = $1 AND person_id = $2 AND project_id = $3`,
		off.Id, personKari, projectActive); n != 1 {
		t.Errorf("the off wish's event is not filed under Kari and the project")
	}
}

// TestWishes_MailsThePlanners is S21's mail and reading 6: every
// planning:manage holder once, in both languages, linking to the Wishes
// page — but the wisher, who is a planner too; nobody but the planners.
func TestWishes_MailsThePlanners(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, kari := wisher(t, h, personKari, "planning:manage")
	_, first := signIn(t, h, "planning:view", "planning:manage")
	_, second := signIn(t, h, "planning:manage")
	_, viewer := signIn(t, h, "planning:view")
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-09-01", to: "2026-10-30", load: 50})

	fileWish(t, c, wishBody(nil))
	if n := noticesTo(h, kari); len(n) != 0 {
		t.Errorf("the wisher was mailed: %+v", n)
	}
	if n := noticesTo(h, viewer); len(n) != 0 {
		t.Errorf("a viewer was mailed: %+v", n)
	}
	for _, planner := range []uuid.UUID{first, second} {
		n := noticesTo(h, planner)
		if len(n) != 1 {
			t.Fatalf("planner %s got %d notices, want one", planner, len(n))
		}
		en, nb := n[0].Texts["en"], n[0].Texts["nb"]
		if en.Subject != "Kari Nordmann wishes off KVEM1000 Kraft-Verket modernisering from 1 October 2026" ||
			!strings.HasPrefix(en.Body, en.Subject+".\n") || !strings.Contains(en.Body, "/planning/wishes") {
			t.Errorf("en = %+v", en)
		}
		if nb.Subject != "Kari Nordmann ønsker seg av KVEM1000 Kraft-Verket modernisering fra 1. oktober 2026" ||
			!strings.Contains(nb.Body, "/planning/wishes") {
			t.Errorf("nb = %+v", nb)
		}
	}

	fileWish(t, c, wishBody(map[string]any{"kind": "new", "projectId": nil}))
	if n := noticesTo(h, first); len(n) != 2 || n[1].Texts["en"].Subject != "Kari Nordmann wishes for something new from 1 October 2026" {
		t.Errorf("the new wish's notice = %+v", n)
	}
}

// TestWishes_HoldersUnread is amendment 77: a holder read that fails is
// logged and mails nobody, and the wish is written all the same.
func TestWishes_HoldersUnread(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithUsers(func(d contracts.UserDirectory) contracts.UserDirectory { return failingHolders{d} }))
	c, _ := wisher(t, h, personKari)
	signIn(t, h, "planning:manage")

	w := fileWish(t, c, wishBody(map[string]any{"kind": "new", "projectId": nil}))
	if n := h.Count(t, `SELECT count(*) FROM planning.wishes WHERE id = $1`, w.Id); n != 1 {
		t.Errorf("the wish is not stored")
	}
	if n := h.Notices(); len(n) != 0 {
		t.Errorf("notices = %+v, want none", n)
	}
	if logs := h.Logs(); !strings.Contains(logs, "the planners to tell of a wish could not be read") {
		t.Errorf("logs = %s, want the failed holder read", logs)
	}
}

// failingHolders is identity's user directory with PermissionHolders
// failing, every other method identity's own.
type failingHolders struct{ contracts.UserDirectory }

func (failingHolders) PermissionHolders(context.Context, string) ([]contracts.Holder, error) {
	return nil, errors.New("the directory is down")
}

// TestWishes_LimitUnderTheLock is S23's person lock on a wish: two wishes
// filed at once by a person with four open are judged one after the other,
// so exactly one is written and the other is wish_limit.
func TestWishes_LimitUnderTheLock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := wisher(t, h, personKari)
	for range 4 {
		seedOpenWish(t, h, personKari, "new", nil, "2026-10-01", 0)
	}
	body := wishBody(map[string]any{"kind": "new", "projectId": nil})
	responses := make([]*modtest.Response, 2)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Go(func() { responses[i] = c.Do(http.MethodPost, myWishesPath, body) })
	}
	wg.Wait()

	created, refused := 0, 0
	for _, res := range responses {
		switch res.Status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			refused++
			wantCode(t, problemOf(t, res, http.StatusConflict), "wish_limit")
		}
	}
	if created != 1 || refused != 1 {
		t.Errorf("concurrent wishes = %d created, %d refused (%d, %d); want one of each", created, refused, responses[0].Status, responses[1].Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.wishes WHERE person_id = $1`, personKari); n != 5 {
		t.Errorf("%d open wishes, want five", n)
	}
}

// TestWishes_NobodyElseToMail: a wisher who is the only planner mails
// nobody, and that is no error.
func TestWishes_NobodyElseToMail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := wisher(t, h, personKari, "planning:manage")
	fileWish(t, c, wishBody(map[string]any{"kind": "new", "projectId": nil}))
	if n := h.Notices(); len(n) != 0 {
		t.Errorf("notices = %+v, want none", n)
	}
}

// TestWishes_Withdraw is S21's withdrawal: the person's own is deleted and
// recorded as wish_withdrawn with the kind, the project id and the date; a
// colleague's is the 404 an unknown id gets, and stays.
func TestWishes_Withdraw(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := wisher(t, h, personKari)
	seedAllocation(t, h, seedRow{person: personKari, project: projectActive, from: "2026-09-01", to: "2026-10-30", load: 50})
	own := fileWish(t, c, wishBody(nil))
	colleague := seedOpenWish(t, h, personOla, "new", nil, "2026-10-01", 0)

	if res := c.Do(http.MethodDelete, myWishPath(own.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("withdraw own = %d %s, want 204", res.Status, res.Body)
	}
	if kinds := wishEventKinds(t, h, own.Id); len(kinds) != 2 || kinds[1] != "wish_withdrawn" {
		t.Errorf("events = %v, want created then withdrawn", kinds)
	}
	if p := wishEventPayload(t, h, own.Id, "wish_withdrawn"); len(p) != 3 || p["kind"] != "off" || p["projectId"] != float64(projectActive) || p["fromDate"] != "2026-10-01" {
		t.Errorf("payload = %v", p)
	}
	problemOf(t, c.Do(http.MethodDelete, myWishPath(own.Id), nil), http.StatusNotFound)
	problemOf(t, c.Do(http.MethodDelete, myWishPath(colleague), nil), http.StatusNotFound)
	if n := h.Count(t, `SELECT count(*) FROM planning.wishes`); n != 1 {
		t.Errorf("%d wishes left, want the colleague's", n)
	}
	// The wish may be filed again once withdrawn.
	fileWish(t, c, wishBody(nil))
}

// TestWishes_SeenAndResolve is S21's planners' stamps: seen once — stamped
// with the time and the planner, wish_seen recorded — and answered as it is
// after; resolve deletes it, the events keeping the kind, the project id and
// the date.
func TestWishes_SeenAndResolve(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	id := seedOpenWish(t, h, personKari, "off", ptr(projectActive), "2026-10-01", 0)

	h.Advance(time.Hour)
	var seen gen.PlanningWish
	getJSONPost(t, c, wishPath(id)+"/seen", http.StatusOK, &seen)
	if seen.Id != id || seen.SeenAt == nil || !seen.SeenAt.Equal(h.Now()) || seen.Person.DisplayName == nil || seen.Project == nil || seen.Project.Code == nil {
		t.Errorf("seen = %+v, want stamped now, named", seen)
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.wishes WHERE id = $1 AND seen_by_user_id IS NOT NULL`, id); n != 1 {
		t.Errorf("the seen stamp names no planner")
	}
	h.Advance(time.Hour)
	var again gen.PlanningWish
	getJSONPost(t, c, wishPath(id)+"/seen", http.StatusOK, &again)
	if again.SeenAt == nil || !again.SeenAt.Equal(*seen.SeenAt) {
		t.Errorf("seen again = %+v, want the first stamp", again)
	}
	if kinds := wishEventKinds(t, h, id); len(kinds) != 1 || kinds[0] != "wish_seen" {
		t.Errorf("events = %v, want one wish_seen", kinds)
	}

	if res := c.Do(http.MethodPost, wishPath(id)+"/resolve", nil); res.Status != http.StatusNoContent {
		t.Fatalf("resolve = %d %s, want 204", res.Status, res.Body)
	}
	if ids := wishIDs(listWishes(t, c, "")); len(ids) != 0 {
		t.Errorf("listed after the resolve = %v", ids)
	}
	if p := wishEventPayload(t, h, id, "wish_resolved"); len(p) != 3 || p["kind"] != "off" || p["projectId"] != float64(projectActive) || p["fromDate"] != "2026-10-01" {
		t.Errorf("payload = %v", p)
	}
	if res := c.Do(http.MethodPost, wishPath(id)+"/resolve", nil); res.Status != http.StatusNotFound {
		t.Errorf("resolve again = %d, want 404", res.Status)
	}
	if res := c.Do(http.MethodPost, wishPath(id)+"/seen", nil); res.Status != http.StatusNotFound {
		t.Errorf("seen on a resolved wish = %d, want 404", res.Status)
	}
}

// getJSONPost is a POST without a body that must answer want, decoded into
// v.
func getJSONPost(t *testing.T, c *modtest.Client, path string, want int, v any) {
	t.Helper()
	res := c.Do(http.MethodPost, path, nil)
	if res.Status != want {
		t.Fatalf("POST %s = %d %s, want %d", path, res.Status, res.Body, want)
	}
	res.JSON(v)
}

// TestWishes_List is GET /wishes: oldest first, narrowed by personId, each
// named; an unknown project read by its id.
func TestWishes_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	newest := seedOpenWish(t, h, personKari, "new", nil, "2026-11-01", 2*time.Hour)
	oldest := seedOpenWish(t, h, personOla, "off", ptr(projectUnknown), "2026-10-01", 0)
	middle := seedOpenWish(t, h, personKari, "off", ptr(projectActive), "2026-10-01", time.Hour)

	all := listWishes(t, c, "")
	if ids := wishIDs(all); len(ids) != 3 || ids[0] != oldest || ids[1] != middle || ids[2] != newest {
		t.Fatalf("list = %v, want oldest first", ids)
	}
	if w := all[0]; w.Person.DisplayName == nil || *w.Person.DisplayName != "Ola Nordmann" || w.Project == nil || w.Project.Id != projectUnknown || w.Project.Code != nil {
		t.Errorf("Ola's wish = %+v, want Ola named and the project by its id", w)
	}
	if ids := wishIDs(listWishes(t, c, "?personId=1001")); len(ids) != 2 || ids[0] != middle || ids[1] != newest {
		t.Errorf("Kari's = %v", ids)
	}
}

// TestWishes_ErasedPerson is S26 and reading 4: a wish whose person People
// no longer answers is deleted by the planners' list, recorded as
// wish_withdrawn with the reason unanswered, and not listed — under a
// personId filter too — but only when People answered somebody: to a
// planner who cannot see the workforce nothing is deleted, and the wishes
// read with no name.
func TestWishes_ErasedPerson(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := planner(t, h)
	kari := seedOpenWish(t, h, personKari, "new", nil, "2026-10-01", 0)
	ola := seedOpenWish(t, h, personOla, "off", ptr(projectActive), "2026-10-01", time.Hour)
	h.workforce.endEmployment(personOla, date("2026-08-31"))

	h.workforce.setVisible(false)
	blind := listWishes(t, c, "")
	if ids := wishIDs(blind); len(ids) != 2 || blind[0].Person.DisplayName != nil {
		t.Fatalf("to a planner without the workforce = %+v, want both, unnamed", blind)
	}
	if kinds := wishEventKinds(t, h, ola); len(kinds) != 0 {
		t.Errorf("events with nobody answered = %v, want none", kinds)
	}

	h.workforce.setVisible(true)
	if ids := wishIDs(listWishes(t, c, "?personId="+strconv.FormatInt(personOla, 10))); len(ids) != 0 {
		t.Fatalf("Ola's list = %v, want none", ids)
	}
	if n := h.Count(t, `SELECT count(*) FROM planning.wishes WHERE id = $1`, ola); n != 0 {
		t.Errorf("Ola's wish is still stored after the narrowed list")
	}
	if ids := wishIDs(listWishes(t, c, "")); len(ids) != 1 || ids[0] != kari {
		t.Fatalf("list = %v, want Kari's alone", ids)
	}
	if kinds := wishEventKinds(t, h, ola); len(kinds) != 1 {
		t.Errorf("Ola's wish's events = %v, want one wish_withdrawn", kinds)
	}
	if p := wishEventPayload(t, h, ola, "wish_withdrawn"); len(p) != 4 || p["reason"] != "unanswered" || p["kind"] != "off" ||
		p["projectId"] != float64(projectActive) || p["fromDate"] != "2026-10-01" {
		t.Errorf("payload = %v", p)
	}
}

// TestWishes_Permissions is §5: the planners' operations need
// planning:manage — planning:view alone and planning:access alone are the
// access layer's 403 — and a planner without view lists them.
func TestWishes_Permissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := seedOpenWish(t, h, personKari, "new", nil, "2026-10-01", 0)
	for _, c := range []*modtest.Client{h.SignIn(t, "planning:access", "planning:view"), h.SignIn(t, "planning:access")} {
		for _, call := range []struct{ method, path string }{
			{http.MethodGet, wishesPath},
			{http.MethodPost, wishPath(id) + "/seen"},
			{http.MethodPost, wishPath(id) + "/resolve"},
		} {
			if res := c.Do(call.method, call.path, nil); res.Status != http.StatusForbidden {
				t.Errorf("%s %s = %d, want 403", call.method, call.path, res.Status)
			}
		}
	}
	if ids := wishIDs(listWishes(t, h.SignIn(t, "planning:access", "planning:manage"), "")); len(ids) != 1 {
		t.Errorf("manage alone = %v, want the wish", ids)
	}
}
