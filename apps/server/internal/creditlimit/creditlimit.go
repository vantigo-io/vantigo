// Package creditlimit is a customer's credit exposure and whether it passes
// the customer's credit limit (invoices phase 6 design I38; plan
// docs/superpowers/plans/2026-11-18-invoices-phase6-e-plan.md, step E13 and
// reading E14): the one definition Customers' overview, Orders' confirm and
// Invoices' draft and issue all judge a limit by, so the three never disagree.
//
// The exposure is in NOK, the limit's currency: what the customer owes on
// issued invoices — each at its open NOK receivable (contracts.
// CurrencyReceivables.OutstandingNOK, Invoices' open_nok), whatever currency
// it is in — plus what its confirmed orders hold that is not yet invoiced,
// plus the document being judged. An order amount in another currency is
// counted at today's rate (contracts.ExchangeRates, read by the caller with
// RatesFor); a currency with no rate within seven days is listed as uncounted,
// never added and never guessed.
//
// It is pure arithmetic over exact decimals (math/big.Rat) but for RatesFor,
// which asks the rates contract. It is a platform package: it imports no
// business module.
package creditlimit

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// nok is the limit's currency.
const nok = "NOK"

// Result is a customer's exposure: NOK the counted total, decimal text with
// two decimals, and Uncounted every currency's non-zero sum that has no rate,
// by code ascending, empty when there is none.
type Result struct {
	NOK       string
	Uncounted []contracts.CurrencyAmount
}

// Rates are NOK per one unit of a currency, decimal text, as
// contracts.ExchangeRate.Rate carries them. A currency absent has no rate.
type Rates map[string]string

// RatesFor reads today's rate of every currency in currencies but NOK through
// rates, at the instant now (the contract judges the day in the business time
// zone, at most seven days back). A currency without a rate is left out, so
// Exposure lists it as uncounted; nil rates (invoicing off) leave them all
// out. An error of the contract is an error: the caller decides what an
// unread rate means.
func RatesFor(ctx context.Context, rates contracts.ExchangeRates, now time.Time, currencies ...string) (Rates, error) {
	out := Rates{}
	if rates == nil {
		return out, nil
	}
	for _, c := range currencies {
		if c == nok || c == "" {
			continue
		}
		if _, done := out[c]; done {
			continue
		}
		r, ok, err := rates.Rate(ctx, c, now)
		if err != nil {
			return nil, fmt.Errorf("creditlimit: read the rate of %s: %w", c, err)
		}
		if ok {
			out[c] = r.Rate
		}
	}
	return out, nil
}

// Currencies are the currencies of amounts, for RatesFor.
func Currencies(amounts ...[]contracts.CurrencyAmount) []string {
	var out []string
	for _, list := range amounts {
		for _, a := range list {
			out = append(out, a.Currency)
		}
	}
	return out
}

// Exposure sums receivables' NOK outstanding (nil when invoicing is off or
// was not read: nothing is counted from it), the order backlog and extra —
// the document being judged, if any — into one NOK total; an amount in
// another currency counts at its rate, and one with no rate is uncounted. A
// text that is not a decimal is an error, never a zero.
func Exposure(receivables *contracts.CustomerReceivableTotals, backlog []contracts.CurrencyAmount, rates Rates, extra ...contracts.CurrencyAmount) (Result, error) {
	total := new(big.Rat)
	uncounted := map[string]*big.Rat{}
	decimal := func(what, text string) (*big.Rat, error) {
		v, ok := new(big.Rat).SetString(text)
		if !ok {
			return nil, fmt.Errorf("creditlimit: %s %q is not a decimal", what, text)
		}
		return v, nil
	}
	add := func(currency, amount string) error {
		v, err := decimal(currency, amount)
		if err != nil {
			return err
		}
		if currency == nok {
			total.Add(total, v)
			return nil
		}
		if rate, ok := rates[currency]; ok {
			r, err := decimal("the rate of "+currency, rate)
			if err != nil {
				return err
			}
			// Each amount at two decimals in NOK, rounded half away from
			// zero as every money figure is.
			nokAmount, _ := new(big.Rat).SetString(new(big.Rat).Mul(v, r).FloatString(2))
			total.Add(total, nokAmount)
			return nil
		}
		if uncounted[currency] == nil {
			uncounted[currency] = new(big.Rat)
		}
		uncounted[currency].Add(uncounted[currency], v)
		return nil
	}
	if receivables != nil {
		for _, c := range receivables.Currencies {
			// The invoice's own NOK, at the rate it was issued at; a
			// provider that does not answer it is read in its currency.
			if c.OutstandingNOK != "" {
				if err := add(nok, c.OutstandingNOK); err != nil {
					return Result{}, err
				}
				continue
			}
			if err := add(c.Currency, c.Outstanding); err != nil {
				return Result{}, err
			}
		}
	}
	for _, b := range backlog {
		if err := add(b.Currency, b.Amount); err != nil {
			return Result{}, err
		}
	}
	for _, e := range extra {
		if err := add(e.Currency, e.Amount); err != nil {
			return Result{}, err
		}
	}
	out := Result{NOK: total.FloatString(2), Uncounted: []contracts.CurrencyAmount{}}
	codes := make([]string, 0, len(uncounted))
	for c := range uncounted {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	for _, c := range codes {
		if uncounted[c].Sign() == 0 {
			continue
		}
		out.Uncounted = append(out.Uncounted, contracts.CurrencyAmount{Currency: c, Amount: uncounted[c].FloatString(2)})
	}
	return out, nil
}

// Exceeds is whether r's NOK total is above limit, a NOK amount as decimal
// text. Equal is not exceeding, and no limit (nil) is never exceeded.
func Exceeds(limit *string, r Result) (bool, error) {
	if limit == nil {
		return false, nil
	}
	l, ok := new(big.Rat).SetString(*limit)
	if !ok {
		return false, fmt.Errorf("creditlimit: the limit %q is not a decimal", *limit)
	}
	e, ok := new(big.Rat).SetString(r.NOK)
	if !ok {
		return false, fmt.Errorf("creditlimit: the exposure %q is not a decimal", r.NOK)
	}
	return e.Cmp(l) > 0, nil
}
