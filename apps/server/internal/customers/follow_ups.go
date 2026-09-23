package customers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is phase 4 delivery C (follow-ups design D1, D2, D3): the
// follow-up a manual timeline entry can carry, the two paths that tick and
// untick it, the two attention types it feeds, and GET /customers/follow-ups.
//
// A follow-up lives on the entry, which is the decision everything here
// follows from. It shares the entry's revision, so setting and replacing it is
// the timeline's existing PUT with three more columns and no new concurrency
// story. Ticking it, though, is deliberately NOT that PUT: a tick comes from a
// list and must not lose a race with somebody editing the note, so the two
// done paths take no expectedRevision at all and lean on the guarded UPDATE's
// own WHERE instead (queries/follow_ups.sql).
//
// The assignee's VALUE belongs to another module — identity's users, reachable
// only through contracts.UserDirectory — with the two consequences owner.go
// spells out and neither of which is optional: every directory call happens
// outside a transaction, and a stored id can outlive the account it names.

// followUpAssigneeDisabled is the assignee's counterpart to ownerDisabled
// (owner.go), worded for what the field is for. ownerNotFound is shared as-is:
// "User %s does not exist" is a fact about the directory, not about what the
// caller wanted the user for, and two literals of it would drift.
func followUpAssigneeDisabled(id uuid.UUID) string {
	return fmt.Sprintf("User %s is disabled and cannot be given a follow-up", id)
}

// parsedFollowUp is a validated followUp: the due date as a UTC calendar date,
// and the assignee id if one was named. nil, wherever it appears, means "this
// entry carries no follow-up" — which on a PUT means "clear it".
type parsedFollowUp struct {
	DueOn      time.Time
	AssigneeID *uuid.UUID
}

// validateFollowUp is validateManualTimelineRequest's follow-up half (design
// D1), collecting into the same errs map so a request with a bad note AND a bad
// due date hears about both — the module's all-errors-at-once shape.
//
// dueOn goes through parseISODate, the same strict yyyy-MM-dd every other date
// in this module is read with, and is deliberately NOT compared against today:
// a follow-up in the future is the normal case and the whole point, which is
// exactly where it differs from occurredOn.
//
// The assignee is only parsed here, never checked for existence: that is a
// directory call, and a directory call belongs in the handler, before any
// transaction opens (actor.go's rule). A follow-up whose date did not parse is
// answered as nil, because there is nothing usable to hand the handler — the
// errs map is what the request hears about, and the handler never reaches a
// write while it is non-empty.
func validateFollowUp(body *gen.TimelineFollowUpRequest, errs map[string][]string) *parsedFollowUp {
	if body == nil {
		return nil
	}
	out := parsedFollowUp{}
	ok := true
	due, err := parseISODate(deref(body.DueOn))
	if err != nil {
		errs["followUp.dueOn"] = []string{"FollowUp.dueOn must be an ISO date (yyyy-MM-dd)"}
		ok = false
	} else {
		out.DueOn = due
	}
	if body.AssigneeUserId != nil {
		id := *body.AssigneeUserId
		out.AssigneeID = &id
	}
	if !ok {
		return nil
	}
	return &out
}

// resolveFollowUpAssignee is the assignee's existence and eligibility check
// (design D1), made against the directory BEFORE the caller opens a
// transaction. It answers field errors to report, or nil when there is nothing
// to say — including when there is no follow-up or no assignee at all.
//
// An assignee disabled AFTER being given a follow-up keeps it (design D1), so
// this only ever runs on a follow-up a request is setting now: the same
// division PutCustomersByIdOwner makes between validating a CHANGE and
// re-rendering stored state.
func (s *server) resolveFollowUpAssignee(ctx context.Context, fu *parsedFollowUp) (map[string][]string, error) {
	if fu == nil || fu.AssigneeID == nil {
		return nil, nil
	}
	user, err := s.deps.Users.User(ctx, *fu.AssigneeID)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve follow-up assignee: %w", err)
	}
	switch {
	case user == nil:
		return map[string][]string{"followUp.assigneeUserId": {ownerNotFound(*fu.AssigneeID)}}, nil
	case !user.Active:
		return map[string][]string{"followUp.assigneeUserId": {followUpAssigneeDisabled(*fu.AssigneeID)}}, nil
	}
	return nil, nil
}

// followUpDateParam and followUpAssigneeParam are one parsedFollowUp as the two
// columns a write sets. nil answers the zero pgtype.Date and a nil pointer,
// which is what NULL in both columns means: no follow-up.
func followUpDateParam(fu *parsedFollowUp) pgtype.Date {
	if fu == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: fu.DueOn, Valid: true}
}

func followUpAssigneeParam(fu *parsedFollowUp) *uuid.UUID {
	if fu == nil {
		return nil
	}
	return fu.AssigneeID
}

// followUpDecoration is the assignee display names one response needs, borrowed
// from identity's directory for the length of that response (design D1) — the
// timeline's counterpart to owner.go's customerDecoration, and the same shape
// for the same reason: one directory call per response, never one per row.
type followUpDecoration struct {
	assignees map[uuid.UUID]contracts.UserEntry
}

// decorateFollowUpAssignees resolves ids in one directory call. It must be
// called with no transaction open: the call inside is out-of-process, and an
// out-of-process call under a lock is how an outage becomes a database incident
// (owner.go's decorate says the same thing at more length).
//
// An empty ids makes no call at all — an entry with no follow-up, or a page of
// unassigned ones, is an ordinary answer, and asking the directory about nobody
// is a round trip for a map that will stay empty.
func (s *server) decorateFollowUpAssignees(ctx context.Context, ids []uuid.UUID) (followUpDecoration, error) {
	dec := followUpDecoration{assignees: map[uuid.UUID]contracts.UserEntry{}}
	distinct := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		distinct = append(distinct, id)
	}
	if len(distinct) == 0 {
		return dec, nil
	}
	users, err := s.deps.Users.Users(ctx, distinct)
	if err != nil {
		// A directory that cannot be reached is a 500, not a page of follow-ups
		// with their assignees quietly missing: "unassigned" is a claim, and
		// design D2 makes it a load-bearing one — an unassigned follow-up is
		// everyone's.
		return followUpDecoration{}, fmt.Errorf("customers: resolve follow-up assignees: %w", err)
	}
	for _, u := range users {
		dec.assignees[u.ID] = u
	}
	return dec, nil
}

// entryAssigneeIDs and revisionAssigneeIDs are the assignee ids of a set of
// rows, for decorateFollowUpAssignees. Duplicates are fine — it de-duplicates
// itself — and a row with no follow-up or no assignee contributes nothing.
func entryAssigneeIDs(entries ...store.CustomersCustomersTimelineEntry) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if e.FollowUpAssigneeUserID != nil {
			ids = append(ids, *e.FollowUpAssigneeUserID)
		}
	}
	return ids
}

func revisionAssigneeIDs(rows []store.CustomersCustomersTimelineEntriesRevision) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if r.FollowUpAssigneeUserID != nil {
			ids = append(ids, *r.FollowUpAssigneeUserID)
		}
	}
	return ids
}

// assignee is one follow-up's assignee as the contract reports it, or nil when
// it is unassigned. An id the directory answered nothing for is still an
// assignee — reported as unknownUserDisplay and inactive, the actorFor and
// owner precedent — because contracts.UserDirectory's absence means "no such
// account", not "the lookup failed", and design D1 keeps the follow-up either
// way.
func (d followUpDecoration) assignee(id *uuid.UUID) *gen.TimelineFollowUpAssignee {
	if id == nil {
		return nil
	}
	if u, ok := d.assignees[*id]; ok {
		return &gen.TimelineFollowUpAssignee{UserId: u.ID, DisplayName: u.DisplayName, Active: u.Active}
	}
	return &gen.TimelineFollowUpAssignee{UserId: *id, DisplayName: unknownUserDisplay, Active: false}
}

// followUpResponse is the one projection every shape that answers a follow-up
// goes through — the entry, a revision of it, and a row of the Follow-ups list
// — which is why it takes three columns rather than a row type. An invalid
// (NULL) date is the whole answer: no date, no follow-up.
func followUpResponse(on pgtype.Date, assigneeID *uuid.UUID, doneAt *time.Time, dec followUpDecoration) *gen.TimelineFollowUp {
	if !on.Valid {
		return nil
	}
	return &gen.TimelineFollowUp{
		DueOn:    openapi_types.Date{Time: on.Time},
		Assignee: dec.assignee(assigneeID),
		DoneAt:   doneAt,
	}
}

// followUpOutcome is what markFollowUp answers: exactly one of the three is
// set. It exists so the two handlers below are six lines each instead of two
// copies of the same forty — the paths differ only in which state they are
// heading for.
type followUpOutcome struct {
	Entry   *gen.TimelineResponse
	Problem *apicommon.ProblemDetails
	Missing bool
}

// markFollowUp is POST and DELETE .../follow-up/done, both of them (design D1).
//
// The ordering, and why each step is where it is:
//  1. the entry, in any state — a generated or deleted one has to be seen to
//     answer its distinct 409 rather than a 404;
//  2. manual and active, or the timeline's own "Timeline entry is immutable";
//  3. a follow-up at all, or 404 — the thing the caller addressed
//     (.../follow-up/done) genuinely does not exist, which is what 404 says,
//     and it is a different answer from "the entry cannot be edited";
//  4. ALREADY in the asked-for state: 200 with the entry and no write at all.
//     That is what idempotent means here, and it is also why no actor is
//     resolved on that path (actor.go: only when a write will happen) and why
//     no second revision row appears (customers foundation design D5's no-op
//     rule);
//  5. the actor, before the transaction opens;
//  6. the guarded write and its revision row, in one transaction.
//
// There is no expectedRevision anywhere in it. What takes its place is the
// UPDATE's own `follow_up_done_at IS NULL` (or IS NOT NULL): two concurrent
// ticks serialize on the row and the loser matches no row, which this function
// answers by re-reading and reporting what is now true. Answering 409 there
// would be telling a caller that a follow-up they can see is done is not done.
func (s *server) markFollowUp(ctx context.Context, customerID, entryID int32, done bool) (followUpOutcome, error) {
	q := store.New(s.deps.Pool)
	entry, err := q.GetTimelineEntry(ctx, store.GetTimelineEntryParams{ID: entryID, CustomerID: customerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return followUpOutcome{Missing: true}, nil
	}
	if err != nil {
		return followUpOutcome{}, fmt.Errorf("customers: get timeline entry: %w", err)
	}

	if entry.Provenance != "manual" || entry.State != "active" {
		problem := timelineProblem(timelineImmutableTitle, "Generated, deleted, or voided timeline entries cannot be edited.")
		return followUpOutcome{Problem: &problem}, nil
	}
	if !entry.FollowUpOn.Valid {
		return followUpOutcome{Missing: true}, nil
	}
	if (entry.FollowUpDoneAt != nil) == done {
		return s.followUpAnswer(ctx, entry)
	}

	act, err := s.actorFor(ctx, manualFallbackActor)
	if err != nil {
		return followUpOutcome{}, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	var written store.CustomersCustomersTimelineEntry
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		if done {
			written, err = txq.SetTimelineEntryFollowUpDone(ctx, store.SetTimelineEntryFollowUpDoneParams{
				ID: entryID, CustomerID: customerID, Now: now,
			})
		} else {
			written, err = txq.ClearTimelineEntryFollowUpDone(ctx, store.ClearTimelineEntryFollowUpDoneParams{
				ID: entryID, CustomerID: customerID, Now: now,
			})
		}
		if err != nil {
			return err
		}
		return insertTimelineRevisionFromEntry(ctx, txq, written, act)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		fresh, ferr := q.GetTimelineEntry(ctx, store.GetTimelineEntryParams{ID: entryID, CustomerID: customerID})
		if errors.Is(ferr, pgx.ErrNoRows) {
			return followUpOutcome{Missing: true}, nil
		}
		if ferr != nil {
			return followUpOutcome{}, fmt.Errorf("customers: re-read timeline entry after conflict: %w", ferr)
		}
		// A concurrent writer reached this state first — or deleted the entry,
		// or cleared its follow-up, in which case what is now true is a 409 or
		// a 404 and the checks above say so on the fresh row.
		switch {
		case fresh.Provenance != "manual" || fresh.State != "active":
			problem := timelineProblem(timelineImmutableTitle, "Generated, deleted, or voided timeline entries cannot be edited.")
			return followUpOutcome{Problem: &problem}, nil
		case !fresh.FollowUpOn.Valid:
			return followUpOutcome{Missing: true}, nil
		}
		return s.followUpAnswer(ctx, fresh)
	case db.IsUniqueViolation(err, timelineRevisionUniqueConstraint):
		// The backstop, unreachable in practice: whichever writer loses the
		// guarded UPDATE never reaches the revision insert at all. Answered as
		// the timeline's own revision conflict, the same as the PUT's.
		problem := timelineProblem(timelineRevisionConflictTitle, "The timeline entry revision was concurrently changed.")
		return followUpOutcome{Problem: &problem}, nil
	case err != nil:
		return followUpOutcome{}, fmt.Errorf("customers: mark timeline follow-up: %w", err)
	}
	return s.followUpAnswer(ctx, written)
}

// followUpAnswer decorates one entry and wraps it as the 200 both done paths
// answer. The directory call inside happens after the transaction has
// committed, never within one.
func (s *server) followUpAnswer(ctx context.Context, e store.CustomersCustomersTimelineEntry) (followUpOutcome, error) {
	dec, err := s.decorateFollowUpAssignees(ctx, entryAssigneeIDs(e))
	if err != nil {
		return followUpOutcome{}, err
	}
	body := timelineResponse(e, dec)
	return followUpOutcome{Entry: &body}, nil
}

// PostCustomersByIdTimelineByEntryIdFollowUpDone Mark a timeline entry's follow-up done
// (POST /api/v1/customers/{id}/timeline/{entryId}/follow-up/done)
func (s *server) PostCustomersByIdTimelineByEntryIdFollowUpDone(ctx context.Context, req gen.PostCustomersByIdTimelineByEntryIdFollowUpDoneRequestObject) (gen.PostCustomersByIdTimelineByEntryIdFollowUpDoneResponseObject, error) {
	out, err := s.markFollowUp(ctx, req.Id, req.EntryId, true)
	switch {
	case err != nil:
		return nil, err
	case out.Missing:
		return gen.PostCustomersByIdTimelineByEntryIdFollowUpDone404Response{}, nil
	case out.Problem != nil:
		return gen.PostCustomersByIdTimelineByEntryIdFollowUpDone409ApplicationProblemPlusJSONResponse(*out.Problem), nil
	}
	return gen.PostCustomersByIdTimelineByEntryIdFollowUpDone200JSONResponse(*out.Entry), nil
}

// DeleteCustomersByIdTimelineByEntryIdFollowUpDone Reopen a timeline entry's follow-up
// (DELETE /api/v1/customers/{id}/timeline/{entryId}/follow-up/done)
func (s *server) DeleteCustomersByIdTimelineByEntryIdFollowUpDone(ctx context.Context, req gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDoneRequestObject) (gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDoneResponseObject, error) {
	out, err := s.markFollowUp(ctx, req.Id, req.EntryId, false)
	switch {
	case err != nil:
		return nil, err
	case out.Missing:
		return gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDone404Response{}, nil
	case out.Problem != nil:
		return gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDone409ApplicationProblemPlusJSONResponse(*out.Problem), nil
	}
	return gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDone200JSONResponse(*out.Entry), nil
}

// The two attention types design D2 defines, computed from state on every call
// like the four registry ones — but unlike them, these depend on WHO asks. That
// is the first time this module's attention list has, and it is why
// GetCustomersStatsAttention now reads the principal.
const (
	attentionFollowUpOverdue = "followUpOverdue"
	attentionFollowUpDue     = "followUpDue"
)

// followUpAttentionItems is the follow-up half of /stats/attention's pure work
// (design D2). The query already narrowed to open follow-ups on non-archived
// customers, assigned to the caller or nobody, due today or earlier; all that
// is left is the one comparison that splits the two types.
//
// occurredAt is the DUE DATE at midnight UTC, not the moment the follow-up was
// written — projects' own rule, which stats.go's attentionOccurredAt already
// states for the registry items: the dashboard sorts by occurredAt and prints
// it as "3 days ago", so an overdue follow-up has to sort by how overdue it is.
//
// entityId is the CUSTOMER id even though the item is about an entry, because
// the host links a customers attention item to /customers/{entityId} and the
// customer's page is where the entry is. The entry id is in the item's own id.
func followUpAttentionItems(rows []store.FollowUpAttentionCandidatesRow, today time.Time) []gen.CustomerStatsAttentionItem {
	items := make([]gen.CustomerStatsAttentionItem, 0, len(rows))
	for _, r := range rows {
		due := civilDate(r.FollowUpOn.Time)
		typ := attentionFollowUpDue
		if due.Before(today) {
			typ = attentionFollowUpOverdue
		}
		items = append(items, gen.CustomerStatsAttentionItem{
			Id:         typ + "/" + strconv.FormatInt(int64(r.EntryID), 10),
			Type:       typ,
			Title:      r.CustomerName,
			OccurredAt: due,
			EntityId:   strconv.FormatInt(int64(r.CustomerID), 10),
		})
	}
	return items
}

// followUpsDefaultPageSize, followUpsMaxPageSize and followUpNoteLength are
// GET /customers/follow-ups' own numbers: the customer list's page size and cap
// (so one control behaves the same everywhere), and design D3's 200 UTF-16
// units of note.
const (
	followUpsDefaultPageSize = 25
	followUpsMaxPageSize     = 100
	followUpNoteLength       = 200
)

// followUpStates is design D3's four values, matched case-sensitively as every
// query parameter in this module is (validateGetCustomersParams says why).
var followUpStates = []string{"open", "overdue", "done", "all"}

// validateGetCustomersFollowUpsParams is validateGetCustomersParams for this
// list: every check runs regardless of the others, and every message is
// collected to be joined with a single space into one ProblemDetails.Detail.
// Query-parameter messages carry a trailing period; body-level ones do not.
//
// assignee is checked for SHAPE here and resolved in the handler: 'me' needs
// the request's principal, which this function deliberately does not see.
func validateGetCustomersFollowUpsParams(p gen.GetCustomersFollowUpsParams) []string {
	var errs []string
	if p.Page != nil && *p.Page < 1 {
		errs = append(errs, fmt.Sprintf("'page' must be 1 or greater, but was %d.", *p.Page))
	}
	if p.PageSize != nil && (*p.PageSize < 1 || *p.PageSize > followUpsMaxPageSize) {
		errs = append(errs, fmt.Sprintf("'pageSize' must be between 1 and %d, but was %d.", followUpsMaxPageSize, *p.PageSize))
	}
	if p.Assignee != nil && !validOwnerFilter(*p.Assignee) {
		errs = append(errs, fmt.Sprintf("'assignee' must be a user id, 'me' or 'none', but was '%s'.", *p.Assignee))
	}
	if p.State != nil {
		known := false
		for _, s := range followUpStates {
			if *p.State == s {
				known = true
				break
			}
		}
		if !known {
			errs = append(errs, fmt.Sprintf("'state' must be one of 'open', 'overdue', 'done' or 'all', but was '%s'.", *p.State))
		}
	}
	if p.CustomerId != nil && *p.CustomerId < 1 {
		errs = append(errs, fmt.Sprintf("'customerId' must be 1 or greater, but was %d.", *p.CustomerId))
	}
	return errs
}

// GetCustomersFollowUps List follow-ups across customers
// (GET /api/v1/customers/follow-ups)
//
// Design D3. Offset paging, not the timeline's keyset cursor: this is a page of
// a list with a page control, the same shape GET /customers answers, and the
// design asks for page/pageSize by name.
//
// 'me' is resolved from the SESSION, never from anything the request says about
// who the caller is — the same rule (and the same unreachable-in-production 400)
// GetCustomers' ownerId=me follows, for the same reason: answering somebody
// else's follow-ups is the one outcome a "what is on my plate" list must never
// have. It is the default, so an unauthenticated caller would hit it without
// asking; the router admits none, which is why that branch is a 400 rather than
// a silent everyone's-follow-ups.
func (s *server) GetCustomersFollowUps(ctx context.Context, req gen.GetCustomersFollowUpsRequestObject) (gen.GetCustomersFollowUpsResponseObject, error) {
	if msgs := validateGetCustomersFollowUpsParams(req.Params); len(msgs) > 0 {
		return gen.GetCustomersFollowUps400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid query parameters", strings.Join(msgs, " "))), nil
	}

	page := int32(1)
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	pageSize := int32(followUpsDefaultPageSize)
	if req.Params.PageSize != nil {
		pageSize = *req.Params.PageSize
	}
	state := "open"
	if req.Params.State != nil {
		state = *req.Params.State
	}

	assignee := "me"
	if req.Params.Assignee != nil {
		assignee = *req.Params.Assignee
	}
	var assigneeID *uuid.UUID
	assigneeNone := false
	switch assignee {
	case "none":
		assigneeNone = true
	case "me":
		p, ok := contracts.PrincipalFrom(ctx)
		if !ok || p.UserID == uuid.Nil {
			return gen.GetCustomersFollowUps400ApplicationProblemPlusJSONResponse(apicommon.Problem(
				"Invalid query parameters", "'assignee' cannot be 'me' without a signed-in user.")), nil
		}
		id := p.UserID
		assigneeID = &id
	default:
		// validateGetCustomersFollowUpsParams already refused anything
		// unparseable, so err is impossible here; the guard means an impossible
		// value filters nothing rather than panicking.
		if id, err := uuid.Parse(assignee); err == nil {
			assigneeID = &id
		}
	}

	today := pgtype.Date{Time: civilDate(s.deps.Clock()), Valid: true}
	q := store.New(s.deps.Pool)
	total, err := q.CountCustomerFollowUps(ctx, store.CountCustomerFollowUpsParams{
		FollowUpState: state, Today: today,
		AssigneeNone: assigneeNone, AssigneeID: assigneeID, CustomerID: req.Params.CustomerId,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: count follow-ups: %w", err)
	}
	rows, err := q.ListCustomerFollowUps(ctx, store.ListCustomerFollowUpsParams{
		FollowUpState: state, Today: today,
		AssigneeNone: assigneeNone, AssigneeID: assigneeID, CustomerID: req.Params.CustomerId,
		PageSize: pageSize, RowOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: list follow-ups: %w", err)
	}

	// One directory call for the whole page, never one per row (owner.go's
	// rule), and after the reads rather than between them.
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if r.FollowUpAssigneeUserID != nil {
			ids = append(ids, *r.FollowUpAssigneeUserID)
		}
	}
	dec, err := s.decorateFollowUpAssignees(ctx, ids)
	if err != nil {
		return nil, err
	}

	data := make([]gen.CustomerFollowUp, 0, len(rows))
	for _, r := range rows {
		row := gen.CustomerFollowUp{
			EntryId:      r.EntryID,
			CustomerId:   r.CustomerID,
			CustomerName: r.CustomerName,
			EventType:    r.EventType,
			OccurredOn:   openapi_types.Date{Time: r.OccurredOn.Time},
		}
		if r.Note != nil {
			// Cut in UTF-16 code units, the unit design D3 names and the one
			// the entry's own summary is already cut in (truncateUTF16,
			// timeline.go): Postgres' left() counts characters, and the two
			// disagree on every astral character.
			row.Note = apicommon.Ptr(truncateUTF16(*r.Note, followUpNoteLength))
		}
		// followUp is required on this shape — carrying one is what put the row
		// on the list — so a nil here would be a bug in the query, not a row
		// with no follow-up. Dereferencing is the assertion.
		row.FollowUp = *followUpResponse(r.FollowUpOn, r.FollowUpAssigneeUserID, r.FollowUpDoneAt, dec)
		data = append(data, row)
	}

	return gen.GetCustomersFollowUps200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}
