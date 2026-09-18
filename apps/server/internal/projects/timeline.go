package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file is the project timeline, both halves (design §3). Every state
// change writes its entry inside the same transaction as the change, so a
// project that exists always has the entry that created it, and a rolled-back
// change leaves no trace of having been attempted. The read is paged, newest
// first, and is visible to anyone who can see the project — the entries carry
// no amounts (D12), so there is nothing on them to shape.

// The timeline's event types. Billing lines add theirs with the operations
// that cause them (Task 10).
const (
	eventProjectCreated  = "project-created"
	eventCodeChanged     = "code-changed"
	eventCustomerChanged = "customer-changed"
	eventBillingChanged  = "billing-changed"
	eventDetailsChanged  = "details-changed"
	eventStatusChanged   = "status-changed"
	eventRoleAdded       = "role-added"
	eventRoleChanged     = "role-changed"
	eventRoleRemoved     = "role-removed"
)

// recordEvent inserts one generated timeline entry. The actor's display name
// is stored, not looked up on read: a timeline is a record of what happened
// and who did it at the time, which a later rename or deletion must not
// rewrite.
func recordEvent(ctx context.Context, q *store.Queries, now time.Time, projectID int32, eventType string, payload any, by actor) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("projects: encode timeline payload: %w", err)
	}
	return q.InsertTimelineEntry(ctx, store.InsertTimelineEntryParams{
		ProjectID:    projectID,
		EventType:    eventType,
		Payload:      encoded,
		ActorUserID:  &by.UserID,
		ActorDisplay: by.Display,
		Now:          now,
	})
}

// recordProjectCreated is the entry every project opens its timeline with.
// Its payload carries the code and name the project was created under, so a
// later rename (D1) is legible against what it replaced — and no amounts,
// which would need the same financial shaping the project itself does (D12).
func recordProjectCreated(ctx context.Context, q *store.Queries, now time.Time, projectID int32, code, name string, by actor) error {
	payload := map[string]any{"code": code, "name": name}
	return recordEvent(ctx, q, now, projectID, eventProjectCreated, payload, by)
}

// recordStatusChanged is D14's own entry. Both statuses are in it: what a
// project came back from matters as much as where it went.
func recordStatusChanged(ctx context.Context, q *store.Queries, now time.Time, projectID int32, from, to string, by actor) error {
	payload := map[string]any{"old": from, "new": to}
	return recordEvent(ctx, q, now, projectID, eventStatusChanged, payload, by)
}

// The three role entries. Each carries the subject's display name as well as
// their id, for the same reason the actor's is stored rather than looked up:
// a timeline says who was put on the project, and an account removed from
// identity afterwards must not blank that out. The subject's name is the one
// the directory gave at the time, or unknownUser when it gave none.
func recordRoleAdded(ctx context.Context, q *store.Queries, now time.Time, projectID int32, subject actor, role string, by actor) error {
	payload := map[string]any{"userId": subject.UserID, "displayName": subject.Display, "role": role}
	return recordEvent(ctx, q, now, projectID, eventRoleAdded, payload, by)
}

func recordRoleChanged(ctx context.Context, q *store.Queries, now time.Time, projectID int32, subject actor, oldRole, newRole string, by actor) error {
	payload := map[string]any{
		"userId": subject.UserID, "displayName": subject.Display,
		"oldRole": oldRole, "newRole": newRole,
	}
	return recordEvent(ctx, q, now, projectID, eventRoleChanged, payload, by)
}

func recordRoleRemoved(ctx context.Context, q *store.Queries, now time.Time, projectID int32, subject actor, role string, by actor) error {
	payload := map[string]any{"userId": subject.UserID, "displayName": subject.Display, "role": role}
	return recordEvent(ctx, q, now, projectID, eventRoleRemoved, payload, by)
}

// projectDiff is what one update actually changed, split the way the
// timeline records it: the code and the customer get their own entries with
// their old and new values, while the financial and the descriptive fields
// get one entry each naming *which* of them moved.
//
// Billing carries names only, never values (D12): amounts on the timeline
// would need the same shaping the project's own financials do, and a
// timeline that had to be shaped per reader could not be cached, paged or
// exported as one thing.
type projectDiff struct {
	OldCode   string
	NewCode   string
	CodeMoved bool

	OldCustomerID *int32
	NewCustomerID *int32
	CustomerMoved bool

	Billing []string
	Details []string
}

// empty reports whether the update changed nothing worth recording. An
// update that changed nothing still happened — the revision moves — but the
// timeline records events, and no event occurred.
func (d projectDiff) empty() bool {
	return !d.CodeMoved && !d.CustomerMoved && len(d.Billing) == 0 && len(d.Details) == 0
}

// diffProjects compares a project before and after one update. The field
// names it collects are the contract's camelCase ones, because the timeline
// is read by the frontend that renders those fields, not by SQL.
func diffProjects(before, after store.ProjectsProject) (projectDiff, error) {
	d := projectDiff{
		OldCode: before.Code, NewCode: after.Code, CodeMoved: before.Code != after.Code,
		OldCustomerID: before.CustomerID, NewCustomerID: after.CustomerID,
		CustomerMoved: !equalInt32Ptr(before.CustomerID, after.CustomerID),
	}

	// The amounts are compared through the same conversion the response
	// renders them with, so "changed" means what a reader of the API would
	// call changed — 1000 and 1000.00 are one number, not two.
	changedAmount := func(fields []string, name string, before, after pgtype.Numeric) ([]string, error) {
		changed, err := numericChanged(before, after)
		if err != nil {
			return nil, err
		}
		if changed {
			return append(fields, name), nil
		}
		return fields, nil
	}

	if before.BillingType != after.BillingType {
		d.Billing = append(d.Billing, "billingType")
	}
	if !equalStringPtr(before.Currency, after.Currency) {
		d.Billing = append(d.Billing, "currency")
	}
	var err error
	if d.Billing, err = changedAmount(d.Billing, "fixedPriceAmount", before.FixedPriceAmount, after.FixedPriceAmount); err != nil {
		return projectDiff{}, err
	}
	if d.Billing, err = changedAmount(d.Billing, "budgetAmount", before.BudgetAmount, after.BudgetAmount); err != nil {
		return projectDiff{}, err
	}

	if before.Name != after.Name {
		d.Details = append(d.Details, "name")
	}
	if !equalStringPtr(before.Description, after.Description) {
		d.Details = append(d.Details, "description")
	}
	if !equalDate(before.StartDate, after.StartDate) {
		d.Details = append(d.Details, "startDate")
	}
	if !equalDate(before.EndDate, after.EndDate) {
		d.Details = append(d.Details, "endDate")
	}
	if d.Details, err = changedAmount(d.Details, "budgetHours", before.BudgetHours, after.BudgetHours); err != nil {
		return projectDiff{}, err
	}
	return d, nil
}

// recordProjectUpdated writes the entries one update owes, in a fixed order
// so a timeline built from several changes at one instant still reads the
// same way every time. It is called inside the update's own transaction.
func recordProjectUpdated(ctx context.Context, q *store.Queries, now time.Time, d projectDiff, projectID int32, by actor) error {
	if d.CodeMoved {
		if err := recordEvent(ctx, q, now, projectID, eventCodeChanged,
			map[string]any{"old": d.OldCode, "new": d.NewCode}, by); err != nil {
			return err
		}
	}
	if d.CustomerMoved {
		if err := recordEvent(ctx, q, now, projectID, eventCustomerChanged,
			map[string]any{"oldCustomerId": d.OldCustomerID, "newCustomerId": d.NewCustomerID}, by); err != nil {
			return err
		}
	}
	if len(d.Billing) > 0 {
		if err := recordEvent(ctx, q, now, projectID, eventBillingChanged,
			map[string]any{"fields": d.Billing}, by); err != nil {
			return err
		}
	}
	if len(d.Details) > 0 {
		if err := recordEvent(ctx, q, now, projectID, eventDetailsChanged,
			map[string]any{"fields": d.Details}, by); err != nil {
			return err
		}
	}
	return nil
}

// GetProjectsByIdTimeline List a project's timeline
// (GET /api/v1/projects/{id}/timeline)
//
// Newest first: a project's timeline is read from the top, and the create
// entry is the one thing on it whose position never has to be hunted for.
// The project is loaded before the paging is validated so that an outsider
// sending a bad page still gets the 404 that tells them nothing (D7).
func (s *server) GetProjectsByIdTimeline(ctx context.Context, req gen.GetProjectsByIdTimelineRequestObject) (gen.GetProjectsByIdTimelineResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetProject(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetProjectsByIdTimeline404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: get project: %w", err)
	}
	a, err := s.authorize(ctx, q, row.ID)
	if err != nil {
		return nil, err
	}
	if !a.CanSee {
		return gen.GetProjectsByIdTimeline404Response{}, nil
	}

	if msgs := validatePageParams(req.Params.Page, req.Params.PageSize); len(msgs) > 0 {
		return gen.GetProjectsByIdTimeline400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid query parameters", strings.Join(msgs, " "))), nil
	}
	page, pageSize := pageParams(req.Params.Page, req.Params.PageSize)

	total, err := q.CountTimelineEntries(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("projects: count timeline entries: %w", err)
	}
	entries, err := q.ListTimelineEntries(ctx, store.ListTimelineEntriesParams{
		ProjectID: req.Id, PageSize: pageSize, PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("projects: list timeline entries: %w", err)
	}

	data := make([]gen.TimelineEntryResponse, 0, len(entries))
	for _, e := range entries {
		entry, err := timelineEntryResponse(e)
		if err != nil {
			return nil, err
		}
		data = append(data, entry)
	}
	return gen.GetProjectsByIdTimeline200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}
