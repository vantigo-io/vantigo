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

// contactsDirectory is the customer directory for the invitations' tests:
// Customer, Contact and ContactsForCustomer from maps a test can change
// between requests (erase), under a lock because the server reads them from
// its own goroutines. Every other method panics, as fakeDirectory's.
type contactsDirectory struct {
	contracts.CustomerDirectory
	mu        sync.Mutex
	customers map[int32]string
	contacts  map[int32]contracts.ContactEntry
	// of is each customer's contacts, in the order ContactsForCustomer
	// answers them.
	of map[int32][]int32
}

func (d *contactsDirectory) Customer(_ context.Context, id int32) (*contracts.CustomerEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	name, ok := d.customers[id]
	if !ok {
		return nil, nil
	}
	return &contracts.CustomerEntry{ID: id, Name: name}, nil
}

func (d *contactsDirectory) Contact(_ context.Context, id int32) (*contracts.ContactEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.contacts[id]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (d *contactsDirectory) ContactsForCustomer(_ context.Context, customerID int32) ([]contracts.ContactEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []contracts.ContactEntry{}
	for _, id := range d.of[customerID] {
		if c, ok := d.contacts[id]; ok && c.Email != nil {
			out = append(out, c)
		}
	}
	return out, nil
}

// setEmail gives contact id a new address, as an edit in Customers does.
func (d *contactsDirectory) setEmail(id int32, address string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.contacts[id]
	c.Email = &address
	d.contacts[id] = c
}

// erase makes the directory answer nothing for contact id, as Customers does
// for an anonymised one.
func (d *contactsDirectory) erase(id int32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.contacts, id)
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

// fakeWorkforce is the workforce register as Staff would answer the one
// method the calendar calls, DirectoryAccount (calendar Outlook sync design
// O3): the account set for the principal in ctx — a request's caller or the
// person contracts.AsUser names — and nil for anyone else, under a lock
// because the worker reads it from its own goroutine. Every other method is
// the embedded nil interface and panics if called, which no test expects.
type fakeWorkforce struct {
	contracts.Workforce
	mu       sync.Mutex
	accounts map[uuid.UUID]contracts.DirectoryAccount
}

func newFakeWorkforce() *fakeWorkforce {
	return &fakeWorkforce{accounts: map[uuid.UUID]contracts.DirectoryAccount{}}
}

func (f *fakeWorkforce) DirectoryAccount(ctx context.Context) (*contracts.DirectoryAccount, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok {
		return nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.accounts[p.UserID]
	if !ok {
		return nil, nil
	}
	return &a, nil
}

// set gives userID the account, or (a nil) takes it away.
func (f *fakeWorkforce) set(userID uuid.UUID, a *contracts.DirectoryAccount) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a == nil {
		delete(f.accounts, userID)
		return
	}
	f.accounts[userID] = *a
}
