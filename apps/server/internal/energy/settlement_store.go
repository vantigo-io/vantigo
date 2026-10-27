package energy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is a settlement's row (energy settlements plan readings 1, 7):
// the subject it is computed for, reading rows back as settlements, and
// writing a computed draft — inserted, or rewritten with its revision bumped
// when a figure changed, or only touched when none did.

// settlementSubject is a supply period as a settlement is computed for it:
// the period, its metering point's billing facts and the power tariff's name.
type settlementSubject struct {
	store.ListSettlementSubjectsByIDsRow
	// Loc is the point's market time zone (marketTimeZone).
	Loc *time.Location
}

// address is the point's address on one line: "street, postal city".
func (s settlementSubject) address() string {
	return strings.TrimSpace(fmt.Sprintf("%s, %s %s", s.StreetAddress, s.PostalCode, s.City))
}

// vatHint is the point's VAT treatment (vatTreatment, design D8): every line
// of its settlements carries it.
func (s settlementSubject) vatHint() string {
	return vatTreatment(s.Household, s.CountryCode, s.PostalCode)
}

// loadSettlementSubjects reads the supply periods by id; one that does not
// exist is absent from the map.
func loadSettlementSubjects(ctx context.Context, q *store.Queries, ids []int32) (map[int32]settlementSubject, error) {
	out := make(map[int32]settlementSubject, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.ListSettlementSubjectsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("energy: read the supply periods to settle: %w", err)
	}
	for _, r := range rows {
		loc, err := marketLocation(r.PriceArea)
		if err != nil {
			return nil, err
		}
		out[r.SupplyPeriodID] = settlementSubject{ListSettlementSubjectsByIDsRow: r, Loc: loc}
	}
	return out, nil
}

// readSettlements is rows as settlements, each with its lines and its
// subject's facts, in rows' order.
func readSettlements(ctx context.Context, q *store.Queries, rows []store.EnergySettlement) ([]settlement, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(rows))
	periodIDs := make([]int32, 0, len(rows))
	seen := map[int32]bool{}
	for _, r := range rows {
		ids = append(ids, r.ID)
		if !seen[r.SupplyPeriodID] {
			seen[r.SupplyPeriodID] = true
			periodIDs = append(periodIDs, r.SupplyPeriodID)
		}
	}
	lineRows, err := q.ListSettlementLines(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("energy: read settlement lines: %w", err)
	}
	lines := make(map[int64][]store.EnergySettlementLine, len(rows))
	for _, l := range lineRows {
		lines[l.SettlementID] = append(lines[l.SettlementID], l)
	}
	subjects, err := loadSettlementSubjects(ctx, q, periodIDs)
	if err != nil {
		return nil, err
	}
	out := make([]settlement, 0, len(rows))
	for _, r := range rows {
		s, err := settlementFromRow(r, lines[r.ID], subjects[r.SupplyPeriodID])
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// settlementFromRow is a stored settlement with its lines; subj supplies the
// point's facts a row does not store.
func settlementFromRow(r store.EnergySettlement, lines []store.EnergySettlementLine, subj settlementSubject) (settlement, error) {
	kwh, err := ratFromNumeric(r.Kwh)
	if err != nil {
		return settlement{}, fmt.Errorf("energy: settlement %d's kWh: %w", r.ID, err)
	}
	amount, err := ratFromNumeric(r.Amount)
	if err != nil {
		return settlement{}, fmt.Errorf("energy: settlement %d's amount: %w", r.ID, err)
	}
	s := settlement{
		ID: r.ID, Revision: r.Revision, Kind: r.Kind, CorrectsID: r.CorrectsID,
		SupplyPeriodID: r.SupplyPeriodID, MeteringPointID: r.MeteringPointID, CustomerID: r.CustomerID,
		GSRN: subj.Gsrn, Address: subj.address(), PriceArea: subj.PriceArea, Household: subj.Household,
		ContractName: r.ContractName, PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd,
		KWh: kwh, Estimated: r.Estimated,
		IntervalCount: int(r.IntervalCount), ExpectedIntervalCount: int(r.ExpectedIntervalCount),
		Blocked: derefString(r.Blocked), BlockedDetail: derefString(r.BlockedDetail),
		Amount: amount, Currency: strings.TrimSpace(r.Currency), State: r.State, ComputedAt: r.ComputedAt,
		Lines: make([]settlementLine, 0, len(lines)),
	}
	if r.State != contracts.SettlementDraft && r.InvoicedInvoiceID != nil && r.InvoicedNumber != nil {
		stamp := &contracts.InvoiceStampRef{InvoiceID: *r.InvoicedInvoiceID, Number: *r.InvoicedNumber, ReleasedAt: r.ReleasedAt}
		if r.InvoicedAt != nil {
			stamp.InvoicedAt = *r.InvoicedAt
		}
		if r.InvoicedByUserID != nil {
			stamp.InvoicedBy = *r.InvoicedByUserID
		}
		if r.InvoiceDate.Valid {
			stamp.IssueDate = r.InvoiceDate.Time
		}
		s.Stamp = stamp
	}
	for _, l := range lines {
		line, err := settlementLineFromRow(l)
		if err != nil {
			return settlement{}, fmt.Errorf("energy: settlement %d's line %d: %w", r.ID, l.Position, err)
		}
		s.Lines = append(s.Lines, line)
	}
	return s, nil
}

func settlementLineFromRow(l store.EnergySettlementLine) (settlementLine, error) {
	quantity, err := ratFromNumeric(l.Quantity)
	if err != nil {
		return settlementLine{}, err
	}
	unitPrice, err := ratFromNumeric(l.UnitPrice)
	if err != nil {
		return settlementLine{}, err
	}
	amount, err := ratFromNumeric(l.Amount)
	if err != nil {
		return settlementLine{}, err
	}
	line := settlementLine{
		Kind: l.Kind, Text: l.Text, Band: derefString(l.Band), Quantity: quantity, Unit: l.Unit,
		UnitPrice: unitPrice, Amount: amount, VATHint: l.VatHint,
	}
	if l.Month.Valid {
		m := l.Month.Time
		line.Month = &m
	}
	return line, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// writeSettlementDraft writes computed (a settlement with no id) as the draft
// of its live key (supply period, period start, kind), in a short transaction
// of its own on pool (plan reading 7), and answers the row as it then stands:
//
//   - no live row: inserted at revision 1;
//   - a live draft with the same figures: only touched, revision kept;
//   - a live draft with other figures: rewritten, revision bumped, lines
//     replaced;
//   - a live invoiced row: left as it is and answered as stored — an
//     invoiced period changes only through a correction.
//
// A racing insert of the same key (23505) is retried once, reading the row
// the other wrote.
func writeSettlementDraft(ctx context.Context, pool db.TxBeginner, computed settlement, now time.Time) (settlement, error) {
	var out settlement
	write := func() error {
		return db.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			out, err = writeSettlementDraftTx(ctx, store.New(tx), computed, now)
			return err
		})
	}
	err := write()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		err = write()
	}
	if err != nil {
		return settlement{}, fmt.Errorf("energy: write the settlement of supply period %d from %s: %w",
			computed.SupplyPeriodID, computed.PeriodStart.Format(time.RFC3339), err)
	}
	return out, nil
}

// writeSettlementDraftTx is writeSettlementDraft's body on a transaction.
func writeSettlementDraftTx(ctx context.Context, q *store.Queries, computed settlement, now time.Time) (settlement, error) {
	live, err := q.LockLiveSettlement(ctx, store.LockLiveSettlementParams{
		SupplyPeriodID: computed.SupplyPeriodID, PeriodStart: computed.PeriodStart, Kind: computed.Kind,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		row, err := insertSettlement(ctx, q, computed, now)
		if err != nil {
			return settlement{}, err
		}
		return storedAs(row, computed), nil
	case err != nil:
		return settlement{}, err
	}
	stored, err := readSettlements(ctx, q, []store.EnergySettlement{live})
	if err != nil {
		return settlement{}, err
	}
	if live.State != contracts.SettlementDraft {
		return stored[0], nil
	}
	if sameFigures(stored[0], computed) {
		if err := q.TouchSettlements(ctx, store.TouchSettlementsParams{Ids: []int64{live.ID}, Now: now}); err != nil {
			return settlement{}, err
		}
		return stored[0], nil
	}
	params, err := updateSettlementParams(live.ID, computed, now)
	if err != nil {
		return settlement{}, err
	}
	row, err := q.UpdateSettlementDraft(ctx, params)
	if err != nil {
		return settlement{}, err
	}
	if err := q.DeleteSettlementLines(ctx, row.ID); err != nil {
		return settlement{}, err
	}
	if err := insertSettlementLines(ctx, q, row.ID, computed.Lines); err != nil {
		return settlement{}, err
	}
	return storedAs(row, computed), nil
}

// storedAs is computed as the row it was just written to: the row's id,
// revision, state and time, the computation's figures.
func storedAs(row store.EnergySettlement, computed settlement) settlement {
	computed.ID = row.ID
	computed.Revision = row.Revision
	computed.State = row.State
	computed.ComputedAt = row.ComputedAt
	computed.Stamp = nil
	return computed
}

func insertSettlement(ctx context.Context, q *store.Queries, s settlement, now time.Time) (store.EnergySettlement, error) {
	kwh, err := numericFromRat(s.KWh, kwhPlaces)
	if err != nil {
		return store.EnergySettlement{}, err
	}
	amount, err := numericFromRat(s.Amount, amountPlaces)
	if err != nil {
		return store.EnergySettlement{}, err
	}
	row, err := q.InsertSettlement(ctx, store.InsertSettlementParams{
		SupplyPeriodID: s.SupplyPeriodID, MeteringPointID: s.MeteringPointID, CustomerID: s.CustomerID,
		Kind: s.Kind, CorrectsID: s.CorrectsID, PeriodStart: s.PeriodStart, PeriodEnd: s.PeriodEnd,
		Kwh: kwh, Estimated: s.Estimated,
		IntervalCount: int32(s.IntervalCount), ExpectedIntervalCount: int32(s.ExpectedIntervalCount),
		Blocked: optionalString(s.Blocked), BlockedDetail: optionalString(s.BlockedDetail),
		Amount: amount, Currency: settlementCurrency, ContractName: s.ContractName, Now: now,
	})
	if err != nil {
		return store.EnergySettlement{}, err
	}
	if err := insertSettlementLines(ctx, q, row.ID, s.Lines); err != nil {
		return store.EnergySettlement{}, err
	}
	return row, nil
}

func updateSettlementParams(id int64, s settlement, now time.Time) (store.UpdateSettlementDraftParams, error) {
	kwh, err := numericFromRat(s.KWh, kwhPlaces)
	if err != nil {
		return store.UpdateSettlementDraftParams{}, err
	}
	amount, err := numericFromRat(s.Amount, amountPlaces)
	if err != nil {
		return store.UpdateSettlementDraftParams{}, err
	}
	return store.UpdateSettlementDraftParams{
		ID: id, PeriodEnd: s.PeriodEnd, CustomerID: s.CustomerID, CorrectsID: s.CorrectsID,
		Kwh: kwh, Estimated: s.Estimated,
		IntervalCount: int32(s.IntervalCount), ExpectedIntervalCount: int32(s.ExpectedIntervalCount),
		Blocked: optionalString(s.Blocked), BlockedDetail: optionalString(s.BlockedDetail),
		Amount: amount, Currency: settlementCurrency, ContractName: s.ContractName, Now: now,
	}, nil
}

func insertSettlementLines(ctx context.Context, q *store.Queries, id int64, lines []settlementLine) error {
	for i, l := range lines {
		quantity, err := numericFromRat(l.Quantity, quantityPlaces)
		if err != nil {
			return err
		}
		unitPrice, err := numericFromRat(l.UnitPrice, unitPricePlaces)
		if err != nil {
			return err
		}
		amount, err := numericFromRat(l.Amount, amountPlaces)
		if err != nil {
			return err
		}
		var month pgtype.Date
		if l.Month != nil {
			month = pgtype.Date{Time: *l.Month, Valid: true}
		}
		if err := q.InsertSettlementLine(ctx, store.InsertSettlementLineParams{
			SettlementID: id, Position: int16(i), Kind: l.Kind, Text: l.Text, Band: optionalString(l.Band),
			Month: month, Quantity: quantity, Unit: l.Unit, UnitPrice: unitPrice, Amount: amount, VatHint: l.VATHint,
		}); err != nil {
			return err
		}
	}
	return nil
}
