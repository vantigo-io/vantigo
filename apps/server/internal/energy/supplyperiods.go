package energy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the SupplyPeriods sub-resource
// (EP/SupplyPeriods/*Endpoint.cs): getEnergyMeteringPointsByIdSupplyPeriods,
// postEnergyMeteringPointsByIdSupplyPeriods,
// postEnergyMeteringPointsByIdSupplyPeriodsSwitch,
// postEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd,
// deleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId and
// putEnergyMeteringPointsByIdSupplyPeriodsByPeriodId (the terms) — the state machine
// (energy inventory §2.3): Active -> Ended (End), Active -> Cancelled
// (Cancel, also reachable from Ended — no illegal-transition guard besides
// End's own single check).

// Status strings are SupplyPeriodStatus's C# member names, stored verbatim
// (HasConversion<string>(), energy inventory §3.1) and matched against in
// SQL (the GiST exclusion constraint's WHERE and the overlap pre-check).
const (
	statusActive    = "Active"
	statusEnded     = "Ended"
	statusCancelled = "Cancelled"
)

const overlapTitle = "Overlapping supply period"
const overlapDetail = "The metering point already has a non-cancelled supply period at that time. End the existing period first."

// supplyPeriodRaceAttempts bounds db.RetrySerializable's retry of a supply
// period write against a genuine serialization failure (40001) only. It is
// not what keeps the GiST exclusion constraint's race clean: confirmed
// empirically, every attempt still deadlocked (40P01) without
// lockSupplyPeriods below — retrying alone does not reliably resolve that
// deadlock, so the lock, not this retry, is the fix's primary mechanism.
const supplyPeriodRaceAttempts = 3

// supplyPeriodLockClass namespaces lockSupplyPeriods's advisory lock key
// away from any other pg_advisory_xact_lock caller (internal/db/migrate.go's
// migration lock uses the single-bigint overload with a distinct constant;
// this uses the two-int32 overload, so a collision would need that other
// caller's key to equal this class in its high 32 bits, which it does not).
const supplyPeriodLockClass = 0x53555052 // "SUPR", arbitrary but memorable

// lockSupplyPeriods takes a transaction-scoped Postgres advisory lock keyed
// by meteringPointID, released automatically at commit or rollback. Every
// write to energy.supply_periods (Create, Switch, End and Cancel) takes it
// immediately before writing.
//
// Without it, two-plus concurrent writers targeting the same, exactly
// overlapping range can make PostgreSQL's own GiST exclusion-constraint
// check (§3.2) *deadlock* (40P01) rather than cleanly serialize one winner
// and 23P01-reject the rest: each inserter tentatively adds its index entry
// and then waits (XactLockTableWait) on any other in-flight transaction
// whose entry might conflict, and with three or more inserters racing the
// identical slot those waits can form a genuine cycle — this is PostgreSQL's
// own documented behaviour for EXCLUDE constraints under concurrent load,
// confirmed empirically against a real instance while porting this task's
// gated race test (four simultaneous creates on one metering point
// deadlocked repeatedly rather than resolving to one success and three
// 23P01s), not a bug in this module or a divergence from .NET. The lock
// forces every writer for one metering point through the constraint check
// one at a time — the constraint itself, not the lock, still decides who
// wins — so the observable outcome (exactly one success, the rest a generic
// 409 from a real 23P01) is unchanged; only the pathological deadlock is
// removed. db.RetrySerializable above is unrelated to this fix: it already
// existed as a backstop for a genuine 40001 serialization failure and
// remains one, but retrying on its own does not reliably resolve the
// deadlock this lock prevents — every attempt still deadlocked without it.
func lockSupplyPeriods(ctx context.Context, tx pgx.Tx, meteringPointID int32) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, int32(supplyPeriodLockClass), meteringPointID)
	return err
}

// hasUTCOffset reports whether t's zone offset is zero — SupplyPeriod.Validate's
// `Offset != TimeSpan.Zero` check (SupplyPeriod.cs:25), which looks at the
// offset value itself, not whether the literal text ended in "Z": a
// "+00:00" timestamp is just as UTC as a "Z" one.
//
// Call this only on a timestamp the *request* supplied, never one read back
// from the database. Fix-round finding: pgx decodes a `timestamptz` column
// in the process's local time zone (time.Local, whatever the process's TZ
// happens to be) — unlike Npgsql's DateTimeOffset, which always decodes at
// offset 0 regardless of the server's zone. .NET's own SupplyPeriod.Validate
// is only ever called with request-supplied timestamps (energy inventory
// §2.3 line 133: "only checked for request-supplied timestamps"), so this
// distinction never had to be made there; it must be made here, or every
// request through a handler that offset-checks a decoded value 400s
// whenever the process runs under a non-UTC TZ — invisible under the UTC
// TZ this repo's tests and CI always run with, until it fires in
// production. See validateSupplyPeriodEnd below, and its test-file
// TestEndSupplyPeriod_NotAffectedByProcessTimeZone (energy_test package)
// and TestValidateSupplyPeriodEnd_DoesNotOffsetCheckStart (domain_test.go).
func hasUTCOffset(t time.Time) bool {
	_, offset := t.Zone()
	return offset == 0
}

// validateSupplyPeriodStart is SupplyPeriod.Validate(start, nil) as Create
// and Switch call it (SupplyPeriod.cs:23-27): start is always
// request-supplied and end is always nil at creation time, so only start's
// offset needs checking. Before that, start must be there at all: the router
// does not enforce a body's required fields, so a body without one decodes
// to the zero time, which is UTC and would pass — label names the field in
// the message ("Start", "Switch at").
func validateSupplyPeriodStart(start time.Time, label string) string {
	if start.IsZero() {
		return label + " is required."
	}
	if !hasUTCOffset(start) {
		return "Start and end must be UTC timestamps."
	}
	return ""
}

// validateSupplyPeriodEnd is SupplyPeriod.Validate(start, end) as End calls
// it (SupplyPeriod.cs:23-27), but offset-checking only end: start here is
// the period's own Start, read back from the database by
// PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd — see
// hasUTCOffset's comment for why it must never be offset-checked. end is
// always request-supplied and is checked; the ordering rule (end strictly
// after start) is a plain instant comparison, unaffected by which zone
// either value happens to be represented in.
func validateSupplyPeriodEnd(start, end time.Time) string {
	if !hasUTCOffset(end) {
		return "Start and end must be UTC timestamps."
	}
	if !end.After(start) {
		return "End must be later than start."
	}
	return ""
}

// supplyPeriodResponseOf builds the response for p; names holds its power
// tariff's name (tariffNames).
func supplyPeriodResponseOf(p store.EnergySupplyPeriod, names map[int32]string) gen.SupplyPeriodResponse {
	cadence := p.Cadence
	return gen.SupplyPeriodResponse{
		Id: p.ID, MeteringPointId: p.MeteringPointID, CustomerId: p.CustomerID,
		Start: p.Start, End: p.End, Status: p.Status,
		TariffId: p.TariffID, TariffName: tariffNameOf(names, p.TariffID), Cadence: &cadence,
	}
}

// supplyPeriodResponseRead is supplyPeriodResponseOf for one period, reading
// its tariff's name.
func supplyPeriodResponseRead(ctx context.Context, q *store.Queries, p store.EnergySupplyPeriod) (gen.SupplyPeriodResponse, error) {
	names, err := tariffNames(ctx, q, p.TariffID)
	if err != nil {
		return gen.SupplyPeriodResponse{}, err
	}
	return supplyPeriodResponseOf(p, names), nil
}

// parseSupplyTerms reads a request's terms (energy tariffs plan, reading 6)
// before the write's transaction: the cadence must be one of the three. The
// tariff is judged inside the transaction, under FOR SHARE
// (supplyTermsRefusal). It answers the terms and the cadence's refusal keyed
// by field, nil when none.
func parseSupplyTerms(tariffID *int32, cadence *string) (supplyTerms, map[string][]string) {
	c, ok := parseCadence(cadence)
	if !ok {
		return supplyTerms{TariffID: tariffID}, map[string][]string{"cadence": {cadenceMessage}}
	}
	return supplyTerms{TariffID: tariffID, Cadence: c}, nil
}

// supplyTermsRefusal judges the terms inside the write's transaction: the
// tariff t (shareTariff of terms.TariffID) must be assignable as a power
// tariff (current is the one the period holds, nil for a new period), and
// parseSupplyTerms's refusal, if any, joins it. It answers the refusals keyed
// by field, nil when the terms may be written.
func supplyTermsRefusal(terms supplyTerms, t *store.GetTariffForShareRow, cadenceErrs map[string][]string, current *int32) map[string][]string {
	errs := map[string][]string{}
	if msg := tariffRefusal(terms.TariffID, t, tariffKindPower, current); msg != "" {
		errs["tariffId"] = []string{msg}
	}
	for field, msgs := range cadenceErrs {
		errs[field] = msgs
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// GetEnergyMeteringPointsByIdSupplyPeriods List supply periods
// (GET /api/v1/energy/metering-points/{id}/supply-periods)
//
// GetSupplyPeriodsEndpoint.cs:11-18: existence (404) -> every period for
// the point, ordered by Start.
func (s *server) GetEnergyMeteringPointsByIdSupplyPeriods(ctx context.Context, req gen.GetEnergyMeteringPointsByIdSupplyPeriodsRequestObject) (gen.GetEnergyMeteringPointsByIdSupplyPeriodsResponseObject, error) {
	q := store.New(s.deps.Pool)
	exists, err := q.MeteringPointExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: check metering point exists: %w", err)
	}
	if !exists {
		return gen.GetEnergyMeteringPointsByIdSupplyPeriods404Response{}, nil
	}
	rows, err := q.ListSupplyPeriodsByMeteringPoint(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: list supply periods: %w", err)
	}
	tariffIDs := make([]*int32, 0, len(rows))
	for _, r := range rows {
		tariffIDs = append(tariffIDs, r.TariffID)
	}
	names, err := tariffNames(ctx, q, tariffIDs...)
	if err != nil {
		return nil, err
	}
	data := make([]gen.SupplyPeriodResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, supplyPeriodResponseOf(r, names))
	}
	return gen.GetEnergyMeteringPointsByIdSupplyPeriods200JSONResponse(data), nil
}

// PostEnergyMeteringPointsByIdSupplyPeriods Create a supply period
// (POST /api/v1/energy/metering-points/{id}/supply-periods)
//
// CreateSupplyPeriodEndpoint.cs:16-28 (energy inventory §1.1 line 39): the
// order is customerId<=0 (400) -> the UTC-offset-only check on start, since
// end is always nil at creation (400) -> metering point existence (404) ->
// the customer-directory miss (400 on field customerId, *not* 404 — this
// task's dispatch correction 1) -> the terms, tariffId then cadence (400,
// energy tariffs plan reading 6) -> the friendly overlap pre-check (409).
// The tariff, the overlap pre-check and the write share one transaction: the
// tariff is read FOR SHARE there (reading 5), so a racing archive or delete is
// seen as the 400 it would have been, and a foreign-key violation on it, the
// backstop, answers the same 400 "does not exist".
// The GiST exclusion constraint is the real backstop for a concurrent
// overlap (energy inventory §3.2/§5): a race that slips past the pre-check
// surfaces as a plain error here, which module.ResponseError/httpx.WriteError
// map to a *different*, generic 409 body — dispatch correction 4.
func (s *server) PostEnergyMeteringPointsByIdSupplyPeriods(ctx context.Context, req gen.PostEnergyMeteringPointsByIdSupplyPeriodsRequestObject) (gen.PostEnergyMeteringPointsByIdSupplyPeriodsResponseObject, error) {
	body := gen.CreateSupplyPeriodRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	if body.CustomerId <= 0 {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid supply period", map[string][]string{"customerId": {"Customer ID must be greater than zero."}})), nil
	}
	if msg := validateSupplyPeriodStart(body.Start, "Start"); msg != "" {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid supply period", map[string][]string{"start": {msg}})), nil
	}

	q := store.New(s.deps.Pool)
	exists, err := q.MeteringPointExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: check metering point exists: %w", err)
	}
	if !exists {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods404Response{}, nil
	}

	customer, err := s.deps.Directory.Customer(ctx, body.CustomerId)
	if err != nil {
		return nil, fmt.Errorf("energy: look up customer: %w", err)
	}
	if customer == nil {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid supply period", map[string][]string{"customerId": {fmt.Sprintf("Customer %d does not exist.", body.CustomerId)}})), nil
	}
	terms, cadenceErrs := parseSupplyTerms(body.TariffId, body.Cadence)

	var created store.EnergySupplyPeriod
	var termErrs map[string][]string
	overlaps := false
	err = db.RetrySerializable(ctx, supplyPeriodRaceAttempts, func() error {
		// Every attempt starts from a fresh outcome, as the switch's does.
		termErrs, overlaps = nil, false
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			tariff, terr := shareTariff(ctx, txq, terms.TariffID)
			if terr != nil {
				return terr
			}
			// A refusal is the outcome, not an error: nothing is written.
			if termErrs = supplyTermsRefusal(terms, tariff, cadenceErrs, nil); termErrs != nil {
				return nil
			}
			var oerr error
			overlaps, oerr = txq.SupplyPeriodOverlapExists(ctx, store.SupplyPeriodOverlapExistsParams{MeteringPointID: req.Id, Start: body.Start})
			if oerr != nil || overlaps {
				return oerr
			}
			var ierr error
			created, ierr = insertSupplyPeriod(ctx, tx, req.Id, body.CustomerId, body.Start, terms)
			return ierr
		})
	})
	if fk := tariffFKRefusal(err, supplyPeriodTariffFK, "tariffId", terms.TariffID); fk != nil {
		termErrs, err = fk, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: create supply period: %w", err)
	}
	if termErrs != nil {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid supply period", termErrs)), nil
	}
	if overlaps {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods409ApplicationProblemPlusJSONResponse(
			apicommon.ProblemStatus(overlapTitle, overlapDetail, http.StatusConflict)), nil
	}
	resp, err := supplyPeriodResponseRead(ctx, q, created)
	if err != nil {
		return nil, err
	}
	return gen.PostEnergyMeteringPointsByIdSupplyPeriods201JSONResponse(resp), nil
}

// PostEnergyMeteringPointsByIdSupplyPeriodsSwitch Switch supply period customer
// (POST /api/v1/energy/metering-points/{id}/supply-periods/switch)
//
// SwitchSupplyPeriodEndpoint.cs:13-75 (energy inventory §1.1 line 40): the
// opposite order from Create above (dispatch correction 2) — metering point
// existence (404) *first*, then customerId<=0 (400), then the switchAt
// UTC-offset check (400), then the directory miss (400 on customerId), then
// the terms (400 on tariffId, cadence) — the cadence's word before the
// transaction, the tariff first inside it, FOR SHARE (energy tariffs plan,
// reading 5), with a foreign-key violation on it answered as the same 400.
// Inside one transaction: if an open (end IS NULL), Active period already
// exists, reject the same customer (400) or a switchAt at or before its
// start (400), else end it at switchAt; otherwise (no open period) this is
// a plain "move-in" and runs the same friendly overlap pre-check as
// Create — which can 409 against a *historical* (Ended) period whose own
// end is still in the future relative to switchAt (dispatch correction 5,
// energy inventory §2.3 line 161, pinned by
// TestSwitchSupplyPeriod_RejectsOverlapWithHistoricalPeriod). No
// overlap pre-check runs on the "ends current, starts new" branch: ending
// the old period first removes the conflict, since the two periods share
// exactly the boundary the half-open interval rule allows.
func (s *server) PostEnergyMeteringPointsByIdSupplyPeriodsSwitch(ctx context.Context, req gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitchRequestObject) (gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitchResponseObject, error) {
	q := store.New(s.deps.Pool)
	exists, err := q.MeteringPointExists(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("energy: check metering point exists: %w", err)
	}
	if !exists {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch404Response{}, nil
	}

	body := gen.SwitchSupplyPeriodRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	if body.CustomerId <= 0 {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid supply period", map[string][]string{"customerId": {"Customer ID must be greater than zero."}})), nil
	}
	if msg := validateSupplyPeriodStart(body.SwitchAt, "Switch at"); msg != "" {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid supply period", map[string][]string{"switchAt": {msg}})), nil
	}
	customer, err := s.deps.Directory.Customer(ctx, body.CustomerId)
	if err != nil {
		return nil, fmt.Errorf("energy: look up customer: %w", err)
	}
	if customer == nil {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid supply period", map[string][]string{"customerId": {fmt.Sprintf("Customer %d does not exist.", body.CustomerId)}})), nil
	}
	// The new customer's terms are the body's: a switch never inherits the
	// ended period's tariff (energy tariffs plan, reading 6).
	terms, cadenceErrs := parseSupplyTerms(body.TariffId, body.Cadence)

	var out switchOutcome
	err = db.RetrySerializable(ctx, supplyPeriodRaceAttempts, func() error {
		// Every attempt starts from a fresh outcome: a retried attempt must
		// not carry a validation/conflict verdict a discarded earlier
		// attempt reached before deadlocking on the insert.
		out = switchOutcome{}
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			tariff, terr := shareTariff(ctx, store.New(tx), terms.TariffID)
			if terr != nil {
				return terr
			}
			if out.validationErrs = supplyTermsRefusal(terms, tariff, cadenceErrs, nil); out.validationErrs != nil {
				return nil
			}
			var serr error
			out, serr = switchSupplyPeriod(ctx, tx, req.Id, body.CustomerId, body.SwitchAt, terms)
			return serr
		})
	})
	if fk := tariffFKRefusal(err, supplyPeriodTariffFK, "tariffId", terms.TariffID); fk != nil {
		out, err = switchOutcome{validationErrs: fk}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: switch supply period: %w", err)
	}
	if out.validationErrs != nil {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid supply period", out.validationErrs)), nil
	}
	if out.conflict {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch409ApplicationProblemPlusJSONResponse(
			apicommon.ProblemStatus(overlapTitle, overlapDetail, http.StatusConflict)), nil
	}

	var endedTariff *int32
	if out.ended != nil {
		endedTariff = out.ended.TariffID
	}
	names, err := tariffNames(ctx, q, endedTariff, out.created.TariffID)
	if err != nil {
		return nil, err
	}
	var endedResp *gen.SupplyPeriodResponse
	if out.ended != nil {
		r := supplyPeriodResponseOf(*out.ended, names)
		endedResp = &r
	}
	return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch201JSONResponse(gen.SwitchSupplyPeriodResponse{
		EndedPeriod: endedResp, NewPeriod: supplyPeriodResponseOf(out.created, names),
	}), nil
}

// insertSupplyPeriod is the create's write, inside the caller's transaction:
// the metering point's supply-period lock, then the new open-ended Active
// period on terms. The create endpoint and the import (import.go, with
// defaultSupplyTerms) both assign a customer through it; the GiST exclusion
// constraint stays the backstop.
func insertSupplyPeriod(ctx context.Context, tx pgx.Tx, meteringPointID, customerID int32, start time.Time, terms supplyTerms) (store.EnergySupplyPeriod, error) {
	if err := lockSupplyPeriods(ctx, tx, meteringPointID); err != nil {
		return store.EnergySupplyPeriod{}, err
	}
	return store.New(tx).InsertSupplyPeriod(ctx, store.InsertSupplyPeriodParams{
		MeteringPointID: meteringPointID, CustomerID: customerID, Start: start, Status: statusActive,
		TariffID: terms.TariffID, Cadence: terms.Cadence,
	})
}

// switchOutcome is what switchSupplyPeriod did: a validation refusal keyed as
// the switch endpoint's 400 keys it, an overlap (its 409), or the period
// ended and the one created.
type switchOutcome struct {
	validationErrs map[string][]string
	conflict       bool
	ended          *store.EnergySupplyPeriod
	created        store.EnergySupplyPeriod
}

// switchSupplyPeriod is the switch's transaction body, under the metering
// point's supply-period lock: when an open Active period exists, the same
// customer is refused, a switch date at or before its start is refused, and
// otherwise it ends at the switch date; with none, the move-in runs the
// create's overlap pre-check. Then the customer's period starts at the switch
// date, on terms. The switch endpoint and the import (import.go, with
// defaultSupplyTerms) both switch a customer through it. A refusal is the outcome, not an error: the caller's
// transaction commits nothing it has not written.
func switchSupplyPeriod(ctx context.Context, tx pgx.Tx, meteringPointID, customerID int32, at time.Time, terms supplyTerms) (switchOutcome, error) {
	var out switchOutcome
	if err := lockSupplyPeriods(ctx, tx, meteringPointID); err != nil {
		return out, err
	}
	txq := store.New(tx)
	active, err := txq.GetActiveOpenSupplyPeriod(ctx, meteringPointID)
	hasActive := true
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		hasActive = false
	case err != nil:
		return out, err
	}

	if hasActive {
		if customerID == active.CustomerID {
			out.validationErrs = map[string][]string{"customerId": {"The customer is already the active customer."}}
			return out, nil
		}
		if !at.After(active.Start) {
			out.validationErrs = map[string][]string{"switchAt": {"Switch date must be after the active period's start."}}
			return out, nil
		}
		endedRow, err := txq.EndSupplyPeriod(ctx, store.EndSupplyPeriodParams{ID: active.ID, EndAt: at, Status: statusEnded})
		if err != nil {
			return out, err
		}
		out.ended = &endedRow
	} else {
		overlaps, err := txq.SupplyPeriodOverlapExists(ctx, store.SupplyPeriodOverlapExistsParams{MeteringPointID: meteringPointID, Start: at})
		if err != nil {
			return out, err
		}
		if overlaps {
			out.conflict = true
			return out, nil
		}
	}

	out.created, err = txq.InsertSupplyPeriod(ctx, store.InsertSupplyPeriodParams{
		MeteringPointID: meteringPointID, CustomerID: customerID, Start: at, Status: statusActive,
		TariffID: terms.TariffID, Cadence: terms.Cadence,
	})
	return out, err
}

// PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd End a supply period
// (POST /api/v1/energy/metering-points/{id}/supply-periods/{periodId}/end)
//
// EndSupplyPeriodEndpoint.cs:12-24: lookup by (periodId, meteringPointId)
// (404) -> a Cancelled period cannot be ended (400, a *plain* Problem body
// despite the contract's declared HttpValidationProblemDetails shape for
// this operation's 400 — energy inventory §1.1 line 41/§8 oddity 2,
// reproduced here via validationProblemNoErrors) -> the UTC/ordering check
// against the period's own Start (400 ValidationProblem, field "end") ->
// apply. Fix-round finding: re-ending a period *extends* its range
// ("end" moves later), which the GiST exclusion constraint (§3.2) treats
// exactly like a fresh insert — it can race a concurrent Create/Switch/End
// on the same metering point. The write is now the same
// lock-then-transact shape as Create and Switch (lockSupplyPeriods'
// comment), for the same race reason and so a reader is not left wondering
// why one write path is unserialized and the others are not.
func (s *server) PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd(ctx context.Context, req gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEndRequestObject) (gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEndResponseObject, error) {
	q := store.New(s.deps.Pool)
	period, err := q.GetSupplyPeriod(ctx, store.GetSupplyPeriodParams{ID: req.PeriodId, MeteringPointID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: get supply period: %w", err)
	}
	if period.Status == statusCancelled {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd400ApplicationProblemPlusJSONResponse(
			validationProblemNoErrors("Invalid supply period", "A cancelled period cannot be ended.")), nil
	}

	body := gen.EndSupplyPeriodRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	if msg := validateSupplyPeriodEnd(period.Start, body.End); msg != "" {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid supply period", map[string][]string{"end": {msg}})), nil
	}

	var updated store.EnergySupplyPeriod
	err = db.RetrySerializable(ctx, supplyPeriodRaceAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if lerr := lockSupplyPeriods(ctx, tx, req.Id); lerr != nil {
				return lerr
			}
			var uerr error
			updated, uerr = store.New(tx).EndSupplyPeriod(ctx, store.EndSupplyPeriodParams{ID: period.ID, EndAt: body.End, Status: statusEnded})
			return uerr
		})
	})
	if err != nil {
		return nil, fmt.Errorf("energy: end supply period: %w", err)
	}
	resp, err := supplyPeriodResponseRead(ctx, q, updated)
	if err != nil {
		return nil, err
	}
	return gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd200JSONResponse(resp), nil
}

// PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodId Change a supply period's terms
// (PUT /api/v1/energy/metering-points/{id}/supply-periods/{periodId})
//
// Energy tariffs plan, reading 6: a period's power tariff and cadence, nothing
// else of it, so the periods created before tariffs existed can be given one.
// Lookup by (periodId, meteringPointId) (404) -> a Cancelled period is not
// changed (400) -> the terms, tariffId then cadence, where the tariff the
// period already holds passes even archived (400) -> the write, under the
// point's supply-period lock as every write to supply_periods takes it.
// Inside the write's transaction the tariff is read FOR SHARE first (reading
// 5), then the lock is taken and the period read again, so the Cancelled
// check and the tariff it holds are judged as they are when it is written;
// a foreign-key violation on the tariff answers the field's "does not exist".
func (s *server) PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodId(ctx context.Context, req gen.PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdRequestObject) (gen.PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	period, err := q.GetSupplyPeriod(ctx, store.GetSupplyPeriodParams{ID: req.PeriodId, MeteringPointID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: get supply period: %w", err)
	}
	if period.Status == statusCancelled {
		return gen.PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodId400ApplicationProblemPlusJSONResponse(
			validationProblemNoErrors("Invalid supply period", "A cancelled period cannot be changed.")), nil
	}

	body := gen.UpdateSupplyPeriodTermsRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	terms, cadenceErrs := parseSupplyTerms(body.TariffId, body.Cadence)

	var updated store.EnergySupplyPeriod
	var termErrs map[string][]string
	gone, cancelled := false, false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		// The tariff before the supply-period lock, as every assignment takes
		// them (shareTariff).
		tariff, terr := shareTariff(ctx, txq, terms.TariffID)
		if terr != nil {
			return terr
		}
		if lerr := lockSupplyPeriods(ctx, tx, req.Id); lerr != nil {
			return lerr
		}
		held, gerr := txq.GetSupplyPeriod(ctx, store.GetSupplyPeriodParams{ID: req.PeriodId, MeteringPointID: req.Id})
		if errors.Is(gerr, pgx.ErrNoRows) {
			gone = true
			return nil
		}
		if gerr != nil {
			return gerr
		}
		if held.Status == statusCancelled {
			cancelled = true
			return nil
		}
		if termErrs = supplyTermsRefusal(terms, tariff, cadenceErrs, held.TariffID); termErrs != nil {
			return nil
		}
		var uerr error
		updated, uerr = txq.UpdateSupplyPeriodTerms(ctx, store.UpdateSupplyPeriodTermsParams{
			ID: held.ID, TariffID: terms.TariffID, Cadence: terms.Cadence,
		})
		return uerr
	})
	if fk := tariffFKRefusal(err, supplyPeriodTariffFK, "tariffId", terms.TariffID); fk != nil {
		termErrs, err = fk, nil
	}
	switch {
	case err != nil:
		return nil, fmt.Errorf("energy: change supply period terms: %w", err)
	case gone:
		return gen.PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodId404Response{}, nil
	case cancelled:
		return gen.PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodId400ApplicationProblemPlusJSONResponse(
			validationProblemNoErrors("Invalid supply period", "A cancelled period cannot be changed.")), nil
	case termErrs != nil:
		return gen.PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodId400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid supply period", termErrs)), nil
	}
	resp, err := supplyPeriodResponseRead(ctx, q, updated)
	if err != nil {
		return nil, err
	}
	return gen.PutEnergyMeteringPointsByIdSupplyPeriodsByPeriodId200JSONResponse(resp), nil
}

// DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId Cancel a supply period
// (DELETE /api/v1/energy/metering-points/{id}/supply-periods/{periodId})
//
// CancelSupplyPeriodEndpoint.cs:11-18: lookup (404) -> Status = Cancelled,
// unconditionally. No status guard: an already-Ended or already-Cancelled
// period can be cancelled again (energy inventory §1.1/§8 oddity 5).
// Cancelling only ever *removes* a row from the exclusion constraint's
// concern (its WHERE clause excludes Cancelled rows), so it cannot itself
// lose a race the way End's extension or Create/Switch's insert can — but
// it takes the same lock-then-transact shape as those anyway (fix-round
// finding), so every write to supply_periods is uniformly serialized per
// metering point and a reader never has to work out why this one write
// path alone was left different.
func (s *server) DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId(ctx context.Context, req gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdRequestObject) (gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetSupplyPeriod(ctx, store.GetSupplyPeriodParams{ID: req.PeriodId, MeteringPointID: req.Id}); errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("energy: get supply period: %w", err)
	}

	err := db.RetrySerializable(ctx, supplyPeriodRaceAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if lerr := lockSupplyPeriods(ctx, tx, req.Id); lerr != nil {
				return lerr
			}
			_, uerr := store.New(tx).SetSupplyPeriodStatus(ctx, store.SetSupplyPeriodStatusParams{ID: req.PeriodId, Status: statusCancelled})
			return uerr
		})
	})
	if err != nil {
		return nil, fmt.Errorf("energy: cancel supply period: %w", err)
	}
	return gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId204Response{}, nil
}
