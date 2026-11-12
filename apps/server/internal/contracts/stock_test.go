package contracts_test

import (
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// day is a date at midnight UTC.
func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

// TestProposeLots is the FEFO proposal (manufacturing phase 2 M65): expiry
// first with none last, FIFO by received when nothing expires, lots on hold
// and expired lots skipped, a shortage answered, and a serial never split.
func TestProposeLots(t *testing.T) {
	today := *day(2026, 9, 12)
	jan, feb, mar := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	lot := func(id int64, expiry *time.Time, received time.Time, q, status string) contracts.StockLot {
		return contracts.StockLot{LotID: id, LocationID: 10, Expiry: expiry, ReceivedAt: received, Quantity: q, Status: status}
	}
	cases := []struct {
		name     string
		rows     []contracts.StockLot
		quantity string
		picks    []contracts.LotPick
		short    string
	}{
		{"FEFO, none last", []contracts.StockLot{
			lot(1, nil, jan, "5", contracts.LotAvailable),
			lot(2, day(2026, 12, 1), feb, "5", contracts.LotAvailable),
			lot(3, day(2026, 10, 1), mar, "5", contracts.LotAvailable),
		}, "12", []contracts.LotPick{{3, 10, "5"}, {2, 10, "5"}, {1, 10, "2"}}, ""},
		{"FIFO without expiry", []contracts.StockLot{
			lot(1, nil, mar, "5", contracts.LotAvailable),
			lot(2, nil, jan, "5", contracts.LotAvailable),
		}, "6", []contracts.LotPick{{2, 10, "5"}, {1, 10, "1"}}, ""},
		{"held and expired skipped", []contracts.StockLot{
			lot(1, day(2026, 9, 11), jan, "5", contracts.LotAvailable),
			lot(2, day(2026, 9, 20), jan, "5", contracts.LotQuarantine),
			lot(3, day(2026, 9, 21), jan, "5", contracts.LotBlocked),
			lot(4, day(2026, 9, 12), jan, "1.5", contracts.LotAvailable),
		}, "2", []contracts.LotPick{{4, 10, "1.5"}}, "0.5"},
		{"nothing usable", nil, "1", nil, "1"},
		{"a serial is not split", []contracts.StockLot{
			{LotID: 1, LocationID: 10, ReceivedAt: jan, Quantity: "1", Status: contracts.LotAvailable, Serial: true},
			{LotID: 2, LocationID: 10, ReceivedAt: feb, Quantity: "1", Status: contracts.LotAvailable, Serial: true},
		}, "1.5", []contracts.LotPick{{1, 10, "1"}}, "0.5"},
	}
	for _, tc := range cases {
		picks, short, err := contracts.ProposeLots(tc.rows, tc.quantity, today)
		if err != nil || short != tc.short || !slices.Equal(picks, tc.picks) {
			t.Errorf("%s: %+v short %q, %v; want %+v short %q", tc.name, picks, short, err, tc.picks, tc.short)
		}
	}
	if _, _, err := contracts.ProposeLots(nil, "0", today); err == nil {
		t.Error("a proposal for 0 is no failure")
	}
}

// TestLotExpired is the expiry rule (M62): expired the day after its expiry,
// never without one.
func TestLotExpired(t *testing.T) {
	today := *day(2026, 9, 12)
	if contracts.LotExpired(nil, today) || contracts.LotExpired(day(2026, 9, 12), today) || !contracts.LotExpired(day(2026, 9, 11), today) {
		t.Error("the expiry rule")
	}
	// A today carrying another zone is the day it names, not its UTC day:
	// 00:30 on 13 September at +02:00 is 22:30 on the 12th in UTC.
	oslo := time.FixedZone("CEST", 2*60*60)
	if !contracts.LotExpired(day(2026, 9, 12), time.Date(2026, 9, 13, 0, 30, 0, 0, oslo)) {
		t.Error("a today in another zone is judged on its UTC day")
	}
}
