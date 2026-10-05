package timetracking

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// This file is the module's contracts.BillableHours (invoices work design
// D3): the entries an invoice can be built from, row by row — approved,
// billable, priced and not yet invoiced. Like ProjectActuals it authorizes
// nothing and reads nothing but this module's own tables, on the pool, never
// under a lock: the projects' facts are the caller's to read. The entry's
// note is never part of it.

// billableHours is this module's contracts.BillableHours.
type billableHours struct{ q *store.Queries }

var _ contracts.BillableHours = (*billableHours)(nil)

// newBillableHours is Module's BillableHours. It takes the pool and nothing
// else.
func newBillableHours(d module.Deps) contracts.BillableHours {
	return &billableHours{q: store.New(d.Pool)}
}

// BillableHours answers the billable entries of req's projects — at most
// contracts.MaxBillableRows of them, More set when there were more — or
// exactly req's ids that are still billable.
func (b *billableHours) BillableHours(ctx context.Context, req contracts.BillableRequest) (contracts.BillableHoursPage, error) {
	if err := req.Validate(); err != nil {
		return contracts.BillableHoursPage{}, fmt.Errorf("time: %w", err)
	}
	until := pgtype.Date{}
	if !req.Until.IsZero() {
		until = pgtype.Date{Time: req.Until, Valid: true}
	}

	var rows []store.BillableHoursForProjectsRow
	if len(req.IDs) > 0 {
		byID, err := b.q.BillableHoursByIDs(ctx, store.BillableHoursByIDsParams{Ids: req.IDs, Until: until})
		if err != nil {
			return contracts.BillableHoursPage{}, fmt.Errorf("time: read the billable entries by id: %w", err)
		}
		rows = make([]store.BillableHoursForProjectsRow, 0, len(byID))
		for _, row := range byID {
			rows = append(rows, store.BillableHoursForProjectsRow(row))
		}
	} else {
		var err error
		rows, err = b.q.BillableHoursForProjects(ctx, store.BillableHoursForProjectsParams{
			ProjectIds: req.ProjectIDs, Until: until, RowLimit: contracts.MaxBillableRows + 1,
		})
		if err != nil {
			return contracts.BillableHoursPage{}, fmt.Errorf("time: read the projects' billable entries: %w", err)
		}
	}

	page := contracts.BillableHoursPage{}
	if len(rows) > contracts.MaxBillableRows {
		rows, page.More = rows[:contracts.MaxBillableRows], true
	}
	page.Hours = make([]contracts.BillableHour, 0, len(rows))
	for _, row := range rows {
		page.Hours = append(page.Hours, billableHour(row))
	}
	return page, nil
}

// billableHour is one row as the contract carries it.
func billableHour(row store.BillableHoursForProjectsRow) contracts.BillableHour {
	hour := contracts.BillableHour{
		ID:              row.ID,
		Revision:        row.Revision,
		ProjectID:       row.ProjectID,
		BillingLineID:   row.BillingLineID,
		UserID:          row.UserID,
		Date:            row.EntryDate.Time,
		HoursHundredths: row.HoursHundredths,
		BillRate:        row.BillRate,
		Currency:        row.Currency,
		WorkTypeID:      row.WorkTypeID,
		Amount:          row.Amount,
	}
	if row.WorkTypeName != nil {
		hour.WorkTypeName = *row.WorkTypeName
	}
	if row.TaskTitle != nil {
		hour.TaskTitle = *row.TaskTitle
	}
	// The multiplier arrives as the column's own decimal text, "" for
	// ordinary hours (queries/invoiced.sql), so no float ever touches it.
	if row.BillMultiplierPercent != "" {
		percent := row.BillMultiplierPercent
		hour.BillMultiplierPercent = &percent
	}
	return hour
}
