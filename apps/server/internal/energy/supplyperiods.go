package energy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
)

// This file is the SupplyPeriods sub-resource
// (EP/SupplyPeriods/*Endpoint.cs): getEnergyMeteringPointsByIdSupplyPeriods,
// postEnergyMeteringPointsByIdSupplyPeriods,
// postEnergyMeteringPointsByIdSupplyPeriodsSwitch,
// postEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd and
// deleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId — the state machine
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
// period write against a serialization failure (40001) — a defensive
// backstop; lockSupplyPeriods below is what actually keeps the exclusion
// constraint's race clean, see its comment.
const supplyPeriodRaceAttempts = 3

// supplyPeriodLockClass namespaces lockSupplyPeriods's advisory lock key
// away from any other pg_advisory_xact_lock caller (internal/db/migrate.go's
// migration lock uses the single-bigint overload with a distinct constant;
// this uses the two-int32 overload, so a collision would need that other
// caller's key to equal this class in its high 32 bits, which it does not).
const supplyPeriodLockClass = 0x53555052 // "SUPR", arbitrary but memorable

// lockSupplyPeriods takes a transaction-scoped Postgres advisory lock keyed
// by meteringPointID, released automatically at commit or rollback. Create
// and Switch both take it immediately before writing to
// energy.supply_periods.
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
// removed. db.RetrySerializable above stays as a defensive backstop for a
// genuine serialization failure, not as this mechanism's primary defence.
func lockSupplyPeriods(ctx context.Context, tx pgx.Tx, meteringPointID int32) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, int32(supplyPeriodLockClass), meteringPointID)
	return err
}

// hasUTCOffset reports whether t's zone offset is zero — SupplyPeriod.Validate's
// `Offset != TimeSpan.Zero` check (SupplyPeriod.cs:25), which looks at the
// offset value itself, not whether the literal text ended in "Z": a
// "+00:00" timestamp is just as UTC as a "Z" one.
func hasUTCOffset(t time.Time) bool {
	_, offset := t.Zone()
	return offset == 0
}

// validateSupplyPeriodTimestamps is SupplyPeriod.Validate(start, end)
// (SupplyPeriod.cs:23-27), shared by create, switch and end: both
// timestamps (when end is given) must carry a zero UTC offset, and end
// must be strictly after start (equal is rejected).
func validateSupplyPeriodTimestamps(start time.Time, end *time.Time) string {
	if !hasUTCOffset(start) || (end != nil && !hasUTCOffset(*end)) {
		return "Start and end must be UTC timestamps."
	}
	if end != nil && !end.After(start) {
		return "End must be later than start."
	}
	return ""
}

// supplyPeriodsOverlap is SupplyPeriod.Overlaps (SupplyPeriod.cs:17-19):
// half-open [start, end) interval semantics, a nil end treated as +∞. It is
// not called by any handler here — the DB-backed SupplyPeriodOverlapExists
// query and the GiST exclusion constraint are what actually decide overlap
// (energy inventory §2.3/§3.2) — but it exists as the same pure predicate
// SupplyPeriodTests pins directly (ported in domain_test.go).
func supplyPeriodsOverlap(firstStart time.Time, firstEnd *time.Time, secondStart time.Time, secondEnd *time.Time) bool {
	firstBeforeSecondEnd := secondEnd == nil || firstStart.Before(*secondEnd)
	secondBeforeFirstEnd := firstEnd == nil || secondStart.Before(*firstEnd)
	return firstBeforeSecondEnd && secondBeforeFirstEnd
}

func supplyPeriodResponseOf(p store.EnergySupplyPeriod) gen.SupplyPeriodResponse {
	return gen.SupplyPeriodResponse{
		Id: p.ID, MeteringPointId: p.MeteringPointID, CustomerId: p.CustomerID,
		Start: p.Start, End: p.End, Status: p.Status,
	}
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
	data := make([]gen.SupplyPeriodResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, supplyPeriodResponseOf(r))
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
// task's dispatch correction 1) -> the friendly overlap pre-check (409).
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
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods400ApplicationProblemPlusJSONResponse(validationProblem(
			"Invalid supply period", map[string][]string{"customerId": {"Customer ID must be greater than zero."}})), nil
	}
	if msg := validateSupplyPeriodTimestamps(body.Start, nil); msg != "" {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods400ApplicationProblemPlusJSONResponse(validationProblem(
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
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods400ApplicationProblemPlusJSONResponse(validationProblem(
			"Invalid supply period", map[string][]string{"customerId": {fmt.Sprintf("Customer %d does not exist.", body.CustomerId)}})), nil
	}

	overlaps, err := q.SupplyPeriodOverlapExists(ctx, store.SupplyPeriodOverlapExistsParams{MeteringPointID: req.Id, Start: body.Start})
	if err != nil {
		return nil, fmt.Errorf("energy: check supply period overlap: %w", err)
	}
	if overlaps {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriods409ApplicationProblemPlusJSONResponse(
			problemStatus(overlapTitle, overlapDetail, http.StatusConflict)), nil
	}

	var created store.EnergySupplyPeriod
	err = db.RetrySerializable(ctx, supplyPeriodRaceAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if lerr := lockSupplyPeriods(ctx, tx, req.Id); lerr != nil {
				return lerr
			}
			var ierr error
			created, ierr = store.New(tx).InsertSupplyPeriod(ctx, store.InsertSupplyPeriodParams{
				MeteringPointID: req.Id, CustomerID: body.CustomerId, Start: body.Start, Status: statusActive,
			})
			return ierr
		})
	})
	if err != nil {
		return nil, fmt.Errorf("energy: create supply period: %w", err)
	}
	return gen.PostEnergyMeteringPointsByIdSupplyPeriods201JSONResponse(supplyPeriodResponseOf(created)), nil
}

// PostEnergyMeteringPointsByIdSupplyPeriodsSwitch Switch supply period customer
// (POST /api/v1/energy/metering-points/{id}/supply-periods/switch)
//
// SwitchSupplyPeriodEndpoint.cs:13-75 (energy inventory §1.1 line 40): the
// opposite order from Create above (dispatch correction 2) — metering point
// existence (404) *first*, then customerId<=0 (400), then the switchAt
// UTC-offset check (400), then the directory miss (400 on customerId).
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
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch400ApplicationProblemPlusJSONResponse(validationProblem(
			"Invalid supply period", map[string][]string{"customerId": {"Customer ID must be greater than zero."}})), nil
	}
	if msg := validateSupplyPeriodTimestamps(body.SwitchAt, nil); msg != "" {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch400ApplicationProblemPlusJSONResponse(validationProblem(
			"Invalid supply period", map[string][]string{"switchAt": {msg}})), nil
	}
	customer, err := s.deps.Directory.Customer(ctx, body.CustomerId)
	if err != nil {
		return nil, fmt.Errorf("energy: look up customer: %w", err)
	}
	if customer == nil {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch400ApplicationProblemPlusJSONResponse(validationProblem(
			"Invalid supply period", map[string][]string{"customerId": {fmt.Sprintf("Customer %d does not exist.", body.CustomerId)}})), nil
	}

	var (
		validationErrs map[string][]string
		conflict       bool
		ended          *store.EnergySupplyPeriod
		created        store.EnergySupplyPeriod
	)
	err = db.RetrySerializable(ctx, supplyPeriodRaceAttempts, func() error {
		// Reset every attempt's ambiguous outputs: a retried attempt must
		// not carry a validation/conflict verdict a discarded earlier
		// attempt reached before deadlocking on the insert below.
		validationErrs, conflict, ended = nil, false, nil

		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if lerr := lockSupplyPeriods(ctx, tx, req.Id); lerr != nil {
				return lerr
			}
			txq := store.New(tx)
			active, aerr := txq.GetActiveOpenSupplyPeriod(ctx, req.Id)
			hasActive := true
			switch {
			case errors.Is(aerr, pgx.ErrNoRows):
				hasActive = false
			case aerr != nil:
				return aerr
			}

			if hasActive {
				if body.CustomerId == active.CustomerID {
					validationErrs = map[string][]string{"customerId": {"The customer is already the active customer."}}
					return nil
				}
				if !body.SwitchAt.After(active.Start) {
					validationErrs = map[string][]string{"switchAt": {"Switch date must be after the active period's start."}}
					return nil
				}
				endedRow, eerr := txq.EndSupplyPeriod(ctx, store.EndSupplyPeriodParams{ID: active.ID, EndAt: body.SwitchAt, Status: statusEnded})
				if eerr != nil {
					return eerr
				}
				ended = &endedRow
			} else {
				overlaps, oerr := txq.SupplyPeriodOverlapExists(ctx, store.SupplyPeriodOverlapExistsParams{MeteringPointID: req.Id, Start: body.SwitchAt})
				if oerr != nil {
					return oerr
				}
				if overlaps {
					conflict = true
					return nil
				}
			}

			var ierr error
			created, ierr = txq.InsertSupplyPeriod(ctx, store.InsertSupplyPeriodParams{
				MeteringPointID: req.Id, CustomerID: body.CustomerId, Start: body.SwitchAt, Status: statusActive,
			})
			return ierr
		})
	})
	if err != nil {
		return nil, fmt.Errorf("energy: switch supply period: %w", err)
	}
	if validationErrs != nil {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch400ApplicationProblemPlusJSONResponse(
			validationProblem("Invalid supply period", validationErrs)), nil
	}
	if conflict {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch409ApplicationProblemPlusJSONResponse(
			problemStatus(overlapTitle, overlapDetail, http.StatusConflict)), nil
	}

	var endedResp *gen.SupplyPeriodResponse
	if ended != nil {
		r := supplyPeriodResponseOf(*ended)
		endedResp = &r
	}
	return gen.PostEnergyMeteringPointsByIdSupplyPeriodsSwitch201JSONResponse(gen.SwitchSupplyPeriodResponse{
		EndedPeriod: endedResp, NewPeriod: supplyPeriodResponseOf(created),
	}), nil
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
// apply.
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
	if msg := validateSupplyPeriodTimestamps(period.Start, &body.End); msg != "" {
		return gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd400ApplicationProblemPlusJSONResponse(
			validationProblem("Invalid supply period", map[string][]string{"end": {msg}})), nil
	}

	updated, err := q.EndSupplyPeriod(ctx, store.EndSupplyPeriodParams{ID: period.ID, EndAt: body.End, Status: statusEnded})
	if err != nil {
		return nil, fmt.Errorf("energy: end supply period: %w", err)
	}
	return gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd200JSONResponse(supplyPeriodResponseOf(updated)), nil
}

// DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId Cancel a supply period
// (DELETE /api/v1/energy/metering-points/{id}/supply-periods/{periodId})
//
// CancelSupplyPeriodEndpoint.cs:11-18: lookup (404) -> Status = Cancelled,
// unconditionally. No status guard: an already-Ended or already-Cancelled
// period can be cancelled again (energy inventory §1.1/§8 oddity 5).
func (s *server) DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId(ctx context.Context, req gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdRequestObject) (gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetSupplyPeriod(ctx, store.GetSupplyPeriodParams{ID: req.PeriodId, MeteringPointID: req.Id}); errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("energy: get supply period: %w", err)
	}
	if _, err := q.SetSupplyPeriodStatus(ctx, store.SetSupplyPeriodStatusParams{ID: req.PeriodId, Status: statusCancelled}); err != nil {
		return nil, fmt.Errorf("energy: cancel supply period: %w", err)
	}
	return gen.DeleteEnergyMeteringPointsByIdSupplyPeriodsByPeriodId204Response{}, nil
}
