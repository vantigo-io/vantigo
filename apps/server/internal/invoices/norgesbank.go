package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file is Norges Bank's exchange rates client (invoices phase 6 design
// I18; plan C2 and its notes on the API as called on 2026-11-18). One GET
// asks for every currency at once:
//
//	{INVOICES_RATES_URL}/EXR/B.EUR+SEK.NOK.SP?format=sdmx-json&startPeriod=…&endPeriod=…&locale=en
//
// and the SDMX-JSON answer is read by its own structure, never by the order
// of the request: a series' first key index points into
// structure.dimensions.series' BASE_CUR values, which Norges Bank does not
// answer in request order (trap 1), and UNIT_MULT's attribute index points
// into its values, whose id is the power of ten the rate is quoted per (JPY,
// SEK and DKK per 100; trap 2). detail=dataonly strips the attributes, so it
// is never sent. A range with no observations answers 404 with an errors
// body: no rates, not a failure.

const (
	// maxRatesBody caps an answer: 30 days of twenty currencies is far below.
	maxRatesBody = 1 << 20
	// ratesFetchBudget is one request's whole time.
	ratesFetchBudget              = 20 * time.Second
	ratesDialTimeout              = 10 * time.Second
	ratesTLSHandshakeTimeout      = 10 * time.Second
	ratesResponseHeaderTimeout    = 15 * time.Second
	ratesMaxResponseHeaderSize    = 64 << 10
	ratesFailureDisallowed        = "disallowed_address"
	ratesFailureUnreachable       = "unreachable"
	ratesFailureUnreadable        = "unreadable"
	ratesFailureTooLarge          = "too_large"
	ratesFailureRedirect          = "answered_redirect"
	ratesFailureAnsweredPrefix    = "answered_"
	ratesFailureNotConfigured     = "not_configured"
	ratesMaxDecimals              = 10
	norgesBankDefaultBaseCurrency = "NOK"
)

// nbCurrencies are the currencies Norges Bank publishes a daily rate of
// against NOK, sorted; settings refuse any other code. Norges Bank's own
// indices (I44, TWI) and the IMF's SDR (XDR) are no currency an invoice is
// paid in, and are left out. A test checks the list against a recorded
// answer of every series (testdata/norgesbank/all.json).
var nbCurrencies = []string{
	"AUD", "BDT", "BRL", "BYN", "CAD", "CHF", "CNY", "CZK", "DKK", "EUR", "GBP", "HKD", "HUF", "IDR", "ILS",
	"INR", "ISK", "JPY", "KRW", "MMK", "MXN", "MYR", "NZD", "PHP", "PKR", "PLN", "RON", "SEK", "SGD", "THB",
	"TRY", "TWD", "USD", "VND", "ZAR",
}

// isNBCurrency reports whether Norges Bank publishes code.
func isNBCurrency(code string) bool {
	_, ok := slices.BinarySearch(nbCurrencies, code)
	return ok
}

// dayRate is one currency's rate of one business day, NOK per one unit, at
// ten decimals.
type dayRate struct {
	Day  time.Time
	Rate *big.Rat
}

// ratesError is a fetch that answered nothing usable: Outcome is one of the
// ratesFailure* codes, answered_<status> for a status, and names no body.
type ratesError struct{ Outcome string }

func (e *ratesError) Error() string { return "invoices: norges bank: " + e.Outcome }

// ratesTransport is the production transport, built once: netguard dials,
// so the host is resolved, checked against the forbidden addresses and then
// dialled by the address checked — Energy's spot-price feed's shape. No
// proxy, no keep-alive: a few requests a day.
var ratesTransport = sync.OnceValue(func() *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		DialContext:            netguard.DialContext(net.DefaultResolver, (&net.Dialer{Timeout: ratesDialTimeout}).DialContext),
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    ratesTLSHandshakeTimeout,
		ResponseHeaderTimeout:  ratesResponseHeaderTimeout,
		MaxResponseHeaderBytes: ratesMaxResponseHeaderSize,
	}
})

// ratesClient is the client a fetch goes through: rt when a test set one
// (Deps.HTTPTransport), the guarded transport otherwise. Redirects are never
// followed: the next address is one the guard never saw.
func ratesClient(rt http.RoundTripper) *http.Client {
	if rt == nil {
		rt = ratesTransport()
	}
	return &http.Client{
		Transport:     rt,
		Timeout:       ratesFetchBudget,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ratesURL is the request for currencies from from to to, civil dates.
func ratesURL(base string, currencies []string, from, to time.Time) string {
	return fmt.Sprintf("%s/EXR/B.%s.%s.SP?format=sdmx-json&startPeriod=%s&endPeriod=%s&locale=en",
		strings.TrimSuffix(base, "/"), strings.Join(currencies, "+"), norgesBankDefaultBaseCurrency,
		from.Format(time.DateOnly), to.Format(time.DateOnly))
}

// fetchRates asks Norges Bank for currencies' rates from from to to, both
// civil dates included, and answers them per currency, oldest first. A 404
// with Norges Bank's no-data body is no rates and no error.
func fetchRates(ctx context.Context, client *http.Client, base string, currencies []string, from, to time.Time) (map[string][]dayRate, error) {
	if base == "" {
		return nil, &ratesError{Outcome: ratesFailureNotConfigured}
	}
	if len(currencies) == 0 {
		return map[string][]dayRate{}, nil
	}
	noteContractCall(ctx, "NorgesBank.Rates")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ratesURL(base, currencies, from, to), nil)
	if err != nil {
		return nil, &ratesError{Outcome: ratesFailureUnreachable}
	}
	req.Header.Set("User-Agent", "Vantigo-Invoices")
	req.Header.Set("Accept", "application/vnd.sdmx.data+json, application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, &ratesError{Outcome: ratesTransportFailure(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRatesBody+1))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		if readErr == nil && isNoData(raw) {
			return map[string][]dayRate{}, nil
		}
		return nil, &ratesError{Outcome: ratesFailureAnsweredPrefix + "404"}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, &ratesError{Outcome: ratesFailureRedirect}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, &ratesError{Outcome: ratesFailureAnsweredPrefix + strconv.Itoa(resp.StatusCode)}
	case readErr != nil:
		return nil, &ratesError{Outcome: ratesTransportFailure(readErr)}
	case len(raw) > maxRatesBody:
		return nil, &ratesError{Outcome: ratesFailureTooLarge}
	}
	out, err := parseRates(raw)
	if err != nil {
		return nil, &ratesError{Outcome: ratesFailureUnreadable}
	}
	return out, nil
}

// ratesTransportFailure is the outcome of a request that answered nothing.
func ratesTransportFailure(err error) string {
	if errors.Is(err, netguard.ErrDisallowed) {
		return ratesFailureDisallowed
	}
	return ratesFailureUnreachable
}

// isNoData reports whether a 404's body is Norges Bank's no-data answer,
// {"errors":[{"code":404,…}]}.
func isNoData(raw []byte) bool {
	var body struct {
		Errors []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	return json.Unmarshal(raw, &body) == nil && len(body.Errors) > 0 && body.Errors[0].Code == http.StatusNotFound
}

// sdmxValue is one value of a dimension or an attribute.
type sdmxValue struct {
	ID string `json:"id"`
}

// sdmxComponent is a dimension or an attribute, its id and its values.
type sdmxComponent struct {
	ID     string      `json:"id"`
	Values []sdmxValue `json:"values"`
}

// sdmxData is the part of the answer the parser reads.
type sdmxData struct {
	Data struct {
		DataSets []struct {
			Series map[string]struct {
				Attributes   []*int                        `json:"attributes"`
				Observations map[string][]*json.RawMessage `json:"observations"`
			} `json:"series"`
		} `json:"dataSets"`
		Structure struct {
			Dimensions struct {
				Series      []sdmxComponent `json:"series"`
				Observation []sdmxComponent `json:"observation"`
			} `json:"dimensions"`
			Attributes struct {
				Series []sdmxComponent `json:"series"`
			} `json:"attributes"`
		} `json:"structure"`
	} `json:"data"`
}

// parseRates reads an SDMX-JSON answer into rates per currency, each NOK per
// one unit at six decimals, oldest first.
func parseRates(raw []byte) (map[string][]dayRate, error) {
	var doc sdmxData
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Data.DataSets) == 0 {
		return map[string][]dayRate{}, nil
	}
	st := doc.Data.Structure
	baseDim := -1
	for i, d := range st.Dimensions.Series {
		if d.ID == "BASE_CUR" {
			baseDim = i
		}
	}
	if baseDim < 0 || len(st.Dimensions.Observation) == 0 {
		return nil, errors.New("no BASE_CUR or no observation dimension")
	}
	unitMult := -1
	for i, a := range st.Attributes.Series {
		if a.ID == "UNIT_MULT" {
			unitMult = i
		}
	}
	if unitMult < 0 {
		return nil, errors.New("no UNIT_MULT attribute")
	}
	days := st.Dimensions.Observation[0].Values
	out := map[string][]dayRate{}
	for key, series := range doc.Data.DataSets[0].Series {
		parts := strings.Split(key, ":")
		if len(parts) != len(st.Dimensions.Series) {
			return nil, fmt.Errorf("series key %q", key)
		}
		idx, err := strconv.Atoi(parts[baseDim])
		if err != nil || idx < 0 || idx >= len(st.Dimensions.Series[baseDim].Values) {
			return nil, fmt.Errorf("series key %q", key)
		}
		currency := st.Dimensions.Series[baseDim].Values[idx].ID
		if unitMult >= len(series.Attributes) || series.Attributes[unitMult] == nil {
			return nil, fmt.Errorf("series %s has no UNIT_MULT", currency)
		}
		mi := *series.Attributes[unitMult]
		if mi < 0 || mi >= len(st.Attributes.Series[unitMult].Values) {
			return nil, fmt.Errorf("series %s UNIT_MULT index %d", currency, mi)
		}
		power, err := strconv.Atoi(st.Attributes.Series[unitMult].Values[mi].ID)
		if err != nil || power < 0 || power > 6 {
			return nil, fmt.Errorf("series %s UNIT_MULT", currency)
		}
		divisor := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(power)), nil))
		rates := make([]dayRate, 0, len(series.Observations))
		for oi, obs := range series.Observations {
			i, err := strconv.Atoi(oi)
			if err != nil || i < 0 || i >= len(days) || len(obs) == 0 || obs[0] == nil {
				return nil, fmt.Errorf("series %s observation %q", currency, oi)
			}
			day, err := time.Parse(time.DateOnly, days[i].ID)
			if err != nil {
				return nil, fmt.Errorf("series %s day %q", currency, days[i].ID)
			}
			var text string
			if err := json.Unmarshal(*obs[0], &text); err != nil {
				return nil, fmt.Errorf("series %s value", currency)
			}
			v, ok := new(big.Rat).SetString(text)
			if !ok || v.Sign() <= 0 {
				return nil, fmt.Errorf("series %s value %q", currency, text)
			}
			rate := roundRat(new(big.Rat).Quo(v, divisor), ratesMaxDecimals)
			if rate.Sign() <= 0 {
				return nil, fmt.Errorf("series %s rate rounds to 0", currency)
			}
			rates = append(rates, dayRate{Day: day, Rate: rate})
		}
		slices.SortFunc(rates, func(a, b dayRate) int { return a.Day.Compare(b.Day) })
		out[currency] = rates
	}
	return out, nil
}

// roundRat is v at places decimals, the half away from zero (FloatString's
// rule, the codebase's).
func roundRat(v *big.Rat, places int) *big.Rat {
	r, _ := new(big.Rat).SetString(v.FloatString(places))
	return r
}
