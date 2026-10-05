package projects

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// billableMilestones is this module's contracts.BillableMilestones (invoices
// work design D3): the billing milestones ready to invoice, row by row, read
// on the pool from this module's own tables — the project's currency and fixed
// price included, so nothing here calls a directory back.
type billableMilestones struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

var _ contracts.BillableMilestones = billableMilestones{}

// newBillableMilestones is Module's BillableMilestones.
func newBillableMilestones(d module.Deps) contracts.BillableMilestones {
	return billableMilestones{pool: d.Pool, logger: d.Logger}
}

// billableMilestoneRow is the one row both reads answer: sqlc emits a type per
// query even when the columns are the same.
type billableMilestoneRow = store.BillableMilestonesForProjectsRow

// BillableMilestones answers the ready milestones of req's projects, or exactly
// req's milestones that are still ready. Amount is milestoneEffectiveAmountRat
// to the cent — the figure the holder compares with and freezes — and Currency
// the milestone's own when it carries a flat amount, else its project's.
//
// A ready milestone nothing can price — a percent of a fixed price its project
// no longer has, which the project's own guard refuses to leave behind — is
// not billable and is left out, with a warning, as the invoice plan leaves it
// out of its totals: invoicing it at any amount would be a guess.
func (b billableMilestones) BillableMilestones(ctx context.Context, req contracts.BillableRequest) (contracts.BillableMilestonesPage, error) {
	if err := req.Validate(); err != nil {
		return contracts.BillableMilestonesPage{}, err
	}
	until := pgtype.Date{}
	if !req.Until.IsZero() {
		until = pgtype.Date{Time: req.Until, Valid: true}
	}
	limit := int32(contracts.MaxBillableRows + 1)

	q := store.New(b.pool)
	var rows []billableMilestoneRow
	var err error
	if len(req.ProjectIDs) > 0 {
		rows, err = q.BillableMilestonesForProjects(ctx, store.BillableMilestonesForProjectsParams{
			ProjectIds: req.ProjectIDs, Until: until, RowLimit: limit,
		})
	} else {
		var found []store.BillableMilestonesByIDsRow
		found, err = q.BillableMilestonesByIDs(ctx, store.BillableMilestonesByIDsParams{
			Ids: milestoneIDs(req.IDs), Until: until, RowLimit: limit,
		})
		for _, r := range found {
			rows = append(rows, billableMilestoneRow(r))
		}
	}
	if err != nil {
		return contracts.BillableMilestonesPage{}, fmt.Errorf("projects: read the billable milestones: %w", err)
	}

	page := contracts.BillableMilestonesPage{Milestones: make([]contracts.BillableMilestone, 0, len(rows))}
	if len(rows) > contracts.MaxBillableRows {
		rows, page.More = rows[:contracts.MaxBillableRows], true
	}
	for _, r := range rows {
		m := store.ProjectsBillingMilestone{
			ID: r.ID, ProjectID: r.ProjectID, Amount: r.Amount, AmountCurrency: r.AmountCurrency,
			Percent: r.Percent, InvoicedAmount: r.InvoicedAmount, Status: milestoneStatusReady,
		}
		project := store.ProjectsProject{ID: r.ProjectID, Currency: r.ProjectCurrency, FixedPriceAmount: r.FixedPriceAmount}
		amount, err := milestoneEffectiveAmountRat(m, project)
		if errors.Is(err, errMilestoneUnpriced) {
			b.logger.WarnContext(ctx, "projects: a ready billing milestone cannot be priced and is not billable",
				"milestone_id", r.ID, "project_id", r.ProjectID, "error", err.Error())
			continue
		}
		if err != nil {
			return contracts.BillableMilestonesPage{}, err
		}
		currency := ""
		if c := milestoneCurrency(m, project); c != nil {
			currency = *c
		}
		description := ""
		if r.Description != nil {
			description = *r.Description
		}
		var planned *time.Time
		if r.PlannedDate.Valid {
			d := r.PlannedDate.Time
			planned = &d
		}
		page.Milestones = append(page.Milestones, contracts.BillableMilestone{
			ID: int64(r.ID), Revision: r.Revision, ProjectID: r.ProjectID,
			Name: r.Name, Description: description, PlannedDate: planned, ReadyAt: r.ReadyAt,
			Amount: amount.FloatString(2), Currency: currency,
		})
	}
	return page, nil
}

// milestoneIDs is the contract's int64 ids as this module's integer ones. An
// id past what the column holds names no milestone, so it is dropped rather
// than wrapped round onto one that exists.
func milestoneIDs(ids []int64) []int32 {
	out := make([]int32, 0, len(ids))
	for _, id := range ids {
		if id >= math.MinInt32 && id <= math.MaxInt32 {
			out = append(out, int32(id))
		}
	}
	return out
}
