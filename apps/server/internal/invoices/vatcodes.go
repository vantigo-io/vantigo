package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the VAT codes (D3): the tenant's label, the SAF-T standard tax
// code and the UNCL5305 category of each, with the rate as dated periods. A
// rate change is a new period on the same code, never a new label every open
// draft must be re-coded to; a line carries only the code until it is issued,
// when the period covering the issue date decides its rate.

// The codes and titles of this file's 409s.
const (
	codeVatCodeInUse        = "vat_code_in_use"
	codeRateChangeInPast    = "rate_change_in_past"
	codeRatePeriodNotLatest = "rate_period_not_latest"
	codeRatePeriodLast      = "rate_period_last"
	codeRatePeriodInUse     = "rate_period_in_use"

	vatCodeInUseTitle     = "VAT code in use"
	rateChangeInPastTitle = "Rate change in the past"
	ratePeriodTitle       = "Rate period cannot be removed"
)

// vatCodeIndex is the unique index on lower(code).
const vatCodeIndex = "ux_vat_codes_code_lower"

// categories are UNCL5305's, the ones EHF Billing 3.0 Norway uses.
var categories = map[string]bool{"S": true, "Z": true, "E": true, "AE": true, "G": true, "O": true, "K": true}

var hundredPercent = big.NewRat(100, 1)

// vatCodeFields is a code's own fields, validated.
type vatCodeFields struct {
	code, name, safT, category string
	reason                     *string
}

// parseVatCodeFields runs D3's rules over a code's own fields, every failure
// collected into errs.
func parseVatCodeFields(code, name, safT, category string, reason *string, errs map[string][]string) (vatCodeFields, map[string][]string) {
	add := func(field, msg string) {
		if msg != "" {
			errs = withFieldError(errs, field, msg)
		}
	}
	f := vatCodeFields{
		code: strings.TrimSpace(code), name: strings.TrimSpace(name),
		safT: strings.TrimSpace(safT), category: strings.ToUpper(strings.TrimSpace(category)),
	}
	if reason != nil && strings.TrimSpace(*reason) != "" {
		f.reason = ptr(strings.TrimSpace(*reason))
	}
	if f.code == "" {
		add("code", "A VAT code needs a code")
	}
	add("code", maxLength("A code", f.code, 10))
	if f.name == "" {
		add("name", "A VAT code needs a name")
	}
	add("name", maxLength("A name", f.name, 100))
	if f.safT == "" {
		add("safTCode", "A VAT code needs its SAF-T standard tax code")
	}
	add("safTCode", maxLength("A SAF-T code", f.safT, 5))
	if !categories[f.category] {
		add("ehfCategory", "The category is one of S, Z, E, AE, G, O and K")
	}
	if f.reason != nil {
		add("exemptionReason", maxLength("An exemption reason", *f.reason, 200))
	}
	if f.category != "S" && categories[f.category] && f.reason == nil {
		add("exemptionReason", fmt.Sprintf("A code in category %s needs the exemption reason its documents print", f.category))
	}
	return f, errs
}

// parseRate is a rate for a code of category: greater than 0 and at most 100
// with at most two decimals for S, exactly 0 for every other category (D3).
// It answers the rate and the message to report on ratePercent.
func parseRate(category string, v float64) (*big.Rat, string) {
	if !finite(v) {
		return nil, "A rate is a number"
	}
	if decimalPlaces(v) > 2 {
		return nil, "A rate has at most two decimals"
	}
	rate := ratFromFloat(v)
	if category == "S" {
		if rate.Sign() <= 0 || rate.Cmp(hundredPercent) > 0 {
			return nil, "A standard-rated code's rate is greater than 0 and at most 100"
		}
		return rate, ""
	}
	if rate.Sign() != 0 {
		return nil, "Only a code in category S carries a rate; every other category's rate is 0"
	}
	return rate, ""
}

// vatCodeResponse renders one code with its periods.
func vatCodeResponse(c store.InvoicesVatCode, rates []store.InvoicesVatCodeRate, inUse bool) (gen.InvoicesVatCode, error) {
	out := gen.InvoicesVatCode{
		Id: c.ID, Code: c.Code, Name: c.Name, SafTCode: c.SafTCode, EhfCategory: c.EhfCategory,
		ExemptionReason: c.ExemptionReason, Active: c.Active, InUse: inUse, Revision: c.Revision,
		Rates: make([]gen.InvoicesVatCodeRate, 0, len(rates)),
	}
	for _, r := range rates {
		rate, err := floatFromNumeric(r.RatePercent)
		if err != nil {
			return gen.InvoicesVatCode{}, err
		}
		period := gen.InvoicesVatCodeRate{Id: r.ID, RatePercent: rate, ValidFrom: wireDate(r.ValidFrom.Time)}
		if r.ValidTo.Valid {
			period.ValidTo = ptr(wireDate(r.ValidTo.Time))
		}
		out.Rates = append(out.Rates, period)
	}
	return out, nil
}

// readVatCode renders one code as it stands now, on q.
func readVatCode(ctx context.Context, q *store.Queries, c store.InvoicesVatCode) (gen.InvoicesVatCode, error) {
	rates, err := q.VatCodeRates(ctx, c.ID)
	if err != nil {
		return gen.InvoicesVatCode{}, fmt.Errorf("invoices: read VAT code %d's rates: %w", c.ID, err)
	}
	inUse, err := q.VatCodeInUse(ctx, c.ID)
	if err != nil {
		return gen.InvoicesVatCode{}, fmt.Errorf("invoices: is VAT code %d in use: %w", c.ID, err)
	}
	return vatCodeResponse(c, rates, inUse)
}

// GetInvoicesVatCodes List the VAT codes
// (GET /api/v1/invoices/vat-codes)
func (s *server) GetInvoicesVatCodes(ctx context.Context, _ gen.GetInvoicesVatCodesRequestObject) (gen.GetInvoicesVatCodesResponseObject, error) {
	q := store.New(s.deps.Pool)
	codes, err := q.ListVatCodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the VAT codes: %w", err)
	}
	rates, err := q.ListVatCodeRates(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the VAT rates: %w", err)
	}
	used, err := q.VatCodesInUse(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the VAT codes in use: %w", err)
	}
	byCode := map[int32][]store.InvoicesVatCodeRate{}
	for _, r := range rates {
		byCode[r.VatCodeID] = append(byCode[r.VatCodeID], r)
	}
	inUse := map[int32]bool{}
	for _, id := range used {
		inUse[id] = true
	}
	out := make([]gen.InvoicesVatCode, 0, len(codes))
	for _, c := range codes {
		code, err := vatCodeResponse(c, byCode[c.ID], inUse[c.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return gen.GetInvoicesVatCodes200JSONResponse(out), nil
}

// PostInvoicesVatCodes Create a VAT code
// (POST /api/v1/invoices/vat-codes)
func (s *server) PostInvoicesVatCodes(ctx context.Context, req gen.PostInvoicesVatCodesRequestObject) (gen.PostInvoicesVatCodesResponseObject, error) {
	body := req.Body
	fields, errs := parseVatCodeFields(body.Code, body.Name, body.SafTCode, body.EhfCategory, body.ExemptionReason, nil)
	var rate *big.Rat
	if categories[fields.category] {
		var msg string
		if rate, msg = parseRate(fields.category, body.RatePercent); msg != "" {
			errs = withFieldError(errs, "ratePercent", msg)
		}
	}
	if len(errs) > 0 {
		return gen.PostInvoicesVatCodes400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle, errs)), nil
	}
	ratePercent, err := numericFromRat(rate, 2)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var created gen.InvoicesVatCode
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		code, err := txq.InsertVatCode(ctx, store.InsertVatCodeParams{
			Code: fields.code, Name: fields.name, SafTCode: fields.safT, EhfCategory: fields.category,
			ExemptionReason: fields.reason, Now: now,
		})
		if err != nil {
			return err
		}
		if _, err := txq.InsertVatCodeRate(ctx, store.InsertVatCodeRateParams{
			VatCodeID: code.ID, RatePercent: ratePercent, ValidFrom: pgDate(utcDay(body.ValidFrom.Time)), Now: now,
		}); err != nil {
			return fmt.Errorf("invoices: add VAT code %d's first rate: %w", code.ID, err)
		}
		created, err = readVatCode(ctx, txq, code)
		return err
	})
	if db.IsUniqueViolation(err, vatCodeIndex) {
		return gen.PostInvoicesVatCodes400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle,
			fieldError("code", fmt.Sprintf("A VAT code '%s' already exists", fields.code)))), nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: create a VAT code: %w", err)
	}
	return gen.PostInvoicesVatCodes201JSONResponse(created), nil
}

// PutInvoicesVatCodesById Change a VAT code
// (PUT /api/v1/invoices/vat-codes/{id})
//
// A code in use keeps its category and SAF-T code (D3): what a line issued
// under it reported to the VAT return must not be re-labelled behind it. The
// row is taken FOR UPDATE, which also waits for any draft save adding a line
// on it, so the in-use check sees every committed line.
func (s *server) PutInvoicesVatCodesById(ctx context.Context, req gen.PutInvoicesVatCodesByIdRequestObject) (gen.PutInvoicesVatCodesByIdResponseObject, error) {
	body := req.Body
	fields, errs := parseVatCodeFields(body.Code, body.Name, body.SafTCode, body.EhfCategory, body.ExemptionReason, nil)
	if len(errs) > 0 {
		return gen.PutInvoicesVatCodesById400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle, errs)), nil
	}
	var refusal *gen.InvoicesConflictProblem
	var rateRefusal string
	notFound := false
	var saved gen.InvoicesVatCode
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := txq.LockVatCode(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock VAT code %d: %w", req.Id, err)
		}
		if current.Revision != body.Revision {
			refusal = ptr(revisionConflict("VAT code", current.Revision, body.Revision))
			return errRefused
		}
		if fields.category != current.EhfCategory || fields.safT != current.SafTCode {
			inUse, err := txq.VatCodeInUse(ctx, current.ID)
			if err != nil {
				return err
			}
			if inUse {
				refusal = ptr(conflict(codeVatCodeInUse, vatCodeInUseTitle,
					"Lines carry this code, so its category and SAF-T code can no longer change. Deactivate it and create a new one instead."))
				return errRefused
			}
		}
		if fields.category != current.EhfCategory {
			// A category change must still fit every period's rate: a code
			// cannot become S at 0 %, nor leave S with a rate.
			rates, err := txq.VatCodeRates(ctx, current.ID)
			if err != nil {
				return err
			}
			for _, r := range rates {
				rate, err := floatFromNumeric(r.RatePercent)
				if err != nil {
					return err
				}
				if _, msg := parseRate(fields.category, rate); msg != "" {
					rateRefusal = "The code's rates do not fit that category: " + msg
					return errRefused
				}
			}
		}
		updated, err := txq.UpdateVatCode(ctx, store.UpdateVatCodeParams{
			ID: current.ID, Code: fields.code, Name: fields.name, SafTCode: fields.safT,
			EhfCategory: fields.category, ExemptionReason: fields.reason, Active: body.Active, Now: s.deps.Clock(),
		})
		if err != nil {
			return err
		}
		saved, err = readVatCode(ctx, txq, updated)
		return err
	})
	switch {
	case notFound:
		return gen.PutInvoicesVatCodesById404Response{}, nil
	case refusal != nil:
		return gen.PutInvoicesVatCodesById409ApplicationProblemPlusJSONResponse(*refusal), nil
	case rateRefusal != "":
		return gen.PutInvoicesVatCodesById400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle,
			fieldError("ehfCategory", rateRefusal))), nil
	case db.IsUniqueViolation(err, vatCodeIndex):
		return gen.PutInvoicesVatCodesById400ApplicationProblemPlusJSONResponse(invalid(invalidVatCodeTitle,
			fieldError("code", fmt.Sprintf("A VAT code '%s' already exists", fields.code)))), nil
	case err != nil:
		return nil, fmt.Errorf("invoices: change VAT code %d: %w", req.Id, err)
	}
	return gen.PutInvoicesVatCodesById200JSONResponse(saved), nil
}

// PostInvoicesVatCodesByIdRates Change a VAT code's rate from a date
// (POST /api/v1/invoices/vat-codes/{id}/rates)
//
// The rate-change rule (D3): the open period closes the day before validFrom
// and a new open period starts on it. validFrom must be after the open
// period's own start, and after the latest issue date of any issued document,
// so no issued line ever falls into a period that changed after it was issued.
// The transaction takes the settings row FOR UPDATE first — an issue in flight
// holds it FOR SHARE until it commits — so the latest issue date it reads is
// final.
func (s *server) PostInvoicesVatCodesByIdRates(ctx context.Context, req gen.PostInvoicesVatCodesByIdRatesRequestObject) (gen.PostInvoicesVatCodesByIdRatesResponseObject, error) {
	validFrom := utcDay(req.Body.ValidFrom.Time)
	var refusal *gen.InvoicesConflictProblem
	var errs map[string][]string
	notFound := false
	var saved gen.InvoicesVatCode
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := txq.LockSettings(ctx); err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
		code, err := txq.LockVatCode(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock VAT code %d: %w", req.Id, err)
		}
		rate, msg := parseRate(code.EhfCategory, req.Body.RatePercent)
		if msg != "" {
			errs = fieldError("ratePercent", msg)
			return errRefused
		}
		rates, err := txq.VatCodeRates(ctx, code.ID)
		if err != nil {
			return err
		}
		// The handlers keep at least one period on every code, but no
		// constraint does: a code with none has no open period to close, and
		// this change opens its first.
		if len(rates) > 0 {
			open := rates[len(rates)-1]
			if !validFrom.After(open.ValidFrom.Time) {
				errs = fieldError("validFrom", fmt.Sprintf("A new rate takes effect after the current one's start, %s",
					open.ValidFrom.Time.Format(time.DateOnly)))
				return errRefused
			}
		}
		latest, err := txq.LatestIssueDate(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the latest issue date: %w", err)
		}
		if latest.Valid && !validFrom.After(latest.Time) {
			refusal = ptr(conflict(codeRateChangeInPast, rateChangeInPastTitle, fmt.Sprintf(
				"A document is already issued on %s, so a rate change takes effect after that day.",
				latest.Time.Format(time.DateOnly))))
			return errRefused
		}
		ratePercent, err := numericFromRat(rate, 2)
		if err != nil {
			return err
		}
		if err := txq.CloseOpenVatCodeRate(ctx, store.CloseOpenVatCodeRateParams{
			VatCodeID: code.ID, ValidTo: pgDate(validFrom.AddDate(0, 0, -1)),
		}); err != nil {
			return fmt.Errorf("invoices: close VAT code %d's open rate: %w", code.ID, err)
		}
		if _, err := txq.InsertVatCodeRate(ctx, store.InsertVatCodeRateParams{
			VatCodeID: code.ID, RatePercent: ratePercent, ValidFrom: pgDate(validFrom), Now: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("invoices: add VAT code %d's rate: %w", code.ID, err)
		}
		saved, err = readVatCode(ctx, txq, code)
		return err
	})
	switch {
	case notFound:
		return gen.PostInvoicesVatCodesByIdRates404Response{}, nil
	case errs != nil:
		return gen.PostInvoicesVatCodesByIdRates400ApplicationProblemPlusJSONResponse(invalid(invalidRateTitle, errs)), nil
	case refusal != nil:
		return gen.PostInvoicesVatCodesByIdRates409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, err
	}
	return gen.PostInvoicesVatCodesByIdRates201JSONResponse(saved), nil
}

// DeleteInvoicesVatCodesByIdRatesByRateId Remove a VAT code's latest rate period
// (DELETE /api/v1/invoices/vat-codes/{id}/rates/{rateId})
//
// A mistaken future period is removed and the one before it reopened (D3). Only
// the latest may go, never the only one, and only while no issued document is
// dated on or after its start — under the same settings lock a rate change
// takes, for the same reason.
func (s *server) DeleteInvoicesVatCodesByIdRatesByRateId(ctx context.Context, req gen.DeleteInvoicesVatCodesByIdRatesByRateIdRequestObject) (gen.DeleteInvoicesVatCodesByIdRatesByRateIdResponseObject, error) {
	var refusal *gen.InvoicesConflictProblem
	notFound := false
	var saved gen.InvoicesVatCode
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := txq.LockSettings(ctx); err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
		code, err := txq.LockVatCode(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			notFound = true
			return errRefused
		}
		if err != nil {
			return fmt.Errorf("invoices: lock VAT code %d: %w", req.Id, err)
		}
		rates, err := txq.VatCodeRates(ctx, code.ID)
		if err != nil {
			return err
		}
		at := -1
		for i, r := range rates {
			if r.ID == req.RateId {
				at = i
			}
		}
		switch {
		case at < 0:
			notFound = true
			return errRefused
		case at != len(rates)-1:
			refusal = ptr(conflict(codeRatePeriodNotLatest, ratePeriodTitle,
				"Only the latest rate period can be removed."))
			return errRefused
		case len(rates) == 1:
			refusal = ptr(conflict(codeRatePeriodLast, ratePeriodTitle,
				"A VAT code always has a rate, so its only period cannot be removed."))
			return errRefused
		}
		latest, err := txq.LatestIssueDate(ctx)
		if err != nil {
			return fmt.Errorf("invoices: read the latest issue date: %w", err)
		}
		if latest.Valid && !latest.Time.Before(rates[at].ValidFrom.Time) {
			refusal = ptr(conflict(codeRatePeriodInUse, ratePeriodTitle, fmt.Sprintf(
				"A document is issued on %s, on or after this period's start, so the period stays.",
				latest.Time.Format(time.DateOnly))))
			return errRefused
		}
		if err := txq.DeleteVatCodeRate(ctx, rates[at].ID); err != nil {
			return fmt.Errorf("invoices: remove VAT rate %d: %w", rates[at].ID, err)
		}
		if err := txq.ReopenVatCodeRate(ctx, rates[at-1].ID); err != nil {
			return fmt.Errorf("invoices: reopen VAT rate %d: %w", rates[at-1].ID, err)
		}
		saved, err = readVatCode(ctx, txq, code)
		return err
	})
	switch {
	case notFound:
		return gen.DeleteInvoicesVatCodesByIdRatesByRateId404Response{}, nil
	case refusal != nil:
		return gen.DeleteInvoicesVatCodesByIdRatesByRateId409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, err
	}
	return gen.DeleteInvoicesVatCodesByIdRatesByRateId200JSONResponse(saved), nil
}
