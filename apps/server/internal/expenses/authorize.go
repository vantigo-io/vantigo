package expenses

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is the whole of the per-expense authorization decision (design §5
// and decision X10). Handlers build one caller per request and ask entryAccess
// about each expense; none of them consults a role or a permission on its own,
// so there is exactly one place where "may this caller see this expense, its
// billing, and do what with it" is answered.

// caller is what one request knows about the person making it, read once:
// their global permissions, the installation's settings — the period lock, the
// default currency and markup a new expense starts from — and, on demand and
// cached, the role they hold on each project an expense is on.
// expenses:access is not here: the router already enforced it on every
// operation, so reaching a handler means the caller holds it.
type caller struct {
	UserID uuid.UUID

	ViewAll bool // expenses:view-all — everyone's expenses
	Approve bool // expenses:approve — approve any submitted expense
	Manage  bool // expenses:manage — past the lock, for a colleague, anyone's draft

	ProjectsViewAll    bool // projects:view-all — sees every project
	ProjectsManageAll  bool // projects:manage-all — sees and manages every project, money included
	ProjectsFinancials bool // projects:view-financials — money on the projects they see

	Settings store.ExpensesSetting

	roles map[int32]string
}

// callerFor reads the request's caller: every permission this module decides
// on, and the installation's settings.
func (s *server) callerFor(ctx context.Context, q *store.Queries) (*caller, error) {
	row, err := settings(ctx, q)
	if err != nil {
		return nil, err
	}
	return &caller{
		UserID:             callerID(ctx),
		ViewAll:            s.has(ctx, "expenses:view-all"),
		Approve:            s.has(ctx, "expenses:approve"),
		Manage:             s.has(ctx, "expenses:manage"),
		ProjectsViewAll:    s.has(ctx, "projects:view-all"),
		ProjectsManageAll:  s.has(ctx, "projects:manage-all"),
		ProjectsFinancials: s.has(ctx, "projects:view-financials"),
		Settings:           row,
		roles:              map[int32]string{},
	}, nil
}

// lockedBefore is the period lock (design §4), nil when none is set.
func lockedBefore(row store.ExpensesSetting) *time.Time {
	if !row.LockedBefore.Valid {
		return nil
	}
	lock := row.LockedBefore.Time
	return &lock
}

// locked reports whether date falls before the period lock. The lock date
// itself is open.
func (c *caller) locked(date time.Time) bool {
	lock := lockedBefore(c.Settings)
	return lock != nil && date.Before(*lock)
}

// mayWritePast reports whether c may record, change or delete something dated
// date — the period lock, which only expenses:manage works past.
func (c *caller) mayWritePast(date time.Time) bool { return !c.locked(date) || c.Manage }

// role is the caller's role on projectID through contracts.ProjectDirectory,
// "" for none, asked once per project per request. An installation without the
// projects module holds no roles at all, so it answers "" without asking.
func (c *caller) role(ctx context.Context, s *server, projectID int32) (string, error) {
	if !s.projectsAvailable() {
		return "", nil
	}
	if role, ok := c.roles[projectID]; ok {
		return role, nil
	}
	role, err := s.projectsRole(ctx, projectID, c.UserID)
	if err != nil {
		return "", fmt.Errorf("expenses: look up the caller's project role: %w", err)
	}
	c.roles[projectID] = role
	return role, nil
}

// roleOf is role for an expense, which may be on no project at all.
func (c *caller) roleOf(ctx context.Context, s *server, projectID *int32) (string, error) {
	if projectID == nil {
		return "", nil
	}
	return c.role(ctx, s, *projectID)
}

// seesEveryone reports whether the caller sees every expense, whoever's and on
// whichever project: expenses:view-all to read them, expenses:approve to
// approve them and expenses:manage to record, change and reimburse them —
// none of the three can do its job on expenses it cannot see.
func (c *caller) seesEveryone() bool { return c.ViewAll || c.Approve || c.Manage }

// managedProjects is every project the caller holds the manager role on — the
// projects whose expenses they see whoever recorded them. It asks the
// directory for the caller's projects and then for the role on each through
// role, so the answer is cached beside the roles entryAccess reads and a list
// filtered on these ids can never disagree with the access its rows are
// rendered with. Without the projects module there are none.
func (c *caller) managedProjects(ctx context.Context, s *server) ([]int32, error) {
	managed := []int32{}
	if !s.projectsAvailable() {
		return managed, nil
	}
	projects, err := s.projectsForUser(ctx, c.UserID)
	if err != nil {
		return nil, fmt.Errorf("expenses: list the caller's projects: %w", err)
	}
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

// seesProject reports whether c, holding role on a project ("" for none), sees
// the project itself: any role on it, projects:view-all or
// projects:manage-all — the rule projects applies. The expenses permissions
// see expenses, not projects.
func (c *caller) seesProject(role string) bool {
	return role != "" || c.ProjectsViewAll || c.ProjectsManageAll
}

// seesProjectFinancials reports whether c, holding role on a project, may see
// the money the project makes on it (design §5): its managers,
// projects:manage-all, and projects:view-financials on a project c sees. It is
// deliberately not the owner: what an employee is owed is theirs to see, what
// the company charges its customer for it is the project's.
func (c *caller) seesProjectFinancials(role string) bool {
	return role == roleManager || c.ProjectsManageAll || (c.ProjectsFinancials && c.seesProject(role))
}

// roleManager is the role name projects gives a project's manager. It is a
// string this module reads and never writes; projects owns the vocabulary.
const roleManager = "manager"

// entryAccess is what one caller may do with one expense.
//
// CanSee is design §5's visibility: its owner, a manager of its project, and
// expenses:view-all, expenses:approve and expenses:manage; anyone else gets
// the bare 404 an unknown id gets. The list applies the same rule in SQL
// (CountEntries and ListEntries), so it never holds an expense a read of it
// would answer 404 for.
//
// CanSeeBilling is the shaping of the project's money on an expense the caller
// sees: the markup, the customer rate and what is billed go to financial
// rights on the expense's project and to nobody else — not to the owner as
// such, who always sees their own gross, VAT and what they are owed.
//
// The seven capabilities are what the expense answers the caller with, so a
// client never re-derives them. The period lock holds every one of them back
// for everyone but expenses:manage.
type entryAccess struct {
	IsOwner    bool
	IsManager  bool
	IsApprover bool

	CanSee        bool
	CanSeeBilling bool

	CanEdit         bool
	CanDelete       bool
	CanSubmit       bool
	CanApprove      bool
	CanOverrideRate bool
	CanMarkInvoiced bool
}

// entryAccess resolves c's access to entry, asking the project directory for
// c's role on the expense's project unless it is cached. It must not run
// inside a locked transaction (withLockedTx); there, read the role first and
// use accessFor.
func (s *server) entryAccess(ctx context.Context, c *caller, entry store.ExpensesEntry) (entryAccess, error) {
	role, err := c.roleOf(ctx, s, entry.ProjectID)
	if err != nil {
		return entryAccess{}, err
	}
	return c.accessFor(entry, role), nil
}

// editableStatuses are the statuses an expense is still its owner's to change
// in (decision X4): a fresh draft, and one a manager sent back.
var editableStatuses = []string{statusDraft, statusRejected}

// accessFor is entryAccess given c's role on the expense's project: the whole
// decision, with nothing left to look up.
func (c *caller) accessFor(entry store.ExpensesEntry, role string) entryAccess {
	a := entryAccess{
		IsOwner:   entry.UserID == c.UserID,
		IsManager: role == roleManager,
	}
	a.IsApprover = a.IsManager || c.Approve
	a.CanSee = a.IsOwner || a.IsManager || c.seesEveryone()
	a.CanSeeBilling = entry.ProjectID != nil && c.seesProjectFinancials(role)

	open := c.mayWritePast(entry.EntryDate.Time)
	writer := a.IsOwner || c.Manage
	a.CanEdit = writer && open && slices.Contains(editableStatuses, entry.Status)
	a.CanDelete = a.CanEdit
	a.CanSubmit = writer && open && entry.Status == statusDraft
	a.CanApprove = a.IsApprover && open && entry.Status == statusSubmitted
	a.CanOverrideRate = (a.IsApprover || c.Manage) && open && entry.Status == statusSubmitted && entry.Kind == kindMileage
	a.CanMarkInvoiced = entry.Billable && entry.Status == statusApproved && entry.InvoicedAt == nil &&
		c.seesProjectFinancials(role)
	return a
}
