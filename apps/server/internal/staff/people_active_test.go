package staff_test

import (
	"bytes"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// shiftDay is the calendar day d days from day (YYYY-MM-DD).
func shiftDay(t *testing.T, day string, d int) string {
	t.Helper()
	at, err := time.Parse(time.DateOnly, day)
	if err != nil {
		t.Fatal(err)
	}
	return at.AddDate(0, 0, d).Format(time.DateOnly)
}

// The colleague audience (staff:view without staff:manage) holds only for a
// person active today in the business zone — an employment that has started
// and not ended (plan reading 15): an unannounced hire, a leaver and someone
// with no employment are the bare 404 an unknown id is, on the person and on
// every sub-resource, and absent from the directory. staff:manage reads them
// all and lists them by status.
func TestColleague_SeesOnlyPeopleActiveToday(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	day := today(t, h)
	ended := shiftDay(t, day, -1)

	active := createPerson(t, hr, with(newPerson("Active", "Today"), "employeeNumber", "A-1"))
	seedEmploymentDated(t, h, active.Id, 0, day, nil)
	endsToday := createPerson(t, hr, with(newPerson("Ends", "Today"), "employeeNumber", "A-2"))
	seedEmploymentDated(t, h, endsToday.Id, 0, shiftDay(t, day, -30), &day)
	future := createPerson(t, hr, with(newPerson("Starts", "Later"), "employeeNumber", "F-1"))
	seedEmploymentDated(t, h, future.Id, 0, shiftDay(t, day, 1), nil)
	left := createPerson(t, hr, with(newPerson("Has", "Left"), "employeeNumber", "L-1"))
	seedEmploymentDated(t, h, left.Id, 0, shiftDay(t, day, -400), &ended)
	none := createPerson(t, hr, with(newPerson("No", "Employment"), "employeeNumber", "N-1"))

	colleague, _ := signIn(t, h, "staff:view")
	unknown := colleague.Do(http.MethodGet, personPath(none.Id+1000), nil)
	for _, id := range []int64{active.Id, endsToday.Id} {
		for _, path := range []string{personPath(id), rolesPath(id)} {
			if r := colleague.Do(http.MethodGet, path, nil); r.Status != http.StatusOK {
				t.Errorf("colleague GET %s of a person active today: %d, want 200", path, r.Status)
			}
		}
	}
	for name, id := range map[string]int64{"a future hire": future.Id, "a leaver": left.Id, "no employment": none.Id} {
		for _, path := range []string{personPath(id), rolesPath(id), nextOfKinPath(id), eventsPath(id)} {
			r := colleague.Do(http.MethodGet, path, nil)
			if r.Status != http.StatusNotFound || !bytes.Equal(r.Body, unknown.Body) {
				t.Errorf("colleague GET %s of %s: %d %q, want the unknown id's 404", path, name, r.Status, r.Body)
			}
		}
		if r := hr.Do(http.MethodGet, personPath(id), nil); r.Status != http.StatusOK {
			t.Errorf("staff:manage GET %s: %d, want 200", name, r.Status)
		}
	}

	numbers := func(c *modtest.Client) []string {
		var out []string
		for _, p := range listPeople(t, c, "").Data {
			out = append(out, p.EmployeeNumber)
		}
		slices.Sort(out)
		return out
	}
	if got := numbers(colleague); !slices.Equal(got, []string{"A-1", "A-2"}) {
		t.Errorf("colleague lists %v, want the two active today", got)
	}
	if page := listPeople(t, colleague, ""); page.Pagination.TotalCount != 2 {
		t.Errorf("colleague's total = %d, want 2", page.Pagination.TotalCount)
	}
	// HR's list defaults to the current people — the leaver is under
	// ?status=ended — and ?status=all is everyone (TestPeopleList_StatusPerAudience).
	both, _ := signIn(t, h, "staff:view", "staff:manage")
	for name, c := range map[string]*modtest.Client{"staff:manage": hr, "staff:view+manage": both} {
		if got := numbers(c); len(got) != 4 || slices.Contains(got, "L-1") {
			t.Errorf("%s lists %v, want the four current", name, got)
		}
		var all []string
		for _, p := range listPeople(t, c, "?status=all").Data {
			all = append(all, p.EmployeeNumber)
		}
		if len(all) != 5 {
			t.Errorf("%s lists %v under status=all, want all five", name, all)
		}
	}
}
