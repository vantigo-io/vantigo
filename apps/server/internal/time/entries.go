package timetracking

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/time/gen"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the time entries' CRUD: create, list, get, update and delete.
// Submitting them is weeks.go's.

// dayLockClass is the class every per-person-per-day advisory lock is taken
// in ("TDAY"), with a hash of the person and the day as the object. Postgres
// keeps two-argument advisory locks in a lock space of their own, and every
// other class in use (projects' task ordering, 9; communications' channel
// default, "CHND"; energy's supply periods, "SUPR") is a different number, so
// this can collide with none of them.
const dayLockClass = 0x54444159

// dayKey is the object a day lock is taken on: one person, one date.
func dayKey(userID uuid.UUID, date time.Time) string {
	return userID.String() + "/" + date.Format(time.DateOnly)
}

// entryRefs is what a create body's references resolved to through
// contracts.ProjectDirectory: the project (always, once the caller may log
// time on it), the billing line and the task's title (when given).
type entryRefs struct {
	Project   contracts.ProjectEntry
	Line      *contracts.BillingLineEntry
	TaskTitle *string
}

// checkReferences is §4.2's rules over another module's data: the caller may
// log time on the project (CanLogTime — active, and a member or manager), the
// line is one of the project's and active, and the task is one of the
// project's, whose title is snapshotted (D5). Failures are added to errs,
// which it answers grown.
//
// A project the caller may not log time on answers the one cannotLogTime
// message, whatever the reason, and stops there: checking a line or a task on
// it would tell a caller who cannot see the project which of its ids exist.
func (s *server) checkReferences(ctx context.Context, userID uuid.UUID, p parsedEntry, errs map[string][]string) (entryRefs, map[string][]string, error) {
	var refs entryRefs
	ok, err := s.deps.Projects.CanLogTime(ctx, p.ProjectID, userID)
	if err != nil {
		return refs, nil, fmt.Errorf("time: check the caller may log time on the project: %w", err)
	}
	var project *contracts.ProjectEntry
	if ok {
		if project, err = s.deps.Projects.Project(ctx, p.ProjectID); err != nil {
			return refs, nil, fmt.Errorf("time: look up the project: %w", err)
		}
	}
	if project == nil {
		return refs, withFieldError(errs, "projectId", cannotLogTime), nil
	}
	refs.Project = *project

	if p.LineID != nil {
		line, err := s.deps.Projects.BillingLine(ctx, p.ProjectID, *p.LineID)
		if err != nil {
			return refs, nil, fmt.Errorf("time: look up the billing line: %w", err)
		}
		switch {
		case line == nil:
			errs = withFieldError(errs, "billingLineId", fmt.Sprintf("Billing line %d is not one of this project's", *p.LineID))
		case !line.Active:
			errs = withFieldError(errs, "billingLineId", fmt.Sprintf("Billing line %d is inactive", *p.LineID))
		default:
			refs.Line = line
		}
	}

	if p.TaskID != nil {
		task, err := s.deps.Projects.Task(ctx, *p.TaskID)
		if err != nil {
			return refs, nil, fmt.Errorf("time: look up the task: %w", err)
		}
		if task == nil || task.ProjectID != p.ProjectID {
			errs = withFieldError(errs, "taskId", fmt.Sprintf("Task %d is not one of this project's", *p.TaskID))
		} else {
			title := task.Title
			refs.TaskTitle = &title
		}
	}
	return refs, errs, nil
}

// PostTimeEntries Log time
// (POST /api/v1/time/entries)
//
// The entry is the caller's own, a draft, with its rates resolved and
// snapshotted (D3). Every rule that can be checked before the transaction is
// — the body, the references, the lock — so one round trip reports every
// problem; only the day cap is decided inside it, under a lock on the
// caller's day, because two creates racing for the same day's last hours
// must not both see room for themselves.
func (s *server) PostTimeEntries(ctx context.Context, req gen.PostTimeEntriesRequestObject) (gen.PostTimeEntriesResponseObject, error) {
	body := gen.TimeEntryRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	parsed, errs := parseEntry(body)

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	refs, errs, err := s.checkReferences(ctx, c.UserID, parsed, errs)
	if err != nil {
		return nil, err
	}
	if !parsed.Date.IsZero() && c.locked(parsed.Date) && !c.Manage {
		errs = withFieldError(errs, "entryDate", lockedBeforeMessage(*c.LockedBefore))
	}
	if len(errs) > 0 {
		return gen.PostTimeEntries400ApplicationProblemPlusJSONResponse(invalidEntry(errs)), nil
	}

	billable := resolveBillable(refs.Project.BillingType, parsed.Billable)
	rates, err := s.resolveRates(ctx, q, rateRequest{
		UserID: c.UserID, Billable: billable, Project: refs.Project, Line: refs.Line, Date: parsed.Date,
	})
	if err != nil {
		return nil, err
	}
	billRate, err := numericFromFloatPtr(rates.BillRate)
	if err != nil {
		return nil, err
	}
	costRate, err := numericFromFloatPtr(rates.CostRate)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var created store.TimeEntry
	var capMsg string
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		msg, err := checkDayCap(ctx, txq, c.UserID, parsed.Date, parsed.HoursCents, nil)
		if err != nil {
			return err
		}
		if msg != "" {
			capMsg = msg
			return nil
		}
		created, err = txq.InsertEntry(ctx, store.InsertEntryParams{
			UserID:        c.UserID,
			ProjectID:     parsed.ProjectID,
			BillingLineID: parsed.LineID,
			TaskID:        parsed.TaskID,
			TaskTitle:     refs.TaskTitle,
			EntryDate:     pgDate(parsed.Date),
			Hours:         numericFromCents(parsed.HoursCents),
			StartTime:     timeFromMinutes(parsed.Start),
			EndTime:       timeFromMinutes(parsed.End),
			Note:          parsed.Note,
			Billable:      billable,
			BillRate:      billRate,
			BillCurrency:  rates.BillCurrency,
			CostRate:      costRate,
			CostCurrency:  rates.CostCurrency,
			RateSource:    rates.Source,
			Now:           now,
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("time: create entry: %w", err)
	}
	if capMsg != "" {
		return gen.PostTimeEntries400ApplicationProblemPlusJSONResponse(invalidEntry(fieldError("hours", capMsg))), nil
	}

	a, err := s.entryAccess(ctx, c, created)
	if err != nil {
		return nil, err
	}
	resp, err := s.entryResponseFor(ctx, created, a)
	if err != nil {
		return nil, err
	}
	return gen.PostTimeEntries201JSONResponse(resp), nil
}

// checkDayCap is D1's day rule, run inside the saving transaction: it takes
// the lock on (user, date) first, then sums the day's other entries
// (excludeID leaves out the one being saved, nil on a create), so a second
// save of the same day waits for the first to commit and then counts it. It
// answers the message the hours field carries when the day would hold more
// than 24 hours with this entry in it, "" when it would not.
func checkDayCap(ctx context.Context, q *store.Queries, userID uuid.UUID, date time.Time, cents int64, excludeID *int64) (string, error) {
	if err := q.AcquireDayLock(ctx, store.AcquireDayLockParams{LockClass: dayLockClass, DayKey: dayKey(userID, date)}); err != nil {
		return "", fmt.Errorf("time: take the day lock: %w", err)
	}
	sum, err := q.SumDayHours(ctx, store.SumDayHoursParams{UserID: userID, EntryDate: pgDate(date), ExcludeID: excludeID})
	if err != nil {
		return "", fmt.Errorf("time: sum the day's hours: %w", err)
	}
	others, err := centsFromNumeric(sum)
	if err != nil {
		return "", err
	}
	if total := others + cents; total > maxDayCents {
		return dayCapExceeded(date, total), nil
	}
	return "", nil
}

// GetTimeEntriesById Get a time entry by id
// (GET /api/v1/time/entries/{id})
//
// A caller who may not see the entry gets the same bare 404 an unknown id
// gets, which is why the row is loaded before the caller's access to it is
// resolved and then discarded.
func (s *server) GetTimeEntriesById(ctx context.Context, req gen.GetTimeEntriesByIdRequestObject) (gen.GetTimeEntriesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, a, found, err := s.visibleEntry(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.GetTimeEntriesById404Response{}, nil
	}
	resp, err := s.entryResponseFor(ctx, row, a)
	if err != nil {
		return nil, err
	}
	return gen.GetTimeEntriesById200JSONResponse(resp), nil
}

// DeleteTimeEntriesById Delete a time entry
// (DELETE /api/v1/time/entries/{id})
//
// Delete is for drafts (D10): the owner's entry, in draft or rejected, and
// not before the lock unless the caller holds time:manage — exactly
// capabilities.canEdit. A caller who may see the entry but not delete it gets
// the access layer's own 403; one who may not see it, the unknown id's 404.
func (s *server) DeleteTimeEntriesById(ctx context.Context, req gen.DeleteTimeEntriesByIdRequestObject) (gen.DeleteTimeEntriesByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	_, a, found, err := s.visibleEntry(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.DeleteTimeEntriesById404Response{}, nil
	}
	if !a.CanEdit {
		return gen.DeleteTimeEntriesById403JSONResponse(forbidden()), nil
	}
	deleted, err := q.DeleteEntry(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("time: delete entry: %w", err)
	}
	if deleted == 0 {
		// Something committed between the read and the delete: a concurrent
		// delete (the entry is gone) or a submit (it is no longer a draft).
		if _, err := q.GetEntry(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
			return gen.DeleteTimeEntriesById404Response{}, nil
		}
		return gen.DeleteTimeEntriesById403JSONResponse(forbidden()), nil
	}
	return gen.DeleteTimeEntriesById204Response{}, nil
}

// visibleEntry loads one entry and the caller's access to it, answering
// found=false both for an unknown id and for an entry the caller may not see,
// so the two can never be told apart.
func (s *server) visibleEntry(ctx context.Context, q *store.Queries, id int64) (store.TimeEntry, entryAccess, bool, error) {
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return store.TimeEntry{}, entryAccess{}, false, err
	}
	return s.visibleEntryFor(ctx, q, c, id)
}

// visibleEntryFor is visibleEntry for a handler that already holds its
// caller.
func (s *server) visibleEntryFor(ctx context.Context, q *store.Queries, c *caller, id int64) (store.TimeEntry, entryAccess, bool, error) {
	row, err := q.GetEntry(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.TimeEntry{}, entryAccess{}, false, nil
	}
	if err != nil {
		return store.TimeEntry{}, entryAccess{}, false, fmt.Errorf("time: get entry: %w", err)
	}
	a, err := s.entryAccess(ctx, c, row)
	if err != nil {
		return store.TimeEntry{}, entryAccess{}, false, err
	}
	if !a.CanSee {
		return store.TimeEntry{}, entryAccess{}, false, nil
	}
	return row, a, true, nil
}

// requestFromUpdate is an update body without its revision: the fields an
// entry stands with, which parseEntry validates the same for both saves.
func requestFromUpdate(body gen.TimeEntryUpdateRequest) gen.TimeEntryRequest {
	return gen.TimeEntryRequest{
		ProjectId:     body.ProjectId,
		BillingLineId: body.BillingLineId,
		TaskId:        body.TaskId,
		EntryDate:     body.EntryDate,
		Hours:         body.Hours,
		StartTime:     body.StartTime,
		EndTime:       body.EndTime,
		Note:          body.Note,
		Billable:      body.Billable,
	}
}

// editable reports whether an entry in status is still its owner's to
// change (D7, D10).
func editable(status string) bool {
	return status == statusDraft || status == statusRejected
}

// PutTimeEntriesById Update a time entry
// (PUT /api/v1/time/entries/{id})
//
// A full replace, by the entry's owner, while it is a draft or rejected and
// not before the lock unless the caller holds time:manage — exactly
// capabilities.canEdit — held to every rule a create is held to, with its
// rates resolved again (D3). A save always leaves a draft: a rejected entry
// returns to draft with its reason cleared.
//
// The refusals come in the order that tells the caller least about what they
// may not touch: an entry they may not see is the unknown id's 404, one they
// may see but not change the access layer's 403, and only then are the body's
// fields judged (400). Inside the transaction the entry's row is locked and
// read again: a submit that committed since the handler read it answers 403,
// an edit that did answers 409 — the status first, because a caller holding a
// stale revision of an entry that has been submitted can do nothing with a
// fresher one either. The day cap is decided last, under the day lock, with
// the entry's own hours left out of the day's sum.
func (s *server) PutTimeEntriesById(ctx context.Context, req gen.PutTimeEntriesByIdRequestObject) (gen.PutTimeEntriesByIdResponseObject, error) {
	body := gen.TimeEntryUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	_, a, found, err := s.visibleEntryFor(ctx, q, c, req.Id)
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.PutTimeEntriesById404Response{}, nil
	}
	if !a.CanEdit {
		return gen.PutTimeEntriesById403JSONResponse(forbidden()), nil
	}

	parsed, errs := parseEntry(requestFromUpdate(body))
	refs, errs, err := s.checkReferences(ctx, c.UserID, parsed, errs)
	if err != nil {
		return nil, err
	}
	if !parsed.Date.IsZero() && c.locked(parsed.Date) && !c.Manage {
		errs = withFieldError(errs, "entryDate", lockedBeforeMessage(*c.LockedBefore))
	}
	if len(errs) > 0 {
		return gen.PutTimeEntriesById400ApplicationProblemPlusJSONResponse(invalidEntry(errs)), nil
	}

	billable := resolveBillable(refs.Project.BillingType, parsed.Billable)
	rates, err := s.resolveRates(ctx, q, rateRequest{
		UserID: c.UserID, Billable: billable, Project: refs.Project, Line: refs.Line, Date: parsed.Date,
	})
	if err != nil {
		return nil, err
	}
	billRate, err := numericFromFloatPtr(rates.BillRate)
	if err != nil {
		return nil, err
	}
	costRate, err := numericFromFloatPtr(rates.CostRate)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock()
	var (
		updated  store.TimeEntry
		gone     bool
		settled  bool   // no longer a draft or rejected
		conflict *int32 // the revision the entry has moved on to
		capMsg   string
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		current, err := txq.LockEntry(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			gone = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("time: lock entry: %w", err)
		}
		switch {
		case !editable(current.Status):
			settled = true
			return nil
		case current.Revision != body.Revision:
			conflict = &current.Revision
			return nil
		}
		id := current.ID
		msg, err := checkDayCap(ctx, txq, c.UserID, parsed.Date, parsed.HoursCents, &id)
		if err != nil {
			return err
		}
		if msg != "" {
			capMsg = msg
			return nil
		}
		updated, err = txq.UpdateEntry(ctx, store.UpdateEntryParams{
			ID:            current.ID,
			Revision:      body.Revision,
			ProjectID:     parsed.ProjectID,
			BillingLineID: parsed.LineID,
			TaskID:        parsed.TaskID,
			TaskTitle:     refs.TaskTitle,
			EntryDate:     pgDate(parsed.Date),
			Hours:         numericFromCents(parsed.HoursCents),
			StartTime:     timeFromMinutes(parsed.Start),
			EndTime:       timeFromMinutes(parsed.End),
			Note:          parsed.Note,
			Billable:      billable,
			BillRate:      billRate,
			BillCurrency:  rates.BillCurrency,
			CostRate:      costRate,
			CostCurrency:  rates.CostCurrency,
			RateSource:    rates.Source,
			Now:           now,
		})
		return err
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("time: update entry: %w", err)
	case gone:
		return gen.PutTimeEntriesById404Response{}, nil
	case settled:
		return gen.PutTimeEntriesById403JSONResponse(forbidden()), nil
	case conflict != nil:
		return gen.PutTimeEntriesById409ApplicationProblemPlusJSONResponse(revisionConflict(*conflict, body.Revision)), nil
	case capMsg != "":
		return gen.PutTimeEntriesById400ApplicationProblemPlusJSONResponse(invalidEntry(fieldError("hours", capMsg))), nil
	}

	a, err = s.entryAccess(ctx, c, updated)
	if err != nil {
		return nil, err
	}
	resp, err := s.entryResponseFor(ctx, updated, a)
	if err != nil {
		return nil, err
	}
	return gen.PutTimeEntriesById200JSONResponse(resp), nil
}

// validateListParams is the list's query rules: the paging, a status from
// the enumeration (a typo is a mistake worth reporting, not a filter that
// matches nothing) and a weekStart that is a Monday.
func validateListParams(p gen.GetTimeEntriesParams) []string {
	errs := validatePageParams(p.Page, p.PageSize)
	if p.Status != nil && *p.Status != "" && !validStatus(*p.Status) {
		errs = append(errs, fmt.Sprintf("'status' must be one of %s, but was '%s'.", statusList(), *p.Status))
	}
	if p.WeekStart != nil {
		if msg := notAMonday(p.WeekStart.Time); msg != "" {
			errs = append(errs, fmt.Sprintf("'weekStart' must be a Monday: %s.", msg))
		}
	}
	return errs
}

// GetTimeEntries List time entries
// (GET /api/v1/time/entries)
//
// One person's entries, the caller's own unless userId names someone else.
// Visibility is the query's first predicate, the same rule entryAccess
// applies to one entry (see_all for time:view-all, time:approve and
// time:manage; the caller's own; the projects the caller manages), so the
// total is the number of entries the caller may see, every page is full but
// the last, and the list never holds an entry its own read answers 404 for.
//
// The projects a caller manages are resolved through the project directory
// before the query (managedProjects) rather than filtered after it, because
// a filter after the fetch would page through rows the caller never sees.
// Naming someone the caller can see none of — no global time permission, and
// managing no project (or not the one filtered on) — is the access layer's
// 403 rather than an empty page: an empty page would say "they logged
// nothing", which the caller cannot know.
func (s *server) GetTimeEntries(ctx context.Context, req gen.GetTimeEntriesRequestObject) (gen.GetTimeEntriesResponseObject, error) {
	p := req.Params
	if msgs := validateListParams(p); len(msgs) > 0 {
		return gen.GetTimeEntries400ApplicationProblemPlusJSONResponse(
			apicommon.Problem(invalidQueryTitle, strings.Join(msgs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)

	q := store.New(s.deps.Pool)
	c, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	userID := c.UserID
	if p.UserId != nil {
		userID = *p.UserId
	}
	managed := []int32{}
	if userID != c.UserID && !c.seesEveryone() {
		if managed, err = c.managedProjects(ctx, s); err != nil {
			return nil, err
		}
		if len(managed) == 0 || (p.ProjectId != nil && !slices.Contains(managed, *p.ProjectId)) {
			return gen.GetTimeEntries403JSONResponse(forbidden()), nil
		}
	}

	var weekStart, weekEndDate pgtype.Date
	if p.WeekStart != nil {
		weekStart, weekEndDate = pgDate(p.WeekStart.Time), pgDate(weekEnd(p.WeekStart.Time))
	}
	// An empty status is no filter, not a filter for the empty string: a
	// frontend that clears its status dropdown sends `status=`.
	status := p.Status
	if status != nil && *status == "" {
		status = nil
	}
	filter := store.CountEntriesParams{
		UserID:            userID,
		SeeAll:            c.seesEveryone(),
		CallerID:          c.UserID,
		ManagedProjectIds: managed,
		WeekStart:         weekStart,
		WeekEnd:           weekEndDate,
		ProjectID:         p.ProjectId,
		Status:            status,
	}
	total, err := q.CountEntries(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("time: count entries: %w", err)
	}
	rows, err := q.ListEntries(ctx, store.ListEntriesParams{
		UserID:            filter.UserID,
		SeeAll:            filter.SeeAll,
		CallerID:          filter.CallerID,
		ManagedProjectIds: filter.ManagedProjectIds,
		WeekStart:         filter.WeekStart,
		WeekEnd:           filter.WeekEnd,
		ProjectID:         filter.ProjectID,
		Status:            filter.Status,
		PageSize:          pageSize,
		PageOffset:        (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("time: list entries: %w", err)
	}

	data, err := s.entryResponses(ctx, c, rows)
	if err != nil {
		return nil, err
	}
	return gen.GetTimeEntries200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}
