package timetracking

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the whole of the per-entry authorization decision (D7, D8,
// D9, §6). Handlers build one caller per request and ask entryAccess about
// each entry; none of them consults a role or a permission on its own, so
// there is exactly one place where "may this caller see this entry, its
// money, and do what with it" is answered.

// caller is what one request knows about the person making it, read once:
// their global permissions, the period lock, and — on demand, cached — the
// role they hold on each project an entry is on. time:access is not here: the
// router already enforced it on every operation, so reaching a handler means
// the caller holds it.
type caller struct {
	UserID uuid.UUID

	ViewAll bool // time:view-all — everyone's entries, and their cost rates
	Approve bool // time:approve — approve any submitted entry
	Manage  bool // time:manage — past the lock, unapprove, cost rates

	ProjectsViewAll    bool // projects:view-all — sees every project
	ProjectsManageAll  bool // projects:manage-all — sees and manages every project, money included
	ProjectsFinancials bool // projects:view-financials — money on the projects they see

	LockedBefore *time.Time // D9; nil when no lock is set

	roles map[int32]string
}

// callerFor reads the request's caller: every permission this module decides
// on, and the lock date.
func (s *server) callerFor(ctx context.Context, q *store.Queries) (*caller, error) {
	lock, err := lockedBefore(ctx, q)
	if err != nil {
		return nil, err
	}
	return &caller{
		UserID:             callerID(ctx),
		ViewAll:            s.has(ctx, "time:view-all"),
		Approve:            s.has(ctx, "time:approve"),
		Manage:             s.has(ctx, "time:manage"),
		ProjectsViewAll:    s.has(ctx, "projects:view-all"),
		ProjectsManageAll:  s.has(ctx, "projects:manage-all"),
		ProjectsFinancials: s.has(ctx, "projects:view-financials"),
		LockedBefore:       lock,
		roles:              map[int32]string{},
	}, nil
}

// role is the caller's role on projectID through contracts.ProjectDirectory,
// "" for none, asked once per project per request.
func (c *caller) role(ctx context.Context, s *server, projectID int32) (string, error) {
	if role, ok := c.roles[projectID]; ok {
		return role, nil
	}
	role, err := s.deps.Projects.Role(ctx, projectID, c.UserID)
	if err != nil {
		return "", fmt.Errorf("time: look up the caller's project role: %w", err)
	}
	c.roles[projectID] = role
	return role, nil
}

// seesEveryone reports whether the caller sees every entry, whoever's and on
// whichever project: time:view-all to read them, time:approve to approve
// them and time:manage to unapprove them — none of the three can do its job
// on entries it cannot see.
func (c *caller) seesEveryone() bool {
	return c.ViewAll || c.Approve || c.Manage
}

// managedProjects is every project the caller holds the manager role on —
// the projects whose entries they see whoever logged them. It asks the
// directory for the caller's projects and then for the role on each through
// role, so the answer is cached beside the roles entryAccess reads and a list
// filtered on these ids can never disagree with the access its rows are
// rendered with.
func (c *caller) managedProjects(ctx context.Context, s *server) ([]int32, error) {
	projects, err := s.deps.Projects.ProjectsForUser(ctx, c.UserID)
	if err != nil {
		return nil, fmt.Errorf("time: list the caller's projects: %w", err)
	}
	managed := []int32{}
	for _, p := range projects {
		role, err := c.role(ctx, s, p.ID)
		if err != nil {
			return nil, err
		}
		if role == roleManager {
			managed = append(managed, p.ID)
		}
	}
	return managed, nil
}

// approvesAnything reports whether the caller approves on any project at
// all: time:approve, or the manager role on at least one (read through
// managedProjects, which also warms the role cache for those projects). With
// orManage, time:manage counts too — it unapproves anywhere. A caller who
// approves nothing is refused an approval operation outright, as the access
// layer would, rather than told about each id.
func (c *caller) approvesAnything(ctx context.Context, s *server, orManage bool) (bool, error) {
	if c.Approve || (orManage && c.Manage) {
		return true, nil
	}
	managed, err := c.managedProjects(ctx, s)
	if err != nil {
		return false, err
	}
	return len(managed) > 0, nil
}

// locked reports whether date falls before the period lock (D9). The lock
// date itself is open.
func (c *caller) locked(date time.Time) bool {
	return c.LockedBefore != nil && date.Before(*c.LockedBefore)
}

// entryAccess is what one caller may do with one entry.
//
// CanSee is visibility (§6): the owner sees their own entries, a manager of
// the entry's project sees every entry on it, and time:view-all,
// time:approve and time:manage see them all (seesEveryone); anyone else gets
// the bare 404 an unknown id gets. A project member sees only their own, and
// a project permission — projects:view-all, projects:view-financials,
// projects:manage-all — sees no one's time. The list applies the same rule
// in SQL (ListEntries: see_all, the caller's own, managedProjects), so it
// never shows an entry a read of it would answer 404 for.
//
// CanSeeBilling and CanSeeCost are D8's shaping of the money on an entry the
// caller sees: the bill rate to its owner and to whoever may see the
// project's financials (its managers, projects:manage-all, and
// projects:view-financials on a project the caller sees — the rule projects
// itself applies); the cost rate only to time:manage and time:view-all.
//
// The four capabilities are what the entry answers the caller with, so a
// client never re-derives them: content edits (and delete) are the owner's,
// in draft or rejected; submit is the owner's, in draft; approve (and
// reject) is an approver's (the project's manager or time:approve), in
// submitted; unapprove is an approver's or time:manage's, in approved. The
// lock (D9) holds back every one of them for everyone but time:manage —
// unapprove too, because an unapproved entry is a draft its owner could not
// change or submit again past the lock, and a locked period's approved hours
// are what gets invoiced. An invoiced entry allows nothing.
type entryAccess struct {
	IsOwner       bool
	CanSeeProject bool
	IsManager     bool
	IsApprover    bool

	CanSee        bool
	CanSeeBilling bool
	CanSeeCost    bool

	CanEdit      bool
	CanSubmit    bool
	CanApprove   bool
	CanUnapprove bool
}

// entryAccess resolves c's access to entry, asking the project directory for
// c's role on the entry's project unless it is cached. It must not run inside
// a locked transaction (withLockedTx); there, warm the roles first and use
// accessFor with cachedRole.
func (s *server) entryAccess(ctx context.Context, c *caller, entry store.TimeEntry) (entryAccess, error) {
	role, err := c.role(ctx, s, entry.ProjectID)
	if err != nil {
		return entryAccess{}, err
	}
	return c.accessFor(entry, role), nil
}

// warmRoles reads c's role on every one of projectIDs into the cache, so a
// decision made later inside a locked transaction needs no directory call.
func (c *caller) warmRoles(ctx context.Context, s *server, projectIDs []int32) error {
	for _, id := range projectIDs {
		if _, err := c.role(ctx, s, id); err != nil {
			return err
		}
	}
	return nil
}

// cachedRole is c's role on projectID as already read, and whether it was.
func (c *caller) cachedRole(projectID int32) (string, bool) {
	role, ok := c.roles[projectID]
	return role, ok
}

// accessFor is entryAccess given c's role on the entry's project: the whole
// decision, with nothing left to look up.
func (c *caller) accessFor(entry store.TimeEntry, role string) entryAccess {
	a := entryAccess{
		IsOwner:       entry.UserID == c.UserID,
		CanSeeProject: c.seesProject(role),
		IsManager:     role == roleManager,
	}
	a.IsApprover = a.IsManager || c.Approve
	a.CanSee = a.IsOwner || a.IsManager || c.seesEveryone()
	a.CanSeeBilling = a.IsOwner || c.seesProjectFinancials(role)
	a.CanSeeCost = c.Manage || c.ViewAll

	open := !c.locked(entry.EntryDate.Time) || c.Manage
	a.CanEdit = a.IsOwner && open && slices.Contains([]string{statusDraft, statusRejected}, entry.Status)
	a.CanSubmit = a.IsOwner && open && entry.Status == statusDraft
	a.CanApprove = a.IsApprover && open && entry.Status == statusSubmitted
	a.CanUnapprove = (a.IsApprover || c.Manage) && open && entry.Status == statusApproved
	return a
}

// seesProject reports whether c, holding role on a project ("" for none),
// sees the project itself: any role on it, projects:view-all or
// projects:manage-all — the rule projects applies. The time permissions see
// entries, not projects.
func (c *caller) seesProject(role string) bool {
	return role != "" || c.ProjectsViewAll || c.ProjectsManageAll
}

// seesProjectFinancials reports whether c, holding role on a project, may see
// the money on it (D8): its managers, projects:manage-all, and
// projects:view-financials on a project c sees.
func (c *caller) seesProjectFinancials(role string) bool {
	return role == roleManager || c.ProjectsManageAll || (c.ProjectsFinancials && c.seesProject(role))
}

// lockedBefore reads the period lock (D9), nil when none is set.
func lockedBefore(ctx context.Context, q *store.Queries) (*time.Time, error) {
	value, err := q.GetSetting(ctx, settingLockedBefore)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("time: read the lock date: %w", err)
	}
	lock, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return nil, fmt.Errorf("time: the stored lock date %q is not a date: %w", value, err)
	}
	return &lock, nil
}
