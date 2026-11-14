package accounting

// The tax codes (A5): the standard list seeded at setup, each with its VAT
// accounts and its rates as dated periods.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

func itoa(i int) string { return strconv.Itoa(i) }

// taxCodeViews is every tax code with its rates and the rate valid today.
func (s *server) taxCodeViews(ctx context.Context, q *store.Queries, only *int32) ([]gen.AccountingTaxCode, error) {
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	codes, err := q.ListTaxCodes(ctx)
	if err != nil {
		return nil, err
	}
	rates, err := q.ListTaxRates(ctx, only)
	if err != nil {
		return nil, err
	}
	out := []gen.AccountingTaxCode{}
	for _, c := range codes {
		if only != nil && c.ID != *only {
			continue
		}
		v := gen.AccountingTaxCode{Id: c.ID, Code: c.Code, Name: c.Name, Direction: gen.AccountingTaxDirection(c.Direction),
			ReverseCharge: c.ReverseCharge, VatAccountId: c.VatAccountID, CounterVatAccountId: c.CounterVatAccountID,
			Reported: c.Reported, Active: c.Active, Revision: c.Revision, Rates: []gen.AccountingTaxRate{}}
		for _, r := range rates {
			if r.TaxCodeID != c.ID {
				continue
			}
			v.Rates = append(v.Rates, gen.AccountingTaxRate{RatePercent: r.RatePercent, ValidFrom: apiDate(r.ValidFrom),
				ValidTo: apiDatePtr(r.ValidTo)})
			if !r.ValidFrom.After(today) && (r.ValidTo == nil || !r.ValidTo.Before(today)) {
				v.CurrentRate = ptr(r.RatePercent)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// ListAccountingTaxCodes is the tax codes by code.
func (s *server) ListAccountingTaxCodes(ctx context.Context, _ gen.ListAccountingTaxCodesRequestObject) (gen.ListAccountingTaxCodesResponseObject, error) {
	items, err := s.taxCodeViews(ctx, store.New(s.deps.Pool), nil)
	if err != nil {
		return nil, err
	}
	return gen.ListAccountingTaxCodes200JSONResponse{Items: items}, nil
}

func (s *server) taxCode(ctx context.Context, q *store.Queries, id int32) (gen.AccountingTaxCode, error) {
	items, err := s.taxCodeViews(ctx, q, &id)
	if err != nil {
		return gen.AccountingTaxCode{}, err
	}
	if len(items) == 0 {
		return gen.AccountingTaxCode{}, notFound()
	}
	return items[0], nil
}

// UpdateAccountingTaxCode changes a code's name, its VAT accounts and whether
// it is offered. Its code, direction and reverse charge are the standard's.
func (s *server) UpdateAccountingTaxCode(ctx context.Context, req gen.UpdateAccountingTaxCodeRequestObject) (gen.UpdateAccountingTaxCodeResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The tax code cannot be changed", "body", "", "A body is required.")
	}
	b := *req.Body
	if strings.TrimSpace(b.Name) == "" || len([]rune(b.Name)) > 200 {
		return nil, invalidField("The tax code cannot be changed", "name", "", "A name of at most 200 characters.")
	}
	var out gen.AccountingTaxCode
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		cur, err := q.LockTaxCode(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if cur.Revision != b.Revision {
			return conflict(codeRevisionConflict, "The tax code has changed", "Reload it and try again.")
		}
		code, err := q.GetTaxCode(ctx, req.Id)
		if err != nil {
			return err
		}
		if !code.ReverseCharge && b.CounterVatAccountId != nil {
			return invalidField("The tax code cannot be changed", "counterVatAccountId", "", "Only a reverse-charge code has a counter account.")
		}
		err = q.UpdateTaxCode(ctx, store.UpdateTaxCodeParams{ID: req.Id, Name: strings.TrimSpace(b.Name), VatAccountID: b.VatAccountId,
			CounterVatAccountID: b.CounterVatAccountId, Active: b.Active, Now: s.now()})
		if unknownForeignKey(err) {
			return invalidField("The tax code cannot be changed", "vatAccountId", codeAccountUnknown, "No such account.")
		}
		if err != nil {
			return err
		}
		out, err = s.taxCode(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.UpdateAccountingTaxCode200JSONResponse(out), nil
}

// AddAccountingTaxCodeRate starts a new rate period (A5): the open one ends
// the day before it, which must be after the open one's start.
func (s *server) AddAccountingTaxCodeRate(ctx context.Context, req gen.AddAccountingTaxCodeRateRequestObject) (gen.AddAccountingTaxCodeRateResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The rate cannot be added", "body", "", "A body is required.")
	}
	rate, ok := parseAmount(req.Body.RatePercent)
	if !ok || rate.Sign() < 0 || rate.Cmp(hundred) >= 0 {
		return nil, invalidField("The rate cannot be added", "ratePercent", codeAmountInvalid, "A rate from 0 to 99.99.")
	}
	from := day(req.Body.ValidFrom)
	var out gen.AccountingTaxCode
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.LockTaxCode(ctx, req.Id); noRows(err) {
			return notFound()
		} else if err != nil {
			return err
		}
		open, err := q.OpenTaxRate(ctx, req.Id)
		switch {
		case noRows(err):
		case err != nil:
			return err
		case !from.After(open.ValidFrom):
			return invalidField("The rate cannot be added", "validFrom", codeRatePeriodInvalid,
				"The new period starts after the current one's start, "+open.ValidFrom.Format(time.DateOnly)+".")
		default:
			if err := q.CloseTaxRate(ctx, store.CloseTaxRateParams{ID: open.ID, ValidTo: ptr(from.AddDate(0, 0, -1))}); err != nil {
				return err
			}
		}
		if err := q.InsertTaxRate(ctx, store.InsertTaxRateParams{TaxCodeID: req.Id, RatePercent: money(rate), ValidFrom: from, Now: s.now()}); err != nil {
			return err
		}
		out, err = s.taxCode(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.AddAccountingTaxCodeRate201JSONResponse(out), nil
}
