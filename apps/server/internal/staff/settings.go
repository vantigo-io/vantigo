package staff

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the installation's Staff settings (design §3.6): the employee
// number sequence, the pension provider and the injury insurer (delivery A1;
// the business time zone was A1's too until the time zones design made it
// the workspace's, read through contracts.BusinessCalendar and answered here
// read-only), the two retention periods with the
// retention run's state, derived (delivery A2), and the self-certification
// policy and automatic carry-over (delivery B, absence_settings.go; plan
// reading 2: the contract grows with the deliveries), and the two account
// settings (delivery C, account_settings.go) and the two erase settings the
// owner asked for (delivery C, Task 5b; retention.go reads them). There is
// exactly one row, written by the migration, and a replace is guarded by its
// revision.

// parsedSettings is one validated settings body. A retention period left
// out is nil, and keeps its value.
type parsedSettings struct {
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
// failure: a prefix of up to eight letters, digits or hyphens, a
// next number of 1 or more, the two names bounded — and the prefix and the
// next number together no longer than an employee number may be (design S3:
// the prefix validated so the number fits), which is the coded rule
// employee_number_too_long, named on both fields since either may be what the
// caller wants to change.
func parseSettings(body gen.StaffSettingsRequest) (parsedSettings, fieldErrors) {
	var f fieldErrors
	var out parsedSettings

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

// eraseSettings is the two erase settings of a replace (Task 5b), each nil
// when left out, which keeps it.
type eraseSettings struct {
	pregnancyHealth *bool
	sickCaseDates   *bool
}

// parseEraseSettings judges the two erase settings into f: each a boolean,
// kept when left out. The decoder reads a null as left out, so a null is
// refused on the field rather than kept — an erase setting is never changed
// or kept by a client that meant to say something else.
func parseEraseSettings(ctx context.Context, body gen.StaffSettingsRequest, f *fieldErrors) eraseSettings {
	for _, field := range []string{"erasePregnancyHealthTypes", "eraseSickCaseDates"} {
		if bodyHas(ctx, field) && !bodyHasValue(ctx, field) {
			f.add(field, "An erase setting is true or false; left out, it keeps its value")
		}
	}
	return eraseSettings{pregnancyHealth: body.ErasePregnancyHealthTypes, sickCaseDates: body.EraseSickCaseDates}
}

// eventSettingsUpdated is the installation event of an erase switch turned
// on or off (review L4): about no person, naming the fields that changed.
const eventSettingsUpdated = "settings_updated"

// recordEraseSwitches records settings_updated when a replace turned either
// erase setting on or off — each erases for good, so who changed it and when
// is kept; nothing when neither changed.
func (s *server) recordEraseSwitches(ctx context.Context, txq *store.Queries, who actor, before, after store.StaffSetting) error {
	c := changes{}
	for _, f := range []struct {
		name     string
		from, to bool
	}{
		{"erasePregnancyHealthTypes", before.ErasePregnancyHealthTypes, after.ErasePregnancyHealthTypes},
		{"eraseSickCaseDates", before.EraseSickCaseDates, after.EraseSickCaseDates},
	} {
		if f.from != f.to {
			c[f.name] = fieldChange{From: f.from, To: f.to}
		}
	}
	if len(c) == 0 {
		return nil
	}
	return s.recordEvent(ctx, txq, nil, eventSettingsUpdated, who, map[string]any{"fields": c}, nil)
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
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	state, err := retentionState(ctx, q, s.has(ctx, permPersonalData))
	if err != nil {
		return nil, err
	}
	out, err := settingsResponse(row, state, zone)
	if err != nil {
		return nil, err
	}
	return gen.GetStaffSettings200JSONResponse(out), nil
}

// retentionState is the retention block of the settings (plan reading 8),
// derived from the deletion ledger and the history — no column holds it:
// the objects waiting, those the store has refused at least once, the oldest
// queued and the latest erased event.
//
// withHealth is whether the caller reads health (staff:personal-data): the
// health erases' instants are theirs alone (review M1).
func retentionState(ctx context.Context, q *store.Queries, withHealth bool) (gen.StaffRetentionState, error) {
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
	if last, err := q.LastErasedAt(ctx, withHealth); err == nil {
		out.LastErasedAt = &last
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return gen.StaffRetentionState{}, fmt.Errorf("staff: read the last erase: %w", err)
	}
	return out, nil
}

// PutStaffSettings Change the Staff settings
// (PUT /api/v1/staff/settings)
//
// A full replace of A1's four fields, guarded by If-Match; A2's retention
// periods, B's self-certification policy and carry-over, C's two account
// settings (account_settings.go) and its two erase settings each keep their
// value when left out. The policy is resolved against the row as locked, and
// "today" for autoCarryOverSince is the business time zone's, read before
// the transaction; the revision is judged under the row's lock, so two
// replaces at once queue and the second is told the first won.
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
	accounts := parseAccountSettings(body, bodyHas(ctx, "deleteExternalAfterDays"), &f)
	erase := parseEraseSettings(ctx, body, &f)
	q := store.New(s.deps.Pool)
	if f.failed() {
		return gen.PutStaffSettings400ApplicationProblemPlusJSONResponse(invalid(invalidSettingsTitle, f)), nil
	}

	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	var (
		saved store.StaffSetting
		stale *int32
	)
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("staff: lock the settings: %w", err)
		}
		if current.Revision != revision {
			stale = &current.Revision
			return nil
		}
		now := s.deps.Clock()
		policy := parsed.Policy.applyTo(policyOf(current), civilToday(now, zone))
		saved, err = txq.UpdateSettings(ctx, store.UpdateSettingsParams{
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
			CreateBeforeDays:           accounts.createBeforeDays,
			SetDeleteExternalAfterDays: accounts.deleteExternal,
			DeleteExternalAfterDays:    accounts.deleteExternalAfterDays,
			ErasePregnancyHealthTypes:  erase.pregnancyHealth,
			EraseSickCaseDates:         erase.sickCaseDates,
			Now:                        now,
		})
		if err != nil {
			return fmt.Errorf("staff: change the settings: %w", err)
		}
		return s.recordEraseSwitches(ctx, txq, who, current, saved)
	})
	if err != nil {
		return nil, err
	}
	if stale != nil {
		return gen.PutStaffSettings409ApplicationProblemPlusJSONResponse(revisionConflict("settings", *stale, revision)), nil
	}
	state, err := retentionState(ctx, q, s.has(ctx, permPersonalData))
	if err != nil {
		return nil, err
	}
	out, err := settingsResponse(saved, state, zone)
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
