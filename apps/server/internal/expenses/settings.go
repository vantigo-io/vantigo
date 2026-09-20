package expenses

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is the installation's expense settings (design §3.5): the default
// currency and markup a new expense starts from, the receipt threshold that
// decides whether an outlay may be submitted without one, and the period lock
// every mutating path reads. There is exactly one row, written by the
// migration, so there is nothing to create and no revision to guard — a replace
// is the last one to save, as time's settings are.

// parsedSettings is one validated settings body, in the shape the update wants.
type parsedSettings struct {
	LockedBefore        pgtype.Date
	DefaultCurrency     string
	DefaultMarkup       pgtype.Numeric
	ReceiptRequiredOver pgtype.Numeric
	TimeZone            string
}

// parseSettings runs §3.5's rules over a settings body: a three-letter
// currency, a markup between 0 and 1000 with at most two decimals, and — when
// given — a receipt threshold greater than zero the column can hold. Every
// failure is collected. A lockedBefore or receiptRequiredOver left out clears
// that setting; any lock date is accepted, one in the future included, because
// the lock is an administrator's statement about which period is closed.
func parseSettings(body gen.ExpensesSettingsRequest, current store.ExpensesSetting) (parsedSettings, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}

	currency, msg := validateCurrency(body.DefaultCurrency)
	add("defaultCurrency", msg)
	add("defaultMarkupPercent", validateDecimal("A markup", body.DefaultMarkupPercent, 0, maxMarkupPercent))
	// Zero is a policy rather than a mistake — every employee-paid outlay then
	// needs a receipt — so the bound is zero or greater. Leaving the field out
	// is what turns the rule off.
	if body.ReceiptRequiredOver != nil {
		add("receiptRequiredOver", validateDecimal("A receipt threshold", *body.ReceiptRequiredOver, 0, maxMoney))
	}
	// The business time zone is the one setting a replace *keeps* rather than
	// clears when it is left out: every date derived from a travel claim's two
	// instants is taken in it, so an omission would silently move every trip in
	// the installation by an hour's worth of days. Go's own tzdata answers here;
	// Postgres is asked separately, because it carries its own.
	zone := current.TimeZone
	if body.TimeZone != nil {
		zone = strings.TrimSpace(*body.TimeZone)
		switch _, err := time.LoadLocation(zone); {
		case zone == "" || zone == "Local":
			// "Local" is whatever zone the *server process* happens to run in,
			// which is not a statement about the company's calendar.
			add("timeZone", "A time zone is an IANA name, such as 'Europe/Oslo'")
		case err != nil:
			add("timeZone", fmt.Sprintf("'%s' is not a time zone this installation knows", zone))
		}
	}
	if len(errs) > 0 {
		return parsedSettings{}, errs, nil
	}

	markup, err := numericFromFloat(body.DefaultMarkupPercent)
	if err != nil {
		return parsedSettings{}, nil, err
	}
	threshold, err := numericFromFloatPtr(body.ReceiptRequiredOver)
	if err != nil {
		return parsedSettings{}, nil, err
	}
	parsed := parsedSettings{
		DefaultCurrency: currency, DefaultMarkup: markup, ReceiptRequiredOver: threshold, TimeZone: zone,
	}
	if body.LockedBefore != nil {
		parsed.LockedBefore = pgDate(body.LockedBefore.Time)
	}
	return parsed, nil, nil
}

// settings reads the installation's settings row. Every path that needs the
// lock, the default currency or the receipt rule goes through it.
func settings(ctx context.Context, q *store.Queries) (store.ExpensesSetting, error) {
	row, err := q.GetSettings(ctx)
	if err != nil {
		return store.ExpensesSetting{}, fmt.Errorf("expenses: read the settings: %w", err)
	}
	return row, nil
}

// GetExpensesSettings Get the expense settings
// (GET /api/v1/expenses/settings)
//
// Anyone with expenses:access reads them: the lock and the receipt rule decide
// what they may record, and the client shows both. The one figure shaped away
// from them is the default markup (responses.go), which is the company's
// commercial decision rather than a rule about what they may record.
func (s *server) GetExpensesSettings(ctx context.Context, _ gen.GetExpensesSettingsRequestObject) (gen.GetExpensesSettingsResponseObject, error) {
	row, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	resp, err := settingsResponse(row, s.has(ctx, "expenses:manage"))
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesSettings200JSONResponse(resp), nil
}

// PutExpensesSettings Change the expense settings
// (PUT /api/v1/expenses/settings)
//
// expenses:manage only, which the contract's access rule enforces. A full
// replace: what is left out is cleared.
func (s *server) PutExpensesSettings(ctx context.Context, req gen.PutExpensesSettingsRequestObject) (gen.PutExpensesSettingsResponseObject, error) {
	body := gen.ExpensesSettingsRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	current, err := settings(ctx, q)
	if err != nil {
		return nil, err
	}
	parsed, errs, err := parseSettings(body, current)
	if err != nil {
		return nil, err
	}
	if len(errs) == 0 && parsed.TimeZone != current.TimeZone {
		// And Postgres, which carries its own tzdata and runs the very same
		// derivation in the claims list's filter. A name the two *read
		// differently* would be stored here and then disagree with itself for
		// half the year, so what is compared is what each of them means by it.
		msg, err := zoneDisagreement(ctx, q, parsed.TimeZone, s.deps.Clock())
		if err != nil {
			return nil, err
		}
		if msg != "" {
			errs = fieldError("timeZone", msg)
		}
	}
	if len(errs) > 0 {
		return gen.PutExpensesSettings400ApplicationProblemPlusJSONResponse(invalidSettings(errs)), nil
	}

	row, err := q.UpdateSettings(ctx, store.UpdateSettingsParams{
		LockedBefore:         parsed.LockedBefore,
		DefaultCurrency:      parsed.DefaultCurrency,
		DefaultMarkupPercent: parsed.DefaultMarkup,
		ReceiptRequiredOver:  parsed.ReceiptRequiredOver,
		TimeZone:             parsed.TimeZone,
		Now:                  s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: change the settings: %w", err)
	}
	// The caller of a replace holds expenses:manage by the operation's own
	// access rule, so their copy carries the markup they just set.
	resp, err := settingsResponse(row, true)
	if err != nil {
		return nil, err
	}
	return gen.PutExpensesSettings200JSONResponse(resp), nil
}

// invalidParameterValue is the SQLSTATE Postgres raises for a time zone name it
// does not know.
const invalidParameterValue = "22023"

// zoneDisagreement is why a zone name may not be stored, "" when it may. It is
// the whole of the database half of the check, and it asks the question that
// matters: not "does Postgres know this name" but "do Postgres and Go mean the
// same thing by it".
//
// They can differ, and the difference is not exotic. Postgres resolves a name
// through pg_timezone_abbrevs **before** pg_timezone_names, so 'CET' is a fixed
// +01:00 to it and the IANA zone with summer time to Go; the same holds for
// 'EET', 'MET' and 'WET'. Both halves "know" the name, and a trip departing at
// 00:30 local on 1 July would then be dated 1 July by every rule written in Go
// and 30 June by every filter, order and export written in SQL — the exact
// month boundary this setting exists to get right.
//
// The comparison is of UTC offsets at a winter instant and a summer one of the
// year the request is made in. Two instants are enough: the disagreements are
// about summer time, and a name the two agree about in both January and July
// they agree about all year. The message names a city zone, because that is
// what the caller should write instead — the abbreviations are exactly the
// names with no single answer.
func zoneDisagreement(ctx context.Context, q *store.Queries, name string, now time.Time) (string, error) {
	winter := time.Date(now.Year(), time.January, 15, 12, 0, 0, 0, time.UTC)
	summer := time.Date(now.Year(), time.July, 15, 12, 0, 0, 0, time.UTC)
	offsets, err := q.ResolveTimeZone(ctx, store.ResolveTimeZoneParams{Name: name, Winter: winter, Summer: summer})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == invalidParameterValue {
			return fmt.Sprintf("'%s' is not a time zone this installation's database knows", name), nil
		}
		return "", fmt.Errorf("expenses: check a time zone: %w", err)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// parseSettings already refused this; answered rather than asserted.
		return fmt.Sprintf("'%s' is not a time zone this installation knows", name), nil
	}
	for _, probe := range []struct {
		at       time.Time
		inSQL    int64
		season   string
		otherEnd string
	}{
		{winter, offsets.WinterOffset, "January", "July"},
		{summer, offsets.SummerOffset, "July", "January"},
	} {
		if _, inGo := probe.at.In(loc).Zone(); int64(inGo) != probe.inSQL {
			return fmt.Sprintf(
				"'%s' means one thing to this installation and another to its database in %s, "+
					"so the two would not agree which day a trip departed on — use a city zone such as 'Europe/Oslo'",
				name, probe.season), nil
		}
	}
	return "", nil
}
