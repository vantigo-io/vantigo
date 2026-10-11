package staff_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

const peoplePath = "/api/v1/staff/people"

// personPath is one person's path, and the others its sub-resources'.
func personPath(id int64) string    { return rowPath(peoplePath, id) }
func nextOfKinPath(id int64) string { return personPath(id) + "/next-of-kin" }
func rolesPath(id int64) string     { return personPath(id) + "/roles" }
func eventsPath(id int64) string    { return personPath(id) + "/events" }
func pagedPath(base string, page, size int) string {
	return fmt.Sprintf("%s?page=%d&pageSize=%d", base, page, size)
}

// createPerson adds a person through the API and fails the test unless it
// answered 201.
func createPerson(t *testing.T, c *modtest.Client, body map[string]any) gen.StaffPerson {
	t.Helper()
	var out gen.StaffPerson
	do(t, c, http.MethodPost, peoplePath, body, http.StatusCreated, &out)
	return out
}

// newPerson is the smallest body a create accepts.
func newPerson(given, family string) map[string]any {
	return map[string]any{"givenName": given, "familyName": family}
}

// with is body with the given fields replaced — a body is a map, so a test
// states only what it changes.
func with(body map[string]any, kv ...any) map[string]any {
	out := make(map[string]any, len(body)+len(kv)/2)
	for k, v := range body {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func listPeople(t *testing.T, c *modtest.Client, query string) gen.PaginatedResponseOfStaffPersonListItem {
	t.Helper()
	var out gen.PaginatedResponseOfStaffPersonListItem
	do(t, c, http.MethodGet, peoplePath+query, nil, http.StatusOK, &out)
	return out
}

// personRevision is a person's revision as stored — what a client that read
// the person sends back in If-Match.
func personRevision(t *testing.T, h *harness, id int64) int32 {
	t.Helper()
	return modtest.One[int32](t, h.Harness, `SELECT revision FROM staff.people WHERE id = $1`, id)
}

// putNextOfKin replaces a person's next of kin under their current revision
// and fails the test unless it answered 200.
func putNextOfKin(t *testing.T, h *harness, c *modtest.Client, id int64, body map[string]any) gen.StaffNextOfKinList {
	t.Helper()
	var out gen.StaffNextOfKinList
	do(t, c, http.MethodPut, nextOfKinPath(id), body, http.StatusOK, &out, ifMatch(personRevision(t, h, id)))
	return out
}

// putRoles replaces a person's roles under their current revision and fails
// the test unless it answered 200.
func putRoles(t *testing.T, h *harness, c *modtest.Client, id int64, body map[string]any) gen.StaffPersonRoleList {
	t.Helper()
	var out gen.StaffPersonRoleList
	do(t, c, http.MethodPut, rolesPath(id), body, http.StatusOK, &out, ifMatch(personRevision(t, h, id)))
	return out
}

// setPrefix sets the employee number sequence's prefix and next number.
func setPrefix(t *testing.T, h *harness, hr *modtest.Client, prefix string, next int64) {
	t.Helper()
	var current gen.StaffSettings
	do(t, hr, http.MethodGet, settingsPath, nil, http.StatusOK, &current)
	do(t, hr, http.MethodPut, settingsPath, map[string]any{
		"timeZone": current.TimeZone, "employeeNumberPrefix": prefix, "nextEmployeeNumber": next,
	}, http.StatusOK, nil, ifMatch(current.Revision))
}

func nextEmployeeNumber(t *testing.T, h *harness) int64 {
	t.Helper()
	return modtest.One[int64](t, h.Harness, `SELECT next_employee_number FROM staff.settings`)
}

// A create with no employee number takes the next from the settings'
// sequence — the prefix and the number, unpadded — and moves it on; one with
// a number keeps it as typed and leaves the sequence alone. The display name
// is the given and the family name unless one is given.
func TestPostPeople_NumberGivenOrGenerated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	setPrefix(t, h, hr, "EMP-", 7)

	generated := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	if generated.EmployeeNumber != "EMP-7" || generated.DisplayName != "Kari Nordmann" || generated.Revision != 1 {
		t.Errorf("generated = %+v, want EMP-7, display Kari Nordmann, revision 1", generated)
	}
	if n := nextEmployeeNumber(t, h); n != 8 {
		t.Errorf("next after a generated number = %d, want 8", n)
	}
	given := createPerson(t, hr, with(newPerson("Ola", "Nordmann"), "employeeNumber", "x-100", "displayName", "Ola N."))
	if given.EmployeeNumber != "x-100" || given.DisplayName != "Ola N." {
		t.Errorf("given = %+v, want x-100 kept as typed, display Ola N.", given)
	}
	if n := nextEmployeeNumber(t, h); n != 8 {
		t.Errorf("next after a given number = %d, want it left at 8", n)
	}
	if generated.WorkCountry != "NO" || generated.PreferredLanguage != gen.StaffLanguage("nb") {
		t.Errorf("defaults = %s/%s, want NO/nb", generated.WorkCountry, generated.PreferredLanguage)
	}
}

// Two creators at once each take the settings row's lock in turn, so each
// gets a number of their own and the sequence moves on by exactly as many.
func TestPostPeople_GenerationUnderConcurrencyGivesDistinctNumbers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	const creators = 6
	numbers := make([]string, creators)
	var wg sync.WaitGroup
	for i := range creators {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := hr.Do(http.MethodPost, peoplePath, newPerson("Kari", fmt.Sprintf("Nordmann %d", i)))
			if r.Status != http.StatusCreated {
				t.Errorf("creator %d: status %d body %s", i, r.Status, r.Body)
				return
			}
			var p gen.StaffPerson
			r.JSON(&p)
			numbers[i] = p.EmployeeNumber
		}()
	}
	wg.Wait()
	slices.Sort(numbers)
	if got := slices.Compact(slices.Clone(numbers)); len(got) != creators {
		t.Errorf("numbers = %v, want %d distinct", numbers, creators)
	}
	if n := nextEmployeeNumber(t, h); n != creators+1 {
		t.Errorf("next = %d, want %d", n, creators+1)
	}
}

// A number HR assigned by hand is skipped by the sequence, however it is
// cased, so a generated number never collides with it.
func TestPostPeople_GenerationSkipsPastATakenNumber(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	setPrefix(t, h, hr, "E", 4)
	createPerson(t, hr, with(newPerson("Hand", "Assigned"), "employeeNumber", "e4"))
	createPerson(t, hr, with(newPerson("Hand", "Assigned"), "employeeNumber", "E5"))

	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	if p.EmployeeNumber != "E6" {
		t.Errorf("generated = %s, want E6 (E4 and E5 taken)", p.EmployeeNumber)
	}
	if n := nextEmployeeNumber(t, h); n != 7 {
		t.Errorf("next = %d, want 7", n)
	}
}

// A typed create that races a generation — committing the number the
// generation chose after the generation checked it — makes the generation's
// insert trip the unique index; the generation is tried once more, takes the
// next free number, and the create succeeds. A typed duplicate still answers
// employee_number_taken (the case-insensitivity test).
func TestPostPeople_GenerationRetriesOnceWhenATypedCreateWinsTheRace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The typed create, not yet committed: the generation cannot see it, picks
	// "1", and its insert waits on the index for this transaction.
	if _, err := tx.Exec(ctx, `INSERT INTO staff.people (employee_number, given_name, family_name, display_name, created_at, updated_at)
		VALUES ('1', 'Typed', 'First', 'Typed First', now(), now())`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *modtest.Response, 1)
	go func() { done <- hr.Do(http.MethodPost, peoplePath, newPerson("Kari", "Nordmann")) }()
	time.Sleep(300 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.Status != http.StatusCreated {
			t.Fatalf("the generated create: %d %s, want 201 after one retry", r.Status, r.Body)
		}
		var p gen.StaffPerson
		r.JSON(&p)
		if p.EmployeeNumber != "2" {
			t.Errorf("generated = %s, want 2 (1 was taken by the race)", p.EmployeeNumber)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the generated create never answered")
	}
}

// An employee number is at most 16 characters, on both paths: a typed one
// longer is refused, and so is a generation that the skip carries past 16.
func TestPostPeople_TooLongIsRefusedOnBothPaths(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")

	p := refused(t, hr.Do(http.MethodPost, peoplePath, with(newPerson("Kari", "Nordmann"), "employeeNumber", "12345678901234567")),
		http.StatusBadRequest, "employee_number_too_long")
	if len(p.Errors["employeeNumber"]) == 0 {
		t.Errorf("typed: errors = %v, want employeeNumber named", p.Errors)
	}

	setPrefix(t, h, hr, "ABCDEFGH", 99999999)
	createPerson(t, hr, with(newPerson("Hand", "Assigned"), "employeeNumber", "ABCDEFGH99999999"))
	p = refused(t, hr.Do(http.MethodPost, peoplePath, newPerson("Kari", "Nordmann")),
		http.StatusBadRequest, "employee_number_too_long")
	if len(p.Errors["employeeNumber"]) == 0 {
		t.Errorf("generated: errors = %v, want employeeNumber named", p.Errors)
	}
	if n := nextEmployeeNumber(t, h); n != 99999999 {
		t.Errorf("next after a refused generation = %d, want it unchanged", n)
	}
}

// The employee number is unique however it is cased: "abc-1" and "ABC-1" are
// one number.
func TestPostPeople_EmployeeNumberIsUniqueCaseInsensitively(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "employeeNumber", "abc-1"))
	p := refused(t, hr.Do(http.MethodPost, peoplePath, with(newPerson("Ola", "Nordmann"), "employeeNumber", "ABC-1")),
		http.StatusBadRequest, "employee_number_taken")
	if len(p.Errors["employeeNumber"]) == 0 {
		t.Errorf("errors = %v, want employeeNumber named", p.Errors)
	}
}

// A work email is stored lower-cased and is unique however it is cased, on a
// create and on a change.
func TestPeople_WorkEmailTaken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	kari := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "workEmail", " Kari@Example.NO "))
	if kari.WorkEmail == nil || *kari.WorkEmail != "kari@example.no" {
		t.Errorf("workEmail = %v, want kari@example.no", kari.WorkEmail)
	}
	p := refused(t, hr.Do(http.MethodPost, peoplePath, with(newPerson("Ola", "Nordmann"), "workEmail", "KARI@example.no")),
		http.StatusBadRequest, "work_email_taken")
	if len(p.Errors["workEmail"]) == 0 {
		t.Errorf("create: errors = %v, want workEmail named", p.Errors)
	}
	ola := createPerson(t, hr, with(newPerson("Ola", "Nordmann"), "workEmail", "ola@example.no"))
	refused(t, hr.Do(http.MethodPut, personPath(ola.Id), with(newPerson("Ola", "Nordmann"), "workEmail", "kari@EXAMPLE.no"), ifMatch(ola.Revision)),
		http.StatusBadRequest, "work_email_taken")
}

// PUT replaces every editable field: an optional field left out is cleared,
// the display name stays when left out and changes when sent, the employee
// number may be sent as it is and never differently. A replace that changes
// nothing writes nothing — the revision stays.
func TestPutPerson_ReplacesTheEditableFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "phoneWork", "+47 22 00 00 00", "notes", "Starts in May"))

	var changed gen.StaffPerson
	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Hansen"), "employeeNumber", p.EmployeeNumber, "city", "Bergen"),
		http.StatusOK, &changed, ifMatch(p.Revision))
	if changed.FamilyName != "Hansen" || changed.DisplayName != "Kari Nordmann" || changed.Revision != 2 {
		t.Errorf("changed = %+v, want Hansen, display kept, revision 2", changed)
	}
	if changed.PhoneWork != nil || changed.Notes != nil || changed.City == nil || *changed.City != "Bergen" {
		t.Errorf("changed = %+v, want phoneWork and notes cleared, city Bergen", changed)
	}
	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Hansen"), "displayName", "Kari H.", "city", "Bergen"),
		http.StatusOK, &changed, ifMatch(changed.Revision))
	if changed.DisplayName != "Kari H." || changed.Revision != 3 {
		t.Errorf("renamed = %+v, want display Kari H., revision 3", changed)
	}

	var same gen.StaffPerson
	do(t, hr, http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Hansen"), "city", "Bergen"),
		http.StatusOK, &same, ifMatch(changed.Revision))
	if same.Revision != changed.Revision {
		t.Errorf("an unchanged replace moved the revision to %d, want %d", same.Revision, changed.Revision)
	}

	r := hr.Do(http.MethodPut, personPath(p.Id), with(newPerson("Kari", "Hansen"), "employeeNumber", "OTHER"), ifMatch(same.Revision))
	if pr := refused(t, r, http.StatusBadRequest, ""); len(pr.Errors["employeeNumber"]) == 0 {
		t.Errorf("a changed employee number: errors = %v, want employeeNumber named", pr.Errors)
	}
}

// A change carrying a revision that has moved on is a 409 revision_conflict;
// an If-Match without a revision is a 400 on it; an unknown person a bare 404.
func TestPutPerson_RevisionAndUnknown(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	do(t, hr, http.MethodPut, personPath(p.Id), newPerson("Kari", "Hansen"), http.StatusOK, nil, ifMatch(p.Revision))

	refused(t, hr.Do(http.MethodPut, personPath(p.Id), newPerson("Kari", "Olsen"), ifMatch(p.Revision)),
		http.StatusConflict, "revision_conflict")
	if pr := refused(t, hr.Do(http.MethodPut, personPath(p.Id), newPerson("Kari", "Olsen"), modtest.Header("If-Match", "latest")),
		http.StatusBadRequest, ""); len(pr.Errors["If-Match"]) == 0 {
		t.Errorf("errors = %v, want If-Match named", pr.Errors)
	}
	r := hr.Do(http.MethodPut, personPath(p.Id+1000), newPerson("Kari", "Olsen"), ifMatch(1))
	if r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("unknown person: %d %q, want a bare 404", r.Status, r.Body)
	}
}

// PersonRequest names the editable groups only: the sealed fields, the flags
// and the link are not on it, and a property it does not name is a 400
// naming that property, on a create and on a change.
func TestPeople_AnUnknownPropertyIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	for _, property := range []string{"nationalId", "bankAccount", "provisionAccount", "legalHold", "userId", "status"} {
		body := with(newPerson("Kari", "Nordmann"), property, "x")
		pr := refused(t, hr.Do(http.MethodPost, peoplePath, body), http.StatusBadRequest, "")
		if len(pr.Errors[property]) == 0 {
			t.Errorf("POST with %s: errors = %v, want it named", property, pr.Errors)
		}
		pr = refused(t, hr.Do(http.MethodPut, personPath(p.Id), body, ifMatch(p.Revision)), http.StatusBadRequest, "")
		if len(pr.Errors[property]) == 0 {
			t.Errorf("PUT with %s: errors = %v, want it named", property, pr.Errors)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM staff.people`); n != 1 {
		t.Errorf("people = %d, want only the first", n)
	}
}

// Writes are staff:manage's; a reader of every other kind is the uniform 403.
func TestPeople_WritesAreStaffManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	for _, perms := range [][]string{nil, {"staff:view"}, {"staff:personal-data"}, {"staff:approve"}} {
		c, _ := signIn(t, h, perms...)
		for _, req := range []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, peoplePath, newPerson("Ola", "Nordmann")},
			{http.MethodPut, personPath(p.Id), newPerson("Ola", "Nordmann")},
			{http.MethodDelete, personPath(p.Id), nil},
			{http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": []any{}}},
			{http.MethodPut, rolesPath(p.Id), map[string]any{"roles": []any{}}},
		} {
			if r := c.Do(req.method, req.path, req.body, ifMatch(p.Revision)); r.Status != http.StatusForbidden {
				t.Errorf("%v %s %s: %d, want 403", perms, req.method, req.path, r.Status)
			}
		}
	}
}

// GET /people is the directory: staff:view and staff:manage list, anyone
// else is the uniform 403. q matches every word against the names, the
// employee number and the work email; the list is paged by display name.
//
// staff:view without staff:manage lists the people active today only
// (plan reading 15; people_active_test.go has the cases).
func TestGetPeople_ListAndSearch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	kari := createPerson(t, hr, with(newPerson("Kari", "Nordmann"), "employeeNumber", "K-1", "workEmail", "kari@example.no"))
	createPerson(t, hr, with(newPerson("Ola", "Nordmann"), "employeeNumber", "O-2"))
	per := createPerson(t, hr, with(newPerson("Per", "Hansen"), "employeeNumber", "P-3", "workEmail", "per.h@firma.no", "phonePrivate", "999"))
	seedEmployment(t, h, kari.Id, 0)
	seedEmployment(t, h, per.Id, 0)

	viewer, _ := signIn(t, h, "staff:view")
	active := listPeople(t, viewer, "")
	if active.Pagination.TotalCount != 2 || len(active.Data) != 2 || active.Data[0].DisplayName != "Kari Nordmann" {
		t.Errorf("staff:view's list = %+v, want the 2 active today by display name", active)
	}
	if all := listPeople(t, hr, ""); all.Pagination.TotalCount != 3 {
		t.Errorf("staff:manage's list = %+v, want all 3", all)
	}
	for _, c := range []struct {
		q    string
		want []string
	}{
		{"nordmann", []string{"K-1", "O-2"}},
		{"kari NORD", []string{"K-1"}},
		{"p-3", []string{"P-3"}},
		{"firma.no", []string{"P-3"}},
		{"999", nil}, // a private phone is never searched
		{"100%", nil},
	} {
		got := listPeople(t, hr, "?q="+strings.ReplaceAll(strings.ReplaceAll(c.q, " ", "+"), "%", "%25"))
		var numbers []string
		for _, p := range got.Data {
			numbers = append(numbers, p.EmployeeNumber)
		}
		if !slices.Equal(numbers, c.want) {
			t.Errorf("q=%q: %v, want %v", c.q, numbers, c.want)
		}
	}
	page := listPeople(t, hr, "?page=2&pageSize=2")
	if len(page.Data) != 1 || page.Pagination.TotalPages != 2 || page.Data[0].DisplayName != "Per Hansen" {
		t.Errorf("page 2 = %+v, want Per Hansen alone of two pages", page)
	}
	if body := raw(t, hr.Do(http.MethodGet, peoplePath, nil)); body != nil {
		item := body["data"].([]any)[2].(map[string]any)
		if _, ok := item["phonePrivate"]; ok {
			t.Errorf("a list item carries phonePrivate: %v", item)
		}
	}
	refused(t, hr.Do(http.MethodGet, peoplePath+"?pageSize=0", nil), http.StatusBadRequest, "")
	for _, perms := range [][]string{nil, {"staff:personal-data"}, {"staff:approve"}} {
		c, _ := signIn(t, h, perms...)
		if r := c.Do(http.MethodGet, peoplePath, nil); r.Status != http.StatusForbidden {
			t.Errorf("%v listing: %d, want 403", perms, r.Status)
		}
	}
}

// Every write reads its actor from the user directory, and every read of a
// linked person reads the user — each before the transaction opens or after
// it commits (the harness fails any test on a call made inside one; this
// pins that the calls are made at all, so that check is not vacuous).
func TestPeople_EveryWriteReadsTheDirectoryOutsideItsTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, hrID := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	_, self := signIn(t, h)
	link(t, h, p.Id, self)
	do(t, hr, http.MethodPut, personPath(p.Id), newPerson("Kari", "Hansen"), http.StatusOK, nil, ifMatch(p.Revision))
	putNextOfKin(t, h, hr, p.Id, map[string]any{"nextOfKin": []any{map[string]any{"name": "Ola", "relationship": "spouse"}}})
	putRoles(t, h, hr, p.Id, map[string]any{"roles": []any{map[string]any{"role": "verneombud"}}})
	other := createPerson(t, hr, newPerson("Ola", "Nordmann"))
	do(t, hr, http.MethodDelete, personPath(other.Id), nil, http.StatusNoContent, nil)

	calls := contractCalls.by(hrID)
	var users int
	for _, c := range calls {
		if c.inTx {
			t.Errorf("%s was called inside a transaction", c.method)
		}
		if c.method == "Users.User" {
			users++
		}
	}
	// six writes, each reading its actor, and the PUT's response naming the
	// linked user.
	if users < 7 {
		t.Errorf("Users.User was called %d times by HR's requests, want at least 7: %+v", users, calls)
	}
}

// Every write of a person's rows takes the person's row first: while another
// transaction holds it, the write waits, and it completes once the row is
// free. The delete also takes the manager-graph lock, after the person's row,
// before it judges whether anyone names the person as manager.
func TestPeople_WritesTakeThePersonLockFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	p := createPerson(t, hr, newPerson("Kari", "Nordmann"))
	gone := createPerson(t, hr, newPerson("Ola", "Nordmann"))

	waitsFor := func(name, hold string, args []any, method, path string, body any, want int, opts ...modtest.RequestOption) {
		t.Helper()
		ctx := context.Background()
		tx, err := h.Pool().Begin(ctx)
		if err != nil {
			t.Fatalf("%s: begin: %v", name, err)
		}
		if _, err := tx.Exec(ctx, hold, args...); err != nil {
			t.Fatalf("%s: hold: %v", name, err)
		}
		done := make(chan *modtest.Response, 1)
		go func() { done <- hr.Do(method, path, body, opts...) }()
		select {
		case r := <-done:
			t.Errorf("%s: answered %d while the lock was held, want it to wait", name, r.Status)
			_ = tx.Rollback(ctx)
			return
		case <-time.After(300 * time.Millisecond):
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("%s: release: %v", name, err)
		}
		select {
		case r := <-done:
			if r.Status != want {
				t.Errorf("%s: %d %s after the lock was released, want %d", name, r.Status, r.Body, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: still waiting after the lock was released", name)
		}
	}
	personRow := `SELECT id FROM staff.people WHERE id = $1 FOR NO KEY UPDATE`
	waitsFor("PUT person", personRow, []any{p.Id}, http.MethodPut, personPath(p.Id), newPerson("Kari", "Hansen"), http.StatusOK, ifMatch(p.Revision))
	waitsFor("PUT next of kin", personRow, []any{p.Id}, http.MethodPut, nextOfKinPath(p.Id), map[string]any{"nextOfKin": []any{}}, http.StatusOK, ifMatch(personRevision(t, h, p.Id)))
	waitsFor("PUT roles", personRow, []any{p.Id}, http.MethodPut, rolesPath(p.Id), map[string]any{"roles": []any{}}, http.StatusOK, ifMatch(personRevision(t, h, p.Id)))
	waitsFor("DELETE on the person", personRow, []any{gone.Id}, http.MethodDelete, personPath(gone.Id), nil, http.StatusNoContent)
	again := createPerson(t, hr, newPerson("Per", "Nordmann"))
	waitsFor("DELETE on the manager graph", `SELECT pg_advisory_xact_lock(hashtext('staff-manager-graph'))`, nil,
		http.MethodDelete, personPath(again.Id), nil, http.StatusNoContent)
}

// signInNamed is a caller whose directory name is displayName, for a test
// that reads the name back off an event or a link.
func signInNamed(t *testing.T, h *harness, displayName string, permissions ...string) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, id := signIn(t, h, permissions...)
	h.Exec(t, `UPDATE identity.users SET display_name = $2 WHERE id = $1`, id, displayName)
	return c, id
}
