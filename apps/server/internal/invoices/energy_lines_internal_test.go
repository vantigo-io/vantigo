package invoices

import (
	"math/big"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The settlements energyLines is fed: a month of a household in the north on
// spot with day/night grid energy, and its correction.
func oslo(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, marketZone) }

func septemberSettlement() contracts.Settlement {
	return contracts.Settlement{
		ID: 501, Revision: 3, Kind: contracts.SettlementRegular, SupplyPeriodID: 9, MeteringPointID: 4, CustomerID: 7,
		GSRN: "707057500012345678", Address: "Storgata 1, 9008 Tromsø", PriceArea: "NO4", Household: true,
		ContractName: "Spot Nord", PeriodStart: oslo(2026, 9, 1), PeriodEnd: oslo(2026, 10, 1), KWh: "1500.250",
		Currency: "NOK", Amount: "1187.07", State: contracts.SettlementDraft,
		Lines: []contracts.SettlementLine{
			// 1500.25 × 0.512345 = 768.6456… → 768.65, and the six-decimal price hits.
			{Kind: "spot_energy", Text: "Spot NO4", Quantity: "1500.250", Unit: "kWh", UnitPrice: "0.512345", Amount: "768.65", VATHint: contracts.VATHintExemptHouseholdNorth},
			// 1000.125 × 0.333333 = 333.37… → 333.37, but Energy's exact sum is 333.38: the price is searched.
			{Kind: "grid_energy", Text: "Energiledd", Band: "day", Quantity: "1000.125", Unit: "kWh", UnitPrice: "0.333333", Amount: "333.38", VATHint: contracts.VATHintExemptHouseholdNorth},
			{Kind: "grid_energy", Text: "Energiledd", Band: "night", Quantity: "0.000", Unit: "kWh", UnitPrice: "0.250000", Amount: "0.00", VATHint: contracts.VATHintExemptHouseholdNorth},
			{Kind: "monthly_fee", Text: "Fastledd", Quantity: "1.000", Unit: "month", UnitPrice: "85.040000", Amount: "85.04", VATHint: contracts.VATHintExemptHouseholdNorth},
		},
	}
}

func TestEnergyLines_ARegularMonth(t *testing.T) {
	codes := energyVatCodes{standard: 1, exempt: 10}
	lines, held, err := energyLines("nb", []contracts.Settlement{septemberSettlement()}, codes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || len(held) != 3 {
		t.Fatalf("lines = %d, held = %d; want 3 of each (the empty night line left out)", len(lines), len(held))
	}
	want := []struct{ text, unit, quantity, price, net string }{
		{"Spot NO4, 707057500012345678 Storgata 1, 9008 Tromsø, september 2026", "kWh", "1500.250", "0.51234500", "768.65"},
		{"Energiledd (dag), 707057500012345678 Storgata 1, 9008 Tromsø, september 2026", "kWh", "1000.125", "0.33333833", "333.38"},
		{"Fastledd, 707057500012345678 Storgata 1, 9008 Tromsø, september 2026", "mnd", "1.000", "85.04000000", "85.04"},
	}
	for i, w := range want {
		l := lines[i]
		if l.description != w.text || l.unit != w.unit || l.quantity.FloatString(3) != w.quantity ||
			l.unitPrice.FloatString(8) != w.price || l.amounts.net.FloatString(2) != w.net {
			t.Errorf("line %d = %q %q %s × %s = %s; want %q %q %s × %s = %s", i+1, l.description, l.unit,
				l.quantity.FloatString(3), l.unitPrice.FloatString(8), l.amounts.net.FloatString(2), w.text, w.unit, w.quantity, w.price, w.net)
		}
		if l.vatCodeID != 10 || l.correction || len(l.sources) != 1 || l.sources[0].id != 501 {
			t.Errorf("line %d: code %d, correction %v, sources %v; want code 10, no correction, settlement 501", i+1, l.vatCodeID, l.correction, l.sources)
		}
	}
	src := held[0]
	if len(src) != 1 || len(held[1]) != 0 || len(held[2]) != 0 {
		t.Fatalf("held = %v, want the settlement's one row on its first line", held)
	}
	h := src[0]
	if h.kind != contracts.WorkSourceSettlement || h.lineCount != 3 || h.linePosition != 1 || h.projectID != 0 ||
		*h.subkind != "regular" || h.quantity.FloatString(3) != "1500.250" || h.amount.FloatString(2) != "1187.07" ||
		!h.date.Equal(time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)) || h.point == nil ||
		!h.point.periodFrom.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) || h.point.gsrn != "707057500012345678" {
		t.Errorf("source = %+v (point %+v), want the settlement over three lines with its snapshot", h, h.point)
	}
}

func TestEnergyLines_ACorrectionOfEachSign(t *testing.T) {
	st := contracts.Settlement{
		ID: 777, Revision: 1, Kind: contracts.SettlementCorrection, CorrectsID: ptr(int64(501)),
		GSRN: "707057500012345678", Address: "Storgata 1, 9008 Tromsø", PeriodStart: oslo(2026, 9, 1), PeriodEnd: oslo(2026, 10, 1),
		KWh: "-12.500", Currency: "NOK", Amount: "-1.27",
		Lines: []contracts.SettlementLine{
			{Kind: "spot_energy", Text: "Spot NO4", Quantity: "-12.500", Unit: "kWh", UnitPrice: "0.512345", Amount: "-6.40", VATHint: contracts.VATHintStandard},
			// A rate changed alone: no quantity, an amount.
			{Kind: "markup", Text: "Påslag", Quantity: "0.000", Unit: "kWh", UnitPrice: "0.020000", Amount: "5.13", VATHint: contracts.VATHintStandard},
		},
	}
	for _, c := range []struct {
		language string
		texts    [2]string
	}{
		{"nb", [2]string{
			"Korreksjon Spot NO4, 707057500012345678 Storgata 1, 9008 Tromsø, september 2026: −12,5 kWh, tidligere fakturert 10023",
			"Korreksjon Påslag, 707057500012345678 Storgata 1, 9008 Tromsø, september 2026, tidligere fakturert 10023",
		}},
		{"en", [2]string{
			"Correction Spot NO4, 707057500012345678 Storgata 1, 9008 Tromsø, September 2026: −12.5 kWh, previously invoiced 10023",
			"Correction Påslag, 707057500012345678 Storgata 1, 9008 Tromsø, September 2026, previously invoiced 10023",
		}},
	} {
		lines, held, err := energyLines(c.language, []contracts.Settlement{st}, energyVatCodes{standard: 1, exempt: 10}, map[int64]int64{501: 10023})
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != 2 || held[0][0].lineCount != 2 || held[0][0].quantity.FloatString(3) != "-12.500" {
			t.Fatalf("%s: lines = %d, source = %+v; want two lines and the kWh difference", c.language, len(lines), held[0][0])
		}
		for i, want := range []struct{ quantity, price, net string }{{"-1.000", "6.40000000", "-6.40"}, {"1.000", "5.13000000", "5.13"}} {
			l := lines[i]
			if l.description != c.texts[i] || !l.correction || l.unit != unitWord(c.language, "pcs") || l.vatCodeID != 1 ||
				l.quantity.FloatString(3) != want.quantity || l.unitPrice.FloatString(8) != want.price || l.amounts.net.FloatString(2) != want.net {
				t.Errorf("%s line %d = %q %s × %s = %s (correction %v); want %q %s × %s = %s", c.language, i+1, l.description,
					l.quantity.FloatString(3), l.unitPrice.FloatString(8), l.amounts.net.FloatString(2), l.correction, c.texts[i], want.quantity, want.price, want.net)
			}
		}
	}
}

func TestEnergyLines_OrderAndTheZeroSettlement(t *testing.T) {
	later, other := septemberSettlement(), septemberSettlement()
	later.ID, later.PeriodStart, later.PeriodEnd = 502, oslo(2026, 10, 1), oslo(2026, 11, 1)
	other.ID, other.GSRN = 503, "707057500000000001"
	zero := septemberSettlement()
	zero.ID, zero.GSRN, zero.Amount, zero.KWh = 504, "707057500099999999", "0.00", "0.000"
	zero.Lines = []contracts.SettlementLine{{Kind: "spot_energy", Text: "Spot NO4", Quantity: "0.000", Unit: "kWh", UnitPrice: "0", Amount: "0.00", VATHint: contracts.VATHintStandard}}
	lines, held, err := energyLines("en", []contracts.Settlement{later, zero, septemberSettlement(), other}, energyVatCodes{standard: 1, exempt: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var order []int64
	for i := range held {
		for _, h := range held[i] {
			order = append(order, h.id)
			if h.linePosition != int32(i+1) {
				t.Errorf("settlement %d at position %d, on line %d", h.id, h.linePosition, i+1)
			}
		}
	}
	if want := []int64{503, 501, 502, 504}; len(order) != 4 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] || order[3] != want[3] {
		t.Errorf("order = %v, want %v (by point, then period)", order, want)
	}
	last := lines[len(lines)-1]
	if last.quantity.Cmp(big.NewRat(1, 1)) != 0 || last.unitPrice.Sign() != 0 || last.unit != "pcs" || held[len(held)-1][0].lineCount != 1 {
		t.Errorf("the zero settlement's line = %s × %s %q, want 1 pcs × 0", last.quantity.FloatString(3), last.unitPrice.FloatString(2), last.unit)
	}
}

func TestEnergyLines_TheCapacityMonthOnAQuarter(t *testing.T) {
	st := septemberSettlement()
	st.PeriodStart, st.PeriodEnd = oslo(2026, 7, 1), oslo(2026, 10, 1)
	aug := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	st.Lines = []contracts.SettlementLine{{Kind: "grid_capacity", Text: "Kapasitetsledd", Month: &aug, Quantity: "1.000", Unit: "month", UnitPrice: "415.000000", Amount: "415.00", VATHint: contracts.VATHintStandard}}
	lines, _, err := energyLines("nb", []contracts.Settlement{st}, energyVatCodes{standard: 1, exempt: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Kapasitetsledd (august 2026), 707057500012345678 Storgata 1, 9008 Tromsø, 1. juli–30. sep. 2026"; lines[0].description != want {
		t.Errorf("text = %q, want %q", lines[0].description, want)
	}
}

func TestThroughInstant(t *testing.T) {
	got := throughInstant(time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, marketZone); !got.Equal(want) {
		t.Errorf("through = %s, want %s", got, want)
	}
}
