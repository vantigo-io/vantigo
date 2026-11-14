// Package mva is the VAT return as Skatteetaten lays it out (accounting
// design A23): the figures of a term's tax codes become the return's lines —
// mvaSpesifikasjonslinje — in the return's groups and order, in whole kroner,
// and the lines become the XML of the published skattemeldingformerverdiavgift
// v1.0 schema. It knows nothing of the ledger; internal/accounting gives it
// the figures, computed from the postings.
//
// The rules it follows are Skatteetaten's (testdata/README.md names the
// source):
//
//   - The groups and the order are the mvaKodeSAFT code list's temaRekkefoelge
//     and kodeRekkefoelgeITema (codes, below; the test holds the table to the
//     list).
//   - An output code's line carries the base (grunnlag), the rate (sats) and
//     the VAT, owed positive (R066). An input code's carries the VAT alone,
//     negative (R065).
//   - Reverse charge and import — 81 to 92 — are one line with the base, the
//     rate and the calculated VAT, and, for the codes with a deduction, a
//     second line of the same code with the VAT deducted alone, negative
//     (R081; the published examples mvakode81.xml and mvakode86.xml).
//   - The VAT a konstatert loss takes back is a specification line tapPåKrav,
//     the VAT alone, negative, on the input code of the loss's rate: 1, 11,
//     12 or 13 (R038; the ledger posts it with the output code it was charged
//     with).
//   - Amounts carry no decimals (R082): each line's base and VAT are the
//     ledger's exact sums with the øre cut (the FAQ's "kutter desimaler";
//     R019 checks the VAT against the base × the rate rounded down, within one
//     krone a line), and the total is the sum of the lines (R018). The
//     difference to the ledger's exact sum is the caller's to post.
//   - A line whose sign is the code's opposite — a term of more credit notes
//     than sales — must say why (R020, R021): it carries the structured
//     remark kreditnota, which the merknadTilsvarendeMvaKode list allows on
//     every code for either sign.
package mva

import (
	"math/big"
	"slices"
	"strings"
)

// Group is one of the return's groups (the code list's tema).
type Group string

// The groups, and not_reported for the codes the return leaves out (0, 7,
// 20, 21, 22), which the specification lists apart.
const (
	SalesDomestic     Group = "sales_domestic"
	SalesAbroad       Group = "sales_abroad"
	PurchasesDomestic Group = "purchases_domestic"
	ImportGoods       Group = "import_goods"
	ImportServices    Group = "import_services"
	Fish              Group = "fish"
	EmissionsGold     Group = "emissions_gold"
	NotReported       Group = "not_reported"
)

// code is a standard code's place in the return.
type code struct {
	group Group
	// groupOrder is temaRekkefoelge, order kodeRekkefoelgeITema.
	groupOrder, order int
}

// codes is mvaKodeSAFT's codes for the general return (alminneligPrimær),
// with their tema and order.
var codes = map[string]code{
	"3": {SalesDomestic, 1, 1}, "31": {SalesDomestic, 1, 2}, "33": {SalesDomestic, 1, 3},
	"5": {SalesDomestic, 1, 4}, "6": {SalesDomestic, 1, 5},
	"52": {SalesAbroad, 2, 1},
	"1":  {PurchasesDomestic, 3, 1}, "11": {PurchasesDomestic, 3, 2}, "13": {PurchasesDomestic, 3, 3},
	"81": {ImportGoods, 4, 1}, "14": {ImportGoods, 4, 2}, "82": {ImportGoods, 4, 3}, "15": {ImportGoods, 4, 4},
	"83": {ImportGoods, 4, 5}, "84": {ImportGoods, 4, 6}, "85": {ImportGoods, 4, 7},
	"86": {ImportServices, 5, 1}, "87": {ImportServices, 5, 2}, "88": {ImportServices, 5, 3}, "89": {ImportServices, 5, 4},
	"32": {Fish, 6, 1}, "12": {Fish, 6, 2},
	"51": {EmissionsGold, 7, 1}, "91": {EmissionsGold, 7, 2}, "92": {EmissionsGold, 7, 3},
}

// GroupOf is the return's group of a standard code, NotReported for one the
// return does not carry.
func GroupOf(c string) Group {
	if info, ok := codes[c]; ok {
		return info.group
	}
	return NotReported
}

// Order is a code's place in the return: its group's order, then its own;
// a code the return does not carry sorts last.
func Order(c string) (group, order int) {
	if info, ok := codes[c]; ok {
		return info.groupOrder, info.order
	}
	return 99, 99
}

// LossCode is the input code a tapPåKrav line goes on for a loss posted with
// the output code c (R038): the input code of the same rate. A code without
// one is answered as itself.
func LossCode(c string) string {
	switch c {
	case "3":
		return "1"
	case "31":
		return "11"
	case "32":
		return "12"
	case "33":
		return "13"
	}
	return c
}

// TapPaaKrav is the specification of a loss's VAT taken back.
const TapPaaKrav = "tapPåKrav"

// RemarkCreditNote is the structured remark a line with the opposite sign
// carries.
const RemarkCreditNote = "kreditnota"

// Figure is one tax code's figures in a term, at one rate and one
// specification, exact, in the return's sign: a base positive for sales and
// purchases alike, VAT owed positive and VAT deducted negative.
type Figure struct {
	TaxCodeID int32
	Code      string
	// Name is the code's name in the books: mvaKodeRegnskapsystem.
	Name string
	// Direction is output, input or none; ReverseCharge marks 81–92.
	Direction     string
	ReverseCharge bool
	// RatePercent is the rate as the books keep it ("25.00"); "" for none.
	RatePercent string
	// Specification is TapPaaKrav for a loss's figure, "" otherwise.
	Specification string
	// LossName is, on a loss's figure, the name of the input code its line
	// goes on (LossCode); DeductionName, on a reverse-charge code, the name
	// its deduction line carries, so its two lines are two combinations of
	// code and mvaKodeRegnskapsystem. "" leaves Name.
	LossName, DeductionName string
	Base                    *big.Rat
	// Output is output VAT; Calculated reverse charge's calculated VAT;
	// Deducted input VAT deducted; Loss the VAT a loss takes back.
	Output, Calculated, Deducted, Loss *big.Rat
}

// Side tells a code's two lines apart.
type Side string

// The sides: the output side (base, rate and VAT) and the deduction.
const (
	SideOutput    Side = "output"
	SideDeduction Side = "deduction"
)

// Line is one mvaSpesifikasjonslinje.
type Line struct {
	Group         Group
	Code          string
	Specification string
	Name          string
	Side          Side
	TaxCodeID     int32
	// ExactBase is nil on a line without a base; Base is it with the øre cut.
	ExactBase *big.Rat
	Base      *big.Int
	// Rate is sats as the return writes it ("25", "11,11"); "" for none.
	Rate     string
	ExactVAT *big.Rat
	VAT      *big.Int
	// Remark is RemarkCreditNote on a line of the opposite sign, "" otherwise.
	Remark string

	groupOrder, order, place int
}

// Return is a term's lines and their total.
type Return struct {
	Lines []Line
	// Total is fastsattMerverdiavgift, the sum of the lines' VAT: to pay when
	// positive, to receive when negative. ExactTotal is the ledger's.
	Total      *big.Int
	ExactTotal *big.Rat
	// Considered is how many lines the figures made, those left out as zero
	// included: the rounding difference is less than a krone each.
	Considered int
}

// RoundingDifference is ExactTotal − Total: the øre the return cut, which
// the settlement voucher posts to the rounding account.
func (r Return) RoundingDifference() *big.Rat {
	return new(big.Rat).Sub(r.ExactTotal, new(big.Rat).SetInt(r.Total))
}

// Build lays the figures out as the return's lines. A figure of a code the
// return does not carry is left out, and so is a line whose base and VAT are
// zero once the øre are cut — its øre stay in ExactTotal.
func Build(figures []Figure) Return {
	out := Return{Lines: []Line{}, Total: new(big.Int), ExactTotal: new(big.Rat)}
	// Lines of one code, specification, side, rate and name are one line
	// (L1): Skatteetaten's model has one mvaSpesifikasjonslinje per
	// combination of code, specification and mvaKodeRegnskapsystem. They are
	// merged exact and cut once.
	type key struct {
		code, spec, rate, name string
		side                   Side
	}
	merged := map[key]*Line{}
	var order []key
	add := func(l Line) {
		k := key{l.Code, l.Specification, l.Rate, l.Name, l.Side}
		m, ok := merged[k]
		if !ok {
			c := l
			merged[k], order = &c, append(order, k)
			return
		}
		m.ExactVAT = new(big.Rat).Add(m.ExactVAT, l.ExactVAT)
		if l.ExactBase != nil {
			m.ExactBase = new(big.Rat).Add(zero(m.ExactBase), l.ExactBase)
		}
	}
	for _, f := range figures {
		group := GroupOf(f.Code)
		if group == NotReported {
			continue
		}
		groupOrder, order := Order(f.Code)
		if f.Specification == TapPaaKrav {
			c := LossCode(f.Code)
			if g := GroupOf(c); g != NotReported {
				group = g
				groupOrder, order = Order(c)
			}
			vat := zero(f.Loss)
			name := f.Name
			if f.LossName != "" {
				name = f.LossName
			}
			add(Line{Group: group, Code: c, Specification: TapPaaKrav, Name: name, Side: SideDeduction, TaxCodeID: f.TaxCodeID,
				ExactVAT: vat, groupOrder: groupOrder, order: order, place: 2})
			continue
		}
		rate := satsOf(f.RatePercent)
		switch {
		case f.Direction == "output":
			base, vat := zero(f.Base), zero(f.Output)
			add(Line{Group: group, Code: f.Code, Name: f.Name, Side: SideOutput, TaxCodeID: f.TaxCodeID, ExactBase: base,
				Rate: rate, ExactVAT: vat, groupOrder: groupOrder, order: order})
		case f.ReverseCharge:
			base, vat := zero(f.Base), zero(f.Calculated)
			add(Line{Group: group, Code: f.Code, Name: f.Name, Side: SideOutput, TaxCodeID: f.TaxCodeID, ExactBase: base,
				Rate: rate, ExactVAT: vat, groupOrder: groupOrder, order: order})
			d := deduction(f, group, groupOrder, order)
			if f.DeductionName != "" {
				d.Name = f.DeductionName
			}
			add(d)
		case f.Direction == "input":
			add(deduction(f, group, groupOrder, order))
		default:
			// A code of direction none is never reported; nothing of it is
			// a line.
		}
	}
	for _, k := range order {
		l := *merged[k]
		out.Considered++
		out.ExactTotal.Add(out.ExactTotal, l.ExactVAT)
		l.VAT = cut(l.ExactVAT)
		if l.ExactBase != nil {
			l.Base = cut(l.ExactBase)
		}
		l.Remark = ""
		switch {
		case l.Specification != "":
			// A tapPåKrav line of either sign needs no remark (R021's exception).
		case l.Side == SideOutput && l.ExactBase.Sign() < 0:
			l.Remark = RemarkCreditNote
		case l.Side == SideDeduction && l.ExactVAT.Sign() > 0:
			l.Remark = RemarkCreditNote
		}
		if (l.Base == nil || l.Base.Sign() == 0) && l.VAT.Sign() == 0 {
			continue
		}
		out.Lines = append(out.Lines, l)
	}
	slices.SortStableFunc(out.Lines, func(a, b Line) int {
		for _, d := range []int{a.groupOrder - b.groupOrder, a.order - b.order, a.place - b.place, strings.Compare(a.Rate, b.Rate)} {
			if d != 0 {
				return d
			}
		}
		return int(a.TaxCodeID - b.TaxCodeID)
	})
	for i := range out.Lines {
		out.Lines[i].place = 0
		out.Total.Add(out.Total, out.Lines[i].VAT)
	}
	return out
}

// deduction is a code's line of input VAT deducted: the VAT alone, negative;
// positive (a term of more credit notes than purchases) with the remark.
func deduction(f Figure, group Group, groupOrder, order int) Line {
	return Line{Group: group, Code: f.Code, Name: f.Name, Side: SideDeduction, TaxCodeID: f.TaxCodeID, ExactVAT: zero(f.Deducted),
		groupOrder: groupOrder, order: order, place: 1}
}

// RateConsistent reports whether an output side's VAT is its base × its rate
// cut to whole kroner, within one krone (R019, which Skatteetaten's FAQ says
// it checks within one krone a line, ten kroner at least for the return). A
// code's VAT is the sum of each document's VAT rounded to the øre, so a term
// of many documents can drift past it; the return then says so. A deduction
// or a specification line has no base to check.
func (l Line) RateConsistent() bool {
	if l.Side != SideOutput || l.Base == nil || l.Specification != "" {
		return true
	}
	rate, ok := new(big.Rat).SetString(strings.ReplaceAll(l.Rate, ",", "."))
	if !ok {
		return true
	}
	want := cut(new(big.Rat).Quo(new(big.Rat).Mul(new(big.Rat).SetInt(l.Base), rate), big.NewRat(100, 1)))
	d := new(big.Int).Sub(want, l.VAT)
	return d.CmpAbs(big.NewInt(1)) <= 0
}

// cut is r with its decimals cut, toward zero: the return's whole kroner.
func cut(r *big.Rat) *big.Int {
	return new(big.Int).Quo(r.Num(), r.Denom())
}

// zero is r, or 0 for nil.
func zero(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return r
}

// satsOf is a rate as the books keep it ("25.00", "11.11") as the return
// writes it: no trailing zeros, the decimal comma ("25", "11,11"). "" is
// "0", a code without a rate.
func satsOf(rate string) string {
	if rate == "" {
		return "0"
	}
	if strings.Contains(rate, ".") {
		rate = strings.TrimRight(strings.TrimRight(rate, "0"), ".")
	}
	if rate == "" {
		return "0"
	}
	return strings.ReplaceAll(rate, ".", ",")
}
