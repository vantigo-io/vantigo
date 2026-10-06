package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the reminder settings (invoices payments and reminders design
// D7) and the regime and its review (D6): one row, read by every
// invoices:access holder and replaced by invoices:manage, off the settings
// row every issue shares. Changing it changes only letters made afterwards —
// a letter records its facts when it is sent.

// invalidReminderSettingsTitle is the title of the PUT's 400.
const invalidReminderSettingsTitle = "Invalid reminder settings"

// reminderSettings is the row and the engine's view of it (D7): every field
// as reminderrules.Settings wants it, the two days as UTC midnights. The
// rule-input loader (Task 7b) and the run read it through here.
func (s *server) reminderSettings(ctx context.Context, q *store.Queries) (reminderrules.Settings, store.InvoicesReminderSetting, error) {
	row, err := q.GetReminderSettings(ctx)
	if err != nil {
		return reminderrules.Settings{}, row, fmt.Errorf("invoices: read the reminder settings: %w", err)
	}
	return reminderrules.Settings{
		Enabled:               row.Enabled,
		FirstReminderDays:     int(row.FirstReminderDays),
		DeadlineDays:          int(row.DeadlineDays),
		GraceDays:             int(row.GraceDays),
		RemindersBeforeNotice: int(row.RemindersBeforeNotice),
		StaleImportDays:       int(row.StaleImportDays),
		CollectionNotice:      row.CollectionNotice,
		LateInterest:          row.LateInterest,
		PersonCharge:          row.PersonCharge,
		BusinessCharge:        row.BusinessCharge,
		Inkassolov2026From:    pgDateOf(row.Inkassolov2026From),
		RegimeReviewedThrough: utcDay(row.RegimeReviewedThrough.Time),
	}, row, nil
}

// reminderSettingsWire is the row on the wire.
func reminderSettingsWire(row store.InvoicesReminderSetting) gen.InvoicesReminderSettings {
	out := gen.InvoicesReminderSettings{
		Enabled: row.Enabled, FirstReminderDays: row.FirstReminderDays, DeadlineDays: row.DeadlineDays,
		GraceDays: row.GraceDays, RemindersBeforeNotice: row.RemindersBeforeNotice, CollectionNotice: row.CollectionNotice,
		PersonCharge:   gen.InvoicesReminderSettingsPersonCharge(row.PersonCharge),
		BusinessCharge: gen.InvoicesReminderSettingsBusinessCharge(row.BusinessCharge),
		LateInterest:   row.LateInterest, StaleImportDays: row.StaleImportDays,
		RegimeReviewedThrough: wireDate(row.RegimeReviewedThrough.Time),
		RegimeReviewedBy:      row.RegimeReviewedByUserID, RegimeReviewedAt: row.RegimeReviewedAt,
		Revision: row.Revision, UpdatedAt: row.UpdatedAt, UpdatedBy: row.UpdatedByUserID,
	}
	if row.Inkassolov2026From.Valid {
		out.Inkassolov2026From = ptr(wireDate(row.Inkassolov2026From.Time))
	}
	return out
}

// parseReminderSettings runs D7's rules over a PUT body, every failure
// collected. Every field is required — absent is a 400 on it, and so is null
// but for inkassolov2026From — so a client can never reset one by leaving it
// out; each is then held to its bounds (the table's CHECKs), and
// regimeReviewedThrough to at most a year after today (D6).
func parseReminderSettings(body gen.InvoicesReminderSettingsRequest, today time.Time) (store.UpdateReminderSettingsParams, map[string][]string) {
	var errs map[string][]string
	add := func(field, msg string) { errs = withFieldError(errs, field, msg) }
	decode := func(field string, raw json.RawMessage, v any, wrongType string) bool {
		switch {
		case len(raw) == 0 || string(raw) == "null":
			add(field, field+" is required")
		case json.Unmarshal(raw, v) != nil:
			add(field, wrongType)
		default:
			return true
		}
		return false
	}
	days := func(field string, raw json.RawMessage, low, high int32, dst *int32) {
		if decode(field, raw, dst, field+" is a whole number of days") && (*dst < low || *dst > high) {
			add(field, fmt.Sprintf("%s is between %d and %d", field, low, high))
		}
	}
	choice := func(field string, raw json.RawMessage, allowed []string, dst *string) {
		msg := fmt.Sprintf("%s is one of %v", field, allowed)
		if decode(field, raw, dst, msg) && !slices.Contains(allowed, *dst) {
			add(field, msg)
		}
	}
	var p store.UpdateReminderSettingsParams
	decode("enabled", body.Enabled, &p.Enabled, "enabled is true or false")
	days("firstReminderDays", body.FirstReminderDays, 1, 60, &p.FirstReminderDays)
	days("deadlineDays", body.DeadlineDays, 14, 60, &p.DeadlineDays)
	days("graceDays", body.GraceDays, 1, 10, &p.GraceDays)
	if decode("remindersBeforeNotice", body.RemindersBeforeNotice, &p.RemindersBeforeNotice, "remindersBeforeNotice is a whole number") &&
		(p.RemindersBeforeNotice < 0 || p.RemindersBeforeNotice > 2) {
		add("remindersBeforeNotice", "remindersBeforeNotice is between 0 and 2")
	}
	decode("collectionNotice", body.CollectionNotice, &p.CollectionNotice, "collectionNotice is true or false")
	choice("personCharge", body.PersonCharge, []string{reminderrules.ChargeFee, reminderrules.ChargeNone}, &p.PersonCharge)
	choice("businessCharge", body.BusinessCharge,
		[]string{reminderrules.ChargeFee, reminderrules.ChargeCompensation, reminderrules.ChargeNone}, &p.BusinessCharge)
	decode("lateInterest", body.LateInterest, &p.LateInterest, "lateInterest is true or false")
	days("staleImportDays", body.StaleImportDays, 1, 30, &p.StaleImportDays)

	const notADay = "is a calendar day, such as 2027-01-01"
	switch raw := body.Inkassolov2026From; {
	case len(raw) == 0:
		add("inkassolov2026From", "inkassolov2026From is required; send null while the day is unknown")
	case string(raw) != "null":
		var text string
		day, err := time.Time{}, json.Unmarshal(raw, &text)
		if err == nil {
			day, err = time.Parse(time.DateOnly, text)
		}
		if err != nil {
			add("inkassolov2026From", "inkassolov2026From "+notADay)
		} else {
			p.Inkassolov2026From = pgDate(day)
		}
	}
	var through string
	if decode("regimeReviewedThrough", body.RegimeReviewedThrough, &through, "regimeReviewedThrough "+notADay) {
		day, err := time.Parse(time.DateOnly, through)
		limit := reminderrules.AddMonthsClamped(today, 12)
		switch {
		case err != nil:
			add("regimeReviewedThrough", "regimeReviewedThrough "+notADay)
		case day.After(limit):
			add("regimeReviewedThrough", fmt.Sprintf("The regime is reviewed at most a year ahead, through %s", limit.Format(time.DateOnly)))
		default:
			p.RegimeReviewedThrough = pgDate(day)
		}
	}
	p.Revision = body.Revision
	return p, errs
}

// GetInvoicesSettingsReminders Get the reminder settings
// (GET /api/v1/invoices/settings/reminders)
func (s *server) GetInvoicesSettingsReminders(ctx context.Context, _ gen.GetInvoicesSettingsRemindersRequestObject) (gen.GetInvoicesSettingsRemindersResponseObject, error) {
	_, row, err := s.reminderSettings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	return gen.GetInvoicesSettingsReminders200JSONResponse(reminderSettingsWire(row)), nil
}

// PutInvoicesSettingsReminders Change the reminder settings
// (PUT /api/v1/invoices/settings/reminders)
//
// One statement replaces the row when the revision is the caller's (D7);
// none matched is a stale revision. Moving regime_reviewed_through, or
// setting inkassolov_2026_from, records the caller and the clock as the
// review's (D6).
func (s *server) PutInvoicesSettingsReminders(ctx context.Context, req gen.PutInvoicesSettingsRemindersRequestObject) (gen.PutInvoicesSettingsRemindersResponseObject, error) {
	now := s.deps.Clock()
	params, errs := parseReminderSettings(*req.Body, businessDay(now))
	if errs != nil {
		return gen.PutInvoicesSettingsReminders400ApplicationProblemPlusJSONResponse(invalid(invalidReminderSettingsTitle, errs)), nil
	}
	params.By, params.Now = callerID(ctx), now
	q := store.New(s.deps.Pool)
	saved, err := q.UpdateReminderSettings(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		current, err := q.GetReminderSettings(ctx)
		if err != nil {
			return nil, fmt.Errorf("invoices: read the reminder settings: %w", err)
		}
		return gen.PutInvoicesSettingsReminders409ApplicationProblemPlusJSONResponse(
			revisionConflict("Reminder settings", current.Revision, params.Revision)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: change the reminder settings: %w", err)
	}
	return gen.PutInvoicesSettingsReminders200JSONResponse(reminderSettingsWire(saved)), nil
}
