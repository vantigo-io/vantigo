package expenses

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// day is a calendar day as the module holds one: UTC midnight.
func day(t *testing.T, text string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return d
}

// The seeds live in two places — the migration writes them, seededRates is what
// POST /rates/reset puts back — and the two cannot be allowed to drift. This
// holds the Go table against the rows the migration actually inserted.
func TestSeededRates_AreExactlyWhatTheMigrationInserted(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()

	rows, err := store.New(pool).ListRates(ctx)
	if err != nil {
		t.Fatalf("list rates: %v", err)
	}
	if len(rows) != len(seededRates) {
		t.Fatalf("the migration inserted %d rates, seededRates holds %d", len(rows), len(seededRates))
	}
	for _, seed := range seededRates {
		var found *store.ExpensesRate
		for i := range rows {
			if rows[i].Kind == seed.Kind && rows[i].ValidFrom.Time.Format(time.DateOnly) == seed.ValidFrom {
				found = &rows[i]
				break
			}
		}
		if found == nil {
			t.Errorf("the migration has no %s row for %s, which seededRates says it seeds", seed.Kind, seed.ValidFrom)
			continue
		}
		value, err := ratFromNumeric(found.Value)
		if err != nil {
			t.Fatalf("read %s value: %v", seed.Kind, err)
		}
		if got := value.FloatString(2); got != seed.Value {
			t.Errorf("%s value = %s, want %s", seed.Kind, got, seed.Value)
		}
		switch {
		case seed.Currency == "" && found.Currency != nil:
			// A percentage of a day's rate carries no currency of its own, and
			// an empty string in the column would be a third answer beside
			// "NOK" and "none".
			t.Errorf("%s currency = %q, want none", seed.Kind, *found.Currency)
		case seed.Currency != "" && (found.Currency == nil || *found.Currency != seed.Currency):
			t.Errorf("%s currency = %v, want %q", seed.Kind, found.Currency, seed.Currency)
		}
		if found.Source == nil || *found.Source != seed.Source {
			t.Errorf("%s source = %v, want %q", seed.Kind, found.Source, seed.Source)
		}
	}
}

// rateFor is what every priced line will ask: the row with the greatest
// valid_from on or before the day, and a typed "no rate" when the table has
// none — which the entry code turns into a field error rather than a 500.
func TestRateFor_IsTheLatestRowOnOrBeforeTheDay(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	q := store.New(pool)
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)

	for _, r := range []struct {
		validFrom, value string
	}{{"2026-04-01", "5.50"}, {"2026-08-01", "6.00"}} {
		value, err := numericFromText(r.value)
		if err != nil {
			t.Fatalf("numeric %s: %v", r.value, err)
		}
		if _, err := q.InsertRate(ctx, store.InsertRateParams{
			Kind: rateKindMileage, ValidFrom: pgDate(day(t, r.validFrom)), Value: value,
			Currency: ptrTo("NOK"), Now: now,
		}); err != nil {
			t.Fatalf("insert the %s rate: %v", r.validFrom, err)
		}
	}

	for _, tc := range []struct {
		date, want, validFrom string
	}{
		{"2026-01-01", "5.30", "2026-01-01"}, // the seeded row
		{"2026-03-31", "5.30", "2026-01-01"},
		{"2026-04-01", "5.50", "2026-04-01"}, // the day a row starts is its own
		{"2026-07-31", "5.50", "2026-04-01"},
		{"2026-08-01", "6.00", "2026-08-01"},
		{"2030-01-01", "6.00", "2026-08-01"}, // the latest row goes on forever
	} {
		got, err := rateFor(ctx, q, rateKindMileage, day(t, tc.date))
		if err != nil {
			t.Fatalf("rateFor(%s): %v", tc.date, err)
		}
		if value := got.Value.FloatString(2); value != tc.want {
			t.Errorf("rateFor(%s) = %s, want %s", tc.date, value, tc.want)
		}
		if from := got.ValidFrom.Format(time.DateOnly); from != tc.validFrom {
			t.Errorf("rateFor(%s) came from %s, want the row of %s", tc.date, from, tc.validFrom)
		}
		if got.Currency == nil || *got.Currency != "NOK" {
			t.Errorf("rateFor(%s) currency = %v, want NOK", tc.date, got.Currency)
		}
	}

	// Before the first row, and for a kind nothing ships or has ever been
	// entered for, there is no rate — and that is a value the caller can act
	// on, not a failure.
	if _, err := rateFor(ctx, q, rateKindMileage, day(t, "2025-12-31")); !errors.Is(err, errNoRate) {
		t.Errorf("rateFor before the first row = %v, want errNoRate", err)
	}
	if _, err := rateFor(ctx, q, rateKindMileageCustomer, day(t, "2026-09-12")); !errors.Is(err, errNoRate) {
		t.Errorf("rateFor for an unseeded kind = %v, want errNoRate", err)
	}
}
