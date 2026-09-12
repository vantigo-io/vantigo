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

// Ported from TS/Domain/SupplyPeriodTests.cs.
func TestSupplyPeriod_Overlaps(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	t.Run("adjacent periods do not overlap", func(t *testing.T) {
		t.Parallel()
		dayLater := start.AddDate(0, 0, 1)
		if supplyPeriodsOverlap(start, &dayLater, dayLater, nil) {
			t.Error("adjacent periods reported as overlapping, want not overlapping")
		}
	})

	t.Run("open-ended period overlaps a later period", func(t *testing.T) {
		t.Parallel()
		dayLater := start.AddDate(0, 0, 1)
		if !supplyPeriodsOverlap(start, nil, dayLater, nil) {
			t.Error("open-ended period reported as not overlapping a later period, want overlapping")
		}
	})
}
