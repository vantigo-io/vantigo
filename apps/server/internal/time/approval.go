package timetracking

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/time/gen"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the approver's side of D2: approving, rejecting and
// unapproving entries in batch, and the approval queue. Every transition is
// all or nothing and follows the pattern the batch submit set (weeks.go):
// the caller's roles are read before any row is locked, the rows are locked
// in id order and judged as they stand under the lock — so two approvers
// racing over one entry end with one approval and one refusal — and the
// response is rendered after the transaction.

// rejectionReasonMaxLength is the rejection_reason column's width.
const rejectionReasonMaxLength = 1000

// transition is one of the three approval operations: the status it moves an
// entry from (which a refusal names), whether time:manage alone may make it,
// and the update that makes it on rows already locked and judged.
type transition struct {
	from     string
	orManage bool
	apply    func(ctx context.Context, txq *store.Queries, ids []int64, now time.Time) ([]store.TimeEntry, error)
}

// transitionRefusal is why c may not move the entry id through t, "" when it
// may. byID is the locked rows; an id missing from it does not exist. It runs
// inside the locked transaction and reads c's roles only from the cache the
// handler warmed: a project it finds no role for — only possible for an
// entry moved to another project between the read and the lock, which only a
// draft can be — counts as none, which can only refuse, never admit.
//
// The checks come in the order that tells the caller least: an entry they
// may not see is the unknown id's "was not found", one they see but do not
// approve for says only that, and only then are its status and date judged.
func (c *caller) transitionRefusal(t transition, id int64, byID map[int64]store.TimeEntry) string {
	row, ok := byID[id]
	if !ok {
		return fmt.Sprintf("Entry %d was not found", id)
	}
	role, _ := c.cachedRole(row.ProjectID)
	a := c.accessFor(row, role)
	switch {
	case !a.CanSee:
		return fmt.Sprintf("Entry %d was not found", id)
	case !a.IsApprover && (!t.orManage || !c.Manage):
		return fmt.Sprintf("Entry %d is not on a project you approve for", id)
	case row.Status == statusInvoiced:
		return fmt.Sprintf("Entry %d is invoiced", id)
	case row.Status != t.from:
		return fmt.Sprintf("Entry %d is not %s", id, t.from)
	case c.locked(row.EntryDate.Time) && !c.Manage:
		return fmt.Sprintf("Entry %d is dated before %s, the lock date", id, c.LockedBefore.Format(time.DateOnly))
	}
	return ""
}

// uniqueIDs is ids with duplicates dropped, in the order given.
func uniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// maxBatchIDs bounds every batch ids array — submit, approve, reject and
// unapprove — matching the openapi contract's maxItems on those schemas. No
// request-validation middleware sits in front of these handlers, so the
// handler enforces its own contract: without a cap, LockEntries would row-lock
// and warmRoles would look up a project role for as many ids as the body's
// 1 MiB cap allows.
const maxBatchIDs = 500

// batchTooLargeMessage is the "ids" field message for a batch over
// maxBatchIDs, naming both the cap and what was sent.
func batchTooLargeMessage(n int) string {
	return fmt.Sprintf("At most %d entry ids may be given at once; %d were given", maxBatchIDs, n)
}

// transitionOutcome is what a transition answers: forbidden for a caller who
// approves for nothing at all, the field errors of a refused request, or the
// moved entries in the order their ids were given.
type transitionOutcome struct {
	forbidden bool
	errs      map[string][]string
	entries   []gen.TimeEntryResponse
}

// transitionEntries runs t over rawIDs for the request's caller. errs is what
// the body's own fields already failed (reject's reason); it is reported with
// a missing ids list, and before any entry is judged.
func (s *server) transitionEntries(ctx context.Context, t transition, rawIDs []int64, errs map[string][]string) (transitionOutcome, error) {
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return transitionOutcome{}, err
	}
	ok, err := c.approvesAnything(ctx, s, t.orManage)
	if err != nil {
		return transitionOutcome{}, err
	}
	if !ok {
		return transitionOutcome{forbidden: true}, nil
	}

	if len(rawIDs) > maxBatchIDs {
		errs = withFieldError(errs, "ids", batchTooLargeMessage(len(rawIDs)))
	}
	ids := uniqueIDs(rawIDs)
	if len(ids) == 0 {
		errs = withFieldError(errs, "ids", "At least one entry id is required")
	}
	if len(errs) > 0 {
		return transitionOutcome{errs: errs}, nil
	}

	// The caller's role on every project the entries are on is read now,
	// before any row is locked: the decision inside the transaction reads
	// only the cache (withLockedTx).
	before, err := q.GetEntries(ctx, ids)
	if err != nil {
		return transitionOutcome{}, fmt.Errorf("time: read the entries: %w", err)
	}
	projectIDs := make([]int32, 0, len(before))
	for _, row := range before {
		projectIDs = append(projectIDs, row.ProjectID)
	}
	if err := c.warmRoles(ctx, s, projectIDs); err != nil {
		return transitionOutcome{}, err
	}

	now := s.deps.Clock()
	var refusals []string
	var moved []store.TimeEntry
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		locked, err := txq.LockEntries(ctx, ids)
		if err != nil {
			return fmt.Errorf("time: lock the entries: %w", err)
		}
		byID := make(map[int64]store.TimeEntry, len(locked))
		for _, row := range locked {
			byID[row.ID] = row
		}
		for _, id := range ids {
			if msg := c.transitionRefusal(t, id, byID); msg != "" {
				refusals = append(refusals, msg)
			}
		}
		if len(refusals) > 0 {
			return nil
		}
		moved, err = t.apply(ctx, txq, ids, now)
		if err == nil && len(moved) != len(ids) {
			// Every row is locked and was judged movable; the update's own
			// status guard refusing one means the two disagree — a bug, not
			// a race, and never a 200 with a blank entry in it.
			err = fmt.Errorf("time: the transition moved %d of %d judged entries", len(moved), len(ids))
		}
		return err
	})
	if err != nil {
		return transitionOutcome{}, err
	}
	if len(refusals) > 0 {
		return transitionOutcome{errs: map[string][]string{"ids": refusals}}, nil
	}

	// The update answers in no particular order; the response is in the
	// order the ids were given.
	byID := make(map[int64]store.TimeEntry, len(moved))
	for _, row := range moved {
		byID[row.ID] = row
	}
	ordered := make([]store.TimeEntry, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, byID[id])
	}
	entries, err := s.entryResponses(ctx, c, ordered)
	if err != nil {
		return transitionOutcome{}, err
	}
	return transitionOutcome{entries: entries}, nil
}

// PostTimeEntriesApprove Approve time entries
// (POST /api/v1/time/entries/approve)
//
// Submitted entries, each on a project the caller approves for (its manager,
// or time:approve) and not before the lock unless they hold time:manage —
// capabilities.canApprove — move to approved, recording the approver and the
// time. All or nothing: one entry that may not be approved refuses the whole
// request with a message per offending id on ids. A caller who approves for
// no project at all gets the access layer's 403.
func (s *server) PostTimeEntriesApprove(ctx context.Context, req gen.PostTimeEntriesApproveRequestObject) (gen.PostTimeEntriesApproveResponseObject, error) {
	var ids []int64
	if req.Body != nil {
		ids = req.Body.Ids
	}
	approver := callerID(ctx)
	out, err := s.transitionEntries(ctx, transition{
		from: statusSubmitted,
		apply: func(ctx context.Context, txq *store.Queries, ids []int64, now time.Time) ([]store.TimeEntry, error) {
			rows, err := txq.ApproveEntries(ctx, store.ApproveEntriesParams{Ids: ids, ApproverID: approver, Now: now})
			if err != nil {
				return nil, fmt.Errorf("time: approve entries: %w", err)
			}
			return rows, nil
		},
	}, ids, nil)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostTimeEntriesApprove403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostTimeEntriesApprove400ApplicationProblemPlusJSONResponse(invalidApproval(out.errs)), nil
	}
	return gen.PostTimeEntriesApprove200JSONResponse(out.entries), nil
}

// validateReason is reject's reason rule: required, at most 1000 characters
// once trimmed. It answers the trimmed reason and the message, "" when the
// rule holds.
func validateReason(raw string) (string, string) {
	reason := strings.TrimSpace(raw)
	if reason == "" {
		return "", "A reason is required to reject entries"
	}
	if n := utf8.RuneCountInString(reason); n > rejectionReasonMaxLength {
		return "", fmt.Sprintf("A reason cannot be longer than %d characters, the given value was %d characters", rejectionReasonMaxLength, n)
	}
	return reason, ""
}

// PostTimeEntriesReject Reject time entries
// (POST /api/v1/time/entries/reject)
//
// Under exactly the rules of an approval, submitted entries move to rejected
// with a reason their owner sees in their week; the owner's next edit
// returns each to draft with the reason cleared.
func (s *server) PostTimeEntriesReject(ctx context.Context, req gen.PostTimeEntriesRejectRequestObject) (gen.PostTimeEntriesRejectResponseObject, error) {
	body := gen.TimeEntryRejectRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	reason, msg := validateReason(body.Reason)
	var errs map[string][]string
	if msg != "" {
		errs = fieldError("reason", msg)
	}
	out, err := s.transitionEntries(ctx, transition{
		from: statusSubmitted,
		apply: func(ctx context.Context, txq *store.Queries, ids []int64, now time.Time) ([]store.TimeEntry, error) {
			rows, err := txq.RejectEntries(ctx, store.RejectEntriesParams{Ids: ids, Reason: reason, Now: now})
			if err != nil {
				return nil, fmt.Errorf("time: reject entries: %w", err)
			}
			return rows, nil
		},
	}, body.Ids, errs)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostTimeEntriesReject403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostTimeEntriesReject400ApplicationProblemPlusJSONResponse(invalidApproval(out.errs)), nil
	}
	return gen.PostTimeEntriesReject200JSONResponse(out.entries), nil
}

// PostTimeEntriesUnapprove Unapprove time entries
// (POST /api/v1/time/entries/unapprove)
//
// Approved entries return to draft — never invoiced ones — by an approver of
// their project or by time:manage, and not before the lock unless the
// caller holds time:manage (capabilities.canUnapprove). The approval and the
// submission stamp are both cleared, so the owner's week shows each entry as
// a fresh draft to submit again.
func (s *server) PostTimeEntriesUnapprove(ctx context.Context, req gen.PostTimeEntriesUnapproveRequestObject) (gen.PostTimeEntriesUnapproveResponseObject, error) {
	var ids []int64
	if req.Body != nil {
		ids = req.Body.Ids
	}
	out, err := s.transitionEntries(ctx, transition{
		from:     statusApproved,
		orManage: true,
		apply: func(ctx context.Context, txq *store.Queries, ids []int64, now time.Time) ([]store.TimeEntry, error) {
			rows, err := txq.UnapproveEntries(ctx, store.UnapproveEntriesParams{Ids: ids, Now: now})
			if err != nil {
				return nil, fmt.Errorf("time: unapprove entries: %w", err)
			}
			return rows, nil
		},
	}, ids, nil)
	switch {
	case err != nil:
		return nil, err
	case out.forbidden:
		return gen.PostTimeEntriesUnapprove403JSONResponse(forbidden()), nil
	case out.errs != nil:
		return gen.PostTimeEntriesUnapprove400ApplicationProblemPlusJSONResponse(invalidApproval(out.errs)), nil
	}
	return gen.PostTimeEntriesUnapprove200JSONResponse(out.entries), nil
}

// compareNames orders people the way every list of them here is ordered: by
// display name ignoring case, then as written, then by id, so two people of
// one name always come in the same order.
func compareNames(a, b contracts.UserEntry) int {
	return cmp.Or(
		cmp.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)),
		cmp.Compare(a.DisplayName, b.DisplayName),
		cmp.Compare(a.ID.String(), b.ID.String()),
	)
}

// approvalGroupKey names one person's week the way ListApprovalEntries
// matches it: '<user id>/<Monday as YYYY-MM-DD>'.
func approvalGroupKey(userID uuid.UUID, weekStart time.Time) string {
	return userID.String() + "/" + weekStart.Format(time.DateOnly)
}

// GetTimeApprovals Get the approval queue
// (GET /api/v1/time/approvals)
//
// The submitted entries the caller may approve — on every project for
// time:approve, on the projects they manage otherwise, and not those dated
// before the lock unless they hold time:manage, whose approval the lock
// would refuse anyway — grouped by person and week, the oldest week first
// and then by display name, paged by group. A caller who approves for no
// project at all gets the access layer's 403 (design §7: approve or
// manager).
//
// The groups are read first, one row each, and ordered here, because the
// names they are ordered by live in identity; only the page's groups' entries
// are then read.
func (s *server) GetTimeApprovals(ctx context.Context, req gen.GetTimeApprovalsRequestObject) (gen.GetTimeApprovalsResponseObject, error) {
	p := req.Params
	if msgs := validatePageParams(p.Page, p.PageSize); len(msgs) > 0 {
		return gen.GetTimeApprovals400ApplicationProblemPlusJSONResponse(
			apicommon.Problem(invalidQueryTitle, strings.Join(msgs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	scope, err := s.approvalScopeFor(ctx, c)
	if err != nil {
		return nil, err
	}
	if !scope.approvesAny() {
		return gen.GetTimeApprovals403JSONResponse(forbidden()), nil
	}

	groups, err := q.ListApprovalGroups(ctx, store.ListApprovalGroupsParams{
		SeeAll: scope.seeAll, ManagedProjectIds: scope.managed, LockedBefore: scope.lock,
	})
	if err != nil {
		return nil, fmt.Errorf("time: list the approval groups: %w", err)
	}
	userIDs := make([]uuid.UUID, 0, len(groups))
	seen := map[uuid.UUID]bool{}
	for _, g := range groups {
		if !seen[g.UserID] {
			seen[g.UserID] = true
			userIDs = append(userIDs, g.UserID)
		}
	}
	users, err := s.userEntries(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(groups, func(a, b store.ListApprovalGroupsRow) int {
		return cmp.Or(a.WeekStart.Time.Compare(b.WeekStart.Time), compareNames(users[a.UserID], users[b.UserID]))
	})

	total := len(groups)
	from := min(int(page-1)*int(pageSize), total)
	to := min(from+int(pageSize), total)
	pageGroups := groups[from:to]

	data := make([]gen.TimeApprovalGroup, 0, len(pageGroups))
	at := make(map[string]int, len(pageGroups))
	keys := make([]string, 0, len(pageGroups))
	for i, g := range pageGroups {
		cents, err := centsFromNumeric(g.Hours)
		if err != nil {
			return nil, err
		}
		key := approvalGroupKey(g.UserID, g.WeekStart.Time)
		at[key] = i
		keys = append(keys, key)
		data = append(data, gen.TimeApprovalGroup{
			UserId:      g.UserID,
			DisplayName: users[g.UserID].DisplayName,
			WeekStart:   openapi_types.Date{Time: g.WeekStart.Time},
			Hours:       float64(cents) / 100,
			Entries:     []gen.TimeEntryResponse{},
		})
	}
	if len(keys) > 0 {
		rows, err := q.ListApprovalEntries(ctx, store.ListApprovalEntriesParams{
			SeeAll: scope.seeAll, ManagedProjectIds: scope.managed, LockedBefore: scope.lock, GroupKeys: keys,
		})
		if err != nil {
			return nil, fmt.Errorf("time: list the approval queue's entries: %w", err)
		}
		entries, err := s.entryResponses(ctx, c, rows)
		if err != nil {
			return nil, err
		}
		for i, row := range rows {
			g := at[approvalGroupKey(row.UserID, mondayOf(row.EntryDate.Time))]
			data[g].Entries = append(data[g].Entries, entries[i])
		}
	}
	return gen.GetTimeApprovals200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}
