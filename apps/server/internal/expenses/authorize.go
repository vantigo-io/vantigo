package expenses

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
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

	// ProjectsOn is whether this installation has the projects module at all
	// (decision X2). It is read once here rather than asked of the server by
	// every rule, so accessFor — which has no server — can answer for the
	// operations that only exist when projects do.
	ProjectsOn bool

	Settings store.ExpensesSetting

	// loc is Settings.TimeZone parsed once (see zone).
	loc *time.Location

	roles map[int32]string
}

// callerFor reads the request's caller: every permission this module decides
// on, and the installation's settings.
//
// The period lock is read here, once, before any transaction, and every rule
// that consults it during the request uses that one reading. A lock an
// administrator moves between this read and the commit is therefore not
// re-checked under the row lock — an accepted race: the lock is an
// administrator's statement about a period, changed rarely and never
// concurrently with the save it would have caught, and re-reading it inside
// the transaction would put a second read under the row lock for nothing.
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
		ProjectsOn:         s.projectsAvailable(),
		Settings:           row,
		roles:              map[int32]string{},
	}, nil
}

// zone is the installation's business time zone — the one every date derived
// from a travel claim's two instants is taken in (businessDay). It is parsed
// from the settings row the caller was built with, so one request parses it
// once and every rule in that request agrees.
func (c *caller) zone() *time.Location {
	if c.loc == nil {
		c.loc = zoneOf(c.Settings)
	}
	return c.loc
}

// zoneOf parses a settings row's business time zone.
//
// A name Go cannot load falls back to UTC rather than failing the request: the
// settings door refuses a name Go cannot load, one Postgres does not know, and
// one the two read differently, so a row that holds such a name has been
// written past this module, and answering "a day, in UTC" is better than
// answering nothing at all.
//
// What that fallback costs is worth naming, because it is the one state in
// which this module's central promise does not hold: SQL keeps using the stored
// name, so every date Go derives would be a UTC day and every date a query
// derives would be a day in whatever the database makes of the name. It cannot
// arise from the default or from any value PUT /settings accepts; it would take
// a row written by hand, or a Go release that dropped a zone the database kept.
// The way back is to set the zone again through PUT /settings, which refuses
// anything the two halves do not agree on.
func zoneOf(row store.ExpensesSetting) *time.Location {
	loc, err := time.LoadLocation(row.TimeZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// businessZone is the installation's own time zone for a path that needs
// "today" — the payroll run's not-in-the-future rule, the export's file name —
// without having built a whole caller for it.
func (s *server) businessZone(ctx context.Context, q *store.Queries) (*time.Location, error) {
	row, err := settings(ctx, q)
	if err != nil {
		return nil, err
	}
	return zoneOf(row), nil
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
//
// It is one Role call per project the caller holds any role on — known, and
// the same N+1 time's managedProjects makes. Its callers ask only when they
// have to (a caller who already sees every expense does not), so the common
// list costs nothing; what would remove it is a role-returning directory call
// (ProjectsForUser answering the role, or a ManagedProjectsFor), which is
// worth adding the day delivery C widens contracts.ProjectDirectory anyway and
// is not worth widening it for on its own.
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

// warmRoles reads c's role on every one of projectIDs into the cache, so a
// decision made later inside a locked transaction needs no directory call. nil
// ids — expenses on no project at all — are skipped.
func (c *caller) warmRoles(ctx context.Context, s *server, projectIDs []*int32) error {
	for _, id := range projectIDs {
		if _, err := c.roleOf(ctx, s, id); err != nil {
			return err
		}
	}
	return nil
}

// cachedRole is c's role on an expense's project as already read, "" when it
// was not. It is what a decision inside a locked transaction reads instead of
// asking the directory: a project the cache has no role for — only possible
// for an expense moved to another project between the read and the lock, which
// only a draft can be — counts as none, which can only refuse, never admit.
func (c *caller) cachedRole(projectID *int32) string {
	if projectID == nil {
		return ""
	}
	return c.roles[*projectID]
}

// approvesAnything reports whether the caller approves anything at all:
// expenses:approve, or the manager role on at least one project (read through
// managedProjects, which also warms the role cache for those projects). With
// orManage, expenses:manage counts too — it unapproves anywhere. A caller who
// approves nothing is refused a decision outright, as the access layer would,
// rather than told about each id (decision X10).
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

// approvalScope is which submitted expenses a caller may approve, in the terms
// the queue's queries filter on: every one of them (seeAll, expenses:approve)
// or the ones on the projects they manage, and not those dated before lock
// (invalid for no lock, or for expenses:manage, whom the lock does not hold
// back).
type approvalScope struct {
	seeAll  bool
	managed []int32
	lock    pgtype.Date
}

// approvesAny reports whether the scope holds anything at all.
func (a approvalScope) approvesAny() bool { return a.seeAll || len(a.managed) > 0 }

// approvalScopeFor reads c's approval scope; for a caller without
// expenses:approve that is a directory call per project they hold a role on
// (managedProjects), so it must not run inside a locked transaction.
func (s *server) approvalScopeFor(ctx context.Context, c *caller) (approvalScope, error) {
	scope := approvalScope{seeAll: c.Approve, managed: []int32{}}
	if !c.Approve {
		managed, err := c.managedProjects(ctx, s)
		if err != nil {
			return approvalScope{}, err
		}
		scope.managed = managed
	}
	if lock := lockedBefore(c.Settings); lock != nil && !c.Manage {
		scope.lock = pgDate(*lock)
	}
	return scope, nil
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

// projectFinancials asks the directory the one question two reads of this
// module are gated on — may this caller see what a project makes? — and hands
// back the project with the answer, because both callers need it: the summary
// publishes its currency, and having asked once nothing should ask again.
//
// It is the whole of the rule, including the two ways it can be moot: an
// installation with no projects module, and a project the directory no longer
// knows. Both answer "no", which is what lets a caller refuse all three causes
// with one indistinguishable answer rather than telling an outsider which
// project ids exist.
//
// Every call it makes goes through contractscalls.go and happens before any
// query and outside any transaction, which is this module's standing rule.
func (s *server) projectFinancials(ctx context.Context, c *caller, projectID int32) (*contracts.ProjectEntry, bool, error) {
	if !s.projectsAvailable() {
		return nil, false, nil
	}
	project, err := s.projectsProject(ctx, projectID)
	if err != nil {
		return nil, false, fmt.Errorf("expenses: look up the project: %w", err)
	}
	if project == nil {
		return nil, false, nil
	}
	role, err := c.role(ctx, s, projectID)
	if err != nil {
		return nil, false, err
	}
	return project, c.seesProjectFinancials(role), nil
}

// entryUnit is **the unit an expense belongs to**: the row whose status, whose
// date and whose decision the expense is judged and rendered by. A standalone
// expense is its own unit; a line inside a travel claim has the *claim* as its
// unit (Global Constraints — "a claim's line is an entry with claim_id").
//
// It exists so that no rule anywhere can forget. A line's own status column
// stays at its default and is never read: the flow, the period lock, the
// capabilities, whether its receipts may still be changed and what a read of
// it shows are all answered from here, and unitOf below is the single place
// that decides which row that is.
type entryUnit struct {
	// ClaimID is the claim this unit is, nil when the unit is the expense
	// itself. Every refusal that is about the claim reports on it.
	ClaimID *int64

	UserID    uuid.UUID
	ProjectID *int32
	Status    string

	// Date is the day the period lock is judged on: the expense's own entry
	// date, or the day its claim departed.
	Date time.Time

	SubmittedAt     *time.Time
	DecidedAt       *time.Time
	DecidedByUserID *uuid.UUID
	RejectionReason *string

	ReimbursedAt           *time.Time
	ReimbursedByUserID     *uuid.UUID
	ReimbursementReference *string
	ReimbursementDate      pgtype.Date
}

// unitOf is that single place. claim is the row entry.ClaimID names, which the
// foreign key guarantees exists — every loader in this module reads it before
// asking.
//
// A line whose claim was not loaded is given a unit with no status at all,
// which is in none of the sets the rules test (editable, submitted, approved):
// it fails closed, so a missing read can only ever refuse, never admit.
func unitOf(entry store.ExpensesEntry, claim *store.ExpensesClaim, loc *time.Location) entryUnit {
	if entry.ClaimID == nil {
		return entryUnit{
			UserID:                 entry.UserID,
			ProjectID:              entry.ProjectID,
			Status:                 entry.Status,
			Date:                   entry.EntryDate.Time,
			SubmittedAt:            entry.SubmittedAt,
			DecidedAt:              entry.DecidedAt,
			DecidedByUserID:        entry.DecidedByUserID,
			RejectionReason:        entry.RejectionReason,
			ReimbursedAt:           entry.ReimbursedAt,
			ReimbursedByUserID:     entry.ReimbursedByUserID,
			ReimbursementReference: entry.ReimbursementReference,
			ReimbursementDate:      entry.ReimbursementDate,
		}
	}
	if claim == nil {
		return entryUnit{ClaimID: entry.ClaimID, UserID: entry.UserID, ProjectID: entry.ProjectID}
	}
	return claimUnit(*claim, loc)
}

// claimUnit is a travel claim as the unit of its own lines — and of itself,
// which is how one set of rules serves both.
func claimUnit(claim store.ExpensesClaim, loc *time.Location) entryUnit {
	return entryUnit{
		ClaimID:                &claim.ID,
		UserID:                 claim.UserID,
		ProjectID:              claim.ProjectID,
		Status:                 claim.Status,
		Date:                   businessDay(claim.DepartureAt, loc),
		SubmittedAt:            claim.SubmittedAt,
		DecidedAt:              claim.DecidedAt,
		DecidedByUserID:        claim.DecidedByUserID,
		RejectionReason:        claim.RejectionReason,
		ReimbursedAt:           claim.ReimbursedAt,
		ReimbursedByUserID:     claim.ReimbursedByUserID,
		ReimbursementReference: claim.ReimbursementReference,
		ReimbursementDate:      claim.ReimbursementDate,
	}
}

// isClaimLine reports whether the unit is a travel claim rather than the
// expense itself.
func (u entryUnit) isClaimLine() bool { return u.ClaimID != nil }

// editable reports whether the unit is still its owner's to change.
func (u entryUnit) editable() bool { return slices.Contains(editableStatuses, u.Status) }

// unitFor loads one expense's unit: itself, or the travel claim it is a line
// of. It reads this module's own table only, so it is safe anywhere — but it
// is a second query, and a page of expenses resolves its claims in bulk
// instead (entryNames).
func (s *server) unitFor(ctx context.Context, q *store.Queries, c *caller, entry store.ExpensesEntry) (entryUnit, error) {
	if entry.ClaimID == nil {
		return unitOf(entry, nil, c.zone()), nil
	}
	claim, err := q.GetClaim(ctx, *entry.ClaimID)
	if err != nil {
		return entryUnit{}, fmt.Errorf("expenses: read an expense's travel claim: %w", err)
	}
	return unitOf(entry, &claim, c.zone()), nil
}

// entryStateRefusal is why an expense cannot be changed right now — because of
// what its *unit* is, not who is asking: it is dated inside a closed period, or
// it has moved past the point where it is still editable. It answers the field
// the reason belongs to and the message, or "" and "" when nothing refuses.
//
// This is the module's one rule for the two codes (and attachments.go's
// refusals go through it as well). A caller who is not the owner and does not
// hold expenses:manage is refused for who they are: a 403, or the bare 404 an
// unknown id gets when they cannot even see the expense. A caller who *would*
// be allowed, and is stopped by the expense's own state, is told what state —
// a 400 naming the lock date or the status — because that is a fact about the
// expense they can act on, and a bare 403 would leave them guessing.
//
// For a line inside a travel claim it is the *claim* that refuses, and the
// refusal says so and reports on claimId: its status and the day it departed
// are what the caller can act on, and a message about the line's own date
// would send them looking at a field that decides nothing.
func entryStateRefusal(c *caller, unit entryUnit) (string, string) {
	locked := !c.mayWritePast(unit.Date)
	if unit.isClaimLine() {
		switch {
		case locked:
			return "claimId", fmt.Sprintf("Travel claim %d departed before %s, the lock date",
				*unit.ClaimID, lockedBefore(c.Settings).Format(time.DateOnly))
		case !unit.editable():
			return "claimId", fmt.Sprintf("Travel claim %d has been %s, so its expenses can no longer be changed",
				*unit.ClaimID, unit.Status)
		}
		return "", ""
	}
	switch {
	case locked:
		return "entryDate", lockedBeforeMessage(*lockedBefore(c.Settings))
	case !unit.editable():
		return "status", fmt.Sprintf("An expense that has been %s can no longer be changed", unit.Status)
	}
	return "", ""
}

// claimStateRefusal is entryStateRefusal for the claim itself, which reports
// on its own fields rather than on the line's.
func claimStateRefusal(c *caller, claim store.ExpensesClaim) (string, string) {
	switch unit := claimUnit(claim, c.zone()); {
	case !c.mayWritePast(unit.Date):
		return "departureAt", fmt.Sprintf("Travel claims departing before %s are locked",
			lockedBefore(c.Settings).Format(time.DateOnly))
	case !unit.editable():
		return "status", fmt.Sprintf("A travel claim that has been %s can no longer be changed", unit.Status)
	}
	return "", ""
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

	// SeesPayrollReference is the shaping of the payroll reference on a
	// reimbursement stamp. That somebody has been paid back is shown to
	// everyone who may see the expense (decision X5); *which payroll run* it
	// went with is the clerk's own record, so it goes to the person it is
	// about and to the three permissions that read everybody's expenses — and
	// not to a project manager, who sees the line because of the project it
	// sits on and has no business in the company's payroll batches.
	SeesPayrollReference bool

	// IsWriter is whether this expense is the caller's to change *at all* —
	// its owner, or expenses:manage. It is the half of CanEdit that is about
	// who is asking; entryStateRefusal is the half that is about what the
	// expense is right now, and the two answer different status codes.
	IsWriter bool

	CanEdit         bool
	CanDelete       bool
	CanSubmit       bool
	CanApprove      bool
	CanUnapprove    bool
	CanOverrideRate bool
	CanSetBilling   bool

	// The two tracks after approval (decision X5). Neither consults the period
	// lock: the lock protects what the employee submitted and what was
	// approved — its date, its amounts, its status — while reimbursing and
	// invoicing are bookkeeping done *after* a period closes. Payroll runs
	// after the books are shut, and an invoice for December goes out in
	// January; refusing either would close the books on the two people whose
	// job starts when they close.
	CanMarkInvoiced   bool
	CanUndoInvoiced   bool
	CanMarkReimbursed bool
	CanUndoReimbursed bool
}

// entryAccess resolves c's access to entry within its unit, asking the project
// directory for c's role on the expense's project unless it is cached. It must
// not run inside a locked transaction (withLockedTx); there, read the role
// first and use accessFor.
func (s *server) entryAccess(ctx context.Context, c *caller, entry store.ExpensesEntry, unit entryUnit) (entryAccess, error) {
	role, err := c.roleOf(ctx, s, entry.ProjectID)
	if err != nil {
		return entryAccess{}, err
	}
	return c.accessFor(entry, unit, role), nil
}

// editableStatuses are the statuses an expense is still its owner's to change
// in (decision X4): a fresh draft, and one a manager sent back.
var editableStatuses = []string{statusDraft, statusRejected}

// accessFor is entryAccess given the expense's unit and c's role on its
// project: the whole decision, with nothing left to look up.
//
// Every status-shaped answer here reads the **unit** rather than the row, so a
// line inside a travel claim follows its claim. The five flow capabilities are
// additionally false on such a line: submitting, approving, unapproving and
// paying back are the claim's, all at once, and the standalone operations
// refuse a line by id and point at the claim rather than moving it alone.
// Pricing, invoicing and a rate override stay the line's own — those are about
// this one amount — and read the unit for the status they are judged in.
func (c *caller) accessFor(entry store.ExpensesEntry, unit entryUnit, role string) entryAccess {
	a := entryAccess{
		IsOwner:   unit.UserID == c.UserID,
		IsManager: role == roleManager,
	}
	standalone := !unit.isClaimLine()
	a.IsApprover = a.IsManager || c.Approve
	a.CanSee = a.IsOwner || a.IsManager || c.seesEveryone()
	// Every billing answer is the projects module's, so none of them is true
	// without it (decision X2). A row that still carries a project_id from
	// before the module was switched off is exactly the case: the four doors
	// behind these capabilities all answer "this installation has no projects
	// module", and a capability that promised otherwise would be the one thing
	// the capabilities exist to prevent.
	a.CanSeeBilling = c.ProjectsOn && entry.ProjectID != nil && c.seesProjectFinancials(role)
	a.SeesPayrollReference = a.IsOwner || c.seesEveryone()

	open := c.mayWritePast(unit.Date)
	a.IsWriter = a.IsOwner || c.Manage
	writer := a.IsWriter
	editable := unit.editable()
	a.CanEdit = writer && open && editable
	a.CanDelete = a.CanEdit
	// Submit takes a rejected expense as well as a fresh draft: a line sent
	// back over its rate or its date needs no edit before it goes again, and
	// the submit reprices it either way.
	a.CanSubmit = standalone && writer && open && editable
	a.CanApprove = standalone && a.IsApprover && open && unit.Status == statusSubmitted
	a.CanUnapprove = standalone && (a.IsApprover || c.Manage) && open && unit.Status == statusApproved &&
		unit.ReimbursedAt == nil && entry.InvoicedAt == nil
	a.CanOverrideRate = (a.IsApprover || c.Manage) && open && unit.Status == statusSubmitted &&
		(entry.Kind == kindMileage || entry.Kind == kindPerDiem)
	// Pricing an expense from the project's side is open in every status the
	// line can still be priced in — its owner's progress through the flow is
	// not the project manager's business — and closed once it has been
	// invoiced, which is what it was priced for.
	//
	// The period lock does not reach it, for the reason it does not reach the
	// two tracks below: the lock protects what the employee submitted and what
	// was approved, while pricing is bookkeeping done *after* a period closes.
	// An invoice for December goes out in January, and the person who prices
	// the line for it holds financial rights on a project rather than
	// expenses:manage, so a locked line would otherwise be unpriceable by
	// anybody the design meant to price it.
	//
	// A per diem day is never one of them: it bills nobody anything (design §4),
	// so the capability does not advertise a door that refuses.
	a.CanSetBilling = a.CanSeeBilling && entry.InvoicedAt == nil && entry.Kind != kindPerDiem

	financial := c.ProjectsOn && c.seesProjectFinancials(role)
	// A line with no amount to bill cannot be invoiced, so the capability does
	// not say it can: a billable mileage line saved while no customer rate was
	// in force carries nothing to put on an invoice until somebody prices it.
	// A per diem day is named here as well as in CanSetBilling, although it can
	// no longer become billable: a row that went billable before that door was
	// closed must not be invoiceable either.
	a.CanMarkInvoiced = financial && entry.Billable && entry.Kind != kindPerDiem &&
		unit.Status == statusApproved && entry.InvoicedAt == nil && entry.BillAmount.Valid
	a.CanUndoInvoiced = financial && entry.InvoicedAt != nil
	a.CanMarkReimbursed = standalone && c.Manage && unit.Status == statusApproved &&
		unit.ReimbursedAt == nil && owesEmployee(entry)
	a.CanUndoReimbursed = standalone && c.Manage && unit.ReimbursedAt != nil
	return a
}

// claimAccess is what one caller may do with one travel claim. It is the same
// decision accessFor makes, over the claim as its own unit: the claim is
// visible to its owner, a manager of its project and the three permissions
// that see everyone's, and it is the claim — not its lines — that is
// submitted, approved, unapproved and paid back.
type claimAccess struct {
	IsOwner    bool
	IsManager  bool
	IsApprover bool

	CanSee bool
	// CanSeeFinancials is whether the caller may see what the claim's lines
	// bill their customer, on exactly the rule a line's own billing object
	// follows: financial rights on the claim's project, and nothing without
	// the projects module.
	CanSeeFinancials bool

	// SeesPayrollReference is the shaping of the payroll reference on the
	// claim's reimbursement stamp — entryAccess's own rule, one level up: the
	// person it paid and the three permissions that read everybody's expenses,
	// and not a project manager.
	SeesPayrollReference bool

	// IsWriter is whether the claim is the caller's to change at all — its
	// owner, or expenses:manage. claimStateRefusal is the other half.
	IsWriter bool

	CanEdit           bool
	CanDelete         bool
	CanSubmit         bool
	CanApprove        bool
	CanUnapprove      bool
	CanMarkReimbursed bool
	CanUndoReimbursed bool
}

// claimAccessFor is claimAccess given c's role on the claim's project.
//
// f is what the claim's lines come to, which only a caller that has read them
// can answer: how many there are, whether they owe the owner anything, and
// whether any of them has been invoiced. The zero value is the safe answer for a
// reading that has not — it makes canSubmit, canUnapprove and canMarkReimbursed
// false, which is exactly what an unread claim should promise.
//
// Three capabilities lean on it. A trip with **no lines at all** cannot be
// submitted: there is nothing to freeze and nothing to approve, and the submit
// refuses it by id, so the capability says so rather than offering a button
// that answers 400. A trip that owes its owner nothing cannot be marked
// reimbursed, for the reason a company-paid outlay cannot. And a trip with an
// invoiced line cannot be unapproved, exactly as an invoiced expense cannot.
func (c *caller) claimAccessFor(claim store.ExpensesClaim, role string, f claimFigures) claimAccess {
	unit := claimUnit(claim, c.zone())
	a := claimAccess{IsOwner: claim.UserID == c.UserID, IsManager: role == roleManager}
	a.IsApprover = a.IsManager || c.Approve
	a.CanSee = a.IsOwner || a.IsManager || c.seesEveryone()
	a.CanSeeFinancials = c.ProjectsOn && claim.ProjectID != nil && c.seesProjectFinancials(role)
	a.SeesPayrollReference = a.IsOwner || c.seesEveryone()

	open := c.mayWritePast(unit.Date)
	a.IsWriter = a.IsOwner || c.Manage
	editable := unit.editable()
	a.CanEdit = a.IsWriter && open && editable
	a.CanDelete = a.CanEdit
	a.CanSubmit = a.CanEdit && f.Lines > 0
	a.CanApprove = a.IsApprover && open && claim.Status == statusSubmitted
	// An unapprove is refused once the trip's money has moved in either
	// direction: the reimbursement is the claim's own, the invoicing is a
	// line's, and the capability answers on both so a queue never offers a
	// button the server refuses. Lines > 0 is what makes the unread zero value
	// safe here: a caller that has not read the lines cannot know that none of
	// them is invoiced, and says nothing rather than promising it.
	a.CanUnapprove = (a.IsApprover || c.Manage) && open && claim.Status == statusApproved &&
		claim.ReimbursedAt == nil && f.Lines > 0 && !f.Invoiced
	// The payroll track does not consult the period lock, for the reason it
	// does not on a standalone expense: payroll runs after the books close.
	a.CanMarkReimbursed = c.Manage && claim.Status == statusApproved &&
		claim.ReimbursedAt == nil && f.Owes
	a.CanUndoReimbursed = c.Manage && claim.ReimbursedAt != nil
	return a
}

// owesEmployee reports whether the expense owes its owner anything at all —
// owedToEmployee above zero, decided without doing the arithmetic.
//
// It is the Go mirror of expenses.owes_employee, the SQL function migration
// 00033 defines and every query that asks the question calls — one rule,
// written twice on purpose (supplier invoices design D1), and
// TestOwesEmployee_TheGoMirrorAgreesWithTheSQLFunction holds the two together
// on every kind and payer. An outlay the company paid for itself owes
// nothing; a supplier invoice owes nobody, whatever its paid_by says, because
// the company pays the supplier; and a line whose gross rounds to nothing owes
// nothing either, which is the half of the question the SQL callers ask
// beside the function (gross_amount > 0).
func owesEmployee(entry store.ExpensesEntry) bool {
	switch entry.Kind {
	case kindSupplierInvoice:
		return false
	case kindOutlay:
		if entry.PaidBy == nil || *entry.PaidBy != paidByEmployee {
			return false
		}
	}
	return entry.GrossAmount.Valid && !entry.GrossAmount.NaN &&
		entry.GrossAmount.Int != nil && entry.GrossAmount.Int.Sign() > 0
}
