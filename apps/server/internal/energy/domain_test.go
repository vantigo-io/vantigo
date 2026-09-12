package energy

import (
	"math"
	"testing"
	"time"
)

// TestNumericFromFloat_RejectsUnstorableValues pins that
// pgtype.Numeric.Scan's error is propagated rather than discarded
// (values.go). Discarding it left the zero pgtype.Numeric — an invalid
// value, which writes as SQL NULL — so an infinity would have become a
// silent NULL in numeric(14,3) instead of failing. No request reaches this
// today, because every value arrives through encoding/json, which refuses to
// decode either an infinity or a NaN; that is exactly why only a direct test
// can hold the behaviour. Mirrors products' own copy in pricing_test.go,
// including its finding that NaN is *not* unstorable: Postgres numeric has
// its own NaN, so only the two infinities fail to scan.
func TestNumericFromFloat_RejectsUnstorableValues(t *testing.T) {
	t.Parallel()
	for _, v := range []float64{math.Inf(1), math.Inf(-1)} {
		if _, err := numericFromFloat(v); err == nil {
			t.Errorf("numericFromFloat(%v) returned no error, want one rather than a silent SQL NULL", v)
		}
	}

	nan, err := numericFromFloat(math.NaN())
	if err != nil {
		t.Fatalf("numericFromFloat(NaN): %v, want no error (Postgres numeric has its own NaN)", err)
	}
	if !nan.Valid || !nan.NaN {
		t.Errorf("numericFromFloat(NaN) = %+v, want a Valid NaN rather than the invalid SQL NULL zero value", nan)
	}

	n, err := numericFromFloat(1.5)
	if err != nil {
		t.Fatalf("numericFromFloat(1.5): %v, want no error", err)
	}
	if !n.Valid {
		t.Error("numericFromFloat(1.5) is not Valid, want a storable value")
	}

	// nil is the column's own NULL for an optional field, never an error.
	null, err := numericFromFloatPtr(nil)
	if err != nil {
		t.Fatalf("numericFromFloatPtr(nil): %v, want no error", err)
	}
	if null.Valid {
		t.Error("numericFromFloatPtr(nil) is Valid, want the invalid (SQL NULL) zero value")
	}
}

// Ported from TS/Domain/GsrnTests.cs.
func TestGsrn_IsValid(t *testing.T) {
	t.Parallel()
	valid := []string{"707057500000000001", "123456789012345678"}
	for _, v := range valid {
		if !gsrnValid(v) {
			t.Errorf("gsrnValid(%q) = false, want true", v)
		}
	}
	invalid := []string{"", "123", "70705750000000000A"}
	for _, v := range invalid {
		if gsrnValid(v) {
			t.Errorf("gsrnValid(%q) = true, want false", v)
		}
	}
}

// Ported from TS/Domain/PriceAreaTests.cs.
func TestPriceArea_IsValid(t *testing.T) {
	t.Parallel()
	valid := []string{"NO1", "SE4", "DK2"}
	for _, v := range valid {
		if !priceAreaValid(v) {
			t.Errorf("priceAreaValid(%q) = false, want true", v)
		}
	}
	invalid := []string{"N1", "no1", "NO123"}
	for _, v := range invalid {
		if priceAreaValid(v) {
			t.Errorf("priceAreaValid(%q) = true, want false", v)
		}
	}
}

// Ported from TS/Domain/MeterTests.cs.
func TestMeter_ValidateMeterNumber(t *testing.T) {
	t.Parallel()
	blank := ""
	spaces := "  "
	for _, v := range []*string{nil, &blank, &spaces} {
		if got := validateMeterNumber(v); got != "Meter number is required." {
			t.Errorf("validateMeterNumber(%v) = %q, want \"Meter number is required.\"", v, got)
		}
	}

	tooLong := ""
	for i := 0; i < 65; i++ {
		tooLong += "x"
	}
	if got := validateMeterNumber(&tooLong); got != "Meter number cannot be longer than 64 characters." {
		t.Errorf("validateMeterNumber(65 chars) = %q, want the length message", got)
	}

	trimmed := " MTR-1 "
	if got := validateMeterNumber(&trimmed); got != "" {
		t.Errorf("validateMeterNumber(%q) = %q, want no error (trimming is allowed)", trimmed, got)
	}
}

// TS/Domain/SupplyPeriodTests.cs (Adjacent_periods_do_not_overlap,
// Open_ended_period_overlaps_later_period) pinned a pure Go
// supplyPeriodsOverlap predicate this port originally carried alongside the
// SQL that actually decides overlap (SupplyPeriodOverlapExists, the GiST
// exclusion constraint). Fix-round finding: that predicate was unreachable
// from any handler — a second, never-executed implementation of the same
// rule that could silently drift from the SQL one decides — so it was
// deleted rather than kept only to satisfy this port; the SQL predicate is
// exercised end-to-end by supplyperiods_test.go's overlap/conflict tests
// instead (TestCreateSupplyPeriod_OverlapConflictThenEndAllowsHandover,
// TestSwitchSupplyPeriod_RejectsOverlapWithHistoricalPeriod, the gated
// TestConcurrentOverlappingSupplyPeriodCreates_YieldOneSuccessAndConflicts).

// TestValidateSupplyPeriodEnd_DoesNotOffsetCheckStart is the unit-level pin
// for the fix-round's critical finding: validateSupplyPeriodEnd's start
// parameter is always a value read back from the database (pgx decodes
// timestamptz in the process's local time zone, hasUTCOffset's comment),
// never one the request supplied, so it must never be offset-checked — only
// end may be. This feeds a non-UTC-offset start (the shape a decoded row
// takes under a non-UTC process TZ) and a UTC end, and asserts no error: a
// mutation that offset-checked start again would fail this immediately,
// with no dependency on the test process's own TZ (see
// TestEndSupplyPeriod_NotAffectedByProcessTimeZone in supplyperiods_test.go
// for the package-level, TZ-mutating pin of the same bug).
func TestValidateSupplyPeriodEnd_DoesNotOffsetCheckStart(t *testing.T) {
	t.Parallel()
	oslo := time.FixedZone("Europe/Oslo (fixed)", 2*60*60) // +02:00, a plausible decoded-row offset
	nonUTCStart := time.Date(2026, 9, 12, 14, 0, 0, 0, oslo)
	utcEnd := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)

	if got := validateSupplyPeriodEnd(nonUTCStart, utcEnd); got != "" {
		t.Errorf("validateSupplyPeriodEnd(non-UTC start, UTC end) = %q, want no error (start must never be offset-checked)", got)
	}

	// end is still request-supplied and must still be offset-checked.
	nonUTCEnd := time.Date(2026, 9, 13, 0, 0, 0, 0, oslo)
	utcStart := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	if got := validateSupplyPeriodEnd(utcStart, nonUTCEnd); got != "Start and end must be UTC timestamps." {
		t.Errorf("validateSupplyPeriodEnd(UTC start, non-UTC end) = %q, want the UTC message (end must still be offset-checked)", got)
	}
}

// Ported from TS/Domain/MarketTimeZoneTests.cs. The unknown ("XX1") and nil
// facts are the whole point of this test (§4.1, this task's dispatch
// correction 1): Oslo is a default, not a match on "NO" — a lookup table
// with an explicit NO->Oslo entry would still pass every other fact here
// and only fail on these two.
func TestMarketTimeZone_GetId(t *testing.T) {
	t.Parallel()
	no1, se4, dk2, fi1, xx1 := "NO1", "SE4", "DK2", "FI1", "XX1"
	cases := []struct {
		priceArea *string
		want      string
	}{
		{&no1, "Europe/Oslo"},
		{&se4, "Europe/Stockholm"},
		{&dk2, "Europe/Copenhagen"},
		{&fi1, "Europe/Helsinki"},
		{&xx1, "Europe/Oslo"},
		{nil, "Europe/Oslo"},
	}
	for _, c := range cases {
		if got := marketTimeZone(c.priceArea); got != c.want {
			area := "nil"
			if c.priceArea != nil {
				area = *c.priceArea
			}
			t.Errorf("marketTimeZone(%q) = %q, want %q", area, got, c.want)
		}
	}
}

// Ported from TS/Domain/ConsumptionIntervalTests.cs.
func TestConsumptionInterval_Validate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	t.Run("rejects end before start", func(t *testing.T) {
		t.Parallel()
		if got := validateConsumptionInterval(now, now.Add(-time.Hour), 1); got != "End must be later than start." {
			t.Errorf("validateConsumptionInterval = %q, want the end-before-start message", got)
		}
	})

	t.Run("rejects negative quantity", func(t *testing.T) {
		t.Parallel()
		if got := validateConsumptionInterval(now, now.Add(time.Hour), -1); got != "Quantity must be zero or greater." {
			t.Errorf("validateConsumptionInterval = %q, want the negative-quantity message", got)
		}
	})
}

// TestClampInt32 pins the fix-round item bounding stats.go's bigint-to-int32
// count narrowing: values inside int32's range pass through unchanged,
// values outside it saturate rather than wrap. An unguarded int32(n)
// conversion would instead wrap math.MaxInt32+1 into math.MinInt32 — a
// dashboard count silently going negative with no error.
func TestClampInt32(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want int32
	}{
		{0, 0},
		{5, 5},
		{-5, -5},
		{math.MaxInt32, math.MaxInt32},
		{math.MaxInt32 + 1, math.MaxInt32},
		{math.MinInt32, math.MinInt32},
		{math.MinInt32 - 1, math.MinInt32},
	}
	for _, c := range cases {
		if got := clampInt32(c.in); got != c.want {
			t.Errorf("clampInt32(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
