package expenses

import (
	"context"
	"fmt"

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
}

// parseSettings runs §3.5's rules over a settings body: a three-letter
// currency, a markup between 0 and 1000 with at most two decimals, and — when
// given — a receipt threshold greater than zero the column can hold. Every
// failure is collected. A lockedBefore or receiptRequiredOver left out clears
// that setting; any lock date is accepted, one in the future included, because
// the lock is an administrator's statement about which period is closed.
func parseSettings(body gen.ExpensesSettingsRequest) (parsedSettings, map[string][]string, error) {
	var errs map[string][]string
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}

	currency, msg := validateCurrency(body.DefaultCurrency)
	add("defaultCurrency", msg)
	add("defaultMarkupPercent", validateDecimal("A markup", body.DefaultMarkupPercent, 0, maxMarkupPercent))
	if body.ReceiptRequiredOver != nil {
		add("receiptRequiredOver", validateAboveZero("A receipt threshold", *body.ReceiptRequiredOver, maxMoney))
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
	parsed := parsedSettings{DefaultCurrency: currency, DefaultMarkup: markup, ReceiptRequiredOver: threshold}
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
// what they may record, and the client shows both. There is nothing here they
// may not know — what an administrator alone may see lives on the rates, which
// this module answers only to expenses:manage.
func (s *server) GetExpensesSettings(ctx context.Context, _ gen.GetExpensesSettingsRequestObject) (gen.GetExpensesSettingsResponseObject, error) {
	row, err := settings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	resp, err := settingsResponse(row)
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
	parsed, errs, err := parseSettings(body)
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return gen.PutExpensesSettings400ApplicationProblemPlusJSONResponse(invalidSettings(errs)), nil
	}

	row, err := store.New(s.deps.Pool).UpdateSettings(ctx, store.UpdateSettingsParams{
		LockedBefore:         parsed.LockedBefore,
		DefaultCurrency:      parsed.DefaultCurrency,
		DefaultMarkupPercent: parsed.DefaultMarkup,
		ReceiptRequiredOver:  parsed.ReceiptRequiredOver,
		Now:                  s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("expenses: change the settings: %w", err)
	}
	resp, err := settingsResponse(row)
	if err != nil {
		return nil, err
	}
	return gen.PutExpensesSettings200JSONResponse(resp), nil
}
