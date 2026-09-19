package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// This file turns a stored project row into the contract's ProjectResponse,
// including D12's financial shaping: the money is not forbidden to a caller
// who may not see it, it is simply not in their copy of the resource.

// projectResponse projects one row for one caller. financials is set exactly
// when the caller may see the project's money — and then always set, even
// with nothing in it, so a client can tell "may see, nothing entered" from
// "may not see" (D12). budgetHours is deliberately outside it: hours are
// planning data everyone who sees the project may see.
func projectResponse(row store.ProjectsProject, a access, customerName *string, managers []gen.ProjectPersonSummary, linesAvailable bool) (gen.ProjectResponse, error) {
	budgetHours, err := floatPtrFromNumeric(row.BudgetHours)
	if err != nil {
		return gen.ProjectResponse{}, err
	}
	resp := gen.ProjectResponse{
		Id:           row.ID,
		Code:         row.Code,
		Name:         row.Name,
		Description:  row.Description,
		CustomerId:   row.CustomerID,
		CustomerName: customerName,
		Internal:     row.CustomerID == nil,
		Status:       row.Status,
		StartDate:    dateFromPgtype(row.StartDate),
		EndDate:      dateFromPgtype(row.EndDate),
		BillingType:  row.BillingType,
		BudgetHours:  budgetHours,
		Revision:     row.Revision,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		Managers:     managers,
		Capabilities: gen.ProjectCapabilities{
			CanManage:           a.CanManage,
			CanContribute:       a.CanContribute,
			CanSeeFinancials:    a.CanSeeFinancials,
			CanManageMilestones: a.canManageMilestones(),
			CanSeeCosts:         a.canSeeCosts(),
		},
		BillingLinesAvailable: linesAvailable,
	}
	if a.CanSeeFinancials {
		fixedPrice, err := floatPtrFromNumeric(row.FixedPriceAmount)
		if err != nil {
			return gen.ProjectResponse{}, err
		}
		budgetAmount, err := floatPtrFromNumeric(row.BudgetAmount)
		if err != nil {
			return gen.ProjectResponse{}, err
		}
		defaultBillRate, err := floatPtrFromNumeric(row.DefaultBillRate)
		if err != nil {
			return gen.ProjectResponse{}, err
		}
		resp.Financials = &gen.ProjectFinancials{
			Currency:         row.Currency,
			FixedPriceAmount: fixedPrice,
			BudgetAmount:     budgetAmount,
			DefaultBillRate:  defaultBillRate,
		}
	}
	return resp, nil
}

// projectSummaryResponse is one list row. It is not projectResponse with
// fields removed: the summary schema has no financial fields at all, so
// there is nothing here to shape and no caller who could be shown a price by
// mistake. capabilities and billingLinesAvailable are left out for the same
// reason — they are answers about one project the caller opened, not about a
// row in a table.
func projectSummaryResponse(row store.ProjectsProject, customerName *string, managers []gen.ProjectPersonSummary) (gen.ProjectSummaryResponse, error) {
	budgetHours, err := floatPtrFromNumeric(row.BudgetHours)
	if err != nil {
		return gen.ProjectSummaryResponse{}, err
	}
	return gen.ProjectSummaryResponse{
		Id:           row.ID,
		Code:         row.Code,
		Name:         row.Name,
		CustomerId:   row.CustomerID,
		CustomerName: customerName,
		Internal:     row.CustomerID == nil,
		Status:       row.Status,
		StartDate:    dateFromPgtype(row.StartDate),
		EndDate:      dateFromPgtype(row.EndDate),
		BillingType:  row.BillingType,
		BudgetHours:  budgetHours,
		Revision:     row.Revision,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		Managers:     managers,
	}, nil
}

// trackableCode is the pair a later module quotes a line by — the project's
// code and the line's, joined with a hyphen (D2/D9). It is computed from the
// project as it stands rather than stored beside the line, so renaming a
// project (D1) moves every one of its lines' trackable codes with it; neither
// code may contain a hyphen, so the pair always splits cleanly.
func trackableCode(projectCode, lineCode string) string {
	return projectCode + "-" + lineCode
}

// catalogOutage is one request's failure to read the product catalog while
// rendering billing lines. A catalog that cannot answer never fails the
// request (the ruling behind `catalogUnavailable`): the lines it could not
// supply fields for come back without them and say so, and the outage itself
// is logged once for the request rather than once per line, because a log
// filled with a line per row buries the very thing it is reporting.
//
// It deliberately only covers *rendering*. A catalog error while deciding
// whether a variant a write is moving a line to exists is still fatal to that
// write (variantExists, lines.go): degrading an answer is honest, degrading a
// decision is not.
type catalogOutage struct{ err error }

// note records a failed catalog call. The first error is the one kept: they
// are all the same outage, and one of them is enough to name it.
func (o *catalogOutage) note(err error) {
	if o.err == nil {
		o.err = err
	}
}

// down reports whether anything this request asked the catalog went
// unanswered.
func (o *catalogOutage) down() bool { return o.err != nil }

// warn logs the request's one warning, and nothing at all when the catalog
// answered everything.
func (o *catalogOutage) warn(ctx context.Context, logger *slog.Logger, projectID int32) {
	if !o.down() {
		return
	}
	logger.WarnContext(ctx, "projects: the product catalog could not be read; billing lines are rendered without it",
		"project_id", projectID, "error", o.err.Error())
}

// billingLineResponses renders a project's lines for one caller. The variant
// details every line embeds (D15) are resolved in one catalog call for the
// whole list rather than one per line; a variant the catalog no longer knows
// is simply absent from its answer, which is what variantMissing reports.
// Deps.Products is never nil here: the handlers answer 409 first (D10).
//
// A catalog that *errors* is a different thing from one that answers "I do
// not know this variant", and is not this read's failure: the lines come back
// without the fields it would have supplied, flagged catalogUnavailable, and
// the outage is logged once. Nothing about the lines themselves depends on it.
func (s *server) billingLineResponses(ctx context.Context, project store.ProjectsProject, a access, rows []store.ProjectsBillingLine) ([]gen.BillingLineResponse, error) {
	ids := make([]int32, 0, len(rows))
	seen := make(map[int32]bool, len(rows))
	for _, row := range rows {
		if !seen[row.VariantID] {
			seen[row.VariantID] = true
			ids = append(ids, row.VariantID)
		}
	}
	outage := &catalogOutage{}
	variants := make(map[int32]contracts.VariantEntry, len(ids))
	if len(ids) > 0 {
		found, err := s.deps.Products.Variants(ctx, ids)
		if err != nil {
			outage.note(fmt.Errorf("projects: resolve the lines' variants: %w", err))
		}
		for _, v := range found {
			variants[v.ID] = v
		}
	}
	// Whether the *names* could be read is decided once, for the whole list,
	// before any line is rendered: one failed Variants call is every line's
	// missing name, and a line must not be told its variant is gone because
	// nobody was there to say otherwise.
	namesUnavailable := outage.down()

	data := make([]gen.BillingLineResponse, 0, len(rows))
	for _, row := range rows {
		line, err := s.billingLineResponse(ctx, project, a, row, variants, namesUnavailable, outage)
		if err != nil {
			return nil, err
		}
		data = append(data, line)
	}
	outage.warn(ctx, s.deps.Logger, project.ID)
	return data, nil
}

// billingLineResponseFor is billingLineResponses for the single line a create
// or a change answers with, so one line is rendered by exactly the code that
// renders a list of them.
func (s *server) billingLineResponseFor(ctx context.Context, project store.ProjectsProject, a access, row store.ProjectsBillingLine) (gen.BillingLineResponse, error) {
	data, err := s.billingLineResponses(ctx, project, a, []store.ProjectsBillingLine{row})
	if err != nil {
		return gen.BillingLineResponse{}, err
	}
	return data[0], nil
}

// billingLineResponse projects one line for one caller. pricing is set
// exactly when the caller may see the project's money (D12) — the same
// shaping the project's own financials get, for the same reason: the rates a
// project bills at are not a member's business. budgetHours sits outside
// that shaping, like the project's own budgetHours: it is planning data,
// visible with the line regardless of financial rights. budgetAmount is the
// opposite — an amount, so it rides inside pricing with the rest of them.
//
// namesUnavailable says the catalog could not be asked for this list's variant
// names at all, which is why variantMissing is then left false: "gone from the
// catalog" is an answer the catalog gave, and there was none.
func (s *server) billingLineResponse(ctx context.Context, project store.ProjectsProject, a access, row store.ProjectsBillingLine, variants map[int32]contracts.VariantEntry, namesUnavailable bool, outage *catalogOutage) (gen.BillingLineResponse, error) {
	budgetHours, err := floatPtrFromNumeric(row.BudgetHours)
	if err != nil {
		return gen.BillingLineResponse{}, err
	}
	resp := gen.BillingLineResponse{
		Id:                 row.ID,
		Code:               row.Code,
		TrackableCode:      trackableCode(project.Code, row.Code),
		VariantId:          row.VariantID,
		Active:             row.Active,
		BudgetHours:        budgetHours,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
		CatalogUnavailable: namesUnavailable,
	}
	// A line whose variant products has since dropped keeps resolving, so
	// that work already billed against it stays readable; it says its variant
	// is gone rather than inventing a name for it.
	if variant, ok := variants[row.VariantID]; ok {
		resp.ProductName = &variant.ProductName
		resp.Sku = &variant.SKU
		resp.Unit = &variant.Unit
	} else if !namesUnavailable {
		resp.VariantMissing = true
	}
	if a.CanSeeFinancials {
		pricing, priced, err := s.linePricing(ctx, project, row, outage)
		if err != nil {
			return gen.BillingLineResponse{}, err
		}
		resp.Pricing = pricing
		if !priced {
			resp.CatalogUnavailable = true
		}
	}
	return resp, nil
}

// linePricing is one line's pricing block: the stored rule, plus the
// variant's list price in the project's currency at this moment. The list
// price is resolved rather than stored — a price list the products module
// changes tomorrow must show through here — and it is simply absent when the
// project has no currency (D13) or the variant has no price in it. Projects
// never applies the rule to the price: that is the invoice's arithmetic, not
// this module's.
//
// The second return says whether the list price was actually resolved: false
// only when the catalog could not answer, which is the line's own
// catalogUnavailable and never the request's failure. The stored rule —
// mode, fixed amount, discount, budget — is read off the row and owes the
// catalog nothing, so it comes back either way.
func (s *server) linePricing(ctx context.Context, project store.ProjectsProject, row store.ProjectsBillingLine, outage *catalogOutage) (*gen.BillingLinePricing, bool, error) {
	fixedAmount, err := floatPtrFromNumeric(row.FixedAmount)
	if err != nil {
		return nil, false, err
	}
	discountPercent, err := floatPtrFromNumeric(row.DiscountPercent)
	if err != nil {
		return nil, false, err
	}
	budgetAmount, err := floatPtrFromNumeric(row.BudgetAmount)
	if err != nil {
		return nil, false, err
	}
	pricing := &gen.BillingLinePricing{
		Mode:            row.PricingMode,
		FixedAmount:     fixedAmount,
		DiscountPercent: discountPercent,
		BudgetAmount:    budgetAmount,
	}
	if project.Currency == nil {
		return pricing, true, nil
	}
	price, err := s.deps.Products.ListPrice(ctx, row.VariantID, *project.Currency, s.deps.Clock())
	if err != nil {
		outage.note(fmt.Errorf("projects: resolve a variant's list price: %w", err))
		return pricing, false, nil
	}
	if price != nil {
		pricing.ListPrice = &gen.BillingLineListPrice{Amount: price.Amount, Currency: price.Currency}
	}
	return pricing, true, nil
}

// milestonePlanResponse is a project's whole invoice plan for one caller: the
// milestones in the order the query answered — position, cancelled last — and
// what they add up to (design §3.2). Nothing here is shaped per caller the
// way a line's pricing is: every milestone operation already needs financial
// rights, so a caller who gets an answer at all may see all of it.
//
// The stamps' users are named in one directory call for the whole plan rather
// than one per milestone, the shape taskAssignees gives a tree of tasks — and
// it is made here, outside any transaction, because a contracts directory is
// another module's pool and must never be read under this module's locks.
func (s *server) milestonePlanResponse(ctx context.Context, project store.ProjectsProject, a access, rows []store.ProjectsBillingMilestone) (gen.BillingMilestonePlanResponse, error) {
	people, err := s.milestonePeople(ctx, rows)
	if err != nil {
		return gen.BillingMilestonePlanResponse{}, err
	}
	now := s.deps.Clock()
	effective, totals, err := s.milestonePlanTotals(ctx, project, rows)
	if err != nil {
		return gen.BillingMilestonePlanResponse{}, err
	}
	data := make([]gen.BillingMilestoneResponse, 0, len(rows))
	for i, row := range rows {
		milestone, err := milestoneResponse(row, project, a, people, now, effective[i])
		if err != nil {
			return gen.BillingMilestonePlanResponse{}, err
		}
		data = append(data, milestone)
	}
	return gen.BillingMilestonePlanResponse{Milestones: data, Totals: totals}, nil
}

// milestonePlanTotals is what a set of a project's milestones adds up to,
// with each row's own effective amount alongside so the plan renders exactly
// the numbers it summed. The invoice plan and the economy read both answer
// from here, which is what makes it impossible for the two endpoints to
// disagree about what a project has planned, ready and invoiced.
//
// The three sums are accumulated in exact decimal and rounded once, at the
// end (milestoneTotals): adding money in float64 answers 0.10 + 0.20 with
// 0.30000000000000004, and this module's money rule is exact decimal.
func (s *server) milestonePlanTotals(ctx context.Context, project store.ProjectsProject, rows []store.ProjectsBillingMilestone) ([]*float64, gen.BillingMilestonePlanTotals, error) {
	amounts := map[string]*big.Rat{}
	effective := make([]*float64, len(rows))
	for i, row := range rows {
		amount, err := s.milestoneEffective(ctx, row, project)
		if err != nil {
			return nil, gen.BillingMilestonePlanTotals{}, err
		}
		effective[i] = amount
		if amount == nil {
			continue
		}
		sum, ok := amounts[row.Status]
		if !ok {
			sum = new(big.Rat)
			amounts[row.Status] = sum
		}
		sum.Add(sum, exactCents(*amount))
	}
	totals, err := milestoneTotals(project, amounts)
	if err != nil {
		return nil, gen.BillingMilestonePlanTotals{}, err
	}
	return effective, totals, nil
}

// milestoneResponseFor is one milestone rendered the way a plan of them is,
// so a create, an edit, a move and a status change all answer through exactly
// the code a read does.
func (s *server) milestoneResponseFor(ctx context.Context, project store.ProjectsProject, a access, row store.ProjectsBillingMilestone) (gen.BillingMilestoneResponse, error) {
	people, err := s.milestonePeople(ctx, []store.ProjectsBillingMilestone{row})
	if err != nil {
		return gen.BillingMilestoneResponse{}, err
	}
	effective, err := s.milestoneEffective(ctx, row, project)
	if err != nil {
		return gen.BillingMilestoneResponse{}, err
	}
	return milestoneResponse(row, project, a, people, s.deps.Clock(), effective)
}

// milestoneEffective is one milestone's effective amount as every surface
// reports it, and nil for a milestone nobody can price.
//
// A milestone nobody can price renders without an amount rather than with
// 0.00 (which would read as a milestone somebody planned at nothing) and
// rather than failing the whole read: one row in a state the module's own
// rules forbid must not take the plan down with it. The expected case — a
// cancelled percent milestone on a project that has since left fixed-price
// billing — is legitimate and silent; anything else is a row that should not
// exist, so it is logged at warning, once per read of it.
func (s *server) milestoneEffective(ctx context.Context, m store.ProjectsBillingMilestone, project store.ProjectsProject) (*float64, error) {
	switch amount, err := milestoneEffectiveAmount(m, project); {
	case errors.Is(err, errMilestoneUnpriced):
		if m.Status != milestoneStatusCancelled {
			s.deps.Logger.WarnContext(ctx, "projects: a billing milestone cannot be priced",
				"milestone_id", m.ID, "project_id", m.ProjectID, "status", m.Status, "error", err.Error())
		}
		return nil, nil
	case err != nil:
		return nil, err
	default:
		return &amount, nil
	}
}

// milestonePeople names everyone a set of milestones was marked ready or
// invoiced by, in one directory call for the whole set.
func (s *server) milestonePeople(ctx context.Context, rows []store.ProjectsBillingMilestone) (map[uuid.UUID]contracts.UserEntry, error) {
	ids := make([]uuid.UUID, 0, 2*len(rows))
	seen := make(map[uuid.UUID]bool, 2*len(rows))
	for _, row := range rows {
		for _, id := range []*uuid.UUID{row.ReadyByUserID, row.InvoicedByUserID} {
			if id == nil || seen[*id] {
				continue
			}
			seen[*id] = true
			ids = append(ids, *id)
		}
	}
	return s.userEntries(ctx, ids)
}

// milestoneResponse projects one milestone. Two of its fields are computed
// rather than stored: the effective amount — which is what makes an open
// percent milestone follow the project's fixed price (design §3.2), and which
// is passed in rather than computed here so that a row carries exactly the
// number milestonePlanTotals summed — and overdue, which is the server's own
// date against the planned one.
//
// currency is the milestone's own when it carries a flat amount — the one it
// was entered in, which a cancelled milestone can carry past a change to the
// project's — and the project's current one when it carries a percent, since
// a percent resolves against a fixed price that is always in that currency.
// It is absent only for a percent milestone on a project that has no currency
// at all, which again only a cancelled one can outlive.
func milestoneResponse(m store.ProjectsBillingMilestone, project store.ProjectsProject, a access, people map[uuid.UUID]contracts.UserEntry, now time.Time, effective *float64) (gen.BillingMilestoneResponse, error) {
	amount, err := floatPtrFromNumeric(m.Amount)
	if err != nil {
		return gen.BillingMilestoneResponse{}, err
	}
	percent, err := floatPtrFromNumeric(m.Percent)
	if err != nil {
		return gen.BillingMilestoneResponse{}, err
	}
	return gen.BillingMilestoneResponse{
		Id:               m.ID,
		ProjectId:        m.ProjectID,
		Name:             m.Name,
		Description:      m.Description,
		PlannedDate:      dateFromPgtype(m.PlannedDate),
		Amount:           amount,
		Percent:          percent,
		EffectiveAmount:  effective,
		Currency:         milestoneCurrency(m, project),
		Status:           m.Status,
		Position:         m.Position,
		Overdue:          milestoneOverdue(m, now),
		ReadyAt:          m.ReadyAt,
		ReadyBy:          milestonePerson(m.ReadyByUserID, people),
		InvoicedAt:       m.InvoicedAt,
		InvoicedBy:       milestonePerson(m.InvoicedByUserID, people),
		InvoiceReference: m.InvoiceReference,
		InvoiceDate:      dateFromPgtype(m.InvoiceDate),
		Revision:         m.Revision,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
		Capabilities:     milestoneCapabilities(m, project, a),
	}, nil
}

// milestoneCurrency is what the milestone's numbers are denominated in: its
// own stamp when it carries a flat amount, and the project's current currency
// otherwise, because a percent resolves against a fixed price that is always
// in it. The two can only differ on a cancelled milestone — the project's
// currency guard (design §3.3) exempts nothing else — and that is exactly the
// case the stamp exists for.
func milestoneCurrency(m store.ProjectsBillingMilestone, project store.ProjectsProject) *string {
	if m.AmountCurrency != nil {
		return m.AmountCurrency
	}
	return project.Currency
}

// milestonePerson names one stamp's user, nil when there is no stamp. An id
// the directory no longer knows still gets an entry — unknownUser, inactive —
// so the record of who marked a milestone survives the account that did it,
// exactly as a task's assignee and a comment's author do.
func milestonePerson(id *uuid.UUID, people map[uuid.UUID]contracts.UserEntry) *gen.BillingMilestonePerson {
	if id == nil {
		return nil
	}
	entry, ok := people[*id]
	if !ok {
		entry = contracts.UserEntry{ID: *id, DisplayName: unknownUser}
	}
	return &gen.BillingMilestonePerson{UserId: *id, DisplayName: entry.DisplayName, Active: entry.Active}
}

// taskRow is the shape every query that reads a task for the API answers in:
// the stored row plus the two aggregates the contract embeds. sqlc emits a
// distinct row type per query even when the selected columns are identical,
// so each call site converts its own row into this one and taskResponse does
// the single-written mapping — the same one-shared-conversion pattern
// directoryProjectRow uses on the read side of the directory.
type taskRow struct {
	Task           store.ProjectsTask
	ChecklistTotal int32
	ChecklistDone  int32
	CommentCount   int32
}

// countedTaskRows, taskRowsOf and taskRowOf convert the three queries that
// carry the aggregates. newTaskRow is the fourth case: a task that was just
// inserted, which has no checklist items and no comments yet because nothing
// has had the chance to add any.
func countedTaskRows(rows []store.ListProjectTasksRow) []taskRow {
	out := make([]taskRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, taskRow{
			Task: r.ProjectsTask, ChecklistTotal: r.ChecklistTotal,
			ChecklistDone: r.ChecklistDone, CommentCount: r.CommentCount,
		})
	}
	return out
}

func taskRowsOf(rows []store.ListTaskWithSubtasksRow) []taskRow {
	out := make([]taskRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, taskRow{
			Task: r.ProjectsTask, ChecklistTotal: r.ChecklistTotal,
			ChecklistDone: r.ChecklistDone, CommentCount: r.CommentCount,
		})
	}
	return out
}

func taskRowOf(row store.GetTaskWithCountsRow) taskRow {
	return taskRow{
		Task: row.ProjectsTask, ChecklistTotal: row.ChecklistTotal,
		ChecklistDone: row.ChecklistDone, CommentCount: row.CommentCount,
	}
}

func newTaskRow(task store.ProjectsTask) taskRow { return taskRow{Task: task} }

// taskResponse projects one task. The assignee is embedded rather than named
// by id alone (design §4.1), and an id the directory no longer knows still
// gets an entry — unknownUser, inactive — so an assignment survives the
// account it points at, exactly as a project role does.
func taskResponse(row taskRow, assignees map[uuid.UUID]contracts.UserEntry) (gen.TaskResponse, error) {
	estimateHours, err := floatPtrFromNumeric(row.Task.EstimateHours)
	if err != nil {
		return gen.TaskResponse{}, err
	}
	resp := gen.TaskResponse{
		Id:            row.Task.ID,
		ProjectId:     row.Task.ProjectID,
		ParentTaskId:  row.Task.ParentTaskID,
		Title:         row.Task.Title,
		Description:   row.Task.Description,
		Status:        row.Task.Status,
		StartDate:     dateFromPgtype(row.Task.StartDate),
		DueDate:       dateFromPgtype(row.Task.DueDate),
		EstimateHours: estimateHours,
		Position:      row.Task.Position,
		CompletedAt:   row.Task.CompletedAt,
		Revision:      row.Task.Revision,
		CreatedAt:     row.Task.CreatedAt,
		UpdatedAt:     row.Task.UpdatedAt,
		Checklist:     gen.TaskChecklistProgress{Total: row.ChecklistTotal, Done: row.ChecklistDone},
		CommentCount:  row.CommentCount,
	}
	if id := row.Task.AssigneeUserID; id != nil {
		entry, ok := assignees[*id]
		if !ok {
			entry = contracts.UserEntry{ID: *id, DisplayName: unknownUser}
		}
		resp.Assignee = &gen.TaskAssignee{UserId: *id, DisplayName: entry.DisplayName, Active: entry.Active}
	}
	return resp, nil
}

// taskTree assembles the tree the project's task list answers with: every
// top-level task in the order the query returned, each carrying its own
// subtasks. A subtask whose parent is not in rows — which only a filtered read
// can produce — is answered at the top level instead of being dropped, so a
// filter never hides a task it actually matched; it still carries its
// parentTaskId, so a client can tell the two apart.
func taskTree(rows []taskRow, assignees map[uuid.UUID]contracts.UserEntry) ([]gen.TaskResponse, error) {
	present := make(map[int32]bool, len(rows))
	for _, row := range rows {
		present[row.Task.ID] = true
	}
	// The tree is built by index rather than by pointer because a subtask is
	// appended to a parent that is already in the slice, and appending to the
	// slice may move it.
	top := make([]gen.TaskResponse, 0, len(rows))
	at := make(map[int32]int, len(rows))
	for _, row := range rows {
		task, err := taskResponse(row, assignees)
		if err != nil {
			return nil, err
		}
		parent := row.Task.ParentTaskID
		if parent != nil && present[*parent] {
			if i, ok := at[*parent]; ok {
				subtasks := append(subtasksOf(top[i]), task)
				top[i].Subtasks = &subtasks
				continue
			}
		}
		at[task.Id] = len(top)
		top = append(top, task)
	}
	return top, nil
}

// subtasksOf is the parent's subtask slice as it stands, empty when the
// contract's optional array is still absent.
func subtasksOf(task gen.TaskResponse) []gen.TaskResponse {
	if task.Subtasks == nil {
		return nil
	}
	return *task.Subtasks
}

// myTaskResponse is one row of the cross-project list: a task plus the
// project it belongs to, named. It is built from taskResponse so that the two
// shapes can never drift on a field they share — the only thing my-tasks adds
// is the project, and the only thing it drops is the subtasks, which a flat
// list across projects has nothing to do with.
func myTaskResponse(row taskRow, projectCode, projectName string, assignees map[uuid.UUID]contracts.UserEntry) (gen.MyTaskResponse, error) {
	task, err := taskResponse(row, assignees)
	if err != nil {
		return gen.MyTaskResponse{}, err
	}
	return gen.MyTaskResponse{
		Id:            task.Id,
		ProjectId:     task.ProjectId,
		ProjectCode:   projectCode,
		ProjectName:   projectName,
		ParentTaskId:  task.ParentTaskId,
		Title:         task.Title,
		Description:   task.Description,
		Status:        task.Status,
		Assignee:      task.Assignee,
		StartDate:     task.StartDate,
		DueDate:       task.DueDate,
		EstimateHours: task.EstimateHours,
		Position:      task.Position,
		CompletedAt:   task.CompletedAt,
		Revision:      task.Revision,
		CreatedAt:     task.CreatedAt,
		UpdatedAt:     task.UpdatedAt,
		Checklist:     task.Checklist,
		CommentCount:  task.CommentCount,
	}, nil
}

// taskAssignees names every user a set of tasks is assigned to, in one
// directory call for the whole set rather than one per task.
func (s *server) taskAssignees(ctx context.Context, rows []taskRow) (map[uuid.UUID]contracts.UserEntry, error) {
	ids := make([]uuid.UUID, 0, len(rows))
	seen := make(map[uuid.UUID]bool, len(rows))
	for _, row := range rows {
		id := row.Task.AssigneeUserID
		if id == nil || seen[*id] {
			continue
		}
		seen[*id] = true
		ids = append(ids, *id)
	}
	return s.userEntries(ctx, ids)
}

// checklistItemResponse projects one checklist item. There is nothing to
// resolve and nothing to shape: an item is a line of text, a tick box and the
// place it sits in, and the counts a task carries are aggregates over exactly
// these rows.
func checklistItemResponse(row store.ProjectsTaskChecklistItem) gen.ChecklistItemResponse {
	return gen.ChecklistItemResponse{
		Id:       row.ID,
		Text:     row.Text,
		Done:     row.Done,
		Position: row.Position,
	}
}

// checklistItemResponses renders a whole checklist, in the order the query
// answered. The slice is never nil: the contract answers an array, and an
// omitted one would decode as null.
func checklistItemResponses(rows []store.ProjectsTaskChecklistItem) []gen.ChecklistItemResponse {
	out := make([]gen.ChecklistItemResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, checklistItemResponse(row))
	}
	return out
}

// commentResponse projects one comment. The author is embedded rather than
// named by id alone, and an id the directory no longer knows still gets an
// entry — unknownUser, inactive — so a comment survives the account that wrote
// it, exactly as a task's assignee survives the account it points at.
func commentResponse(row store.ProjectsTaskComment, authors map[uuid.UUID]contracts.UserEntry) gen.CommentResponse {
	entry, ok := authors[row.AuthorUserID]
	if !ok {
		entry = contracts.UserEntry{ID: row.AuthorUserID, DisplayName: unknownUser}
	}
	return gen.CommentResponse{
		Id:        row.ID,
		Author:    gen.CommentAuthor{UserId: row.AuthorUserID, DisplayName: entry.DisplayName, Active: entry.Active},
		Body:      row.Body,
		CreatedAt: row.CreatedAt,
		EditedAt:  row.EditedAt,
	}
}

// commentResponses renders one page of comments with every author named in
// one directory call for the whole page rather than one per comment — the
// same shape taskAssignees gives a tree of tasks.
func (s *server) commentResponses(ctx context.Context, rows []store.ProjectsTaskComment) ([]gen.CommentResponse, error) {
	ids := make([]uuid.UUID, 0, len(rows))
	seen := make(map[uuid.UUID]bool, len(rows))
	for _, row := range rows {
		if seen[row.AuthorUserID] {
			continue
		}
		seen[row.AuthorUserID] = true
		ids = append(ids, row.AuthorUserID)
	}
	authors, err := s.userEntries(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]gen.CommentResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, commentResponse(row, authors))
	}
	return out, nil
}

// commentResponseFor is commentResponses for the single comment a write
// answers with, so one comment is rendered by exactly the code that renders a
// page of them.
func (s *server) commentResponseFor(ctx context.Context, row store.ProjectsTaskComment) (gen.CommentResponse, error) {
	data, err := s.commentResponses(ctx, []store.ProjectsTaskComment{row})
	if err != nil {
		return gen.CommentResponse{}, err
	}
	return data[0], nil
}

// timelineEntryResponse renders one stored entry. The payload is decoded
// from the jsonb column rather than passed through as text, so the contract
// answers a JSON object and not a string containing one; a payload that
// cannot be decoded is an error rather than an empty object, because an
// entry whose payload is silently dropped is worse than no answer.
func timelineEntryResponse(e store.ProjectsTimelineEntry) (gen.TimelineEntryResponse, error) {
	payload := map[string]any{}
	if len(e.Payload) > 0 {
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return gen.TimelineEntryResponse{}, fmt.Errorf("projects: decode timeline payload: %w", err)
		}
	}
	return gen.TimelineEntryResponse{
		Id:           e.ID,
		EventType:    e.EventType,
		Payload:      payload,
		ActorUserId:  e.ActorUserID,
		ActorDisplay: e.ActorDisplay,
		OccurredAt:   e.OccurredAt,
	}, nil
}

// projectResponseFor is projectResponse with the two lookups it needs made
// first: the project's customer name, through contracts.CustomerDirectory,
// and its managers' display names, through contracts.UserDirectory. Both
// live in other modules' schemas, so neither can be a join.
func (s *server) projectResponseFor(ctx context.Context, q *store.Queries, row store.ProjectsProject, a access) (gen.ProjectResponse, error) {
	customerName, err := s.customerName(ctx, row.CustomerID)
	if err != nil {
		return gen.ProjectResponse{}, err
	}
	managers, err := s.managers(ctx, q, row.ID)
	if err != nil {
		return gen.ProjectResponse{}, err
	}
	return projectResponse(row, a, customerName, managers, s.billingLinesAvailable())
}

// customerName resolves a project's customer for display. An internal
// project has none; so, as far as this module can tell, does a project whose
// customer the directory no longer knows — a name it cannot resolve is left
// out rather than invented. Deps.Directory is never nil here: config refuses
// to start with projects enabled and customers disabled.
func (s *server) customerName(ctx context.Context, customerID *int32) (*string, error) {
	if customerID == nil {
		return nil, nil
	}
	customer, err := s.deps.Directory.Customer(ctx, *customerID)
	if err != nil {
		return nil, fmt.Errorf("projects: look up customer: %w", err)
	}
	if customer == nil {
		return nil, nil
	}
	return &customer.Name, nil
}

// managers lists a project's managers, oldest assignment first, each named
// through the user directory. The order is the query's, not the display
// names', so the person who created the project — its first manager — is
// always first.
func (s *server) managers(ctx context.Context, q *store.Queries, projectID int32) ([]gen.ProjectPersonSummary, error) {
	ids, err := q.ListProjectManagers(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("projects: list managers: %w", err)
	}
	names, err := s.displayNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	return personSummaries(ids, names), nil
}

// personSummaries pairs ids with their display names, in ids' order. The
// slice is never nil: `managers` is a required array in the contract, and an
// omitted one would decode as null.
func personSummaries(ids []uuid.UUID, names map[uuid.UUID]string) []gen.ProjectPersonSummary {
	out := make([]gen.ProjectPersonSummary, 0, len(ids))
	for _, id := range ids {
		out = append(out, gen.ProjectPersonSummary{UserId: id, DisplayName: names[id]})
	}
	return out
}
