package staff

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is what the terms chain answers without being typed (design S5,
// §4 Managers, reading 1): the row in force on a day, the dates the
// a-melding wants derived — when the percentage and the pay in force took
// effect — and who manages whom on a day. Every function here is pure over
// rows already loaded, so the rules are table-tested without a database; the
// loading is the callers'.

// termsInForce is the row of chain in force on date: the latest whose
// validFrom is on or before it, within the employment's own days — nothing
// before the start or after the end (the end day itself is still worked). A
// row dated after date, a change agreed for later, is not yet in force.
// chain is oldest first.
func termsInForce(e store.StaffEmployment, chain []store.StaffEmploymentTerm, date time.Time) *store.StaffEmploymentTerm {
	if date.Before(civil(e.StartDate)) || (e.EndDate.Valid && date.After(e.EndDate.Time)) {
		return nil
	}
	i := rowOn(chain, date)
	if i < 0 {
		return nil
	}
	return &chain[i]
}

// rowOn is the index of chain's latest row from on or before date, -1 when
// every row is later. chain is oldest first.
func rowOn(chain []store.StaffEmploymentTerm, date time.Time) int {
	found := -1
	for i, r := range chain {
		if civil(r.ValidFrom).After(date) {
			break
		}
		found = i
	}
	return found
}

// changedAt is the day the value in force on date took effect: the earliest
// row of the unbroken run of rows, ending at the one in force, that same
// reports equal. A date before the first row is judged on the first row, so
// an employment that has not started answers its first row's day; a
// single-row chain answers that row's validFrom; no rows answer nil.
func changedAt(chain []store.StaffEmploymentTerm, date time.Time, same func(a, b store.StaffEmploymentTerm) bool) *time.Time {
	if len(chain) == 0 {
		return nil
	}
	i := max(rowOn(chain, date), 0)
	for i > 0 && same(chain[i-1], chain[i]) {
		i--
	}
	d := civil(chain[i].ValidFrom)
	return &d
}

// fteChangedAt is the a-melding's "date of the last change of the
// stillingsprosent" for the row in force on date.
func fteChangedAt(chain []store.StaffEmploymentTerm, date time.Time) *time.Time {
	return changedAt(chain, date, func(a, b store.StaffEmploymentTerm) bool {
		return hundredthsOfNumeric(a.FtePercent) == hundredthsOfNumeric(b.FtePercent)
	})
}

// salaryChangedAt is the a-melding's "date of the last pay change" for the
// row in force on date, judged on the pay components alone (spec S5): a list
// sorted by name and amount and compared on those two, so a reordered list,
// or a component whose currency or period alone changed, is the same pay.
func salaryChangedAt(chain []store.StaffEmploymentTerm, date time.Time) *time.Time {
	return changedAt(chain, date, func(a, b store.StaffEmploymentTerm) bool {
		return slices.Equal(payKey(a.PayComponents), payKey(b.PayComponents))
	})
}

// payLine is a pay component as salaryChangedAt compares it.
type payLine struct {
	name  string
	cents int64
}

// payKey is a stored pay_components list as the sorted (name, amount) pairs
// the comparison runs on. NULL, an empty list and a list the module did not
// write are all no pay at all: the column is the module's own, written
// through validation, so the last never happens but is not a panic.
func payKey(stored []byte) []payLine {
	var components []struct {
		Name   string  `json:"name"`
		Amount float64 `json:"amount"`
	}
	if len(stored) == 0 || json.Unmarshal(stored, &components) != nil {
		return nil
	}
	out := make([]payLine, 0, len(components))
	for _, c := range components {
		cents, _ := hundredthsOf(c.Amount)
		out = append(out, payLine{name: c.Name, cents: cents})
	}
	slices.SortFunc(out, func(a, b payLine) int {
		if a.name != b.name {
			if a.name < b.name {
				return -1
			}
			return 1
		}
		return int(a.cents - b.cents)
	})
	return out
}

// ---- The manager graph ------------------------------------------------------

// graphEmployment is an employment as the manager graph reads it: whose it is
// and the days it lasts (End nil while open-ended), and the business unit a
// person's place today names.
type graphEmployment struct {
	ID, PersonID   int64
	BusinessUnitID int64
	Start          time.Time
	End            *time.Time
}

// graphTerms is a terms row's edges: the manager it names, and the
// department whose manager is the fallback — and the title and the location
// a person's place today names, which no walk reads.
type graphTerms struct {
	EmploymentID          int64
	ValidFrom             time.Time
	DepartmentID, Manager *int64
	Title                 *string
	LocationID            *int64
}

// graphDepartment is a department's own edges: its manager and its parent.
type graphDepartment struct {
	ID                int64
	Manager, ParentID *int64
}

// managerGraph is who manages whom over time (design §4 Managers): the
// people's employments, each one's terms chain, and the departments. It is
// built once from rows loaded together and only read.
type managerGraph struct {
	employments map[int64][]graphEmployment // by person, by start
	chains      map[int64][]graphTerms      // by employment, oldest first
	departments map[int64]graphDepartment
}

func newManagerGraph(employments []graphEmployment, terms []graphTerms, departments []graphDepartment) managerGraph {
	g := managerGraph{
		employments: map[int64][]graphEmployment{},
		chains:      map[int64][]graphTerms{},
		departments: map[int64]graphDepartment{},
	}
	for _, e := range employments {
		g.employments[e.PersonID] = append(g.employments[e.PersonID], e)
	}
	for _, list := range g.employments {
		slices.SortFunc(list, func(a, b graphEmployment) int {
			if c := a.Start.Compare(b.Start); c != 0 {
				return c
			}
			return int(a.ID - b.ID)
		})
	}
	for _, t := range terms {
		g.chains[t.EmploymentID] = append(g.chains[t.EmploymentID], t)
	}
	for _, chain := range g.chains {
		slices.SortFunc(chain, func(a, b graphTerms) int { return a.ValidFrom.Compare(b.ValidFrom) })
	}
	for _, d := range departments {
		g.departments[d.ID] = d
	}
	return g
}

// inForce is the terms row of employment e in force on date, nil when the
// employment is not in force that day or has no row yet.
func (g managerGraph) inForce(e graphEmployment, date time.Time) *graphTerms {
	if date.Before(e.Start) || (e.End != nil && date.After(*e.End)) {
		return nil
	}
	var found *graphTerms
	for i, t := range g.chains[e.ID] {
		if t.ValidFrom.After(date) {
			break
		}
		found = &g.chains[e.ID][i]
	}
	return found
}

// managerOf is the person's managers on date as the structure names them,
// sorted and each once: for each employment in force that day, the manager
// its terms name, else the manager of its department — unless that is the
// person, or nobody, when the parent departments are climbed to the first
// whose manager is someone else — else none, whether or not the one named is
// in employment that day. Concurrent employments may give two. The climb
// carries a visited set, so departments that loop (written past this module)
// end it. Only the cycle check reads it: a loop is one of the edges written,
// whoever has left; who manages whom is managersOf's.
func (g managerGraph) managerOf(person int64, date time.Time) []int64 {
	var out []int64
	for _, e := range g.employments[person] {
		t := g.inForce(e, date)
		if t == nil {
			continue
		}
		m := t.Manager
		if m == nil && t.DepartmentID != nil {
			m = g.departmentManager(*t.DepartmentID, person)
		}
		if m != nil && !slices.Contains(out, *m) {
			out = append(out, *m)
		}
	}
	slices.Sort(out)
	return out
}

// departmentManager is the manager department id gives person: its own
// manager unless that is the person or nobody, else its parent's by the same
// rule, else nil.
func (g managerGraph) departmentManager(id, person int64) *int64 {
	visited := map[int64]bool{}
	for cur, ok := g.departments[id]; ok && !visited[cur.ID]; cur, ok = g.parentOf(cur) {
		visited[cur.ID] = true
		if cur.Manager != nil && *cur.Manager != person {
			return cur.Manager
		}
	}
	return nil
}

// effectiveManager is the manager employment e gives its person on date with
// anyone not in employment that day skipped (design §9, "a manager who
// leaves: their reports route to the department's manager"): the manager the
// terms in force name, else the first department from the terms' up whose
// manager is someone else — each candidate in employment on date — else nil.
// It is the module's one manager rule: the Manager audience, the org view,
// the manager a person's work group shows and the dashboard's items all read
// it, through managersOf or directly.
func (g managerGraph) effectiveManager(e graphEmployment, date time.Time) *int64 {
	t := g.inForce(e, date)
	if t == nil {
		return nil
	}
	present := func(m *int64) bool { return m != nil && *m != e.PersonID && g.activeOn(*m, date) }
	if present(t.Manager) {
		return t.Manager
	}
	if t.DepartmentID == nil {
		return nil
	}
	visited := map[int64]bool{}
	for cur, ok := g.departments[*t.DepartmentID]; ok && !visited[cur.ID]; cur, ok = g.parentOf(cur) {
		visited[cur.ID] = true
		if present(cur.Manager) {
			return cur.Manager
		}
	}
	return nil
}

// managersOf is the person's managers on date by effectiveManager, sorted and
// each once: one for each employment in force that day that has one, so
// concurrent employments may give two.
func (g managerGraph) managersOf(person int64, date time.Time) []int64 {
	var out []int64
	for _, e := range g.employments[person] {
		if m := g.effectiveManager(e, date); m != nil && !slices.Contains(out, *m) {
			out = append(out, *m)
		}
	}
	slices.Sort(out)
	return out
}

// headsTopOfStructure reports whether e's person is, on date and by the
// structure, nobody's report: the terms in force place them in a department
// they manage, and no department above it names a manager other than them —
// their department climb starts and ends in their own department, whatever
// manager the terms name.
func (g managerGraph) headsTopOfStructure(e graphEmployment, date time.Time) bool {
	t := g.inForce(e, date)
	if t == nil || t.DepartmentID == nil {
		return false
	}
	d, ok := g.departments[*t.DepartmentID]
	if !ok || d.Manager == nil || *d.Manager != e.PersonID {
		return false
	}
	visited := map[int64]bool{d.ID: true}
	for cur, ok := g.parentOf(d); ok && !visited[cur.ID]; cur, ok = g.parentOf(cur) {
		visited[cur.ID] = true
		if cur.Manager != nil && *cur.Manager != e.PersonID {
			return false
		}
	}
	return true
}

// parentOf is d's parent department, false at the top.
func (g managerGraph) parentOf(d graphDepartment) (graphDepartment, bool) {
	if d.ParentID == nil {
		return graphDepartment{}, false
	}
	p, ok := g.departments[*d.ParentID]
	return p, ok
}

// changeDates is every day on or after from on which the graph can change:
// from itself, each terms row's validFrom, each employment's start and the
// day after each end. Between two of them every person's managers stay the
// same, so judging the graph on these days judges it on every day.
func (g managerGraph) changeDates(from time.Time) []time.Time {
	seen := map[time.Time]bool{from: true}
	out := []time.Time{from}
	add := func(d time.Time) {
		if d.After(from) && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, list := range g.employments {
		for _, e := range list {
			add(e.Start)
			if e.End != nil {
				add(e.End.AddDate(0, 0, 1))
			}
		}
	}
	for _, chain := range g.chains {
		for _, t := range chain {
			add(t.ValidFrom)
		}
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return out
}

// onCycle reports whether person manages themselves on date through anyone:
// a walk up their managers, with a visited set, that comes back to them.
func (g managerGraph) onCycle(person int64, date time.Time) bool {
	visited := map[int64]bool{}
	stack := g.managerOf(person, date)
	for len(stack) > 0 {
		m := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if m == person {
			return true
		}
		if visited[m] {
			continue
		}
		visited[m] = true
		stack = append(stack, g.managerOf(m, date)...)
	}
	return false
}

// cycleFrom is the first day on or after from on which person is on a loop
// of the manager graph — judged on from and on every later day the graph
// changes (design §4 Managers) — and false when there is none.
func (g managerGraph) cycleFrom(person int64, from time.Time) (time.Time, bool) {
	for _, d := range g.changeDates(from) {
		if g.onCycle(person, d) {
			return d, true
		}
	}
	return time.Time{}, false
}

// peopleUnder is every person with an employment whose terms, on some row,
// name department id or one below it — the people whose manager a change of
// that department's manager or parent can change.
func (g managerGraph) peopleUnder(id int64) []int64 {
	under := func(dept int64) bool {
		visited := map[int64]bool{}
		for cur, ok := g.departments[dept]; ok && !visited[cur.ID]; cur, ok = g.parentOf(cur) {
			if cur.ID == id {
				return true
			}
			visited[cur.ID] = true
		}
		return false
	}
	var out []int64
	for person, list := range g.employments {
	employments:
		for _, e := range list {
			for _, t := range g.chains[e.ID] {
				if t.DepartmentID != nil && under(*t.DepartmentID) {
					out = append(out, person)
					break employments
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
