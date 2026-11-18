package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the exchange rates (invoices phase 6 design I17–I19; plan C2,
// C3): the rate a day resolves to, the manual rates a manager enters, and
// turning a currency on in the settings, which fetches its last 30 days from
// Norges Bank (norgesbank.go) before it is allowed.

// The foreign currency's refusals and warnings (plan C3–C5).
const (
	codeExchangeRateMissing  = "exchange_rate_missing"
	codeIbanRequired         = "iban_required"
	codeCurrencyNotEnabled   = "currency_not_enabled"
	codeExchangeRateFetched  = "exchange_rate_fetched"
	warningExchangeRateMiss  = "exchange_rate_missing"
	exchangeRateMissingTitle = "No exchange rate"
	ibanRequiredTitle        = "The IBAN and BIC are missing"
	invalidExchangeRateTitle = "Invalid exchange rate"
	msgCurrencyNotEnabled    = "This currency is not turned on in the settings"
	// rateSourceNOK, rateSourceNorgesBank and rateSourceManual are where a
	// document's rate came from (ck_invoices_exchange_rate_source), the last
	// two also an exchange_rates row's source.
	rateSourceNOK        = "nok"
	rateSourceNorgesBank = "norges_bank"
	rateSourceManual     = "manual"
	// rateLookback is how far back a rate holds (I19): the latest business
	// day with a rate, at most seven days before.
	rateLookback = 7
	// rateBackfillDays is what turning a currency on fetches (I18).
	rateBackfillDays = 30
	// maxCurrencies is how many currencies the settings may turn on.
	maxCurrencies = 20
	// maxRatesPage is the most rows GET /exchange-rates answers.
	maxRatesPage = 400
)

// currencyPattern is an ISO 4217 code as the columns' CHECKs have it.
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// maxRate is the highest rate numeric(14,6) holds.
var maxRate = big.NewRat(99999999, 1)

// resolvedRate is the rate a day resolves to: NOK per one unit, its day and
// its row's source.
type resolvedRate struct {
	Rate   *big.Rat
	Day    time.Time
	Source string
}

// resolveRate is currency's rate on day (I19): the latest row on or before it
// and no more than seven days before, a manual entry being that day's row
// (reading 4). ok is false when none is found. NOK is always 1.
func resolveRate(ctx context.Context, q *store.Queries, currency string, day time.Time) (resolvedRate, bool, error) {
	if currency == "NOK" {
		return resolvedRate{Rate: big.NewRat(1, 1), Day: day, Source: rateSourceNOK}, true, nil
	}
	row, err := q.RateOn(ctx, store.RateOnParams{Currency: currency, Day: pgDate(day)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return resolvedRate{}, false, nil
	case err != nil:
		return resolvedRate{}, false, fmt.Errorf("invoices: read the %s rate of %s: %w", currency, day.Format(time.DateOnly), err)
	}
	rate, err := ratFromNumeric(row.Rate)
	if err != nil {
		return resolvedRate{}, false, err
	}
	return resolvedRate{Rate: rate, Day: row.RateDate.Time, Source: row.Source}, true, nil
}

// exchangeRateResponse is one rate for the wire.
func exchangeRateResponse(r store.InvoicesExchangeRate) gen.InvoicesExchangeRate {
	rate, _ := floatFromNumeric(r.Rate)
	return gen.InvoicesExchangeRate{
		Currency: r.Currency, Day: openapi_types.Date{Time: r.RateDate.Time}, Rate: rate,
		Source: gen.InvoicesExchangeRateSource(r.Source), FetchedAt: r.FetchedAt, EnteredByUserId: r.EnteredByUserID,
	}
}

// rateFetchResponses is every fetch row for the wire.
func rateFetchResponses(rows []store.InvoicesRateFetch, enabled []string) []gen.InvoicesRateFetch {
	out := make([]gen.InvoicesRateFetch, 0, len(rows))
	for _, r := range rows {
		if !slices.Contains(enabled, r.Currency) {
			continue
		}
		out = append(out, gen.InvoicesRateFetch{
			Currency: r.Currency, LastAttemptAt: r.LastAttemptAt, LastSuccessAt: r.LastSuccessAt, LastError: r.LastError,
		})
	}
	return out
}

// enabledCurrencies is the settings' currencies, read on q.
func enabledCurrencies(ctx context.Context, q *store.Queries) ([]string, error) {
	c, err := q.EnabledCurrencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the currencies: %w", err)
	}
	return c, nil
}

// parseCurrencies reads the settings' currencies (plan C3): upper-cased and
// de-duplicated, at most 20, each one Norges Bank publishes, NOK never.
func parseCurrencies(raw []string, add func(field, msg string)) []string {
	out := []string{}
	for _, c := range raw {
		c = strings.ToUpper(strings.TrimSpace(c))
		switch {
		case c == "NOK":
			add("currencies", "NOK is the functional currency and is always on")
			continue
		case !isNBCurrency(c):
			add("currencies", fmt.Sprintf("Norges Bank publishes no rate of %q", c))
			continue
		}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	if len(out) > maxCurrencies {
		add("currencies", fmt.Sprintf("At most %d currencies can be turned on", maxCurrencies))
	}
	slices.Sort(out)
	return out
}

// backfillRates fetches currencies' last 30 days from Norges Bank and stores
// what came back, with no transaction open (reading 16): turning a currency
// on judges exchange_rate_missing on what this stored. A failed fetch is not
// itself a refusal — a manual rate entered earlier still satisfies it — and
// is recorded on the currency's fetch row.
func (s *server) backfillRates(ctx context.Context, currencies []string, today time.Time) error {
	q := store.New(s.deps.Pool)
	for _, c := range currencies {
		if err := q.EnsureRateFetch(ctx, c); err != nil {
			return fmt.Errorf("invoices: add the %s fetch: %w", c, err)
		}
	}
	if !s.ratesEnabled() || len(currencies) == 0 {
		return nil
	}
	_, err := s.fetchAndStore(ctx, currencies, today.AddDate(0, 0, -rateBackfillDays), today)
	return err
}

// ratesEnabled reports whether this installation calls Norges Bank.
func (s *server) ratesEnabled() bool {
	return s.deps.Config != nil && s.deps.Config.InvoicesRatesEnabled
}

// fetchAndStore claims currencies' fetch rows, asks Norges Bank for them from
// from to to with no transaction open, and stores the answer and each
// claim's outcome in one short transaction per currency (plan C2: no
// hold-and-wait). A currency another worker holds is skipped. It answers the
// currencies it fetched; the error is its own — a claim or a write that
// failed — never Norges Bank's, which is the fetch row's last_error.
func (s *server) fetchAndStore(ctx context.Context, currencies []string, from, to time.Time) ([]string, error) {
	now := s.deps.Clock().UTC()
	lease := newLeaseID()
	q := store.New(s.deps.Pool)
	var claimed []string
	for _, c := range currencies {
		_, err := q.ClaimRateFetch(ctx, store.ClaimRateFetchParams{
			LeaseID: lease, LeaseUntil: now.Add(rateFetchLease), Now: now, Currency: c,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			continue
		case err != nil:
			return claimed, fmt.Errorf("invoices: claim the %s fetch: %w", c, err)
		}
		claimed = append(claimed, c)
	}
	if len(claimed) == 0 {
		return nil, nil
	}
	base := ""
	if s.deps.Config != nil {
		base = s.deps.Config.InvoicesRatesURL
	}
	rates, fetchErr := fetchRates(ctx, ratesClient(s.deps.HTTPTransport), base, claimed, from, to)
	var outcome *string
	if fetchErr != nil {
		var re *ratesError
		text := ratesFailureUnreachable
		if errors.As(fetchErr, &re) {
			text = re.Outcome
		}
		outcome = &text
		s.deps.Logger.WarnContext(ctx, "invoices: Norges Bank's exchange rates could not be fetched",
			"currencies", strings.Join(claimed, ","), "outcome", text)
	}
	for _, c := range claimed {
		err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			if outcome == nil {
				for _, r := range rates[c] {
					n, err := numericFromRat(r.Rate, ratesMaxDecimals)
					if err != nil {
						return err
					}
					if _, err := txq.UpsertFetchedRate(ctx, store.UpsertFetchedRateParams{
						Currency: c, RateDate: pgDate(r.Day), Rate: n, FetchedAt: now,
					}); err != nil {
						return fmt.Errorf("invoices: store the %s rate of %s: %w", c, r.Day.Format(time.DateOnly), err)
					}
				}
			}
			_, err := txq.ReleaseRateFetch(ctx, store.ReleaseRateFetchParams{
				LastError: outcome, Now: now, Currency: c, LeaseID: lease,
			})
			return err
		})
		if err != nil {
			return claimed, err
		}
	}
	return claimed, nil
}

// GetInvoicesExchangeRates List exchange rates
// (GET /api/v1/invoices/exchange-rates)
func (s *server) GetInvoicesExchangeRates(ctx context.Context, req gen.GetInvoicesExchangeRatesRequestObject) (gen.GetInvoicesExchangeRatesResponseObject, error) {
	currency := strings.ToUpper(strings.TrimSpace(req.Params.Currency))
	if !currencyPattern.MatchString(currency) {
		return gen.GetInvoicesExchangeRates400ApplicationProblemPlusJSONResponse(invalid(invalidExchangeRateTitle,
			fieldError("currency", "A currency is three letters, such as EUR"))), nil
	}
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	to := holidays.DayIn(s.deps.Clock(), loc)
	if req.Params.To != nil {
		to = req.Params.To.Time
	}
	from := to.AddDate(0, 0, -rateBackfillDays)
	if req.Params.From != nil {
		from = req.Params.From.Time
	}
	if from.After(to) {
		return gen.GetInvoicesExchangeRates400ApplicationProblemPlusJSONResponse(invalid(invalidExchangeRateTitle,
			fieldError("from", "from is on or before to"))), nil
	}
	rows, err := store.New(s.deps.Pool).RatesBetween(ctx, store.RatesBetweenParams{
		Currency: currency, FirstDay: pgDate(from), LastDay: pgDate(to), MaxRows: maxRatesPage,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: read the %s rates: %w", currency, err)
	}
	items := make([]gen.InvoicesExchangeRate, 0, len(rows))
	for _, r := range rows {
		items = append(items, exchangeRateResponse(r))
	}
	return gen.GetInvoicesExchangeRates200JSONResponse{Items: items}, nil
}

// PutInvoicesExchangeRatesByCurrencyByDay Enter a manual exchange rate
// (PUT /api/v1/invoices/exchange-rates/{currency}/{day})
func (s *server) PutInvoicesExchangeRatesByCurrencyByDay(ctx context.Context, req gen.PutInvoicesExchangeRatesByCurrencyByDayRequestObject) (gen.PutInvoicesExchangeRatesByCurrencyByDayResponseObject, error) {
	currency := strings.ToUpper(req.Currency)
	q := store.New(s.deps.Pool)
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	// Any currency Norges Bank publishes, not only one turned on: turning a
	// currency on without a fetched rate needs a manual one first (I17).
	var errs map[string][]string
	if !isNBCurrency(currency) {
		errs = withFieldError(errs, "currency", "Norges Bank publishes no rate of this currency")
	}
	if req.Day.After(holidays.DayIn(s.deps.Clock(), loc)) {
		errs = withFieldError(errs, "day", "A rate is entered for today or a day before")
	}
	rate, msg := parseExchangeRate("The rate", req.Body.Rate)
	if msg != "" {
		errs = withFieldError(errs, "rate", msg)
	}
	if errs != nil {
		return gen.PutInvoicesExchangeRatesByCurrencyByDay400ApplicationProblemPlusJSONResponse(invalid(invalidExchangeRateTitle, errs)), nil
	}
	n, err := numericFromRat(rate, ratesMaxDecimals)
	if err != nil {
		return nil, err
	}
	caller := callerID(ctx)
	row, err := q.PutManualRate(ctx, store.PutManualRateParams{
		Currency: currency, RateDate: pgDate(req.Day.Time), Rate: n, EnteredAt: s.deps.Clock(), EnteredByUserID: &caller,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: enter the %s rate: %w", currency, err)
	}
	return gen.PutInvoicesExchangeRatesByCurrencyByDay200JSONResponse(exchangeRateResponse(row)), nil
}

// parseExchangeRate reads an exchange rate: above 0, at most six decimals.
func parseExchangeRate(label string, v float64) (*big.Rat, string) {
	return amount(label, v, ratesMaxDecimals, zero, true, maxRate)
}

// DeleteInvoicesExchangeRatesByCurrencyByDay Remove a manual exchange rate
// (DELETE /api/v1/invoices/exchange-rates/{currency}/{day})
func (s *server) DeleteInvoicesExchangeRatesByCurrencyByDay(ctx context.Context, req gen.DeleteInvoicesExchangeRatesByCurrencyByDayRequestObject) (gen.DeleteInvoicesExchangeRatesByCurrencyByDayResponseObject, error) {
	currency := strings.ToUpper(req.Currency)
	q := store.New(s.deps.Pool)
	n, err := q.DeleteManualRate(ctx, store.DeleteManualRateParams{Currency: currency, RateDate: pgDate(req.Day.Time)})
	if err != nil {
		return nil, fmt.Errorf("invoices: remove the %s rate: %w", currency, err)
	}
	if n == 1 {
		// The day is Norges Bank's again: fetched at once, with no
		// transaction open; a failure is the fetch row's last_error, and the
		// worker's next cycle asks again.
		if s.ratesEnabled() {
			if _, err := s.fetchAndStore(ctx, []string{currency}, req.Day.Time, req.Day.Time); err != nil {
				return nil, err
			}
		}
		return gen.DeleteInvoicesExchangeRatesByCurrencyByDay204Response{}, nil
	}
	_, err = q.GetRate(ctx, store.GetRateParams{Currency: currency, RateDate: pgDate(req.Day.Time)})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.DeleteInvoicesExchangeRatesByCurrencyByDay404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("invoices: read the %s rate: %w", currency, err)
	}
	return gen.DeleteInvoicesExchangeRatesByCurrencyByDay409ApplicationProblemPlusJSONResponse(conflict(codeExchangeRateFetched,
		"The rate is Norges Bank's", "A rate fetched from Norges Bank is never removed; enter a manual rate of the day to use another.")), nil
}

// PostInvoicesExchangeRatesFetch Fetch exchange rates now
// (POST /api/v1/invoices/exchange-rates/fetch)
func (s *server) PostInvoicesExchangeRatesFetch(ctx context.Context, _ gen.PostInvoicesExchangeRatesFetchRequestObject) (gen.PostInvoicesExchangeRatesFetchResponseObject, error) {
	loc, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	enabled, err := enabledCurrencies(ctx, q)
	if err != nil {
		return nil, err
	}
	if s.ratesEnabled() {
		if err := s.ratesCycle(ctx, enabled, s.deps.Clock().In(loc), true); err != nil {
			return nil, err
		}
	}
	rows, err := q.RateFetchStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the rate fetches: %w", err)
	}
	return gen.PostInvoicesExchangeRatesFetch200JSONResponse{RateFetches: rateFetchResponses(rows, enabled)}, nil
}

// ratesCycle is one cycle of the invoices-rates worker over enabled, at now
// (in the business time zone): every currency due — or every one, forced
// (Fetch now) — is fetched from the day after its newest fetched rate, or 30
// days back, to today.
func (s *server) ratesCycle(ctx context.Context, enabled []string, now time.Time, force bool) error {
	q := store.New(s.deps.Pool)
	statuses, err := q.RateFetchStatus(ctx)
	if err != nil {
		return fmt.Errorf("invoices: read the rate fetches: %w", err)
	}
	byCurrency := map[string]store.InvoicesRateFetch{}
	for _, st := range statuses {
		byCurrency[st.Currency] = st
	}
	today := holidays.DayIn(now, now.Location())
	published := latestPublication(now)
	var due []string
	from := today
	for _, c := range enabled {
		if err := q.EnsureRateFetch(ctx, c); err != nil {
			return fmt.Errorf("invoices: add the %s fetch: %w", c, err)
		}
		latest, err := q.LatestRateDate(ctx, c)
		if err != nil {
			return fmt.Errorf("invoices: read the newest %s rate: %w", c, err)
		}
		st := byCurrency[c]
		if !force && !rateFetchDue(st, latest, now, published) {
			continue
		}
		// From a week before the newest fetched day — a rate Norges Bank
		// revised is taken again, a manual one never — else 30 days back.
		start := today.AddDate(0, 0, -rateBackfillDays)
		if latest.Valid && latest.Time.AddDate(0, 0, -rateLookback).After(start) {
			start = latest.Time.AddDate(0, 0, -rateLookback)
		}
		if start.Before(from) {
			from = start
		}
		due = append(due, c)
	}
	if len(due) == 0 {
		return nil
	}
	_, err = s.fetchAndStore(ctx, due, from, today)
	return err
}

// ratesPublishedAt is when Norges Bank's rates are read as published on a
// business day: 16:30 in the business time zone, half an hour after its
// "approximately 16:00 CET".
const (
	ratesPublishedHour   = 16
	ratesPublishedMinute = 30
	// rateRetryAfter is how long a currency whose day is not yet in waits
	// before it is asked again.
	rateRetryAfter = time.Hour
)

// latestPublication is the latest business day's 16:30 at or before now, in
// now's location.
func latestPublication(now time.Time) time.Time {
	day := holidays.DayIn(now, now.Location())
	for i := 0; i < 15; i++ {
		at := time.Date(day.Year(), day.Month(), day.Day(), ratesPublishedHour, ratesPublishedMinute, 0, 0, now.Location())
		if holidays.BusinessDay(day) && !at.After(now) {
			return at
		}
		day = day.AddDate(0, 0, -1)
	}
	return now
}

// rateFetchDue reports whether a currency is fetched this cycle: never
// fetched since the latest publication, or fetched since but without that
// day's rate and not asked for an hour — Norges Bank publishes late on some
// days, and a public holiday of its own the calendar does not know answers
// no rows.
func rateFetchDue(st store.InvoicesRateFetch, latest pgtype.Date, now, published time.Time) bool {
	if st.LastSuccessAt == nil || st.LastSuccessAt.Before(published) {
		if st.LastAttemptAt != nil && st.LastError != nil && now.Sub(*st.LastAttemptAt) < rateRetryAfter {
			return false
		}
		return true
	}
	pubDay := holidays.DayIn(published, published.Location())
	if latest.Valid && !latest.Time.Before(pubDay) {
		return false
	}
	return st.LastAttemptAt == nil || now.Sub(*st.LastAttemptAt) >= rateRetryAfter
}

// exchangeRates is this module's contracts.ExchangeRates (I18, reading 10):
// resolveRate on the pool, the day taken in the business time zone.
type exchangeRates struct {
	pool     store.DBTX
	calendar contracts.BusinessCalendar
}

var _ contracts.ExchangeRates = exchangeRates{}

// newExchangeRates is Module's ExchangeRates.
func newExchangeRates(d module.Deps) contracts.ExchangeRates {
	return exchangeRates{pool: d.Pool, calendar: d.Calendar}
}

// Rate is resolveRate of currency on the day the instant day falls on in the
// business time zone, as every day of this module is judged.
func (e exchangeRates) Rate(ctx context.Context, currency string, day time.Time) (contracts.ExchangeRate, bool, error) {
	currency = strings.ToUpper(currency)
	loc := time.UTC
	if e.calendar != nil {
		noteContractCall(ctx, "Calendar.Zone")
		z, err := e.calendar.Zone(ctx)
		if err != nil {
			return contracts.ExchangeRate{}, false, fmt.Errorf("invoices: read the business time zone: %w", err)
		}
		loc = z
	}
	r, ok, err := resolveRate(ctx, store.New(e.pool), currency, holidays.DayIn(day, loc))
	if err != nil || !ok {
		return contracts.ExchangeRate{}, ok, err
	}
	return contracts.ExchangeRate{Currency: currency, Day: r.Day, Rate: r.Rate.FloatString(ratesMaxDecimals), Source: r.Source}, true, nil
}
