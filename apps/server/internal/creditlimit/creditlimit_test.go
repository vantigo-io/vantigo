package creditlimit

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// A EUR invoice counts at its open NOK; EUR on an order at today's rate; a
// currency with no rate is listed, never added.
func TestExposure_CountsEveryCurrencyInNOK(t *testing.T) {
	receivables := &contracts.CustomerReceivableTotals{Currencies: []contracts.CurrencyReceivables{
		{Currency: "EUR", Outstanding: "100.00", OutstandingNOK: "1150.00"},
		{Currency: "NOK", Outstanding: "1200.50", OutstandingNOK: "1200.50"},
		{Currency: "SEK", Outstanding: "0.00", OutstandingNOK: "0.00"},
	}}
	backlog := []contracts.CurrencyAmount{{Currency: "NOK", Amount: "300.25"}, {Currency: "EUR", Amount: "50.00"}, {Currency: "DKK", Amount: "20.00"}}
	got, err := Exposure(receivables, backlog, Rates{"EUR": "11.5000000000"}, contracts.CurrencyAmount{Currency: "NOK", Amount: "99.25"})
	if err != nil {
		t.Fatal(err)
	}
	// 1150 + 1200.50 + 300.25 + 50 × 11.5 + 99.25 = 3325.00; DKK has no rate.
	want := Result{NOK: "3325.00", Uncounted: []contracts.CurrencyAmount{{Currency: "DKK", Amount: "20.00"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Exposure = %+v, want %+v", got, want)
	}
}

// A provider that answers no NOK figure is read in its currency, at its rate.
func TestExposure_WithoutOutstandingNOKTheRateCounts(t *testing.T) {
	receivables := &contracts.CustomerReceivableTotals{Currencies: []contracts.CurrencyReceivables{{Currency: "EUR", Outstanding: "10.00"}}}
	got, err := Exposure(receivables, nil, Rates{"EUR": "11"})
	if err != nil || got.NOK != "110.00" {
		t.Errorf("Exposure = %+v, %v; want 110.00", got, err)
	}
}

func TestExposure_NilReceivablesCountsTheBacklogOnly(t *testing.T) {
	got, err := Exposure(nil, []contracts.CurrencyAmount{{Currency: "NOK", Amount: "10.00"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.NOK != "10.00" || len(got.Uncounted) != 0 {
		t.Errorf("Exposure = %+v, want NOK 10.00 and nothing uncounted", got)
	}
}

func TestExposure_EmptyIsZero(t *testing.T) {
	got, err := Exposure(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.NOK != "0.00" || got.Uncounted == nil {
		t.Errorf("Exposure = %+v, want 0.00 and an empty list", got)
	}
}

func TestExposure_RefusesANonDecimal(t *testing.T) {
	if _, err := Exposure(nil, []contracts.CurrencyAmount{{Currency: "NOK", Amount: "ten"}}, nil); err == nil {
		t.Error("Exposure of a non-decimal = nil error, want one")
	}
}

type fakeRates map[string]string

func (f fakeRates) Rate(_ context.Context, currency string, _ time.Time) (contracts.ExchangeRate, bool, error) {
	r, ok := f[currency]
	return contracts.ExchangeRate{Currency: currency, Rate: r}, ok, nil
}

// RatesFor asks once per currency but NOK, and leaves out one with no rate
// within seven days.
func TestRatesFor(t *testing.T) {
	got, err := RatesFor(context.Background(), fakeRates{"EUR": "11.5"}, time.Now(), "NOK", "EUR", "EUR", "DKK")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Rates{"EUR": "11.5"}) {
		t.Errorf("RatesFor = %v, want EUR alone", got)
	}
	if none, err := RatesFor(context.Background(), nil, time.Now(), "EUR"); err != nil || len(none) != 0 {
		t.Errorf("RatesFor without rates = %v, %v; want none", none, err)
	}
}

func TestExceeds(t *testing.T) {
	limit := "1000.00"
	cases := []struct {
		name  string
		limit *string
		nok   string
		want  bool
	}{
		{"no limit", nil, "999999.00", false},
		{"below", &limit, "999.99", false},
		{"equal is not exceeding", &limit, "1000.00", false},
		{"above", &limit, "1000.01", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Exceeds(c.limit, Result{NOK: c.nok})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("Exceeds = %v, want %v", got, c.want)
			}
		})
	}
}
