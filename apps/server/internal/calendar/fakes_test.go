package calendar_test

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// fakeSource is a contracts.CalendarSource standing in for one module's
// provider (calendar design D1) — depguard keeps the real ones out of this
// package. It declares the kinds a test gives it and answers, for the user a
// query names, the items a test set for that user; like a real provider it is
// its own gatekeeper, answering nothing — not an error — when the caller of
// the request in ctx lacks its key. That check is contracts.HasPermission
// on the request's context, so an answer at all proves the calendar handed
// the provider the request's context. fail makes it answer an error instead.
type fakeSource struct {
	kinds []contracts.CalendarKind
	gate  string

	mu      sync.Mutex
	access  contracts.Access
	items   map[uuid.UUID][]contracts.CalendarItem
	queries []contracts.CalendarQuery
	fail    error

	asked atomic.Int32
}

var _ contracts.CalendarSource = (*fakeSource)(nil)

// newFakeSource is a provider of module's kinds, gated on gate.
func newFakeSource(module, gate string, nouns ...string) *fakeSource {
	f := &fakeSource{gate: gate, items: map[uuid.UUID][]contracts.CalendarItem{}}
	for _, noun := range nouns {
		f.kinds = append(f.kinds, contracts.CalendarKind{Key: module + "." + noun, Module: module})
	}
	return f
}

func (f *fakeSource) CalendarKinds() []contracts.CalendarKind { return f.kinds }

func (f *fakeSource) CalendarItems(ctx context.Context, q contracts.CalendarQuery) ([]contracts.CalendarItem, error) {
	f.asked.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	if f.fail != nil {
		return nil, f.fail
	}
	if f.access == nil || !contracts.HasPermission(ctx, f.access, f.gate) {
		return nil, nil
	}
	return append([]contracts.CalendarItem(nil), f.items[q.UserID]...), nil
}

// give sets the items the provider answers for userID.
func (f *fakeSource) give(userID uuid.UUID, items ...contracts.CalendarItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items[userID] = items
}

// failWith makes the provider answer err on every call.
func (f *fakeSource) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

// lastQuery is the last window the provider was asked about.
func (f *fakeSource) lastQuery() (contracts.CalendarQuery, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queries) == 0 {
		return contracts.CalendarQuery{}, false
	}
	return f.queries[len(f.queries)-1], true
}

// fakeSources is the five providers of delivery A, one per module, each on
// that module's real gate key.
type fakeSources struct {
	customers, staff, projects, time, expenses *fakeSource
}

func newFakeSources() *fakeSources {
	return &fakeSources{
		customers: newFakeSource("customers", "customers:view", "follow-up", "interaction"),
		staff:     newFakeSource("staff", "staff:access", "absence"),
		projects:  newFakeSource("projects", "projects:access", "task"),
		time:      newFakeSource("time", "time:access", "entry"),
		expenses:  newFakeSource("expenses", "expenses:access", "claim"),
	}
}

func (s *fakeSources) all() []*fakeSource {
	return []*fakeSource{s.customers, s.staff, s.projects, s.time, s.expenses}
}

func (s *fakeSources) slot() []contracts.CalendarSource {
	out := make([]contracts.CalendarSource, 0, 5)
	for _, f := range s.all() {
		out = append(out, f)
	}
	return out
}

// wire hands every provider the installation's access layer, which the
// harness builds only once it is composed.
func (s *fakeSources) wire(access contracts.Access) {
	for _, f := range s.all() {
		f.mu.Lock()
		f.access = access
		f.mu.Unlock()
	}
}

// fakeDirectory is the customer directory as Customers would answer it: the
// one method the calendar calls, Customer, from a map; every other method is
// the embedded nil interface and panics if called, which no test expects.
type fakeDirectory struct {
	contracts.CustomerDirectory
	customers map[int32]string
}

func (d fakeDirectory) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	name, ok := d.customers[id]
	if !ok {
		return nil, nil
	}
	return &contracts.CustomerEntry{ID: id, Name: name}, nil
}

// fakeProjects is the project directory as Projects would answer it, for
// the two methods the calendar calls: Project, and Role — the role a user
// holds on a project, "" for none, from roles keyed by project and user.
type fakeProjects struct {
	contracts.ProjectDirectory
	projects map[int32][2]string // code, name
	roles    map[int32]map[uuid.UUID]string
}

func (p fakeProjects) Role(_ context.Context, projectID int32, userID uuid.UUID) (string, error) {
	return p.roles[projectID][userID], nil
}

func (p fakeProjects) Project(_ context.Context, id int32) (*contracts.ProjectEntry, error) {
	v, ok := p.projects[id]
	if !ok {
		return nil, nil
	}
	return &contracts.ProjectEntry{ID: id, Code: v[0], Name: v[1]}, nil
}
