package staff

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the installation's Staff settings (design §3.6): the business
// time zone, the employee number sequence, the pension provider and the
// injury insurer (delivery A1), the two retention periods with the
// retention run's state, derived (delivery A2), and the self-certification
// policy and automatic carry-over (delivery B, absence_settings.go; plan
// reading 2: the contract grows with the deliveries; the account columns are
// already on the row). There is exactly one row, written by the
// migration, and a replace is guarded by its revision.

// parsedSettings is one validated settings body. A retention period left
// out is nil, and keeps its value.
type parsedSettings struct {
	TimeZone            string
	Prefix              string
	Next                int64
	PensionProvider     *string
	InjuryInsurer       *string
	RetentionGraceDays  *int16
	HealthRetentionDays *int16
	// Policy is delivery B's: the self-certification policy and automatic
	// carry-over (absence_settings.go), each left out keeping its value.
	Policy parsedPolicy
}

// The retention periods' bounds (design S15, plan "Exact values"): the grace
// before the D erase may be nothing — an installation that erases at once —
// and the health period at least a month; neither past ten years.
const (
	retentionGraceDaysMin  = 0
	healthRetentionDaysMin = 30
	retentionDaysMax       = 3650
)

// retentionPeriod is an optional period within [low, retentionDaysMax], or
// the message refusing it.
func retentionPeriod(what string, v *int32, low int) (*int16, string) {
	if v == nil {
		return nil, ""
	}
	if *v < int32(low) || *v > retentionDaysMax {
		return nil, fmt.Sprintf("%s is %d to %d days", what, low, retentionDaysMax)
	}
	out := int16(*v)
	return &out, ""
}

// parseSettings runs the settings' rules over a body and collects every
// failure: an IANA zone Go knows (the database is asked separately, by
// zoneDisagreement), a prefix of up to eight letters, digits or hyphens, a
// next number of 1 or more, the two names bounded — and the prefix and the
// next number together no longer than an employee number may be (design S3:
// the prefix validated so the number fits), which is the coded rule
// employee_number_too_long, named on both fields since either may be what the
// caller wants to change.
func parseSettings(body gen.StaffSettingsRequest) (parsedSettings, fieldErrors) {
	var f fieldErrors
	var out parsedSettings

	out.TimeZone = strings.TrimSpace(body.TimeZone)
	switch _, err := time.LoadLocation(out.TimeZone); {
	case out.TimeZone == "" || out.TimeZone == "Local":
		// Go's LoadLocation answers UTC for "" without an error, and "Local"
		// is whatever zone the server process runs in, which says nothing
		// about the company's calendar.
		f.add("timeZone", "A time zone is an IANA name, such as 'Europe/Oslo'")
	case err != nil:
		f.add("timeZone", fmt.Sprintf("'%s' is not a time zone this installation knows", out.TimeZone))
	}

	if body.EmployeeNumberPrefix != nil {
		out.Prefix = strings.TrimSpace(*body.EmployeeNumberPrefix)
	}
	prefixOK := true
	switch {
	case utf8.RuneCountInString(out.Prefix) > prefixMaxLength:
		f.add("employeeNumberPrefix", fmt.Sprintf("A prefix cannot be longer than %d characters", prefixMaxLength))
		prefixOK = false
	case !numberPattern.MatchString(out.Prefix):
		f.add("employeeNumberPrefix", "A prefix may hold only letters a–z, digits and hyphens")
		prefixOK = false
	}

	out.Next = body.NextEmployeeNumber
	if out.Next < 1 {
		f.add("nextEmployeeNumber", "The next employee number must be 1 or more")
	} else if prefixOK {
		number := out.Prefix + strconv.FormatInt(out.Next, 10)
		if n := len(number); n > employeeNumberMax {
			msg := fmt.Sprintf("The next employee number would be '%s', %d characters; an employee number is at most %d", number, n, employeeNumberMax)
			f.addCoded("employeeNumberPrefix", codeEmployeeNumberTooLong, msg)
			f.addCoded("nextEmployeeNumber", codeEmployeeNumberTooLong, msg)
		}
	}

	var msg string
	out.PensionProvider, msg = optionalText("A pension provider", body.PensionProvider, providerMaxLength)
	f.add("pensionProvider", msg)
	out.InjuryInsurer, msg = optionalText("An injury insurer", body.InjuryInsurer, providerMaxLength)
	f.add("injuryInsurer", msg)
	out.RetentionGraceDays, msg = retentionPeriod("The grace period before the D erase", body.RetentionGraceDays, retentionGraceDaysMin)
	f.add("retentionGraceDays", msg)
	out.HealthRetentionDays, msg = retentionPeriod("The health documents' period", body.HealthRetentionDays, healthRetentionDaysMin)
	f.add("healthRetentionDays", msg)
	out.Policy = parsePolicy(body, &f)
	return out, f
}

// settings reads the installation's settings row. Every path that needs the
// zone or the sequence goes through it.
func settings(ctx context.Context, q *store.Queries) (store.StaffSetting, error) {
	row, err := q.GetSettings(ctx)
	if err != nil {
		return store.StaffSetting{}, fmt.Errorf("staff: read the settings: %w", err)
	}
	return row, nil
}

// GetStaffSettings Get the Staff settings
// (GET /api/v1/staff/settings)
//
// staff:manage only, which the contract's access rule enforces: the app reads
// the zone from GET /meta, and nothing else here is anyone else's business.
func (s *server) GetStaffSettings(ctx context.Context, _ gen.GetStaffSettingsRequestObject) (gen.GetStaffSettingsResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := settings(ctx, q)
	if err != nil {
		return nil, err
	}
	state, err := retentionState(ctx, q)
	if err != nil {
		return nil, err
	}
	out, err := settingsResponse(row, state)
	if err != nil {
		return nil, err
	}
	return gen.GetStaffSettings200JSONResponse(out), nil
}

// retentionState is the retention block of the settings (plan reading 8),
// derived from the deletion ledger and the history — no column holds it:
// the objects waiting, those the store has refused at least once, the oldest
// queued and the latest erased event.
func retentionState(ctx context.Context, q *store.Queries) (gen.StaffRetentionState, error) {
	ledger, err := q.RetentionLedgerState(ctx)
	if err != nil {
		return gen.StaffRetentionState{}, fmt.Errorf("staff: read the deletion ledger: %w", err)
	}
	out := gen.StaffRetentionState{PendingDeletions: ledger.Pending, FailingDeletions: ledger.Failing}
	if oldest, err := q.OldestPendingDeletion(ctx); err == nil {
		out.OldestPendingSince = &oldest
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return gen.StaffRetentionState{}, fmt.Errorf("staff: read the oldest ledger row: %w", err)
	}
	if last, err := q.LastErasedAt(ctx); err == nil {
		out.LastErasedAt = &last
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return gen.StaffRetentionState{}, fmt.Errorf("staff: read the last erase: %w", err)
	}
	return out, nil
}

// PutStaffSettings Change the Staff settings
// (PUT /api/v1/staff/settings)
//
// A full replace of A1's five fields, guarded by If-Match; A2's retention
// periods and B's self-certification policy and carry-over each keep their
// value when left out. The policy is resolved against the row as locked, and
// "today" for autoCarryOverSince is the stored zone's. The zone is judged
// by Go, then — when it changes — by the database, before the transaction;
// the revision is judged under the row's lock, so two replaces at once queue
// and the second is told the first won.
func (s *server) PutStaffSettings(ctx context.Context, req gen.PutStaffSettingsRequestObject) (gen.PutStaffSettingsResponseObject, error) {
	var body gen.StaffSettingsRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffSettings400ApplicationProblemPlusJSONResponse(invalid(invalidSettingsTitle, badIfMatch())), nil
	}
	parsed, f := parseSettings(body)
	q := store.New(s.deps.Pool)
	if !f.failed() {
		current, err := settings(ctx, q)
		if err != nil {
			return nil, err
		}
		if parsed.TimeZone != current.TimeZone {
			msg, err := zoneDisagreement(ctx, q, parsed.TimeZone, s.deps.Clock())
			if err != nil {
				return nil, err
			}
			f.add("timeZone", msg)
		}
	}
	if f.failed() {
		return gen.PutStaffSettings400ApplicationProblemPlusJSONResponse(invalid(invalidSettingsTitle, f)), nil
	}

	var (
		saved store.StaffSetting
		stale *int32
	)
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("staff: lock the settings: %w", err)
		}
		if current.Revision != revision {
			stale = &current.Revision
			return nil
		}
		now := s.deps.Clock()
		policy := parsed.Policy.applyTo(policyOf(current), civilToday(now, zoneOf(current)))
		saved, err = txq.UpdateSettings(ctx, store.UpdateSettingsParams{
			TimeZone:                   parsed.TimeZone,
			EmployeeNumberPrefix:       parsed.Prefix,
			NextEmployeeNumber:         parsed.Next,
			PensionProvider:            parsed.PensionProvider,
			InjuryInsurer:              parsed.InjuryInsurer,
			RetentionGraceDays:         parsed.RetentionGraceDays,
			HealthRetentionDays:        parsed.HealthRetentionDays,
			SelfCertPolicy:             policy.Policy,
			SelfCertDaysPerOccurrence:  policy.DaysPerOccurrence,
			SelfCertOccurrencesPerYear: policy.OccurrencesPerYear,
			SelfCertDaysPerYear:        policy.DaysPerYear,
			SelfCertAfterMonths:        policy.AfterMonths,
			AutoCarryOver:              policy.AutoCarryOver,
			AutoCarryOverSince:         policy.AutoCarryOverSince,
			Now:                        now,
		})
		if err != nil {
			return fmt.Errorf("staff: change the settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if stale != nil {
		return gen.PutStaffSettings409ApplicationProblemPlusJSONResponse(revisionConflict("settings", *stale, revision)), nil
	}
	state, err := retentionState(ctx, q)
	if err != nil {
		return nil, err
	}
	out, err := settingsResponse(saved, state)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffSettings200JSONResponse(out), nil
}

// badIfMatch is the field error an If-Match that carries no revision answers.
// The header is required by the contract, so a missing one never reaches a
// handler: the generated wrapper refuses it first, as a bare 400.
func badIfMatch() fieldErrors {
	return fieldErrors{errs: fieldError("If-Match", "If-Match must carry the revision the row was read at, such as \"3\"")}
}

// invalidParameterValue is the SQLSTATE Postgres raises for a time zone name it
// does not know.
const invalidParameterValue = "22023"

// zoneDisagreement is why a zone name may not be stored, "" when it may. It is
// Expenses' check (internal/expenses/settings.go), asked again here because no
// module reads another's, and it asks the question that matters: not "does
// Postgres know this name" but "do Postgres and Go mean the same thing by it".
// Postgres resolves pg_timezone_abbrevs before pg_timezone_names, so 'CET' is
// a fixed +01:00 to it and a zone with summer time to Go; a person employed
// from 1 July would then start on 30 June by every rule written in SQL. The
// comparison is of UTC offsets at a winter instant and a summer one of the
// request's year: the disagreements are about summer time, and a name the two
// agree about in January and in July they agree about all year.
func zoneDisagreement(ctx context.Context, q *store.Queries, name string, now time.Time) (string, error) {
	winter := time.Date(now.Year(), time.January, 15, 12, 0, 0, 0, time.UTC)
	summer := time.Date(now.Year(), time.July, 15, 12, 0, 0, 0, time.UTC)
	offsets, err := q.ResolveTimeZone(ctx, store.ResolveTimeZoneParams{Name: name, Winter: winter, Summer: summer})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == invalidParameterValue {
			return fmt.Sprintf("'%s' is not a time zone this installation's database knows", name), nil
		}
		return "", fmt.Errorf("staff: check a time zone: %w", err)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// parseSettings already refused this; answered rather than asserted.
		return fmt.Sprintf("'%s' is not a time zone this installation knows", name), nil
	}
	for _, probe := range []struct {
		at     time.Time
		inSQL  int64
		season string
	}{
		{winter, offsets.WinterOffset, "January"},
		{summer, offsets.SummerOffset, "July"},
	} {
		if _, inGo := probe.at.In(loc).Zone(); int64(inGo) != probe.inSQL {
			return fmt.Sprintf(
				"'%s' means one thing to this installation and another to its database in %s, "+
					"so the two would not agree which day a date falls on — use a city zone such as 'Europe/Oslo'",
				name, probe.season), nil
		}
	}
	return "", nil
}
