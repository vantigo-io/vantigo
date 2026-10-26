package planning

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The silent contracts answer nothing: this file's tests are about where a
// call is made from, not what it answers. Each embeds its interface, so a
// method no accessor calls panics rather than answers.
type (
	silentWorkforce struct{ contracts.Workforce }
	silentProjects  struct{ contracts.ProjectDirectory }
	silentCustomers struct{ contracts.CustomerDirectory }
	silentUsers     struct{ contracts.UserDirectory }
	silentCalendar  struct{ contracts.BusinessCalendar }
)

func (silentWorkforce) People(context.Context, time.Time, time.Time) ([]contracts.PersonEntry, error) {
	return nil, nil
}

func (silentWorkforce) Capacity(context.Context, time.Time, time.Time, []int64) (map[int64]contracts.PersonCapacity, error) {
	return nil, nil
}

func (silentWorkforce) Self(context.Context) (*contracts.PersonEntry, error) { return nil, nil }

func (silentWorkforce) HeldCompetences(context.Context, time.Time, []int64) (map[int64][]contracts.HeldCompetence, error) {
	return nil, nil
}

func (silentProjects) ProjectsByStatus(context.Context, []string) ([]contracts.ProjectEntry, error) {
	return nil, nil
}

func (silentProjects) Project(context.Context, int32) (*contracts.ProjectEntry, error) {
	return nil, nil
}

func (silentProjects) Projects(context.Context, []int32) ([]contracts.ProjectEntry, error) {
	return nil, nil
}

func (silentCustomers) Customers(context.Context, []int32) ([]contracts.CustomerEntry, error) {
	return nil, nil
}

func (silentUsers) User(context.Context, uuid.UUID) (*contracts.UserEntry, error) { return nil, nil }

func (silentUsers) Notify(context.Context, uuid.UUID, contracts.Notice) error { return nil }

func (silentUsers) PermissionHolders(context.Context, string) ([]contracts.Holder, error) {
	return nil, nil
}

func (silentCalendar) Zone(context.Context) (*time.Location, error) { return time.UTC, nil }

// The whole point of routing every call out of the module through
// contractscalls.go is that the suite can prove none is made inside one of
// the module's transactions. That check is only worth having if it fires, so
// this drives every accessor from outside any transaction, from inside a
// read-only snapshot and from inside each of the locks, and holds the hook's
// record and each accessor's answer against what was called where: outside,
// every call goes through; inside any of them, every call is reported and
// refused with errCallInsideTransaction.
//
// It is deliberately not parallel: it swaps the package-level hook TestMain
// installed and puts it back, and Go pauses every parallel test while a
// sequential one runs.
func TestContractCalls_AreReportedAndRefusedInsideATransaction(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	s := &server{deps: module.Deps{Pool: pool, Workforce: silentWorkforce{}, Projects: silentProjects{},
		Directory: silentCustomers{}, Users: silentUsers{}, Calendar: silentCalendar{}}}

	var outside, inside []string
	previous := contractCallHook
	t.Cleanup(func() { contractCallHook = previous })
	contractCallHook = func(ctx context.Context, method string) {
		if inOpenTx(ctx) {
			inside = append(inside, method)
			return
		}
		outside = append(outside, method)
	}

	today := day("2026-10-26")
	call := func(ctx context.Context) []error {
		user := uuid.New()
		_, e1 := s.workforcePeople(ctx, today, today)
		_, e2 := s.workforceCapacity(ctx, today, today, []int64{1001})
		_, e3 := s.workforceSelf(ctx)
		_, e4 := s.workforceHeld(ctx, today, []int64{1001})
		_, e5 := s.projectsByStatus(ctx, []string{"active"})
		_, e6 := s.projectEntry(ctx, 2001)
		_, e7 := s.projectEntries(ctx, []int32{2001})
		_, e8 := s.customerNames(ctx, []int32{3001})
		_, e9 := s.usersUser(ctx, user)
		e10 := s.usersNotify(ctx, user, contracts.Notice{})
		_, e11 := s.usersPermissionHolders(ctx, permManage)
		_, e12 := s.businessZone(ctx)
		return []error{e1, e2, e3, e4, e5, e6, e7, e8, e9, e10, e11, e12}
	}
	want := []string{"Workforce.People", "Workforce.Capacity", "Workforce.Self", "Workforce.HeldCompetences",
		"Projects.ProjectsByStatus", "Projects.Project", "Projects.Projects", "Directory.Customers",
		"Users.User", "Users.Notify", "Users.PermissionHolders", "Calendar.Zone"}

	for _, err := range call(ctx) {
		if err != nil {
			t.Errorf("outside a transaction a call failed: %v", err)
		}
	}
	if !slices.Equal(outside, want) || len(inside) != 0 {
		t.Errorf("outside a transaction the hook saw %v outside and %v inside, want %v outside", outside, inside, want)
	}

	for name, run := range map[string]func(func(context.Context) error) error{
		"withPersonLock": func(fn func(context.Context) error) error {
			return s.withPersonLock(ctx, 1001, func(ctx context.Context, _ *store.Queries) error { return fn(ctx) })
		},
		"withProjectLock": func(fn func(context.Context) error) error {
			return s.withProjectLock(ctx, 2001, func(ctx context.Context, _ *store.Queries) error { return fn(ctx) })
		},
		"withLocks": func(fn func(context.Context) error) error {
			set := lockSet{projects: []int32{2002, 2001}, persons: []int64{1002, 1001}}
			return s.withLocks(ctx, set, func(ctx context.Context, _ *store.Queries) error { return fn(ctx) })
		},
		"withReadTx": func(fn func(context.Context) error) error {
			return s.withReadTx(ctx, func(ctx context.Context, _ *store.Queries) error { return fn(ctx) })
		},
	} {
		t.Run(name, func(t *testing.T) {
			outside, inside = nil, nil
			err := run(func(ctx context.Context) error {
				for _, err := range call(ctx) {
					if !errors.Is(err, errCallInsideTransaction) {
						t.Errorf("inside %s a call answered %v, want errCallInsideTransaction", name, err)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !slices.Equal(inside, want) || len(outside) != 0 {
				t.Errorf("inside %s the hook saw %v inside and %v outside, want %v inside", name, inside, outside, want)
			}
		})
	}
}

// TestContractCalls_AnEmptyBatchAsksNobody proves the batch accessors answer
// an empty batch without calling out — a call that can only answer nothing.
func TestContractCalls_AnEmptyBatchAsksNobody(t *testing.T) {
	s := &server{deps: module.Deps{}}
	ctx := context.Background()
	capacity, e1 := s.workforceCapacity(ctx, time.Time{}, time.Time{}, nil)
	held, e2 := s.workforceHeld(ctx, time.Time{}, nil)
	projects, e3 := s.projectsByStatus(ctx, nil)
	entries, e4 := s.projectEntries(ctx, nil)
	names, e5 := s.customerNames(ctx, nil)
	if err := errors.Join(e1, e2, e3, e4, e5); err != nil {
		t.Fatalf("an empty batch failed: %v", err)
	}
	if len(capacity) != 0 || len(held) != 0 || len(projects) != 0 || len(entries) != 0 || len(names) != 0 {
		t.Errorf("an empty batch answered something: %v %v %v %v %v", capacity, held, projects, entries, names)
	}
}

// TestContractCalls_AContractNotComposedIsUnavailable is S25's floor: a
// contract config makes impossible to miss is still answered as
// errDependencyUnavailable — the 503 — rather than a nil-interface panic.
func TestContractCalls_AContractNotComposedIsUnavailable(t *testing.T) {
	s := &server{deps: module.Deps{}}
	ctx := context.Background()
	today := day("2026-10-26")
	_, e1 := s.workforcePeople(ctx, today, today)
	_, e2 := s.workforceCapacity(ctx, today, today, []int64{1001})
	_, e3 := s.workforceSelf(ctx)
	_, e4 := s.workforceHeld(ctx, today, []int64{1001})
	_, e5 := s.projectsByStatus(ctx, []string{"active"})
	_, e6 := s.projectEntry(ctx, 2001)
	_, e7 := s.projectEntries(ctx, []int32{2001})
	_, e8 := s.customerNames(ctx, []int32{3001})
	for i, err := range []error{e1, e2, e3, e4, e5, e6, e7, e8} {
		if !errors.Is(err, errDependencyUnavailable) {
			t.Errorf("accessor %d answered %v, want errDependencyUnavailable", i+1, err)
		}
	}
}

// TestLockSet_TakesProjectsBeforePersonsEachAscending is S23's lock order: a
// write that needs a project's lock and a person's takes the project's
// first, and two persons in ascending id, each lock once — the one order that
// keeps two such writes from deadlocking.
func TestLockSet_TakesProjectsBeforePersonsEachAscending(t *testing.T) {
	got := lockSet{persons: []int64{1009, 1002, 1009}, projects: []int32{2005, 2001}}.names()
	want := []string{"planning-project-2001", "planning-project-2005", "planning-person-1002", "planning-person-1009"}
	if !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}
