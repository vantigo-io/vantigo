package energy

import (
	"testing"
	"time"
)

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
