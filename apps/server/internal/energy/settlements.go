package energy

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// settlementsWindowMonths is how far back the endpoint answers: periods
// ending within the last 24 months (energy settlements plan reading 12).
const settlementsWindowMonths = 24

// GetEnergyMeteringPointsByIdSettlements lists a metering point's settlements
// (GET /api/v1/energy/metering-points/{id}/settlements, plan reading 12). It
// writes nothing: for every non-cancelled supply period of the point, each
// billing period closed by now and ending in the window is answered as its
// stored live settlement — an invoiced one as frozen, a draft recomputed in
// memory — or, with none stored, computed in memory with no id; beside them
// every stored invoiced or released settlement and correction in the window.
// Newest period first, then regular before correction, then id. 404 for an
// unknown point.
func (s *server) GetEnergyMeteringPointsByIdSettlements(ctx context.Context, req gen.GetEnergyMeteringPointsByIdSettlementsRequestObject) (gen.GetEnergyMeteringPointsByIdSettlementsResponseObject, error) {
	q := store.New(s.deps.Pool)
	exists, err := q.MeteringPointExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: check metering point exists: %w", err)
	}
	if !exists {
		return gen.GetEnergyMeteringPointsByIdSettlements404Response{}, nil
	}
	now := time.Now().UTC()
	if s.deps.Clock != nil {
		now = s.deps.Clock().UTC()
	}
	since := now.AddDate(0, -settlementsWindowMonths, 0)

	ids, err := q.ListSettlementSupplyPeriodIDsByMeteringPoint(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: read the point's supply periods: %w", err)
	}
	rows, err := q.ListSettlementsByMeteringPoint(ctx, store.ListSettlementsByMeteringPointParams{MeteringPointID: req.Id, Since: since})
	if err != nil {
		return nil, fmt.Errorf("energy: read the point's settlements: %w", err)
	}
	stored, err := readSettlements(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	periodIDs := slices.Clone(ids)
	for _, st := range stored {
		if !slices.Contains(periodIDs, st.SupplyPeriodID) {
			periodIDs = append(periodIDs, st.SupplyPeriodID)
		}
	}
	subjects, err := loadSettlementSubjects(ctx, q, periodIDs)
	if err != nil {
		return nil, err
	}

	type spKey struct {
		supplyPeriodID int32
		key            liveKey
	}
	live := map[spKey]settlement{}
	for _, st := range stored {
		if isLive(st.State) {
			live[spKey{st.SupplyPeriodID, liveKeyOf(st.PeriodStart, st.Kind)}] = st
		}
	}

	var out []settlement
	answered := map[int64]bool{}
	for _, id := range ids {
		subj := subjects[id]
		periods, err := duePeriodsOf(subj, now, now)
		if err != nil {
			return nil, err
		}
		for _, period := range periods {
			if !period.End.After(since) {
				continue
			}
			if st, ok := live[spKey{id, liveKeyOf(period.Start, contracts.SettlementRegular)}]; ok {
				answered[st.ID] = true
				if st.State != contracts.SettlementDraft {
					out = append(out, st)
					continue
				}
				current, _, err := recomputeDraft(ctx, q, st, subj, now)
				if err != nil {
					return nil, err
				}
				out = append(out, current)
				continue
			}
			computed, err := computeSettlement(ctx, q, subj, period, now)
			if err != nil {
				return nil, err
			}
			out = append(out, computed)
		}
	}
	for _, st := range stored {
		switch {
		case answered[st.ID]:
		case st.State != contracts.SettlementDraft:
			out = append(out, st)
		case st.Kind == contracts.SettlementCorrection:
			current, present, err := recomputeDraft(ctx, q, st, subjects[st.SupplyPeriodID], now)
			if err != nil {
				return nil, err
			}
			if present {
				out = append(out, current)
			}
		}
		// A regular draft of a period no longer due is the housekeeping's.
	}

	slices.SortFunc(out, func(a, b settlement) int {
		return cmp.Or(b.PeriodStart.Compare(a.PeriodStart), cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)),
			cmp.Compare(a.ID, b.ID), cmp.Compare(a.SupplyPeriodID, b.SupplyPeriodID))
	})
	resp := make(gen.GetEnergyMeteringPointsByIdSettlements200JSONResponse, 0, len(out))
	for _, st := range out {
		resp = append(resp, settlementResponse(st))
	}
	return resp, nil
}

// settlementResponse is s on the wire: numbers at their places, every
// optional field left out when it has nothing to say.
func settlementResponse(s settlement) gen.SettlementResponse {
	out := gen.SettlementResponse{
		Amount: floatFromRat(s.Amount, amountPlaces), ContractName: s.ContractName, CorrectsId: s.CorrectsID,
		Currency: s.Currency, CustomerId: s.CustomerID, Estimated: s.Estimated, Kind: s.Kind,
		Kwh: floatFromRat(s.KWh, kwhPlaces), MeteringPointId: s.MeteringPointID,
		MissingIntervals: int32(s.missingIntervals()), PeriodStart: s.PeriodStart, PeriodEnd: s.PeriodEnd,
		State: s.State, SupplyPeriodId: s.SupplyPeriodID, Blocked: optionalString(s.Blocked),
		BlockedDetail: optionalString(s.BlockedDetail), Lines: make([]gen.SettlementLine, 0, len(s.Lines)),
	}
	if s.Currency == "" {
		out.Currency = settlementCurrency
	}
	if s.ID != 0 {
		id, revision := s.ID, s.Revision
		out.Id, out.Revision = &id, &revision
	}
	if s.Stamp != nil {
		out.Invoice = &gen.SettlementInvoice{
			Id: s.Stamp.InvoiceID, Number: s.Stamp.Number, IssueDate: openapi_types.Date{Time: s.Stamp.IssueDate},
			InvoicedAt: s.Stamp.InvoicedAt, ReleasedAt: s.Stamp.ReleasedAt,
		}
	}
	for _, l := range s.Lines {
		line := gen.SettlementLine{
			Amount: floatFromRat(l.Amount, amountPlaces), Band: optionalString(l.Band), Kind: l.Kind,
			Quantity: floatFromRat(l.Quantity, quantityPlaces), Text: l.Text, Unit: l.Unit,
			UnitPrice: floatFromRat(l.UnitPrice, unitPricePlaces), VatHint: l.VATHint,
		}
		if l.Month != nil {
			line.Month = &openapi_types.Date{Time: *l.Month}
		}
		out.Lines = append(out.Lines, line)
	}
	return out
}
