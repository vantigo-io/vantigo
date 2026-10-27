package invoices

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is how a settlement of the energy module becomes a draft's lines
// (energy consumption billing design D7, D8; invoices from consumption plan
// reading 3): one line per settlement line, in the buyer's language, its VAT
// code by the line's hint, and the one source row the settlement's lines
// hold. It reads nothing and writes nothing: the settlements come in, the
// lines and their sources go out. The From consumption wizard and a save's
// refresh build with it.

// marketZone is the power market's time zone: a settlement's period bounds
// are its midnights (the provider's PeriodStart and PeriodEnd), and a
// wizard's "through" date is a day in it.
var marketZone = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		panic(fmt.Sprintf("invoices: load the market time zone: %v", err))
	}
	return loc
}()

// throughInstant is the provider's Through for a through date: the start of
// the day after it in the market zone, so a period ending at that day's end
// is due.
func throughInstant(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, marketZone)
}

// civilDay is the calendar day an instant falls on in the market zone, as
// this module keeps dates (midnight UTC).
func civilDay(t time.Time) time.Time {
	l := t.In(marketZone)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// settlementDays is a settlement's first and last civil day: its half-open
// bounds' days, the end's the day before it.
func settlementDays(st contracts.Settlement) (first, last time.Time) {
	return civilDay(st.PeriodStart), civilDay(st.PeriodEnd).AddDate(0, 0, -1)
}

// energyVatCodes is the VAT code each hint's lines take (D8): the settings'
// energy code for standard, the exempt code for the north's households — or
// both overridden by the wizard's request, or code 9 while the seller is not
// VAT registered.
type energyVatCodes struct {
	standard, exempt int32
}

func (c energyVatCodes) of(hint string) int32 {
	if hint == contracts.VATHintExemptHouseholdNorth {
		return c.exempt
	}
	return c.standard
}

// energyWords are the words an energy line's text is made of, per language.
type energyWords struct {
	day, night, correction, previously string
	units                              map[string]string
}

var energyLanguages = map[string]energyWords{
	"nb": {
		day: "dag", night: "natt", correction: "Korreksjon", previously: "tidligere fakturert",
		units: map[string]string{
			contracts.SettlementUnitKWh: "kWh", contracts.SettlementUnitMonth: "mnd",
			contracts.SettlementUnitYear: "år", contracts.SettlementUnitPcs: "stk",
		},
	},
	"en": {
		day: "day", night: "night", correction: "Correction", previously: "previously invoiced",
		units: map[string]string{
			contracts.SettlementUnitKWh: "kWh", contracts.SettlementUnitMonth: "month",
			contracts.SettlementUnitYear: "year", contracts.SettlementUnitPcs: "pcs",
		},
	},
}

// unitWord is a settlement unit as a line's unit in language, one
// ehf.UnitCode maps (KWH, MON, ANN, C62); a unit it does not know is
// printed as Energy spelled it.
func unitWord(language, unit string) string {
	if w, ok := energyLanguages[language].units[unit]; ok {
		return w
	}
	return unit
}

// sortSettlements puts settlements in a document's order: by metering point,
// then by period, a regular one before its period's correction, then by id.
func sortSettlements(settlements []contracts.Settlement) {
	slices.SortFunc(settlements, func(a, b contracts.Settlement) int {
		return cmp.Or(cmp.Compare(a.GSRN, b.GSRN), a.PeriodStart.Compare(b.PeriodStart),
			cmp.Compare(kindOrder(a.Kind), kindOrder(b.Kind)), cmp.Compare(a.ID, b.ID))
	})
}

func kindOrder(kind string) int {
	if kind == contracts.SettlementCorrection {
		return 1
	}
	return 0
}

// settlementSource is the row a settlement's lines hold (plan reading 3): its
// identity and revision, its kind as the subkind, no project, its kWh as the
// quantity — a correction's the difference — and its amount, its period's
// last day as the date and the snapshot of its metering point, over
// lineCount lines.
func settlementSource(st contracts.Settlement, lineCount int32) (heldSource, error) {
	kwh, err := billableAmount(st.KWh)
	if err != nil {
		return heldSource{}, err
	}
	amount, err := billableAmount(st.Amount)
	if err != nil {
		return heldSource{}, err
	}
	first, last := settlementDays(st)
	kind := st.Kind
	return heldSource{
		kind: contracts.WorkSourceSettlement, id: st.ID, revision: st.Revision, subkind: &kind,
		quantity: kwh, amount: amount, currency: st.Currency, date: last, lineCount: lineCount,
		point: &settlementPoint{
			gsrn: st.GSRN, address: st.Address, priceArea: st.PriceArea, contractName: st.ContractName,
			periodFrom: first, estimated: st.Estimated,
		},
	}, nil
}

// energyLines builds the lines of settlements (plan reading 3), in the order
// sortSettlements gives: one per settlement line, its text in language, its
// VAT code by its hint through codes. held[i] is the sources line i is the
// first line of — a settlement's one row, its lineCount the lines it makes —
// with the line's position from 1. invoiced names the number of the invoice
// that invoiced each correction's original, by the original's id, as this
// module's own rows know it; a correction whose original it does not name
// says nothing of it.
func energyLines(language string, settlements []contracts.Settlement, codes energyVatCodes, invoiced map[int64]int64) ([]draftLine, [][]heldSource, error) {
	sorted := slices.Clone(settlements)
	sortSettlements(sorted)
	var lines []draftLine
	var held [][]heldSource
	for _, st := range sorted {
		made, err := settlementLines(language, st, codes, invoiced)
		if err != nil {
			return nil, nil, err
		}
		src, err := settlementSource(st, int32(len(made)))
		if err != nil {
			return nil, nil, err
		}
		src.state, src.linePosition = sourceHeld, int32(len(lines)+1)
		for i := range made {
			made[i].sources, made[i].sourcesGiven = []sourceRef{src.ref()}, true
			rows := []heldSource(nil)
			if i == 0 {
				rows = []heldSource{src}
			}
			lines, held = append(lines, made[i]), append(held, rows)
		}
	}
	return lines, held, nil
}

// settlementLines is one settlement's lines: every settlement line but one of
// no quantity and no amount, and — so the settlement's row always sits on a
// line — its first line at 1 × 0.00 when none is left.
func settlementLines(language string, st contracts.Settlement, codes energyVatCodes, invoiced map[int64]int64) ([]draftLine, error) {
	first, last := settlementDays(st)
	period := periodText(language, first, last)
	months := first.Year() != last.Year() || first.Month() != last.Month()
	correction := st.Kind == contracts.SettlementCorrection
	var out []draftLine
	for _, sl := range st.Lines {
		quantity, err := billableAmount(sl.Quantity)
		if err != nil {
			return nil, err
		}
		amount, err := billableAmount(sl.Amount)
		if err != nil {
			return nil, err
		}
		price, err := billableAmount(sl.UnitPrice)
		if err != nil {
			return nil, err
		}
		if quantity.Sign() == 0 && amount.Sign() == 0 {
			continue
		}
		l := draftLine{discount: new(big.Rat), vatCodeID: codes.of(sl.VATHint), correction: correction}
		text := lineText(language, st, sl, period, months)
		switch {
		case correction:
			l.description = correctionText(language, st, sl, text, quantity, invoiced)
			l.unit, l.quantity, l.unitPrice = unitWord(language, contracts.SettlementUnitPcs), big.NewRat(1, 1), new(big.Rat).Abs(amount)
			if amount.Sign() < 0 {
				l.quantity = big.NewRat(-1, 1)
			}
		case quantity.Sign() <= 0:
			// A regular line priced with no quantity Energy does not write;
			// should one come, its amount is billed as one piece.
			l.description = text
			l.unit, l.quantity, l.unitPrice = unitWord(language, contracts.SettlementUnitPcs), big.NewRat(1, 1), new(big.Rat).Abs(amount)
		default:
			l.description = text
			l.unit, l.quantity, l.unitPrice = unitWord(language, sl.Unit), quantity, linePrice(quantity, price, amount)
		}
		l.description = clip(l.description)
		l.amounts = computeLine(l.quantity, l.unitPrice, l.discount)
		out = append(out, l)
	}
	if len(out) == 0 && len(st.Lines) > 0 {
		sl := st.Lines[0]
		l := draftLine{
			description: clip(lineText(language, st, sl, period, months)), unit: unitWord(language, contracts.SettlementUnitPcs),
			quantity: big.NewRat(1, 1), unitPrice: new(big.Rat), discount: new(big.Rat), vatCodeID: codes.of(sl.VATHint),
			correction: correction,
		}
		l.amounts = computeLine(l.quantity, l.unitPrice, l.discount)
		out = append(out, l)
	}
	return out, nil
}

// linePrice is a regular line's unit price (readings on the record 3, the
// amount wins): the settlement's own when quantity × it rounds to the
// amount; else the amount ÷ quantity at the eight decimals a line keeps,
// when that does; else the settlement's, and the line then differs from its
// source (line_differs_from_sources).
func linePrice(quantity, price, amount *big.Rat) *big.Rat {
	hits := func(p *big.Rat) bool { return round2(new(big.Rat).Mul(quantity, p)).Cmp(amount) == 0 }
	if hits(price) {
		return price
	}
	exact := mustRat(new(big.Rat).Quo(amount, quantity).FloatString(unitPricePlaces))
	if exact.Sign() >= 0 && hits(exact) {
		return exact
	}
	return price
}

// lineText is a settlement line's text: "<component><qualifier>, <GSRN>
// <address>, <period>", the qualifier the band in words or, on a capacity
// line of a settlement spanning more than one month, the month.
func lineText(language string, st contracts.Settlement, sl contracts.SettlementLine, period string, months bool) string {
	w := energyLanguages[language]
	name := strings.TrimSpace(sl.Text)
	switch {
	case sl.Band == contracts.SettlementBandDay:
		name += " (" + w.day + ")"
	case sl.Band == contracts.SettlementBandNight:
		name += " (" + w.night + ")"
	case sl.Month != nil && months:
		m := civilDay(*sl.Month) // a date column's UTC midnight and a market midnight alike
		name += " (" + lineLanguages[language].months[m.Month()-1] + fmt.Sprintf(" %d", m.Year()) + ")"
	}
	return fmt.Sprintf("%s, %s %s, %s", name, st.GSRN, st.Address, period)
}

// correctionText is a correction line's text: "Korreksjon <text>: <±q>
// <unit>, tidligere fakturert <n>", the quantity left out when it did not
// change and the number when this module does not know the original's.
func correctionText(language string, st contracts.Settlement, sl contracts.SettlementLine, text string, quantity *big.Rat, invoiced map[int64]int64) string {
	w := energyLanguages[language]
	s := w.correction + " " + text
	if quantity.Sign() != 0 {
		s += ": " + signedQuantity(language, quantity) + " " + unitWord(language, sl.Unit)
	}
	if st.CorrectsID != nil {
		if n, ok := invoiced[*st.CorrectsID]; ok {
			s += fmt.Sprintf(", %s %d", w.previously, n)
		}
	}
	return s
}

// signedQuantity is a difference with its sign and up to three decimals, a
// decimal comma in Norwegian: "+12,5", "−3".
func signedQuantity(language string, q *big.Rat) string {
	sign := "+"
	if q.Sign() < 0 {
		sign = "−"
	}
	s := strings.TrimRight(strings.TrimRight(new(big.Rat).Abs(q).FloatString(3), "0"), ".")
	if language != "en" {
		s = strings.ReplaceAll(s, ".", ",")
	}
	return sign + s
}
