package expenses

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// silentDirectory is contracts.ProjectDirectory answering nothing, and
// silentUsers is contracts.UserDirectory doing the same: this test is about
// where a call is made from, not what it answers.
type silentDirectory struct{}

var _ contracts.ProjectDirectory = silentDirectory{}

func (silentDirectory) Project(context.Context, int32) (*contracts.ProjectEntry, error) {
	return nil, nil
}

func (silentDirectory) Role(context.Context, int32, uuid.UUID) (string, error) { return "", nil }

func (silentDirectory) BillingLine(context.Context, int32, int32) (*contracts.BillingLineEntry, error) {
	return nil, nil
}

func (silentDirectory) ProjectsForUser(context.Context, uuid.UUID) ([]contracts.ProjectEntry, error) {
	return nil, nil
}

func (silentDirectory) ProjectsForCustomer(context.Context, int32) ([]contracts.ProjectEntry, error) {
	return nil, nil
}

func (silentDirectory) Projects(context.Context, []int32) ([]contracts.ProjectEntry, error) {
	return nil, nil
}

func (silentDirectory) ProjectByCode(context.Context, string) (*contracts.ProjectEntry, error) {
	return nil, nil
}

func (silentDirectory) BillingLines(context.Context, int32) ([]contracts.BillingLineEntry, error) {
	return nil, nil
}

func (silentDirectory) Task(context.Context, int32) (*contracts.TaskEntry, error) { return nil, nil }

func (silentDirectory) OpenTasksForUser(context.Context, uuid.UUID) ([]contracts.TaskEntry, error) {
	return nil, nil
}

func (silentDirectory) CanLogTime(context.Context, int32, uuid.UUID) (bool, error) {
	return false, nil
}

type silentUsers struct{}

var _ contracts.UserDirectory = silentUsers{}

func (silentUsers) User(context.Context, uuid.UUID) (*contracts.UserEntry, error) { return nil, nil }

func (silentUsers) Users(context.Context, []uuid.UUID) ([]contracts.UserEntry, error) {
	return nil, nil
}

func (silentUsers) SearchUsers(context.Context, string, int) ([]contracts.UserEntry, error) {
	return nil, nil
}

// The whole point of routing every cross-module call through contractscalls.go
// is that the suite can prove none is made while a transaction holds locks.
// That check is only worth having if it actually fires, so this drives every
// accessor from both sides of withLockedTx and holds the hook's answer against
// what was called where. It is the mechanism's own test; the harness then
// applies it to every test in the package.
//
// It is deliberately not parallel: it swaps the package-level hook that
// TestMain installed and puts it back, and Go pauses every parallel test while
// a sequential one runs, so nothing else can be making a reportable call
// meanwhile.
func TestContractCalls_AreReportedWithWhetherLocksWereHeld(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	s := &server{deps: module.Deps{Pool: pool, Projects: silentDirectory{}, Users: silentUsers{}}}

	var outside, inside []string
	previous := contractCallHook
	t.Cleanup(func() { contractCallHook = previous })
	contractCallHook = func(ctx context.Context, method string) {
		if inLockedTx(ctx) {
			inside = append(inside, method)
			return
		}
		outside = append(outside, method)
	}

	call := func(ctx context.Context) {
		user := uuid.New()
		_, _ = s.projectsProject(ctx, 1)
		_, _ = s.projectsProjects(ctx, []int32{1})
		_, _ = s.projectsForUser(ctx, user)
		_, _ = s.projectsRole(ctx, 1, user)
		_, _ = s.projectsBillingLine(ctx, 1, 2)
		_, _ = s.projectsBillingLines(ctx, 1)
		_, _ = s.projectsCanLogTime(ctx, 1, user)
		_, _ = s.usersUser(ctx, user)
		_, _ = s.usersUsers(ctx, []uuid.UUID{user})
	}
	want := []string{
		"Projects.Project", "Projects.Projects", "Projects.ProjectsForUser", "Projects.Role",
		"Projects.BillingLine", "Projects.BillingLines", "Projects.CanLogTime",
		"Users.User", "Users.Users",
	}

	call(ctx)
	if !slices.Equal(outside, want) {
		t.Errorf("outside a transaction the hook saw %v, want %v", outside, want)
	}
	if len(inside) != 0 {
		t.Errorf("outside a transaction the hook saw %v as locked, want none", inside)
	}

	if err := s.withLockedTx(ctx, func(ctx context.Context, _ *store.Queries) error {
		call(ctx)
		return nil
	}); err != nil {
		t.Fatalf("withLockedTx: %v", err)
	}
	if !slices.Equal(inside, want) {
		t.Errorf("inside a locked transaction the hook saw %v, want every call reported: %v", inside, want)
	}

	// A batch of no names is not a call. The dashboard's attention read builds
	// its id list from an approval scope that is usually empty, so the common
	// caller would otherwise make a cross-module call on every load that can
	// only answer nothing.
	outside = nil
	if users, err := s.usersUsers(ctx, nil); err != nil || len(users) != 0 {
		t.Errorf("usersUsers(nil) = %v, %v, want no users and no error", users, err)
	}
	if len(outside) != 0 {
		t.Errorf("an empty batch of names reported %v, want no call at all", outside)
	}
}
