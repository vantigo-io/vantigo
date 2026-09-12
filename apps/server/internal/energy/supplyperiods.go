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
// offset needs checking.
func validateSupplyPeriodStart(start time.Time) string {
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
	if msg := validateSupplyPeriodStart(body.Start); msg != "" {
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
	if msg := validateSupplyPeriodStart(body.SwitchAt); msg != "" {
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
			validationProblem("Invalid supply period", map[string][]string{"end": {msg}})), nil
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
	return gen.PostEnergyMeteringPointsByIdSupplyPeriodsByPeriodIdEnd200JSONResponse(supplyPeriodResponseOf(updated)), nil
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
