package expenses

import (
	"context"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the whole of this module's reach into its neighbours. Every call
// into contracts.ProjectDirectory (deps.Projects) and contracts.UserDirectory
// (deps.Users) is made through one of the thin accessors below and through
// nowhere else, which is what makes "what does Expenses ask of another module,
// and when" a question with one place to read the answer — and one place to
// check it from.
//
// What is checked is the rule of Global Constraints and design decision X10: a
// cross-module call is always made *before* a transaction takes its locks,
// never from inside one holding them (withLockedTx). These are in-process calls
// into another module that read through the same connection pool, so a
// transaction that holds row locks and then waits for one of them stalls every
// other writer, and under enough load starves the pool outright — every
// connection held by a transaction waiting for one more. noteContractCall pins
// the rule for the whole module, on every path, at a production cost of one nil
// comparison per call: nothing here changes what any of these calls do or when
// they are made.

// contractCallHook is handed the context and the name of every cross-module
// call this module makes. It is nil in production and installed once, before
// any test runs, by this package's own tests (SetContractCallHook in
// export_test.go); the harness's hook fails the test when the context it is
// given is one withLockedTx marked.
var contractCallHook func(ctx context.Context, method string)

// noteContractCall reports one call into another module's contract.
func noteContractCall(ctx context.Context, method string) {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
}

// projectsAvailable reports whether this installation has the projects module
// enabled, and so whether an expense can carry a project, a billing line, a
// billable flag or a markup at all (decisions X1 and X2). deps.Projects is the
// optional contract: nil when projects is disabled. It is answered on GET /meta
// so the frontend never has to ask separately, and every accessor below is
// reached only after a caller has checked it.
func (s *server) projectsAvailable() bool { return s.deps.Projects != nil }

// projectsProject, projectsProjects, projectsForUser, projectsRole,
// projectsBillingLine, projectsBillingLines and projectsCanLogTime are the
// project directory. Every caller has already answered for an installation
// without one (projectsAvailable), so none of these is reached with a nil
// directory.
func (s *server) projectsProject(ctx context.Context, id int32) (*contracts.ProjectEntry, error) {
	noteContractCall(ctx, "Projects.Project")
	return s.deps.Projects.Project(ctx, id)
}

func (s *server) projectsProjects(ctx context.Context, ids []int32) ([]contracts.ProjectEntry, error) {
	noteContractCall(ctx, "Projects.Projects")
	return s.deps.Projects.Projects(ctx, ids)
}

func (s *server) projectsForUser(ctx context.Context, userID uuid.UUID) ([]contracts.ProjectEntry, error) {
	noteContractCall(ctx, "Projects.ProjectsForUser")
	return s.deps.Projects.ProjectsForUser(ctx, userID)
}

func (s *server) projectsRole(ctx context.Context, projectID int32, userID uuid.UUID) (string, error) {
	noteContractCall(ctx, "Projects.Role")
	return s.deps.Projects.Role(ctx, projectID, userID)
}

func (s *server) projectsBillingLine(ctx context.Context, projectID, lineID int32) (*contracts.BillingLineEntry, error) {
	noteContractCall(ctx, "Projects.BillingLine")
	return s.deps.Projects.BillingLine(ctx, projectID, lineID)
}

func (s *server) projectsBillingLines(ctx context.Context, projectID int32) ([]contracts.BillingLineEntry, error) {
	noteContractCall(ctx, "Projects.BillingLines")
	return s.deps.Projects.BillingLines(ctx, projectID)
}

// projectsCanLogTime is projects' own rule for whether a person may book work
// on a project, which is exactly what booking an expense on one needs
// (decision X9).
func (s *server) projectsCanLogTime(ctx context.Context, projectID int32, userID uuid.UUID) (bool, error) {
	noteContractCall(ctx, "Projects.CanLogTime")
	return s.deps.Projects.CanLogTime(ctx, projectID, userID)
}

// usersUser and usersUsers are identity's user directory, the only way this
// module may read a user. It is always composed.
func (s *server) usersUser(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	noteContractCall(ctx, "Users.User")
	return s.deps.Users.User(ctx, id)
}

func (s *server) usersUsers(ctx context.Context, ids []uuid.UUID) ([]contracts.UserEntry, error) {
	noteContractCall(ctx, "Users.Users")
	return s.deps.Users.Users(ctx, ids)
}
