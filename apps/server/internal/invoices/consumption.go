package invoices

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the Consumption section's read and the From consumption
// wizard (energy consumption billing design D7, D11; invoices from
// consumption plan readings 4 and 5): GET /invoices/consumption lists a
// customer's supply periods with billing periods due, as Energy's
// DueSupplies answers them; GET /invoices/consumption/settlements settles
// them for the wizard, with what this module's own rows hold of each; POST
// /invoices/from-consumption makes a draft of the settlements — or adds them
// to one — one line per settlement line, held under the draft's lock. Every
// read of Energy is made on the pool, before the wizard's transaction, never
// in it: Settle writes Energy's own draft rows in transactions of its own.

// The consumption wizard's and views' refusals, beside the work wizard's
// (work.go), which it shares where the rule is the same.
const (
	codeConsumptionUnavailable = "consumption_unavailable"
	codeSettlementBlocked      = "settlement_blocked"
	codeCadenceNotAllowed      = "cadence_not_allowed"
	codeNoConsumptionDue       = "no_consumption_due"

	cannotInvoiceConsumptionTitle = "The consumption cannot be invoiced"
	invalidConsumptionTitle       = "Invalid consumption selection"
)

// warningConsumptionTruncated is a view whose provider had more than
// MaxBillableRows to answer: the rest is not listed.
const warningConsumptionTruncated = "consumption_truncated"

// Why a settlement of the wizard's view is not taken without settlementIds
// (plan reading 4), beside reasonHeld.
const (
	reasonBlocked = "blocked"
	reasonCadence = "cadence"
)

func consumptionUnavailable() gen.InvoicesConflictProblem {
	return conflict(codeConsumptionUnavailable, cannotInvoiceConsumptionTitle,
		"The energy module, which settles consumption, is switched off.")
}

// settlementConflict is one of the wizard's refusals about a named
// settlement, carrying its kind and id.
func settlementConflict(code string, id int64, detail string) *gen.InvoicesConflictProblem {
	p := sourceConflict(code, sourceRef{kind: contracts.WorkSourceSettlement, id: id}, detail)
	p.Title = ptr(cannotInvoiceConsumptionTitle)
	return p
}

// consumptionThrough is a view's or the wizard's through date: the request's,
// else today in the business time zone.
func (s *server) consumptionThrough(ctx context.Context, given *openapi_types.Date) (time.Time, error) {
	if given != nil {
		return utcDay(given.Time), nil
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return holidays.DayIn(s.deps.Clock(), loc), nil
}

// supplyCadences is each due supply period's cadence, by id.
func supplyCadences(page contracts.DueSuppliesPage) map[int32]string {
	out := make(map[int32]string, len(page.Supplies))
	for _, d := range page.Supplies {
		out[d.SupplyPeriodID] = d.Cadence
	}
	return out
}

// cadenceRefused is cadence_not_allowed's rule (D4, plan reading 5, U6): a
// person is billed at least quarterly, so a settlement of a yearly supply is
// not theirs to invoice. The cadence is the supply's as DueSupplies answers
// it; a settlement of a supply it does not list — a correction of a supply
// billed through — is yearly when its period is longer than three months.
func cadenceRefused(person bool, st contracts.Settlement, cadences map[int32]string) bool {
	if !person {
		return false
	}
	if c, ok := cadences[st.SupplyPeriodID]; ok {
		return c == contracts.CadenceYearly
	}
	return st.PeriodEnd.After(st.PeriodStart.In(marketZone).AddDate(0, 3, 0))
}

func isPerson(profile *contracts.CustomerBillingProfile) bool {
	return profile != nil && profile.Type == "person"
}

// settlementHolds is what live documents of this module hold of
// settlements, by id: the first holder of each, from its own rows.
func settlementHolds(ctx context.Context, q *store.Queries, settlements []contracts.Settlement) (map[int64]gen.InvoicesWorkHeldBy, error) {
	var p store.LiveSourcesForParams
	for _, st := range settlements {
		p.Kinds, p.Ids = append(p.Kinds, string(contracts.WorkSourceSettlement)), append(p.Ids, st.ID)
	}
	held := map[int64]gen.InvoicesWorkHeldBy{}
	if len(p.Ids) == 0 {
		return held, nil
	}
	rows, err := q.LiveSourcesFor(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("invoices: read what holds the settlements: %w", err)
	}
	for _, r := range rows {
		if _, ok := held[r.SourceID]; !ok {
			held[r.SourceID] = gen.InvoicesWorkHeldBy{InvoiceId: r.InvoiceID, Number: r.Number, Status: r.Status}
		}
	}
	return held, nil
}

// GetInvoicesConsumption List a customer's consumption due
// (GET /api/v1/invoices/consumption)
//
// The panel's Consumption section (D11, plan reading 4): a customer, the
// through date — today in the business zone when absent — and Energy's
// DueSupplies for them through the start of the next day in the market zone.
// Nothing is written and nothing settled.
func (s *server) GetInvoicesConsumption(ctx context.Context, req gen.GetInvoicesConsumptionRequestObject) (gen.GetInvoicesConsumptionResponseObject, error) {
	p := req.Params
	if p.CustomerId <= 0 {
		return gen.GetInvoicesConsumption400ApplicationProblemPlusJSONResponse(invalid(invalidConsumptionTitle,
			fieldError("customerId", "Name the customer whose consumption to list"))), nil
	}
	if s.deps.Consumption == nil {
		return gen.GetInvoicesConsumption409ApplicationProblemPlusJSONResponse(consumptionUnavailable()), nil
	}
	through, err := s.consumptionThrough(ctx, p.Through)
	if err != nil {
		return nil, err
	}
	page, err := s.dueSupplies(ctx, contracts.DueSuppliesRequest{CustomerIDs: []int32{p.CustomerId}, Through: throughInstant(through)})
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's due supplies: %w", p.CustomerId, err)
	}
	view := gen.InvoicesConsumptionResponse{
		CustomerId: p.CustomerId, Through: wireDate(through), Supplies: []gen.InvoicesDueSupply{}, Warnings: []string{},
	}
	if page.More {
		view.Warnings = append(view.Warnings, warningConsumptionTruncated)
	}
	supplies := slices.Clone(page.Supplies)
	slices.SortFunc(supplies, func(a, b contracts.DueSupply) int {
		return cmp.Or(cmp.Compare(a.GSRN, b.GSRN), cmp.Compare(a.SupplyPeriodID, b.SupplyPeriodID))
	})
	for _, d := range supplies {
		var billed *openapi_types.Date
		if d.BilledThrough != nil {
			billed = ptr(wireDate(civilDay(*d.BilledThrough)))
		}
		view.Supplies = append(view.Supplies, gen.InvoicesDueSupply{
			SupplyPeriodId: d.SupplyPeriodID, MeteringPointId: d.MeteringPointID, Gsrn: d.GSRN, Address: d.Address,
			PriceArea: d.PriceArea, ContractName: d.ContractName, Cadence: d.Cadence, BilledThrough: billed,
			DuePeriods: int32(d.DuePeriods), Blocked: nonEmpty(d.Blocked),
		})
	}
	return gen.GetInvoicesConsumption200JSONResponse(view), nil
}

// GetInvoicesConsumptionSettlements Settle a customer's consumption for the wizard
// (GET /api/v1/invoices/consumption/settlements)
//
// The wizard's list (D7, D11, plan reading 4): Energy's Settle for the
// customer through the date — which writes Energy's own drafts; this module
// writes nothing — each settlement with what this module's own rows hold of
// it, and whether the wizard takes it without settlementIds: not when held,
// blocked or of a yearly supply of a person, in that order.
func (s *server) GetInvoicesConsumptionSettlements(ctx context.Context, req gen.GetInvoicesConsumptionSettlementsRequestObject) (gen.GetInvoicesConsumptionSettlementsResponseObject, error) {
	p := req.Params
	if p.CustomerId <= 0 {
		return gen.GetInvoicesConsumptionSettlements400ApplicationProblemPlusJSONResponse(invalid(invalidConsumptionTitle,
			fieldError("customerId", "Name the customer whose consumption to settle"))), nil
	}
	if s.deps.Consumption == nil {
		return gen.GetInvoicesConsumptionSettlements409ApplicationProblemPlusJSONResponse(consumptionUnavailable()), nil
	}
	through, err := s.consumptionThrough(ctx, p.Through)
	if err != nil {
		return nil, err
	}
	profile, err := s.customerProfile(ctx, p.CustomerId)
	if err != nil {
		return nil, err
	}
	instant := throughInstant(through)
	due, err := s.dueSupplies(ctx, contracts.DueSuppliesRequest{CustomerIDs: []int32{p.CustomerId}, Through: instant})
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's due supplies: %w", p.CustomerId, err)
	}
	page, err := s.settle(ctx, contracts.SettleRequest{CustomerIDs: []int32{p.CustomerId}, Through: instant})
	if err != nil {
		return nil, fmt.Errorf("invoices: settle customer %d's consumption: %w", p.CustomerId, err)
	}
	settlements := slices.Clone(page.Settlements)
	sortSettlements(settlements)
	held, err := settlementHolds(ctx, store.New(s.deps.Pool), settlements)
	if err != nil {
		return nil, err
	}
	cadences, person := supplyCadences(due), isPerson(profile)
	view := gen.InvoicesConsumptionSettlementsResponse{
		CustomerId: p.CustomerId, Through: wireDate(through), Settlements: []gen.InvoicesConsumptionSettlement{}, Warnings: []string{},
	}
	if page.More || due.More {
		view.Warnings = append(view.Warnings, warningConsumptionTruncated)
	}
	for _, st := range settlements {
		out, err := settlementView(st)
		if err != nil {
			return nil, err
		}
		reason := ""
		if h, ok := held[st.ID]; ok {
			out.HeldBy, reason = &h, reasonHeld
		} else if st.Blocked != "" {
			reason = reasonBlocked
		} else if cadenceRefused(person, st, cadences) {
			reason = reasonCadence
		}
		out.Selectable, out.Reason = reason == "", nonEmpty(reason)
		view.Settlements = append(view.Settlements, out)
	}
	return gen.GetInvoicesConsumptionSettlements200JSONResponse(view), nil
}

// settlementView is a settlement on the wire as Energy answered it, its
// numbers at their own decimals and its optional fields absent when empty.
func settlementView(st contracts.Settlement) (gen.InvoicesConsumptionSettlement, error) {
	kwh, err := decimalFloat(st.KWh, 3)
	if err != nil {
		return gen.InvoicesConsumptionSettlement{}, err
	}
	amount, err := decimalFloat(st.Amount, 2)
	if err != nil {
		return gen.InvoicesConsumptionSettlement{}, err
	}
	first, last := settlementDays(st)
	out := gen.InvoicesConsumptionSettlement{
		Id: st.ID, Revision: st.Revision, Kind: st.Kind, CorrectsId: st.CorrectsID, SupplyPeriodId: st.SupplyPeriodID,
		MeteringPointId: st.MeteringPointID, Gsrn: st.GSRN, Address: st.Address, PriceArea: st.PriceArea, ContractName: st.ContractName,
		PeriodFrom: wireDate(first), PeriodTo: wireDate(last), Kwh: kwh, Estimated: st.Estimated,
		MissingIntervals: int32(st.MissingIntervals), Currency: st.Currency, Amount: amount,
		Blocked: nonEmpty(st.Blocked), BlockedDetail: nonEmpty(st.BlockedDetail), State: st.State,
		Lines: make([]gen.InvoicesConsumptionSettlementLine, 0, len(st.Lines)),
	}
	for _, sl := range st.Lines {
		quantity, err := decimalFloat(sl.Quantity, 3)
		if err != nil {
			return out, err
		}
		price, err := decimalFloat(sl.UnitPrice, 6)
		if err != nil {
			return out, err
		}
		lineAmount, err := decimalFloat(sl.Amount, 2)
		if err != nil {
			return out, err
		}
		var month *openapi_types.Date
		if sl.Month != nil {
			month = ptr(wireDate(civilDay(*sl.Month)))
		}
		out.Lines = append(out.Lines, gen.InvoicesConsumptionSettlementLine{
			Kind: sl.Kind, Text: sl.Text, Band: nonEmpty(sl.Band), Month: month, Quantity: quantity, Unit: sl.Unit,
			UnitPrice: price, Amount: lineAmount, VatHint: sl.VATHint,
		})
	}
	return out, nil
}

// parseFromConsumption is step 1's 400s (plan reading 5): a customer, the
// named settlements — when named, at least one and at most MaxBillableRows,
// each positive and named once — an append's id and revision together, and a
// note of at most 1 000 characters. named says whether settlementIds was
// given at all.
func parseFromConsumption(body gen.InvoicesFromConsumptionRequest) (ids []int64, named bool, errs map[string][]string) {
	if body.CustomerId <= 0 {
		errs = withFieldError(errs, "customerId", "Consumption is invoiced to a customer")
	}
	if msg := maxLength("The note", optionalText(body.Note), 1000); msg != "" {
		errs = withFieldError(errs, "note", msg)
	}
	if body.SettlementIds != nil {
		named = true
		given := *body.SettlementIds
		switch {
		case len(given) == 0:
			errs = withFieldError(errs, "settlementIds", "Choose the settlements to invoice, or leave settlementIds out to take every one due")
		case len(given) > contracts.MaxBillableRows:
			errs = withFieldError(errs, "settlementIds", fmt.Sprintf("At most %d settlements are invoiced at once", contracts.MaxBillableRows))
		}
		seen := make(map[int64]bool, len(given))
		for i, id := range given {
			field := fmt.Sprintf("settlementIds[%d]", i)
			switch {
			case id <= 0:
				errs = withFieldError(errs, field, "A settlement's id is positive")
			case seen[id]:
				errs = withFieldError(errs, field, msgSourceTwice)
			default:
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if (body.InvoiceId == nil) != (body.Revision == nil) {
		errs = withFieldError(errs, "revision", "Adding to a draft names the draft and the revision it was read at")
	}
	return ids, named, errs
}

// judgeSettlements is step 6's refusals, in their order, each over every
// named settlement before the next (plan reading 5): one of another
// customer; one missing, invoiced or released; one Energy cannot bill, its
// blocker in the detail; one of a yearly supply of a person; and last a
// currency other than the module's. ids are the settlements judged, in the
// body's order; found is what Energy answered for them.
func judgeSettlements(ids []int64, found map[int64]contracts.Settlement, customerID int32, person bool,
	cadences map[int32]string, currency string,
) *gen.InvoicesConflictProblem {
	for _, id := range ids {
		if st, ok := found[id]; ok && st.CustomerID != customerID {
			return settlementConflict(codeSourceNotForCustomer, id, fmt.Sprintf(
				"Settlement %d is customer %d's consumption, not this customer's.", id, st.CustomerID))
		}
	}
	for _, id := range ids {
		if st, ok := found[id]; !ok || st.State != contracts.SettlementDraft {
			return settlementConflict(codeSourceNotInvoiceable, id, fmt.Sprintf(
				"Settlement %d can no longer be invoiced: it is gone, invoiced already or released.", id))
		}
	}
	for _, id := range ids {
		if st := found[id]; st.Blocked != "" {
			detail := fmt.Sprintf("Settlement %d cannot be billed yet (%s)", id, st.Blocked)
			if st.BlockedDetail != "" {
				detail += ": " + st.BlockedDetail
			}
			return settlementConflict(codeSettlementBlocked, id, detail+".")
		}
	}
	for _, id := range ids {
		if cadenceRefused(person, found[id], cadences) {
			return settlementConflict(codeCadenceNotAllowed, id, fmt.Sprintf(
				"Settlement %d is of a yearly supply, and a person is billed at least quarterly.", id))
		}
	}
	for _, id := range ids {
		if st := found[id]; st.Currency != currency {
			return ptr(conflict(codeCurrencyNotNOK, cannotInvoiceConsumptionTitle, fmt.Sprintf(
				"Settlement %d is in %s, and this module invoices in %s only.", id, st.Currency, currency)))
		}
	}
	return nil
}

// resolveEnergyVatCodes is step 7 (D8, resolveVatCodes' way): per hint the
// settlements' lines carry, the request's code or else the default —
// energyDefaults — each one known and active, else a 400 on vatCodes.energy
// or vatCodes.energyExempt. A hint no line carries is not judged.
func resolveEnergyVatCodes(settings store.InvoicesSetting, req *gen.InvoicesFromConsumptionVatCodes, settlements []contracts.Settlement,
	codes map[int32]vatCodeOnDay,
) (energyVatCodes, map[string][]string) {
	defaults := energyDefaults(settings)
	var given gen.InvoicesFromConsumptionVatCodes
	if req != nil {
		given = *req
	}
	hints := map[string]bool{}
	for _, st := range settlements {
		for _, sl := range st.Lines {
			hints[sl.VATHint] = true
		}
	}
	var errs map[string][]string
	resolve := func(field, words string, present bool, override *int32, fallback int32) int32 {
		if !present {
			return fallback
		}
		if override != nil {
			switch c, ok := codes[*override]; {
			case !ok:
				errs = withFieldError(errs, field, "No VAT code has this id")
			case !c.active:
				errs = withFieldError(errs, field, "This VAT code is no longer offered for new lines")
			}
			return *override
		}
		if c, ok := codes[fallback]; !ok || !c.active {
			errs = withFieldError(errs, field, fmt.Sprintf(
				"The default VAT code for %s, code %s, is no longer offered for new lines: choose another, or change the default in the settings",
				words, c.code))
		}
		return fallback
	}
	// Every hint but the north's households' takes the standard code, as
	// energyVatCodes.of does.
	standard := false
	for h := range hints {
		if h != contracts.VATHintExemptHouseholdNorth {
			standard = true
		}
	}
	return energyVatCodes{
		standard: resolve("vatCodes.energy", "energy", standard, given.Energy, defaults.standard),
		exempt: resolve("vatCodes.energyExempt", "energy exempt from VAT", hints[contracts.VATHintExemptHouseholdNorth],
			given.EnergyExempt, defaults.exempt),
	}, errs
}

// consumptionSpan is the first and last day the settlements cover: the
// earliest period's first day and the latest period's last.
func consumptionSpan(settlements []contracts.Settlement) (first, last pgtype.Date) {
	for _, st := range settlements {
		from, to := settlementDays(st)
		if !first.Valid || from.Before(first.Time) {
			first = pgDate(from)
		}
		if !last.Valid || to.After(last.Time) {
			last = pgDate(to)
		}
	}
	return first, last
}

// spanRows are rows as widenDelivery reads them, by their dates: each row's
// own date and, for a settlement, its period's first day too, so an append
// widens to every day a held settlement covers.
func spanRows(rows []heldSource) []heldSource {
	out := make([]heldSource, 0, len(rows))
	for _, r := range rows {
		out = append(out, heldSource{date: r.date})
		if r.point != nil {
			out = append(out, heldSource{date: r.point.periodFrom})
		}
	}
	return out
}

// workRows are the rows that carry a project — the work, never a settlement
// — which alone derive a document's project (D9, plan reading 5).
func workRows(rows []heldSource) []heldSource {
	var out []heldSource
	for _, r := range rows {
		if r.projectID != 0 {
			out = append(out, r)
		}
	}
	return out
}

// PostInvoicesFromConsumption Invoice consumption
// (POST /api/v1/invoices/from-consumption)
//
// The wizard (D7, plan reading 5): every judgment in the reading's order,
// each read of Energy or a directory before the transaction; then one
// withLockedTx, as from-work's, that inserts the draft — or locks the target,
// still a draft at the revision the body names — writes the lines (a
// target's own first, their sources carried), refuses a settlement another
// live document holds, and holds the settlements in one statement. Two
// wizards racing for one settlement end in one, as two from-work wizards do.
func (s *server) PostInvoicesFromConsumption(ctx context.Context, req gen.PostInvoicesFromConsumptionRequestObject) (gen.PostInvoicesFromConsumptionResponseObject, error) {
	body := *req.Body
	q := store.New(s.deps.Pool)
	// (1) The body. (2) An append's target, and the bound with its holds.
	ids, named, errs := parseFromConsumption(body)
	if len(errs) > 0 {
		return gen.PostInvoicesFromConsumption400ApplicationProblemPlusJSONResponse(invalid(invalidConsumptionTitle, errs)), nil
	}
	var target *store.InvoicesInvoice
	var targetHeld []heldSource
	if body.InvoiceId != nil {
		t, err := q.GetInvoice(ctx, *body.InvoiceId)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PostInvoicesFromConsumption404Response{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("invoices: read document %d: %w", *body.InvoiceId, err)
		}
		switch {
		case t.Status != statusDraft:
			return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
		case t.Kind != kindInvoice:
			return gen.PostInvoicesFromConsumption400ApplicationProblemPlusJSONResponse(invalid(invalidConsumptionTitle,
				fieldError("invoiceId", msgSourcesOnCredit))), nil
		case t.CustomerID != body.CustomerId:
			return gen.PostInvoicesFromConsumption400ApplicationProblemPlusJSONResponse(invalid(invalidConsumptionTitle,
				fieldError("invoiceId", "This draft invoices another customer"))), nil
		}
		target = &t
		if targetHeld, err = sourcesOf(ctx, q, t.ID); err != nil {
			return nil, err
		}
	}
	tooMany := func(n int) gen.PostInvoicesFromConsumptionResponseObject {
		return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(conflict(codeTooManySources, cannotInvoiceConsumptionTitle,
			fmt.Sprintf("One document holds at most %d sources; this would hold %d.", contracts.MaxBillableRows, n)))
	}
	if n := len(ids) + len(targetHeld); n > contracts.MaxBillableRows {
		return tooMany(n), nil
	}

	// (3) Energy switched off.
	if s.deps.Consumption == nil {
		return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(consumptionUnavailable()), nil
	}

	// (4) The settings, the profile and the customer's gates.
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	profile, err := s.customerProfile(ctx, body.CustomerId)
	if err != nil {
		return nil, err
	}
	if refusal := customerGate(profile); refusal != nil {
		return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(*refusal), nil
	}

	// (5) The supplies' cadences, then the settlements: Settle for the
	// customer through the date, or the named ones by id — on the pool.
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	through := holidays.DayIn(s.deps.Clock(), loc)
	if body.Through != nil {
		through = utcDay(body.Through.Time)
	}
	instant := throughInstant(through)
	due, err := s.dueSupplies(ctx, contracts.DueSuppliesRequest{CustomerIDs: []int32{body.CustomerId}, Through: instant})
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's due supplies: %w", body.CustomerId, err)
	}
	var page contracts.SettlementsPage
	if named {
		page, err = s.settlementsByID(ctx, ids)
	} else {
		page, err = s.settle(ctx, contracts.SettleRequest{CustomerIDs: []int32{body.CustomerId}, Through: instant})
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's settlements: %w", body.CustomerId, err)
	}
	if page.More {
		return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(conflict(codeTooManySources, cannotInvoiceConsumptionTitle,
			fmt.Sprintf("Energy has more than %d settlements to invoice for this customer; name the ones to take.", contracts.MaxBillableRows))), nil
	}

	// (6) The judgments. Without settlementIds, what the wizard would refuse
	// by name is left out instead — blocked, held by a live document (the
	// target included: it holds it already), or of a yearly supply of a
	// person — and what is left is judged as named.
	cadences, person := supplyCadences(due), isPerson(profile)
	found := make(map[int64]contracts.Settlement, len(page.Settlements))
	for _, st := range page.Settlements {
		found[st.ID] = st
	}
	if !named {
		held, err := settlementHolds(ctx, q, page.Settlements)
		if err != nil {
			return nil, err
		}
		left := slices.Clone(page.Settlements)
		sortSettlements(left)
		for _, st := range left {
			if _, ok := held[st.ID]; ok || st.Blocked != "" || cadenceRefused(person, st, cadences) {
				continue
			}
			ids = append(ids, st.ID)
		}
		if len(ids) == 0 {
			return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(conflict(codeNoConsumptionDue, cannotInvoiceConsumptionTitle,
				fmt.Sprintf("No consumption of this customer's is left to invoice through %s.", through.Format(time.DateOnly)))), nil
		}
		if n := len(ids) + len(targetHeld); n > contracts.MaxBillableRows {
			return tooMany(n), nil
		}
	}
	if refusal := judgeSettlements(ids, found, body.CustomerId, person, cadences, settings.DefaultCurrency); refusal != nil {
		return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	taken := make([]contracts.Settlement, 0, len(ids))
	refs := make([]sourceRef, 0, len(ids))
	for _, id := range ids {
		taken = append(taken, found[id])
		refs = append(refs, sourceRef{kind: contracts.WorkSourceSettlement, id: id})
	}

	// (7) The VAT codes, by the hints the lines carry.
	codes, err := vatCodesOn(ctx, q, pgDate(holidays.DayIn(s.deps.Clock(), loc)))
	if err != nil {
		return nil, err
	}
	vat, errs := resolveEnergyVatCodes(settings, body.VatCodes, taken, codes)
	if len(errs) > 0 {
		return gen.PostInvoicesFromConsumption400ApplicationProblemPlusJSONResponse(invalid(invalidConsumptionTitle, errs)), nil
	}

	// (8) The lines, in the buyer's language, a correction naming the invoice
	// its original is on; at most maxLines with the target's; each within its
	// columns.
	language := lineLanguage(profile.Language)
	invoiced, err := correctedNumbers(ctx, q, taken)
	if err != nil {
		return nil, err
	}
	lines, held, err := energyLines(language, taken, vat, invoiced)
	if err != nil {
		return nil, err
	}
	existing := 0
	if target != nil {
		n, err := q.Lines(ctx, target.ID)
		if err != nil {
			return nil, fmt.Errorf("invoices: read document %d's lines: %w", target.ID, err)
		}
		existing = len(n)
	}
	if existing+len(lines) > maxLines {
		return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(conflict(codeTooManyLines, cannotInvoiceConsumptionTitle,
			fmt.Sprintf("The settlements make %d lines, and a document holds at most %d; choose fewer settlements.", existing+len(lines), maxLines))), nil
	}
	for _, l := range lines {
		if l.quantity.Cmp(maxQuantity) > 0 || l.unitPrice.Cmp(maxUnitPrice) > 0 || l.amounts.gross.Cmp(maxLineGross) > 0 {
			return gen.PostInvoicesFromConsumption400ApplicationProblemPlusJSONResponse(invalid(invalidConsumptionTitle,
				fieldError("settlementIds", "A settlement makes a line too large for one invoice line"))), nil
		}
	}

	// (9) The delivery period: the settlements' first and last day — on an
	// append widened from the target's, under its lock. The note: the
	// request's, else the one naming the invoice released settlements were
	// on (invoices work design D8); an append writes one only on a target
	// whose note is empty.
	from, to := consumptionSpan(taken)
	note := optionalText(body.Note)
	if note == "" && (target == nil || target.Note == "") {
		if note, err = rePullNote(ctx, q, language, refs); err != nil {
			return nil, err
		}
	}
	// The codes of the projects the target's work belongs to, for its derived
	// project, read before the lock; settlements carry none.
	var projectIDs []int32
	for _, h := range workRows(targetHeld) {
		if !slices.Contains(projectIDs, h.projectID) {
			projectIDs = append(projectIDs, h.projectID)
		}
	}
	var entries []contracts.ProjectEntry
	if len(projectIDs) > 0 && s.deps.Projects != nil {
		slices.Sort(projectIDs)
		if entries, err = s.projectEntries(ctx, projectIDs); err != nil {
			return nil, fmt.Errorf("invoices: read the target's projects: %w", err)
		}
	}

	var out fromWorkResult
	var rows []heldSource
	var invoiceID int64
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		all, carried := lines, []heldSource(nil)
		var doc store.InvoicesInvoice
		if target == nil {
			_, totals, _ := summarize(taxedLines(all, codes), big.NewRat(1, 1))
			if e := checkTotal(totals, nil); e != nil {
				out.invalid = e
				return errRefused
			}
			net, vatTotal, gross, vatNOK, err := numerics(totals)
			if err != nil {
				return err
			}
			terms := profile.PaymentTermsDays
			if terms == nil {
				terms = &settings.DefaultPaymentTermsDays
			}
			doc, err = txq.InsertInvoiceDraft(ctx, store.InsertInvoiceDraftParams{
				CustomerID: body.CustomerId, DeliveryFrom: from, DeliveryTo: to, PaymentTermsDays: terms,
				Currency: settings.DefaultCurrency, YourReference: profile.BuyerReference, Note: note,
				NetTotal: net, VatTotal: vatTotal, GrossTotal: gross, VatTotalNok: vatNOK, Timesheet: false,
				CreatedByUserID: callerID(ctx), Now: s.deps.Clock(),
			})
			if err != nil {
				return fmt.Errorf("invoices: create a draft from consumption: %w", err)
			}
		} else {
			locked, err := txq.LockInvoice(ctx, target.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				return errDocumentGone
			}
			if err != nil {
				return fmt.Errorf("invoices: lock document %d: %w", target.ID, err)
			}
			if locked.Status != statusDraft {
				out.refusal = ptr(invoiceIssued())
				return errRefused
			}
			if locked.Revision != *body.Revision {
				out.refusal = ptr(revisionConflict("Invoice", locked.Revision, *body.Revision))
				return errRefused
			}
			if carried, err = sourcesOf(ctx, txq, locked.ID); err != nil {
				return err
			}
			// A settlement the target holds already is held by this very
			// draft: the index would refuse it, and the view lists it held.
			own := make(map[sourceRef]bool, len(carried))
			for _, h := range carried {
				own[h.ref()] = true
			}
			for _, r := range refs {
				if own[r] {
					out.refusal = heldByProblem(string(r.kind), r.id,
						gen.InvoicesWorkHeldBy{InvoiceId: locked.ID, Number: locked.Number, Status: locked.Status})
					return errRefused
				}
			}
			stored, err := txq.Lines(ctx, locked.ID)
			if err != nil {
				return fmt.Errorf("invoices: read document %d's lines: %w", locked.ID, err)
			}
			kept, err := storedLines(stored)
			if err != nil {
				return err
			}
			// A settlement's deductions keep their a-kontos' snapshots
			// (invoices work design D7), read from the documents' own rows.
			if err := withDeductionSnapshots(ctx, txq, kept); err != nil {
				return err
			}
			all = append(kept, lines...)
			rate, err := ratFromNumeric(locked.ExchangeRate)
			if err != nil {
				return err
			}
			_, totals, _ := summarize(taxedLines(all, codes), rate)
			if e := checkTotal(totals, nil); e != nil {
				out.invalid = e
				return errRefused
			}
			net, vatTotal, gross, vatNOK, err := numerics(totals)
			if err != nil {
				return err
			}
			deliveryDate, deliveryFrom, deliveryTo := widenDelivery(locked, spanRows(carried),
				[]heldSource{{date: from.Time}, {date: to.Time}})
			targetNote := locked.Note
			if targetNote == "" {
				targetNote = note
			}
			doc, err = txq.UpdateDraft(ctx, store.UpdateDraftParams{
				ID: locked.ID, CustomerID: locked.CustomerID, DeliveryDate: deliveryDate, DeliveryFrom: deliveryFrom, DeliveryTo: deliveryTo,
				DeliveryAddressLine1: locked.DeliveryAddressLine1, DeliveryAddressLine2: locked.DeliveryAddressLine2,
				DeliveryPostalCode: locked.DeliveryPostalCode, DeliveryCity: locked.DeliveryCity, DeliveryCountry: locked.DeliveryCountry,
				PaymentTermsDays: locked.PaymentTermsDays, YourReference: locked.YourReference, OurReference: locked.OurReference,
				OrderReference: locked.OrderReference, Note: targetNote, InternalNote: locked.InternalNote,
				NetTotal: net, VatTotal: vatTotal, GrossTotal: gross, VatTotalNok: vatNOK, Timesheet: locked.Timesheet, Now: s.deps.Clock(),
			})
			if err != nil {
				return fmt.Errorf("invoices: add consumption to draft %d: %w", locked.ID, err)
			}
		}
		invoiceID = doc.ID
		lineIDs, err := writeLines(ctx, txq, doc.ID, all)
		if err != nil {
			return err
		}
		// The settlements go under the lines after the target's own.
		offset := int32(len(all) - len(lines))
		var added []heldSource
		for _, line := range held {
			for _, h := range line {
				h.linePosition += offset
				added = append(added, h)
			}
		}
		if refusal, err := liveElsewhere(ctx, txq, doc.ID, added); refusal != nil || err != nil {
			out.refusal = refusal
			return cmp.Or(err, errRefused)
		}
		rows = append(carried, added...)
		if err := insertSources(ctx, txq, doc.ID, lineIDs, rows); err != nil {
			rows = added
			return err
		}
		// The draft's project, derived from the work it holds alone — a
		// settlement carries no project and makes none.
		if doc, err = setDocumentProject(ctx, txq, doc, workRows(rows), projectCodes(entries)); err != nil {
			return err
		}
		out.doc = doc
		return nil
	})
	switch {
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesFromConsumption404Response{}, nil
	case errors.Is(err, errSourceHeldElsewhere):
		refusal, err := heldElsewhere(ctx, q, invoiceID, rows)
		if err != nil {
			return nil, err
		}
		return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(refusal), nil
	case out.refusal != nil:
		return gen.PostInvoicesFromConsumption409ApplicationProblemPlusJSONResponse(*out.refusal), nil
	case out.invalid != nil:
		return gen.PostInvoicesFromConsumption400ApplicationProblemPlusJSONResponse(invalid(invalidConsumptionTitle, out.invalid)), nil
	case err != nil:
		return nil, err
	}
	resp, err := s.invoiceResponse(ctx, q, out.doc, profile, loc)
	if err != nil {
		return nil, err
	}
	if target != nil {
		return gen.PostInvoicesFromConsumption200JSONResponse(resp), nil
	}
	return gen.PostInvoicesFromConsumption201JSONResponse(resp), nil
}
