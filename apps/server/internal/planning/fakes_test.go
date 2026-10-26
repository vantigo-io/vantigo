package planning_test

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The fakes the harness composes the module with (plan reading 1: fakes live
// in the module's tests, as Time's fakeProjects does): Staff's register, the
// project directory and the customer directory, each built directly against
// internal/contracts — depguard forbids internal/planning/** from importing
// internal/staff, internal/projects or internal/customers, even in tests.
// Every one is safe for concurrent use, because the handlers of one harness
// read it from many requests at once, and every one notes a call made from
// inside one of the module's transactions to the package's recorder — a
// second net under contractscalls.go's own, for a call that bypassed it.

// date is a civil date at midnight UTC, as every date of the contracts is.
func date(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

// The people the fake register starts with: Kari full time from the start of
// 2026, Ola at 80 % from the start of 2026, and Siri, who starts on
// 1 December 2026 — next month's starter, whom People answers for a window
// reaching it (S8). No one is away and no one holds anything until a test
// says so.
const (
	personKari = int64(1001)
	personOla  = int64(1002)
	personSiri = int64(1003)

	departmentDelivery = int64(4001)
)

// fakePerson is one person of the fake register: the entry People answers
// (Employed as the person's whole employment, which People clips to the
// window asked), the hours a week in force, the days away and the
// competences held.
type fakePerson struct {
	entry    contracts.PersonEntry
	segments []contracts.CapacitySegment
	away     []contracts.DateSpan
	held     []contracts.HeldCompetence
}

// fakeWorkforce is contracts.Workforce over a register a test shapes. visible
// false is the caller without staff:view: People, Capacity and
// HeldCompetences answer nothing, as Staff's Colleague rule does (S8); Self
// answers under its own rule regardless. self maps a user to the person their
// self-service link names. refuseWindow makes People and Capacity answer
// contracts.ErrWindowOutOfBounds, the bound's refusal.
type fakeWorkforce struct {
	mu           sync.Mutex
	people       map[int64]*fakePerson
	self         map[uuid.UUID]int64
	visible      bool
	refuseWindow bool
}

var _ contracts.Workforce = (*fakeWorkforce)(nil)

func newFakeWorkforce() *fakeWorkforce {
	department := departmentDelivery
	return &fakeWorkforce{
		visible: true,
		self:    map[uuid.UUID]int64{},
		people: map[int64]*fakePerson{
			personKari: {
				entry: contracts.PersonEntry{ID: personKari, DisplayName: "Kari Nordmann", EmployeeNumber: "A-1001",
					DepartmentID: &department, DepartmentName: "Leveranse",
					Employed: []contracts.DateSpan{{From: date("2026-01-01"), To: date("2029-12-31")}}},
				segments: []contracts.CapacitySegment{{From: date("2026-01-01"), To: date("2029-12-31"), HoursPerWeek: 37.5}},
			},
			personOla: {
				entry: contracts.PersonEntry{ID: personOla, DisplayName: "Ola Nordmann", EmployeeNumber: "A-1002",
					DepartmentID: &department, DepartmentName: "Leveranse",
					Employed: []contracts.DateSpan{{From: date("2026-01-01"), To: date("2029-12-31")}}},
				segments: []contracts.CapacitySegment{{From: date("2026-01-01"), To: date("2029-12-31"), HoursPerWeek: 30}},
			},
			personSiri: {
				entry: contracts.PersonEntry{ID: personSiri, DisplayName: "Siri Starter", EmployeeNumber: "A-1003",
					Employed: []contracts.DateSpan{{From: date("2026-12-01"), To: date("2029-12-31")}}},
				segments: []contracts.CapacitySegment{{From: date("2026-12-01"), To: date("2029-12-31"), HoursPerWeek: 37.5}},
			},
		},
	}
}

// link makes personID the person Self answers for userID.
func (w *fakeWorkforce) link(userID uuid.UUID, personID int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.self[userID] = personID
	w.people[personID].entry.UserID = &userID
}

// setVisible answers the Colleague reads (false: nothing, as to a caller
// without staff:view).
func (w *fakeWorkforce) setVisible(visible bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.visible = visible
}

// setAway makes personID away over spans.
func (w *fakeWorkforce) setAway(personID int64, spans ...contracts.DateSpan) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.people[personID].away = spans
}

// setHeld gives personID the competences held.
func (w *fakeWorkforce) setHeld(personID int64, held ...contracts.HeldCompetence) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.people[personID].held = held
}

func (w *fakeWorkforce) Competences(ctx context.Context) ([]contracts.CompetenceEntry, error) {
	lockedContractCalls.note(ctx, "fake Workforce.Competences")
	return nil, nil
}

func (w *fakeWorkforce) HeldCompetences(ctx context.Context, on time.Time, personIDs []int64) (map[int64][]contracts.HeldCompetence, error) {
	lockedContractCalls.note(ctx, "fake Workforce.HeldCompetences")
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[int64][]contracts.HeldCompetence{}
	if !w.visible {
		return out, nil
	}
	for _, id := range personIDs {
		p, ok := w.people[id]
		if !ok {
			continue
		}
		for _, h := range p.held {
			if !h.ValidFrom.After(on) && (h.ValidTo == nil || !h.ValidTo.Before(on)) {
				out[id] = append(out[id], h)
			}
		}
	}
	return out, nil
}

// People answers everyone employed on a day of the window, Employed clipped
// to it, by display name then id.
func (w *fakeWorkforce) People(ctx context.Context, from, to time.Time) ([]contracts.PersonEntry, error) {
	lockedContractCalls.note(ctx, "fake Workforce.People")
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.refuseWindow {
		return nil, contracts.ErrWindowOutOfBounds
	}
	if !w.visible {
		return nil, nil
	}
	var out []contracts.PersonEntry
	for _, p := range w.people {
		employed := clip(p.entry.Employed, from, to)
		if len(employed) == 0 {
			continue
		}
		e := p.entry
		e.Employed = employed
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b contracts.PersonEntry) int {
		return cmp.Or(strings.Compare(a.DisplayName, b.DisplayName), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// Capacity answers each known person's segments and away spans clipped to
// the window.
func (w *fakeWorkforce) Capacity(ctx context.Context, from, to time.Time, personIDs []int64) (map[int64]contracts.PersonCapacity, error) {
	lockedContractCalls.note(ctx, "fake Workforce.Capacity")
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.refuseWindow {
		return nil, contracts.ErrWindowOutOfBounds
	}
	out := map[int64]contracts.PersonCapacity{}
	if !w.visible {
		return out, nil
	}
	for _, id := range personIDs {
		p, ok := w.people[id]
		if !ok {
			continue
		}
		var segments []contracts.CapacitySegment
		for _, s := range p.segments {
			if s.To.Before(from) || s.From.After(to) {
				continue
			}
			segments = append(segments, contracts.CapacitySegment{From: latest(s.From, from), To: earliest(s.To, to), HoursPerWeek: s.HoursPerWeek})
		}
		out[id] = contracts.PersonCapacity{Segments: segments, Away: clip(p.away, from, to)}
	}
	return out, nil
}

// Self answers the person linked to the request's user, Employed empty, as
// Staff's Self does.
func (w *fakeWorkforce) Self(ctx context.Context) (*contracts.PersonEntry, error) {
	lockedContractCalls.note(ctx, "fake Workforce.Self")
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok {
		return nil, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	id, ok := w.self[p.UserID]
	if !ok {
		return nil, nil
	}
	e := w.people[id].entry
	e.Employed = nil
	return &e, nil
}

// clip is spans cut to from..to, a span wholly outside dropped.
func clip(spans []contracts.DateSpan, from, to time.Time) []contracts.DateSpan {
	var out []contracts.DateSpan
	for _, s := range spans {
		if s.To.Before(from) || s.From.After(to) {
			continue
		}
		out = append(out, contracts.DateSpan{From: latest(s.From, from), To: earliest(s.To, to)})
	}
	return out
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// The projects the fake directory knows: an active project of Kraft-Verket
// with dates, a planned bid of the same customer (tentative by default, S3),
// a completed one (project_closed), and an internal project with no
// customer and no dates.
const (
	projectActive    = int32(2001)
	projectPlanned   = int32(2002)
	projectCompleted = int32(2003)
	projectInternal  = int32(2004)
	projectUnknown   = int32(2999)

	customerKraftVerket = int32(3001)
)

// fakeProjects is contracts.ProjectDirectory over a map a test may add to
// (setProject). It answers every method; those Planning does not read answer
// nothing.
type fakeProjects struct {
	mu       sync.Mutex
	projects map[int32]contracts.ProjectEntry
}

var _ contracts.ProjectDirectory = (*fakeProjects)(nil)

func newFakeProjects() *fakeProjects {
	customer := customerKraftVerket
	return &fakeProjects{projects: map[int32]contracts.ProjectEntry{
		projectActive: {ID: projectActive, Code: "KVEM1000", Name: "Kraft-Verket modernisering", CustomerID: &customer,
			Status: "active", OpenForWork: true, BillingType: "time-and-materials",
			StartDate: ptr(date("2026-09-01")), EndDate: ptr(date("2027-06-30"))},
		projectPlanned: {ID: projectPlanned, Code: "KVEM2000", Name: "Kraft-Verket fase 2", CustomerID: &customer,
			Status: "planned", BillingType: "time-and-materials"},
		projectCompleted: {ID: projectCompleted, Code: "FERDIG01", Name: "Avsluttet prosjekt", CustomerID: &customer,
			Status: "completed", BillingType: "time-and-materials"},
		projectInternal: {ID: projectInternal, Code: "INTERN", Name: "Internt arbeid",
			Status: "active", OpenForWork: true, BillingType: "non-billable"},
	}}
}

// setProject adds or replaces a project.
func (p *fakeProjects) setProject(e contracts.ProjectEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.projects[e.ID] = e
}

func (p *fakeProjects) Project(ctx context.Context, id int32) (*contracts.ProjectEntry, error) {
	lockedContractCalls.note(ctx, "fake Projects.Project")
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.projects[id]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (p *fakeProjects) Projects(ctx context.Context, ids []int32) ([]contracts.ProjectEntry, error) {
	lockedContractCalls.note(ctx, "fake Projects.Projects")
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []contracts.ProjectEntry
	for _, id := range ids {
		if e, ok := p.projects[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// ProjectsByStatus answers the projects in statuses by code then id, at
// most contracts.MaxDirectoryProjects, as Projects does.
func (p *fakeProjects) ProjectsByStatus(ctx context.Context, statuses []string) ([]contracts.ProjectEntry, error) {
	lockedContractCalls.note(ctx, "fake Projects.ProjectsByStatus")
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []contracts.ProjectEntry
	for _, e := range p.projects {
		if slices.Contains(statuses, e.Status) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b contracts.ProjectEntry) int {
		return cmp.Or(strings.Compare(a.Code, b.Code), cmp.Compare(a.ID, b.ID))
	})
	if len(out) > contracts.MaxDirectoryProjects {
		out = out[:contracts.MaxDirectoryProjects]
	}
	return out, nil
}

func (p *fakeProjects) ProjectByCode(ctx context.Context, code string) (*contracts.ProjectEntry, error) {
	lockedContractCalls.note(ctx, "fake Projects.ProjectByCode")
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.projects {
		if strings.EqualFold(e.Code, code) {
			return &e, nil
		}
	}
	return nil, nil
}

func (p *fakeProjects) ProjectsForCustomer(ctx context.Context, customerID int32) ([]contracts.ProjectEntry, error) {
	lockedContractCalls.note(ctx, "fake Projects.ProjectsForCustomer")
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []contracts.ProjectEntry
	for _, e := range p.projects {
		if e.CustomerID != nil && *e.CustomerID == customerID {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b contracts.ProjectEntry) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (p *fakeProjects) Role(context.Context, int32, uuid.UUID) (string, error) { return "", nil }

func (p *fakeProjects) BillingLine(context.Context, int32, int32) (*contracts.BillingLineEntry, error) {
	return nil, nil
}

func (p *fakeProjects) ProjectsForUser(context.Context, uuid.UUID) ([]contracts.ProjectEntry, error) {
	return nil, nil
}

func (p *fakeProjects) BillingLines(context.Context, int32) ([]contracts.BillingLineEntry, error) {
	return nil, nil
}

func (p *fakeProjects) Task(context.Context, int32) (*contracts.TaskEntry, error) { return nil, nil }

func (p *fakeProjects) OpenTasksForUser(context.Context, uuid.UUID) ([]contracts.TaskEntry, error) {
	return nil, nil
}

func (p *fakeProjects) CanLogTime(context.Context, int32, uuid.UUID) (bool, error) { return false, nil }

func (p *fakeProjects) WorkType(context.Context, int32) (*contracts.WorkTypeEntry, error) {
	return nil, nil
}

func (p *fakeProjects) WorkTypes(context.Context, int32) ([]contracts.WorkTypeEntry, error) {
	return nil, nil
}

// fakeCustomers is contracts.CustomerDirectory answering the customers'
// names, the one thing Planning reads of it (S9). It embeds the interface, so
// a method Planning has no business calling panics rather than answers.
type fakeCustomers struct {
	contracts.CustomerDirectory
	mu    sync.Mutex
	names map[int32]string
}

func newFakeCustomers() *fakeCustomers {
	return &fakeCustomers{names: map[int32]string{customerKraftVerket: "Kraft-Verket AS"}}
}

func (c *fakeCustomers) Customers(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	lockedContractCalls.note(ctx, "fake Directory.Customers")
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []contracts.CustomerEntry{}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(ids))) {
		if name, ok := c.names[id]; ok {
			out = append(out, contracts.CustomerEntry{ID: id, Name: name})
		}
	}
	return out, nil
}

func (c *fakeCustomers) Customer(ctx context.Context, id int32) (*contracts.CustomerEntry, error) {
	lockedContractCalls.note(ctx, "fake Directory.Customer")
	c.mu.Lock()
	defer c.mu.Unlock()
	name, ok := c.names[id]
	if !ok {
		return nil, nil
	}
	return &contracts.CustomerEntry{ID: id, Name: name}, nil
}

// addPerson adds a person to the fake register, employed from..to at
// hoursPerWeek, in no department.
func (w *fakeWorkforce) addPerson(id int64, name, number string, from, to time.Time, hoursPerWeek float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.people[id] = &fakePerson{
		entry:    contracts.PersonEntry{ID: id, DisplayName: name, EmployeeNumber: number, Employed: []contracts.DateSpan{{From: from, To: to}}},
		segments: []contracts.CapacitySegment{{From: from, To: to, HoursPerWeek: hoursPerWeek}},
	}
}

// endEmployment ends personID's employment, and the hours in force, on last.
func (w *fakeWorkforce) endEmployment(personID int64, last time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p := w.people[personID]
	for i := range p.entry.Employed {
		p.entry.Employed[i].To = earliest(p.entry.Employed[i].To, last)
	}
	for i := range p.segments {
		p.segments[i].To = earliest(p.segments[i].To, last)
	}
}

// setRefuseWindow makes People and Capacity answer
// contracts.ErrWindowOutOfBounds, the bound's refusal.
func (w *fakeWorkforce) setRefuseWindow(refuse bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.refuseWindow = refuse
}
