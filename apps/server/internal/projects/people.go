package projects

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is the people half of a project (design §5, D6, D11): who holds a
// role on it, who may change that, and who is left to put on it. The roles
// themselves — what each one lets its holder do — stay in roles.go; nothing
// here decides what a role means.
//
// Every one of these operations names users through contracts.UserDirectory,
// never by reading identity's tables: a project role is a row holding a user
// id, and everything else about that user is borrowed for the answer. That is
// also why a role survives the account it points at — a user the directory no
// longer knows lists as unknownUser and inactive rather than vanishing.

// assignableUserLimit is how many candidates the assignable-user search
// answers. It is a picker's first page, not a report: a manager who cannot
// find a colleague in twenty rows types more of their name.
const assignableUserLimit = 20

// userNotFound and userDisabled are the two ways the `userId` an assignment
// names can fail. Both are ordinary field errors rather than a 404: the
// project exists and the caller may manage it, so what is wrong is the body
// they sent, not the resource they addressed.
func userNotFound(id uuid.UUID) string {
	return fmt.Sprintf("User %s does not exist", id)
}

func userDisabled(id uuid.UUID) string {
	return fmt.Sprintf("User %s is disabled and cannot be given a project role", id)
}

// userEntries names a project's people in one directory call. An id the
// directory does not know still gets an entry — unknownUser, inactive — so
// every assignment has something to render and the caller never has to
// distinguish "missing from the map" from "unnamed".
func (s *server) userEntries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]contracts.UserEntry, error) {
	entries := make(map[uuid.UUID]contracts.UserEntry, len(ids))
	if len(ids) == 0 {
		return entries, nil
	}
	found, err := s.deps.Users.Users(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("projects: resolve the project's people: %w", err)
	}
	for _, e := range found {
		entries[e.ID] = e
	}
	for _, id := range ids {
		if _, ok := entries[id]; !ok {
			entries[id] = contracts.UserEntry{ID: id, DisplayName: unknownUser}
		}
	}
	return entries, nil
}

// subjectOf names the user an assignment is about, the way callerAs names the
// caller who is making it: for the timeline, which stores the name rather than
// resolving it on read. The directory's entry comes back beside the actor,
// because whether that user can still act is what decides if a *new* role may
// be given to them at all.
func (s *server) subjectOf(ctx context.Context, userID uuid.UUID) (actor, *contracts.UserEntry, error) {
	entry, err := s.deps.Users.User(ctx, userID)
	if err != nil {
		return actor{}, nil, fmt.Errorf("projects: resolve the assignment's user: %w", err)
	}
	subject := actor{UserID: userID, Display: unknownUser}
	if entry != nil {
		subject.Display = entry.DisplayName
	}
	return subject, entry, nil
}

// GetProjectsByIdRoles List a project's people
// (GET /api/v1/projects/{id}/roles)
//
// Anyone who sees the project sees who is on it (design §7): knowing who to
// ask about a project is not privileged information, and a member who could
// not see the managers would have nobody to ask for the access they lack.
//
// The order is managers first and then by display name, and it is applied in
// Go: the names come from identity's schema, which this module may not join
// against, so SQL cannot sort by them.
func (s *server) GetProjectsByIdRoles(ctx context.Context, req gen.GetProjectsByIdRolesRequestObject) (gen.GetProjectsByIdRolesResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdRoles404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, row.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdRoles404Response{}, nil
	}

	rows, err := q.ListProjectRoles(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list project roles: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	entries, err := s.userEntries(ctx, ids)
	if err != nil {
		return nil, err
	}

	data := make([]gen.ProjectRoleResponse, 0, len(rows))
	for _, r := range rows {
		entry := entries[r.UserID]
		data = append(data, gen.ProjectRoleResponse{
			UserId:      r.UserID,
			DisplayName: entry.DisplayName,
			Active:      entry.Active,
			Role:        r.Role,
			CreatedAt:   r.CreatedAt,
		})
	}
	slices.SortStableFunc(data, func(x, y gen.ProjectRoleResponse) int {
		if c := cmp.Compare(manageRank(x.Role), manageRank(y.Role)); c != 0 {
			return c
		}
		return strings.Compare(x.DisplayName, y.DisplayName)
	})
	return gen.GetProjectsByIdRoles200JSONResponse(data), nil
}

// PutProjectsByIdRolesByUserId Add or change a user's role on a project
// (PUT /api/v1/projects/{id}/roles/{userId})
//
// One operation adds and changes, because from the caller's side there is one
// intention — "this person's role here is X" — and making them know which of
// the two it is before asking would only mean reading the list first. The
// timeline still tells them apart: role-added and role-changed are different
// events, and the role somebody already holds is neither, so re-sending it
// answers the assignment and writes nothing.
//
// The user must resolve and be active to be *added*; changing or removing the
// role of an account disabled afterwards stays possible, or a project could
// never be tidied up after somebody left.
func (s *server) PutProjectsByIdRolesByUserId(ctx context.Context, req gen.PutProjectsByIdRolesByUserIdRequestObject) (gen.PutProjectsByIdRolesByUserIdResponseObject, error) {
	body := gen.ProjectRoleAssignmentRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	row, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutProjectsByIdRolesByUserId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, row.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.PutProjectsByIdRolesByUserId404Response{}, nil
	}
	if !a.CanManage {
		return gen.PutProjectsByIdRolesByUserId403JSONResponse(forbidden()), nil
	}

	// The body is validated after the caller's access to the project, so a
	// stranger cannot learn a project exists by sending it a bad role.
	role, msg := validateProjectRole(body.Role)
	if msg != "" {
		return gen.PutProjectsByIdRolesByUserId400ApplicationProblemPlusJSONResponse(
			invalidProject(fieldError("role", msg))), nil
	}

	held, err := q.GetProjectRole(ctx, store.GetProjectRoleParams{ProjectID: row.ID, UserID: req.UserId})
	adding := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !adding {
		return nil, fmt.Errorf("projects: get project role: %w", err)
	}

	subject, entry, err := s.subjectOf(ctx, req.UserId)
	if err != nil {
		return nil, err
	}
	if adding {
		switch {
		case entry == nil:
			return gen.PutProjectsByIdRolesByUserId400ApplicationProblemPlusJSONResponse(
				invalidProject(fieldError("userId", userNotFound(req.UserId)))), nil
		case !entry.Active:
			return gen.PutProjectsByIdRolesByUserId400ApplicationProblemPlusJSONResponse(
				invalidProject(fieldError("userId", userDisabled(req.UserId)))), nil
		}
	}
	active := entry != nil && entry.Active

	if !adding && held.Role == role {
		return gen.PutProjectsByIdRolesByUserId200JSONResponse(gen.ProjectRoleResponse{
			UserId:      held.UserID,
			DisplayName: subject.Display,
			Active:      active,
			Role:        held.Role,
			CreatedAt:   held.CreatedAt,
		}), nil
	}

	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var assigned store.UpsertProjectRoleRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		assigned, err = txq.UpsertProjectRole(ctx, store.UpsertProjectRoleParams{
			ProjectID: row.ID, UserID: req.UserId, Role: role, Now: now,
		})
		if err != nil {
			return err
		}
		if adding {
			return recordRoleAdded(ctx, txq, now, row.ID, subject, role, by)
		}
		return recordRoleChanged(ctx, txq, now, row.ID, subject, held.Role, role, by)
	})
	if err != nil {
		return nil, fmt.Errorf("projects: assign a project role: %w", err)
	}

	return gen.PutProjectsByIdRolesByUserId200JSONResponse(gen.ProjectRoleResponse{
		UserId:      assigned.UserID,
		DisplayName: subject.Display,
		Active:      active,
		Role:        assigned.Role,
		CreatedAt:   assigned.CreatedAt,
	}), nil
}

// DeleteProjectsByIdRolesByUserId Remove a user's role on a project
// (DELETE /api/v1/projects/{id}/roles/{userId})
//
// A manager may remove themself, the last one included (D6): there is no
// last-manager rule, because projects:manage-all can always step in and
// enforcing one gets awkward the moment an account is disabled. What that
// costs is that a manager who steps off a project they hold no global
// permission over stops seeing it — which is exactly what removing their own
// role means.
func (s *server) DeleteProjectsByIdRolesByUserId(ctx context.Context, req gen.DeleteProjectsByIdRolesByUserIdRequestObject) (gen.DeleteProjectsByIdRolesByUserIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProjectsByIdRolesByUserId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, row.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.DeleteProjectsByIdRolesByUserId404Response{}, nil
	}
	if !a.CanManage {
		return gen.DeleteProjectsByIdRolesByUserId403JSONResponse(forbidden()), nil
	}

	held, err := q.GetProjectRole(ctx, store.GetProjectRoleParams{ProjectID: row.ID, UserID: req.UserId})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteProjectsByIdRolesByUserId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project role: %w", err)
	}

	subject, _, err := s.subjectOf(ctx, req.UserId)
	if err != nil {
		return nil, err
	}
	by, err := s.callerAs(ctx)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.DeleteProjectRole(ctx, store.DeleteProjectRoleParams{ProjectID: row.ID, UserID: req.UserId}); err != nil {
			return err
		}
		return recordRoleRemoved(ctx, txq, now, row.ID, subject, held.Role, by)
	})
	if err != nil {
		return nil, fmt.Errorf("projects: remove a project role: %w", err)
	}
	return gen.DeleteProjectsByIdRolesByUserId204Response{}, nil
}

// GetProjectsByIdAssignableUsers Search users assignable to a project
// (GET /api/v1/projects/{id}/assignable-users)
//
// D11's reason for contracts.UserDirectory existing: the only user listing
// identity offers needs the authorization-management policy, so without this
// a project manager who is not an administrator could not pick a colleague.
// It is the manager's search — the people list is readable by everyone who
// sees the project, but who *could* be added is only useful to whoever may
// add them.
//
// The directory is asked for the page plus as many rows as this project has
// assigned, and the assigned ones are dropped afterwards: filtering in Go
// keeps the exclusion out of identity's contract, which has no business
// knowing what a project is. The directory clamps its own limit, so a project
// with an unusually large team can answer a short page; typing more of a name
// is the way out, and it is the same way out an over-full search has.
func (s *server) GetProjectsByIdAssignableUsers(ctx context.Context, req gen.GetProjectsByIdAssignableUsersRequestObject) (gen.GetProjectsByIdAssignableUsersResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdAssignableUsers404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, row.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdAssignableUsers404Response{}, nil
	}
	if !a.CanManage {
		return gen.GetProjectsByIdAssignableUsers403JSONResponse(forbidden()), nil
	}

	assigned, err := q.ListProjectRoles(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("projects: list project roles: %w", err)
	}
	taken := make(map[uuid.UUID]bool, len(assigned))
	for _, r := range assigned {
		taken[r.UserID] = true
	}

	search := ""
	if req.Params.Search != nil {
		search = strings.TrimSpace(*req.Params.Search)
	}
	// SearchUsers answers active users only, so a disabled account is never a
	// candidate; nothing here has to filter for that.
	found, err := s.deps.Users.SearchUsers(ctx, search, assignableUserLimit+len(taken))
	if err != nil {
		return nil, fmt.Errorf("projects: search assignable users: %w", err)
	}

	data := make([]gen.ProjectPersonSummary, 0, assignableUserLimit)
	for _, e := range found {
		if taken[e.ID] {
			continue
		}
		data = append(data, gen.ProjectPersonSummary{UserId: e.ID, DisplayName: e.DisplayName})
		if len(data) == assignableUserLimit {
			break
		}
	}
	return gen.GetProjectsByIdAssignableUsers200JSONResponse(data), nil
}
