package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

// The timeline's event types.
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
	eventLineAdded       = "line-added"
	eventLineChanged     = "line-changed"
	eventLineDeactivated = "line-deactivated"
	eventLineReactivated = "line-reactivated"

	eventMilestoneAdded         = "milestone-added"
	eventMilestoneChanged       = "milestone-changed"
	eventMilestoneRemoved       = "milestone-removed"
	eventMilestoneReady         = "milestone-ready"
	eventMilestonePlanned       = "milestone-planned"
	eventMilestoneInvoiced      = "milestone-invoiced"
	eventMilestoneInvoiceUndone = "milestone-invoice-undone"
	eventMilestoneCancelled     = "milestone-cancelled"
	eventMilestoneReopened      = "milestone-reopened"
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

// The four billing-line entries. Each names the line by its code — the half
// of the trackable code that does not change when the project is renamed —
// and never an amount: a line's pricing is shaped out for a caller who may
// not see financials (D12), and a timeline that had to be shaped per reader
// could not be paged or exported as one thing.
func recordLineAdded(ctx context.Context, q *store.Queries, now time.Time, projectID int32, line store.ProjectsBillingLine, by actor) error {
	payload := map[string]any{"code": line.Code, "fields": lineFields(line)}
	return recordEvent(ctx, q, now, projectID, eventLineAdded, payload, by)
}

// lineFields names what a new line carries, the way line-changed names what
// moved: the variant and the rule always, and whichever amount the rule has.
// It is the same vocabulary a reader of a later line-changed entry sees.
func lineFields(line store.ProjectsBillingLine) []string {
	fields := []string{"variantId", "pricingMode"}
	if line.FixedAmount.Valid {
		fields = append(fields, "fixedAmount")
	}
	if line.DiscountPercent.Valid {
		fields = append(fields, "discountPercent")
	}
	if line.BudgetHours.Valid {
		fields = append(fields, "budgetHours")
	}
	if line.BudgetAmount.Valid {
		fields = append(fields, "budgetAmount")
	}
	return fields
}

// lineDiff is what one change to a line actually did, split the way the
// timeline records it: the fields that moved get one entry naming them, and
// switching the line off or on is its own event — it is the thing a reader of
// the timeline is looking for, not a field among four others.
type lineDiff struct {
	Fields      []string
	Deactivated bool
	Reactivated bool
}

// empty reports whether the change changed nothing worth recording. A change
// that changed nothing still happened — updated_at moves — but the timeline
// records events, and no event occurred.
func (d lineDiff) empty() bool {
	return len(d.Fields) == 0 && !d.Deactivated && !d.Reactivated
}

// diffLines compares a line before and after one change. The field names are
// the contract's camelCase ones, because the timeline is read by the frontend
// that renders those fields, not by SQL.
func diffLines(before, after store.ProjectsBillingLine) (lineDiff, error) {
	d := lineDiff{
		Deactivated: before.Active && !after.Active,
		Reactivated: !before.Active && after.Active,
	}
	if before.Code != after.Code {
		d.Fields = append(d.Fields, "code")
	}
	if before.VariantID != after.VariantID {
		d.Fields = append(d.Fields, "variantId")
	}
	if before.PricingMode != after.PricingMode {
		d.Fields = append(d.Fields, "pricingMode")
	}
	// The amounts are compared through the same conversion the response
	// renders them with, so "changed" means what a reader of the API would
	// call changed — 900 and 900.00 are one number, not two.
	changed, err := numericChanged(before.FixedAmount, after.FixedAmount)
	if err != nil {
		return lineDiff{}, err
	}
	if changed {
		d.Fields = append(d.Fields, "fixedAmount")
	}
	changed, err = numericChanged(before.DiscountPercent, after.DiscountPercent)
	if err != nil {
		return lineDiff{}, err
	}
	if changed {
		d.Fields = append(d.Fields, "discountPercent")
	}
	changed, err = numericChanged(before.BudgetHours, after.BudgetHours)
	if err != nil {
		return lineDiff{}, err
	}
	if changed {
		d.Fields = append(d.Fields, "budgetHours")
	}
	changed, err = numericChanged(before.BudgetAmount, after.BudgetAmount)
	if err != nil {
		return lineDiff{}, err
	}
	if changed {
		d.Fields = append(d.Fields, "budgetAmount")
	}
	return d, nil
}

// recordLineUpdated writes the entries one change to a line owes, in a fixed
// order so a timeline built from several changes at one instant still reads
// the same way every time. A change that only switched the line off or on
// writes that entry alone: `active` is not one of the fields line-changed
// names. It is called inside the change's own transaction.
func recordLineUpdated(ctx context.Context, q *store.Queries, now time.Time, d lineDiff, projectID int32, code string, by actor) error {
	if len(d.Fields) > 0 {
		if err := recordEvent(ctx, q, now, projectID, eventLineChanged,
			map[string]any{"code": code, "fields": d.Fields}, by); err != nil {
			return err
		}
	}
	switch {
	case d.Deactivated:
		return recordEvent(ctx, q, now, projectID, eventLineDeactivated, map[string]any{"code": code}, by)
	case d.Reactivated:
		return recordEvent(ctx, q, now, projectID, eventLineReactivated, map[string]any{"code": code}, by)
	}
	return nil
}

// recordMilestoneEvent writes one of the eight milestone entries (design
// §3.2). The payload is the milestone's id and the name it carried at that
// moment, and never an amount: a milestone is financial data the project's
// members may not see (D12), while the timeline is read by everyone who can
// see the project — so a timeline entry that named a figure would have to be
// shaped per reader, which a paged, exportable history cannot be.
//
// The name is stored rather than resolved on read for the reason every
// timeline payload here stores what it saw: a milestone renamed or deleted
// afterwards must not rewrite what the history says happened.
func recordMilestoneEvent(ctx context.Context, q *store.Queries, now time.Time, eventType string, m store.ProjectsBillingMilestone, by actor) error {
	return recordMilestoneEventWith(ctx, q, now, eventType, m, nil, by)
}

// recordMilestoneEventWith is recordMilestoneEvent for the two entries that
// say something more than "this happened to this milestone": the fields a
// content edit moved, and the flag an invoice-undo sets when it had to turn a
// percent milestone into an amount one. Everything extra is a name or a flag,
// never a value, for the same reason the base payload is.
func recordMilestoneEventWith(ctx context.Context, q *store.Queries, now time.Time, eventType string, m store.ProjectsBillingMilestone, extra map[string]any, by actor) error {
	payload := map[string]any{"milestoneId": m.ID, "name": m.Name}
	maps.Copy(payload, extra)
	return recordEvent(ctx, q, now, m.ProjectID, eventType, payload, by)
}

// milestoneFields names what one content edit actually moved, in the
// contract's camelCase, the way a billing line's line-changed entry names its
// own fields — and never a value: a milestone is financial data a member may
// not see (D12), while the timeline is read by everyone who can see the
// project. An empty answer means nothing moved, and nothing is recorded.
//
// Position and status are deliberately not among them: neither is part of an
// edit (position is the move's, status is the status operation's), and the
// status flow writes its own entries.
func milestoneFields(before, after store.ProjectsBillingMilestone) ([]string, error) {
	var fields []string
	if before.Name != after.Name {
		fields = append(fields, "name")
	}
	if !equalStringPtr(before.Description, after.Description) {
		fields = append(fields, "description")
	}
	if !equalDate(before.PlannedDate, after.PlannedDate) {
		fields = append(fields, "plannedDate")
	}
	// The amounts are compared through the same conversion the response
	// renders them with, so "changed" means what a reader of the API would
	// call changed — 1000 and 1000.00 are one number, not two.
	for _, pair := range []struct {
		name          string
		before, after pgtype.Numeric
	}{
		{"amount", before.Amount, after.Amount},
		{"percent", before.Percent, after.Percent},
	} {
		changed, err := numericChanged(pair.before, pair.after)
		if err != nil {
			return nil, err
		}
		if changed {
			fields = append(fields, pair.name)
		}
	}
	return fields, nil
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
	if d.Billing, err = changedAmount(d.Billing, "defaultBillRate", before.DefaultBillRate, after.DefaultBillRate); err != nil {
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
