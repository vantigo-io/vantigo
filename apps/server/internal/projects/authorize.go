package projects

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is the whole of the per-project authorization decision (design
// §5). Handlers call authorize once and read the result; none of them
// consults a role or a global permission on its own, so there is exactly one
// place where "may this caller see this project" is answered.

// access is what one caller may do with one project. CanContribute is the
// write side of the project's *work* — tasks and what hangs off them — which
// a member has and a viewer has not; CanManage is the write side of the
// project itself.
type access struct {
	Role             string // "" when the caller holds none
	CanSee           bool
	CanSeeFinancials bool
	CanContribute    bool
	CanManage        bool
}

// globalAccess is what the caller's global permissions grant on every
// project. projects:access is not consulted here: the router already
// enforced it on every operation (D8), so reaching a handler means the
// caller holds it.
func (s *server) globalAccess(ctx context.Context) access {
	manageAll := contracts.HasPermission(ctx, s.deps.Access, "projects:manage-all")
	viewAll := contracts.HasPermission(ctx, s.deps.Access, "projects:view-all")
	financials := contracts.HasPermission(ctx, s.deps.Access, "projects:view-financials")
	return access{
		CanSee:           manageAll || viewAll,
		CanSeeFinancials: manageAll || financials,
		CanContribute:    manageAll,
		CanManage:        manageAll,
	}
}

// authorize resolves the caller's access to one project: global permissions
// widened by the caller's role there. view-financials applies only to
// projects the caller can see. The caller answers 404 when !CanSee (D7) and
// 403 when it needs CanManage and lacks it.
func (s *server) authorize(ctx context.Context, q *store.Queries, projectID int32) (access, error) {
	a := s.globalAccess(ctx)
	p, _ := contracts.PrincipalFrom(ctx)
	role, err := q.RoleForUser(ctx, store.RoleForUserParams{ProjectID: projectID, UserID: p.UserID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return access{}, fmt.Errorf("projects: load role: %w", err)
	}
	a = a.widenBy(role)
	if !a.CanSee {
		a.CanSeeFinancials = false
		a.CanContribute = false
	}
	return a, nil
}

// widenBy adds what role grants to a and records the role on it. It is a
// widening only: a capability a global permission already granted is never
// taken away by a narrower role. An unknown or empty role grants nothing,
// which is what makes "the caller holds no role" the zero case rather than a
// branch of its own.
func (a access) widenBy(role string) access {
	a.Role = role
	for _, c := range roleCapabilities[role] {
		switch c {
		case capSee:
			a.CanSee = true
		case capSeeFinancials:
			a.CanSeeFinancials = true
		case capContribute:
			a.CanContribute = true
		case capManage:
			a.CanManage = true
		}
	}
	return a
}
