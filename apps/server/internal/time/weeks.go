package timetracking

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/time/gen"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the caller's week and submission (D2): the week as timesheet
// rows, submitting a week, and submitting single entries. Submission is the
// one step that freezes an entry's rates (D3), so every submit locks the rows
// it moves and decides on them as they stand under the lock — an edit or a
// delete racing it ends either wholly before or wholly after it, and its
// loser gets a clean refusal (weeks_concurrency_test.go).

// weekRowKey is what makes two entries one timesheet row: the same project,
// billing line and task (none being a value of its own).
type weekRowKey struct {
	projectID, lineID, taskID int32
	hasLine, hasTask          bool
}

func weekRowKeyOf(row store.TimeEntry) weekRowKey {
	key := weekRowKey{projectID: row.ProjectID}
	if row.BillingLineID != nil {
		key.lineID, key.hasLine = *row.BillingLineID, true
	}
	if row.TaskID != nil {
		key.taskID, key.hasTask = *row.TaskID, true
	}
	return key
}

// compareWeekRows orders a week's rows the way the timesheet lists them: by
// project code, then no line before a line and lines by code, then no task
// before a task and tasks by title — each tie broken on the id, so the order
// never depends on the order the entries came in.
func compareWeekRows(a, b gen.TimeWeekRow) int {
	return cmp.Or(
		cmp.Compare(a.ProjectCode, b.ProjectCode),
		cmp.Compare(a.ProjectId, b.ProjectId),
		compareOptional(a.BillingLineId, b.BillingLineId, a.BillingLineCode, b.BillingLineCode),
		compareOptional(a.TaskId, b.TaskId, a.TaskTitle, b.TaskTitle),
	)
}

// compareOptional orders an optional reference: absent first, then by its
// label, then by its id.
func compareOptional(aID, bID *int32, aLabel, bLabel *string) int {
	switch {
	case aID == nil && bID == nil:
		return 0
	case aID == nil:
		return -1
	case bID == nil:
		return 1
	}
	return cmp.Or(cmp.Compare(orEmpty(aLabel), orEmpty(bLabel)), cmp.Compare(*aID, *bID))
}

// orEmpty is a string pointer's value, "" for none.
func orEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// weekResponse is the caller's week starting on the Monday weekStart: every
// one of their entries in it, whatever the status, grouped into rows of
// seven days; the day and week totals; and the week's submission state. A
// week that has been submitted and holds a draft — one added, or edited back
// to draft, since — has unsubmitted changes.
func (s *server) weekResponse(ctx context.Context, q *store.Queries, c *caller, weekStart time.Time) (gen.TimeWeekResponse, error) {
	rows, err := q.ListWeekEntries(ctx, store.ListWeekEntriesParams{
		UserID: c.UserID, WeekStart: pgDate(weekStart), WeekEnd: pgDate(weekEnd(weekStart)),
	})
	if err != nil {
		return gen.TimeWeekResponse{}, fmt.Errorf("time: list the week's entries: %w", err)
	}
	var submittedAt *time.Time
	switch at, err := q.GetWeekSubmission(ctx, store.GetWeekSubmissionParams{UserID: c.UserID, WeekStart: pgDate(weekStart)}); {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return gen.TimeWeekResponse{}, fmt.Errorf("time: read the week's submission: %w", err)
	default:
		submittedAt = &at
	}

	entries, err := s.entryResponses(ctx, c, rows)
	if err != nil {
		return gen.TimeWeekResponse{}, err
	}
	byKey := map[weekRowKey]int{}
	var weekRows []gen.TimeWeekRow
	perDay := make([]int64, daysInWeek)
	hasDraft := false
	for i, row := range rows {
		entry := entries[i]
		key := weekRowKeyOf(row)
		at, ok := byKey[key]
		if !ok {
			days := make([]gen.TimeWeekDay, daysInWeek)
			for d := range days {
				days[d] = gen.TimeWeekDay{
					Date:    openapi_types.Date{Time: weekStart.AddDate(0, 0, d)},
					Entries: []gen.TimeEntryResponse{},
				}
			}
			at = len(weekRows)
			byKey[key] = at
			weekRows = append(weekRows, gen.TimeWeekRow{
				ProjectId:       entry.ProjectId,
				ProjectCode:     entry.ProjectCode,
				ProjectName:     entry.ProjectName,
				BillingLineId:   entry.BillingLineId,
				BillingLineCode: entry.BillingLineCode,
				TrackableCode:   entry.TrackableCode,
				TaskId:          entry.TaskId,
				Days:            days,
			})
		}
		// The rows come by date, so the last entry's snapshot of the task's
		// title is the latest one.
		weekRows[at].TaskTitle = entry.TaskTitle
		day := int(row.EntryDate.Time.Sub(weekStart) / (24 * time.Hour))
		weekRows[at].Days[day].Entries = append(weekRows[at].Days[day].Entries, entry)

		cents, err := centsFromNumeric(row.Hours)
		if err != nil {
			return gen.TimeWeekResponse{}, err
		}
		perDay[day] += cents
		hasDraft = hasDraft || row.Status == statusDraft
	}
	slices.SortStableFunc(weekRows, compareWeekRows)

	totals := gen.TimeWeekTotals{PerDay: make([]float64, daysInWeek)}
	var week int64
	for d, cents := range perDay {
		totals.PerDay[d] = float64(cents) / 100
		week += cents
	}
	totals.Week = float64(week) / 100
	if weekRows == nil {
		weekRows = []gen.TimeWeekRow{}
	}
	return gen.TimeWeekResponse{
		WeekStart:             openapi_types.Date{Time: weekStart},
		SubmittedAt:           submittedAt,
		HasUnsubmittedChanges: submittedAt != nil && hasDraft,
		Rows:                  weekRows,
		Totals:                totals,
	}, nil
}

// GetTimeWeeksByWeekStart Get my week
// (GET /api/v1/time/weeks/{weekStart})
//
// Always the caller's own week: someone else's time is read through the list.
func (s *server) GetTimeWeeksByWeekStart(ctx context.Context, req gen.GetTimeWeeksByWeekStartRequestObject) (gen.GetTimeWeeksByWeekStartResponseObject, error) {
	if msg := notAMonday(req.WeekStart.Time); msg != "" {
		return gen.GetTimeWeeksByWeekStart400ApplicationProblemPlusJSONResponse(invalidWeek(fieldError("weekStart", msg))), nil
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	resp, err := s.weekResponse(ctx, q, c, req.WeekStart.Time)
	if err != nil {
		return nil, err
	}
	return gen.GetTimeWeeksByWeekStart200JSONResponse(resp), nil
}

// PostTimeWeeksByWeekStartSubmit Submit my week
// (POST /api/v1/time/weeks/{weekStart}/submit)
//
// Every one of the caller's drafts in the week moves to submitted, and the
// week is recorded as submitted now — also when it holds no drafts at all,
// which is how a person reports "nothing this week" (§4.2). Entries in any
// other status are left as they are: a rejected entry goes back through an
// edit (which makes it a draft) before it is submitted again.
//
// The drafts are locked first and every decision is made on them as they
// stand under the lock: a draft an edit is holding is waited for and
// submitted as edited; one a single submit or a delete took is no longer
// there to submit. A draft dated before the lock refuses the whole week for
// anyone but time:manage, and then nothing is submitted or recorded.
func (s *server) PostTimeWeeksByWeekStartSubmit(ctx context.Context, req gen.PostTimeWeeksByWeekStartSubmitRequestObject) (gen.PostTimeWeeksByWeekStartSubmitResponseObject, error) {
	weekStart := req.WeekStart.Time
	if msg := notAMonday(weekStart); msg != "" {
		return gen.PostTimeWeeksByWeekStartSubmit400ApplicationProblemPlusJSONResponse(invalidWeek(fieldError("weekStart", msg))), nil
	}
	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var lockedMsg string
	err = s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		drafts, err := txq.LockWeekDrafts(ctx, store.LockWeekDraftsParams{
			UserID: c.UserID, WeekStart: pgDate(weekStart), WeekEnd: pgDate(weekEnd(weekStart)),
		})
		if err != nil {
			return fmt.Errorf("time: lock the week's drafts: %w", err)
		}
		var lockedDates []string
		ids := make([]int64, 0, len(drafts))
		for _, d := range drafts {
			if c.locked(d.EntryDate.Time) && !c.Manage {
				lockedDates = append(lockedDates, d.EntryDate.Time.Format(time.DateOnly))
			}
			ids = append(ids, d.ID)
		}
		if len(lockedDates) > 0 {
			slices.Sort(lockedDates)
			lockedMsg = fmt.Sprintf("%s, and this week holds draft entries dated before it (%s)",
				lockedBeforeMessage(*c.LockedBefore), strings.Join(slices.Compact(lockedDates), ", "))
			return nil
		}
		if len(ids) > 0 {
			if _, err := txq.SubmitEntries(ctx, store.SubmitEntriesParams{Ids: ids, Now: now}); err != nil {
				return fmt.Errorf("time: submit the week's drafts: %w", err)
			}
		}
		if err := txq.UpsertWeekSubmission(ctx, store.UpsertWeekSubmissionParams{
			UserID: c.UserID, WeekStart: pgDate(weekStart), Now: now,
		}); err != nil {
			return fmt.Errorf("time: record the week's submission: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if lockedMsg != "" {
		return gen.PostTimeWeeksByWeekStartSubmit400ApplicationProblemPlusJSONResponse(
			invalidSubmission(fieldError("weekStart", lockedMsg))), nil
	}

	resp, err := s.weekResponse(ctx, q, c, weekStart)
	if err != nil {
		return nil, err
	}
	return gen.PostTimeWeeksByWeekStartSubmit200JSONResponse(resp), nil
}

// PostTimeEntriesSubmit Submit time entries
// (POST /api/v1/time/entries/submit)
//
// Single entries, all or nothing: each must be the caller's own draft, not
// before the lock unless they hold time:manage — capabilities.canSubmit —
// and one that is not refuses the whole request with a message per offending
// id on ids. An entry the caller may not see is reported exactly as an
// unknown id is ("was not found"), so the refusal tells a stranger nothing.
// The rows are locked in id order and judged as they stand under the lock,
// as the week's submit judges its drafts — against roles read before the
// transaction, so no directory is asked while rows are locked. Submitting
// single entries does not record the week as submitted.
func (s *server) PostTimeEntriesSubmit(ctx context.Context, req gen.PostTimeEntriesSubmitRequestObject) (gen.PostTimeEntriesSubmitResponseObject, error) {
	var ids []int64
	if req.Body != nil {
		seen := make(map[int64]bool, len(req.Body.Ids))
		for _, id := range req.Body.Ids {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return gen.PostTimeEntriesSubmit400ApplicationProblemPlusJSONResponse(
			invalidSubmission(fieldError("ids", "At least one entry id is required"))), nil
	}

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}

	// The caller's role on every project the entries are on is read now,
	// before any row is locked: the decision inside the transaction reads
	// only the cache (withLockedTx).
	before, err := q.GetEntries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("time: read the entries: %w", err)
	}
	projectIDs := make([]int32, 0, len(before))
	for _, row := range before {
		projectIDs = append(projectIDs, row.ProjectID)
	}
	if err := c.warmRoles(ctx, s, projectIDs); err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var refusals []string
	var submitted []store.TimeEntry
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
			if msg := c.submitRefusal(id, byID); msg != "" {
				refusals = append(refusals, msg)
			}
		}
		if len(refusals) > 0 {
			return nil
		}
		submitted, err = txq.SubmitEntries(ctx, store.SubmitEntriesParams{Ids: ids, Now: now})
		if err != nil {
			return fmt.Errorf("time: submit entries: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(refusals) > 0 {
		return gen.PostTimeEntriesSubmit400ApplicationProblemPlusJSONResponse(
			invalidSubmission(map[string][]string{"ids": refusals})), nil
	}

	// SubmitEntries answers in no particular order; the response is in the
	// order the ids were given.
	byID := make(map[int64]store.TimeEntry, len(submitted))
	for _, row := range submitted {
		byID[row.ID] = row
	}
	ordered := make([]store.TimeEntry, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, byID[id])
	}
	resp, err := s.entryResponses(ctx, c, ordered)
	if err != nil {
		return nil, err
	}
	return gen.PostTimeEntriesSubmit200JSONResponse(resp), nil
}

// submitRefusal is why the entry id may not be submitted by c, "" when it
// may. byID is the locked rows; an id missing from it does not exist. It runs
// inside the locked transaction, so it reads c's roles from the cache the
// handler warmed and asks no directory: a project it finds no role for —
// only possible for an entry moved to another project between the read and
// the lock — counts as none, which can only turn "is not yours" into "was not
// found", never let an entry through (submitting needs ownership, not a
// role).
func (c *caller) submitRefusal(id int64, byID map[int64]store.TimeEntry) string {
	row, ok := byID[id]
	if !ok {
		return fmt.Sprintf("Entry %d was not found", id)
	}
	role, _ := c.cachedRole(row.ProjectID)
	a := c.accessFor(row, role)
	switch {
	case !a.CanSee:
		return fmt.Sprintf("Entry %d was not found", id)
	case !a.IsOwner:
		return fmt.Sprintf("Entry %d is not yours", id)
	case row.Status != statusDraft:
		return fmt.Sprintf("Entry %d is not a draft", id)
	case !a.CanSubmit:
		return fmt.Sprintf("Entry %d is dated before %s, the lock date", id, c.LockedBefore.Format(time.DateOnly))
	}
	return ""
}
