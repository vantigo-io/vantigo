package energy

import (
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is a settlement in memory (energy consumption billing design D5,
// energy settlements plan readings 6, 7, 9): what the computation answers,
// what a stored row reads back as, how two are compared and how one crosses
// the contract. Money and quantities are exact (math/big.Rat) at their
// places: kWh and quantities three decimals, unit prices six, amounts two.

// The places each figure is kept at, as the columns hold them.
const (
	kwhPlaces       = 3
	quantityPlaces  = 3
	unitPricePlaces = 6
	amountPlaces    = 2
)

// settlementCurrency is every settlement's currency: prices are NOK.
const settlementCurrency = "NOK"

// settlement is one settlement in memory: computed, or read from its row.
type settlement struct {
	// ID is the stored row's, 0 for one computed and not written.
	ID         int64
	Revision   int32
	Kind       string // contracts.SettlementRegular or SettlementCorrection
	CorrectsID *int64

	SupplyPeriodID, MeteringPointID, CustomerID int32
	GSRN, Address, PriceArea                    string
	Household                                   bool
	ContractName                                string

	PeriodStart, PeriodEnd time.Time

	KWh       *big.Rat // three decimals; a correction's is the difference
	Estimated bool
	// IntervalCount is the intervals present; ExpectedIntervalCount is
	// IntervalCount plus the missing ones (plan reading 3).
	IntervalCount, ExpectedIntervalCount int

	Blocked, BlockedDetail string

	Amount   *big.Rat // two decimals, Σ Lines' amounts
	Currency string
	Lines    []settlementLine

	State      string // contracts.SettlementDraft, Invoiced or Released
	ComputedAt time.Time
	Stamp      *contracts.InvoiceStampRef
}

// settlementLine is one priced line.
type settlementLine struct {
	Kind, Text string
	Band       string     // contracts.SettlementBandDay, Night or ""
	Month      *time.Time // a grid_capacity line's month (its first day, UTC midnight)
	Quantity   *big.Rat   // three decimals
	Unit       string
	UnitPrice  *big.Rat // six decimals
	Amount     *big.Rat // two decimals
	VATHint    string
}

// missingIntervals is how many intervals the period lacks; 0 is complete.
func (s settlement) missingIntervals() int {
	if s.ExpectedIntervalCount > s.IntervalCount {
		return s.ExpectedIntervalCount - s.IntervalCount
	}
	return 0
}

// roundRat is v at places decimals, the half rounded away from zero
// (big.Rat's FloatString rule), as a numeric column of that scale stores it.
func roundRat(v *big.Rat, places int) *big.Rat {
	r, ok := new(big.Rat).SetString(v.FloatString(places))
	if !ok {
		panic(fmt.Sprintf("energy: %s is not a decimal", v.FloatString(places)))
	}
	return r
}

// zeroRat is a fresh zero.
func zeroRat() *big.Rat { return new(big.Rat) }

// ratEqual is whether a and b are the same value; nil is zero.
func ratEqual(a, b *big.Rat) bool {
	if a == nil {
		a = zeroRat()
	}
	if b == nil {
		b = zeroRat()
	}
	return a.Cmp(b) == 0
}

// ratFromNumeric reads a numeric column as the exact decimal it holds — Int ×
// 10^Exp, never through a float.
func ratFromNumeric(n pgtype.Numeric) (*big.Rat, error) {
	if !n.Valid || n.Int == nil {
		return nil, fmt.Errorf("energy: read a stored decimal: the column holds no number")
	}
	if n.NaN || n.InfinityModifier != pgtype.Finite {
		return nil, fmt.Errorf("energy: read a stored decimal: the column holds %v", n.InfinityModifier)
	}
	value := new(big.Rat).SetInt(n.Int)
	exp := n.Exp
	if exp < 0 {
		exp = -exp
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exp)), nil))
	if n.Exp < 0 {
		return value.Quo(value, scale), nil
	}
	return value.Mul(value, scale), nil
}

// numericFromRat stores v at places decimals.
func numericFromRat(v *big.Rat, places int) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if v == nil {
		v = zeroRat()
	}
	if err := n.Scan(v.FloatString(places)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("energy: %s is not a storable decimal: %w", v.FloatString(places), err)
	}
	return n, nil
}

// floatFromRat is v onto the wire as a JSON number at places decimals: the
// wire carries doubles, the arithmetic never did.
func floatFromRat(v *big.Rat, places int) float64 {
	if v == nil {
		return 0
	}
	f, _ := roundRat(v, places).Float64()
	return f
}

// sameFigures is whether two settlements of one live key say the same thing:
// every figure a revision stands for (plan reading 7). Ids, revisions, states,
// stamps and the computation's time are not figures.
func sameFigures(a, b settlement) bool {
	if a.Kind != b.Kind || !a.PeriodStart.Equal(b.PeriodStart) || !a.PeriodEnd.Equal(b.PeriodEnd) ||
		a.CustomerID != b.CustomerID || !sameInt64Ptr(a.CorrectsID, b.CorrectsID) ||
		!ratEqual(a.KWh, b.KWh) || a.Estimated != b.Estimated ||
		a.IntervalCount != b.IntervalCount || a.ExpectedIntervalCount != b.ExpectedIntervalCount ||
		a.Blocked != b.Blocked || a.BlockedDetail != b.BlockedDetail ||
		!ratEqual(a.Amount, b.Amount) || a.Currency != b.Currency || a.ContractName != b.ContractName {
		return false
	}
	return slices.EqualFunc(a.Lines, b.Lines, sameLine)
}

// sameLine is whether two lines are the same line with the same figures.
func sameLine(a, b settlementLine) bool {
	return a.Kind == b.Kind && a.Text == b.Text && a.Band == b.Band && sameTimePtr(a.Month, b.Month) &&
		ratEqual(a.Quantity, b.Quantity) && a.Unit == b.Unit && ratEqual(a.UnitPrice, b.UnitPrice) &&
		ratEqual(a.Amount, b.Amount) && a.VATHint == b.VATHint
}

func sameInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// toContract is s as it crosses contracts.ConsumptionProvider: decimal text at
// each figure's places.
func (s settlement) toContract() contracts.Settlement {
	out := contracts.Settlement{
		ID: s.ID, Revision: s.Revision, Kind: s.Kind, CorrectsID: s.CorrectsID,
		SupplyPeriodID: s.SupplyPeriodID, MeteringPointID: s.MeteringPointID, CustomerID: s.CustomerID,
		GSRN: s.GSRN, Address: s.Address, PriceArea: s.PriceArea, Household: s.Household,
		ContractName: s.ContractName, PeriodStart: s.PeriodStart, PeriodEnd: s.PeriodEnd,
		KWh: ratText(s.KWh, kwhPlaces), Estimated: s.Estimated, MissingIntervals: s.missingIntervals(),
		Currency: s.Currency, Amount: ratText(s.Amount, amountPlaces),
		Blocked: s.Blocked, BlockedDetail: s.BlockedDetail, State: s.State, InvoicedBy: s.Stamp,
		Lines: make([]contracts.SettlementLine, 0, len(s.Lines)),
	}
	for _, l := range s.Lines {
		out.Lines = append(out.Lines, contracts.SettlementLine{
			Kind: l.Kind, Text: l.Text, Band: l.Band, Month: l.Month,
			Quantity: ratText(l.Quantity, quantityPlaces), Unit: l.Unit,
			UnitPrice: ratText(l.UnitPrice, unitPricePlaces), Amount: ratText(l.Amount, amountPlaces),
			VATHint: l.VATHint,
		})
	}
	return out
}

// ratText is v as decimal text at places; nil is zero.
func ratText(v *big.Rat, places int) string {
	if v == nil {
		v = zeroRat()
	}
	return v.FloatString(places)
}

// sumLineAmounts is Σ the lines' amounts.
func sumLineAmounts(lines []settlementLine) *big.Rat {
	sum := zeroRat()
	for _, l := range lines {
		sum.Add(sum, l.Amount)
	}
	return sum
}
