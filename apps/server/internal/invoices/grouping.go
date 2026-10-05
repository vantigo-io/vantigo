package invoices

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is D4: how the wizard turns the chosen work into a draft's lines
// — the five groupings, the split by unit price within a grouping's key, the
// lines' order and their text in the buyer's language. It reads nothing and
// writes nothing: the work, its projects and its people's names come in, the
// lines and the work each one bills go out.

// The groupings, from the finest to the coarsest: the order too_many_lines
// suggests a coarser one along.
const (
	groupingItemised = "itemised"
	groupingDate     = "date"
	groupingPerson   = "person"
	groupingWorkType = "work_type"
	groupingProject  = "project"
)

var groupings = []string{groupingItemised, groupingDate, groupingPerson, groupingWorkType, groupingProject}

// suggestCoarser is the next coarser grouping than g, "" for project — the
// coarsest — and for a grouping this module does not know.
func suggestCoarser(g string) string {
	i := slices.Index(groupings, g)
	if i < 0 || i == len(groupings)-1 {
		return ""
	}
	return groupings[i+1]
}

// The expense kinds a line's text tells apart, as Expenses spells them; an
// outlay — and a kind added later — reads as a re-billed cost.
const (
	expenseMileage         = "mileage"
	expenseSupplierInvoice = "supplier_invoice"
)

// workVatCodes is the VAT code each kind of work's lines take (D6).
type workVatCodes struct {
	hours, expenses, milestones int32
}

// workSelection is the work the wizard takes, as its modules answer it now.
type workSelection struct {
	hours      []contracts.BillableHour
	expenses   []contracts.BillableExpense
	milestones []contracts.BillableMilestone
}

// lineWords are the words a line's text is made of, per language (D3's
// table). Neither language says "utlegg": utlegg is not supported (D6).
type lineWords struct {
	hours, hoursUnit, outlay, mileage, supplierInvoice, supplierInvoices, unknownPerson string
	months, shortMonths                                                                 [12]string
}

var lineLanguages = map[string]lineWords{
	"nb": {
		hours: "Konsulenttimer", hoursUnit: "timer", outlay: "Viderefakturerte kostnader", mileage: "Kjøregodtgjørelse",
		supplierInvoice: "Viderefakturert leverandørfaktura", supplierInvoices: "Viderefakturerte leverandørfakturaer",
		unknownPerson: "Ukjent person",
		months:        [12]string{"januar", "februar", "mars", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "desember"},
		shortMonths:   [12]string{"jan.", "feb.", "mars", "apr.", "mai", "juni", "juli", "aug.", "sep.", "okt.", "nov.", "des."},
	},
	"en": {
		hours: "Consulting hours", hoursUnit: "hours", outlay: "Re-billed costs", mileage: "Mileage",
		supplierInvoice: "Re-billed supplier invoice", supplierInvoices: "Re-billed supplier invoices",
		unknownPerson: "Unknown person",
		months:        [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
		shortMonths:   [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
	},
}

// lineLanguage is the language a buyer's lines are written in: English for a
// profile that asks for it, else Norwegian — buyerSnapshot's rule.
func lineLanguage(profileLanguage string) string {
	if profileLanguage == "en" {
		return "en"
	}
	return "nb"
}

// periodText is the period a line covers, from its first to its last work
// date (D3): a whole calendar month by name ("september 2026"), else the days
// — "3. sep. 2026", "1.–15. sep. 2026", "28. aug.–3. sep. 2026", "28. des.
// 2025–3. jan. 2026" — and in English "September 2026", "3 Sep 2026",
// "1–15 Sep 2026", "28 Aug – 3 Sep 2026", "28 Dec 2025 – 3 Jan 2026".
func periodText(language string, from, to time.Time) string {
	w := lineLanguages[language]
	en := language == "en"
	day := func(d time.Time, withMonth, withYear bool) string {
		s := fmt.Sprintf("%d.", d.Day())
		if en {
			s = fmt.Sprintf("%d", d.Day())
		}
		if withMonth {
			s += " " + w.shortMonths[d.Month()-1]
		}
		if withYear {
			s += fmt.Sprintf(" %d", d.Year())
		}
		return s
	}
	lastOfMonth := time.Date(from.Year(), from.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	switch {
	case from.Day() == 1 && to.Equal(lastOfMonth):
		return fmt.Sprintf("%s %d", w.months[from.Month()-1], from.Year())
	case from.Equal(to):
		return day(from, true, true)
	case from.Year() == to.Year() && from.Month() == to.Month():
		if en {
			return fmt.Sprintf("%d–%s", from.Day(), day(to, true, true))
		}
		return fmt.Sprintf("%s–%s", day(from, false, false), day(to, true, true))
	}
	sep := "–"
	if en {
		sep = " – "
	}
	return day(from, true, from.Year() != to.Year()) + sep + day(to, true, true)
}

// effectiveRate is an hour's unit price on a line (D4): the bill rate times
// the multiplier (100 % for none), rounded half away from zero to the four
// decimals unit_price holds.
func effectiveRate(h contracts.BillableHour) (*big.Rat, error) {
	rate, err := billableAmount(h.BillRate)
	if err != nil {
		return nil, err
	}
	if h.BillMultiplierPercent != nil {
		multiplier, err := billableAmount(*h.BillMultiplierPercent)
		if err != nil {
			return nil, err
		}
		rate.Mul(rate, multiplier).Quo(rate, big.NewRat(100, 1))
	}
	return mustRat(rate.FloatString(4)), nil
}

// The kinds' order on a document: hours, then expenses, then milestones.
const (
	orderHours = iota
	orderExpenses
	orderMilestones
)

// workGroup is one line in the making: where it sorts, what it says, what it
// bills and the work it holds.
type workGroup struct {
	projectCode string
	projectID   int32
	kindOrder   int
	key         string
	rate        *big.Rat
	unit        string
	quantity    *big.Rat
	unitPrice   *big.Rat
	vatCodeID   int32
	sources     []heldSource
	dates       []time.Time
	// supplierInvoice is the one supplier invoice a group of that kind
	// bills, for its own text; nil when it bills more than one.
	supplierInvoice *contracts.BillableExpense
	// textFn writes the text once every source is in.
	textFn func(g *workGroup) string
}

func compareGroups(a, b *workGroup) int {
	return cmp.Or(cmp.Compare(a.projectCode, b.projectCode), cmp.Compare(a.projectID, b.projectID),
		cmp.Compare(a.kindOrder, b.kindOrder), cmp.Compare(a.key, b.key), a.rate.Cmp(b.rate))
}

func (g *workGroup) period(language string) string {
	from, to := slices.MinFunc(g.dates, time.Time.Compare), slices.MaxFunc(g.dates, time.Time.Compare)
	return periodText(language, from, to)
}

// maxDescription is a line description's column bound.
const maxDescription = 500

func clip(s string) string {
	if utf8.RuneCountInString(s) <= maxDescription {
		return s
	}
	return string([]rune(s)[:maxDescription])
}

// groupLines is D4: the selection as lines under grouping, in language, each
// line's work beside it (the heldSources by line, positions from 1). The key
// is (project, kind) and then the grouping's term — the work type, the
// person, the day, or the source itself for itemised; hours split further by
// their effective rate, since a line has one unit price; expenses group by
// their kind under every grouping but itemised; a milestone is always its own
// line. Lines are ordered by the project's code, the kind (hours, expenses,
// milestones) and the key. people names the users whose hours the work holds.
func groupLines(language, grouping string, projects map[int32]contracts.ProjectEntry, people map[uuid.UUID]string,
	sel workSelection, codes workVatCodes,
) ([]draftLine, [][]heldSource, error) {
	w := lineLanguages[language]
	groups := map[string]*workGroup{}
	group := func(id string, build func() *workGroup) *workGroup {
		g, ok := groups[id]
		if !ok {
			g = build()
			g.quantity = new(big.Rat)
			if g.rate == nil {
				g.rate = new(big.Rat)
			}
			groups[id] = g
		}
		return g
	}
	project := func(id int32) contracts.ProjectEntry { return projects[id] }
	person := func(id uuid.UUID) string {
		if name, ok := people[id]; ok && name != "" {
			return name
		}
		return w.unknownPerson
	}
	day := func(d time.Time) string { return periodText(language, d, d) }

	for _, h := range sel.hours {
		src, err := hourSource(h)
		if err != nil {
			return nil, nil, err
		}
		rate, err := effectiveRate(h)
		if err != nil {
			return nil, nil, err
		}
		p := project(h.ProjectID)
		var key, suffix string
		switch grouping {
		case groupingWorkType:
			workType := int32(0)
			if h.WorkTypeID != nil {
				workType = *h.WorkTypeID
			}
			key, suffix = fmt.Sprintf("%s|%d", strings.ToLower(h.WorkTypeName), workType), h.WorkTypeName
		case groupingPerson:
			key, suffix = strings.ToLower(person(h.UserID))+"|"+h.UserID.String(), person(h.UserID)
		case groupingDate:
			key = src.date.Format(time.DateOnly)
		case groupingItemised:
			key = fmt.Sprintf("%s|%s|%020d", src.date.Format(time.DateOnly), strings.ToLower(person(h.UserID)), h.ID)
		}
		id := fmt.Sprintf("%d|h|%s|%s", h.ProjectID, key, rate.FloatString(4))
		g := group(id, func() *workGroup {
			g := &workGroup{
				projectCode: p.Code, projectID: h.ProjectID, kindOrder: orderHours, key: key, rate: rate,
				unit: w.hoursUnit, unitPrice: rate, vatCodeID: codes.hours,
			}
			switch grouping {
			case groupingItemised:
				text := fmt.Sprintf("%s, %s, %s – %s", w.hours, p.Name, day(src.date), person(h.UserID))
				if h.WorkTypeName != "" {
					text += " – " + h.WorkTypeName
				}
				g.textFn = func(*workGroup) string { return text }
			default:
				g.textFn = func(g *workGroup) string {
					text := fmt.Sprintf("%s, %s, %s", w.hours, p.Name, g.period(language))
					if suffix != "" {
						text += " – " + suffix
					}
					return text
				}
			}
			return g
		})
		g.quantity.Add(g.quantity, src.quantity)
		g.sources, g.dates = append(g.sources, src), append(g.dates, src.date)
	}

	for _, e := range sel.expenses {
		src, err := expenseSource(e)
		if err != nil {
			return nil, nil, err
		}
		p := project(e.ProjectID)
		key := e.Kind
		if grouping == groupingItemised {
			key = fmt.Sprintf("%s|%020d", src.date.Format(time.DateOnly), e.ID)
		}
		id := fmt.Sprintf("%d|e|%s", e.ProjectID, key)
		g := group(id, func() *workGroup {
			g := &workGroup{projectCode: p.Code, projectID: e.ProjectID, kindOrder: orderExpenses, key: key, vatCodeID: codes.expenses}
			g.textFn = func(g *workGroup) string {
				switch {
				case e.Kind == expenseSupplierInvoice && g.supplierInvoice != nil:
					return strings.TrimSpace(fmt.Sprintf("%s %s %s", w.supplierInvoice,
						strings.TrimSpace(g.supplierInvoice.Supplier), strings.TrimSpace(g.supplierInvoice.SupplierInvoiceNumber)))
				case e.Kind == expenseSupplierInvoice:
					return fmt.Sprintf("%s, %s, %s", w.supplierInvoices, p.Name, g.period(language))
				case e.Kind == expenseMileage:
					text := fmt.Sprintf("%s, %s, %s", w.mileage, p.Name, g.period(language))
					if grouping == groupingItemised && strings.TrimSpace(e.Description) != "" {
						text += " – " + strings.TrimSpace(e.Description)
					}
					return text
				case grouping == groupingItemised && strings.TrimSpace(e.Description) != "":
					return strings.TrimSpace(e.Description)
				}
				return fmt.Sprintf("%s, %s, %s", w.outlay, p.Name, g.period(language))
			}
			return g
		})
		switch {
		case len(g.sources) == 0 && e.Kind == expenseSupplierInvoice:
			g.supplierInvoice = &e
		case e.Kind == expenseSupplierInvoice:
			g.supplierInvoice = nil
		}
		// An itemised mileage line states its kilometres at the rate per km;
		// every other expense line is 1 at the bill amount.
		if grouping == groupingItemised && e.Kind == expenseMileage && e.DistanceKm != nil && e.BillRatePerKm != nil {
			rate, err := billableAmount(*e.BillRatePerKm)
			if err != nil {
				return nil, nil, err
			}
			g.quantity, g.unit, g.unitPrice = src.quantity, "km", mustRat(rate.FloatString(4))
		} else {
			if g.unitPrice == nil {
				g.unitPrice = new(big.Rat)
			}
			g.quantity = big.NewRat(1, 1)
			g.unitPrice = mustRat(new(big.Rat).Add(g.unitPrice, src.amount).FloatString(4))
		}
		g.sources, g.dates = append(g.sources, src), append(g.dates, src.date)
	}

	for _, m := range sel.milestones {
		src, err := milestoneSource(m)
		if err != nil {
			return nil, nil, err
		}
		p := project(m.ProjectID)
		key := fmt.Sprintf("%s|%020d", src.date.Format(time.DateOnly), m.ID)
		name := strings.TrimSpace(m.Name)
		g := group(fmt.Sprintf("%d|m|%s", m.ProjectID, key), func() *workGroup {
			return &workGroup{
				projectCode: p.Code, projectID: m.ProjectID, kindOrder: orderMilestones, key: key, vatCodeID: codes.milestones,
				textFn: func(*workGroup) string { return name },
			}
		})
		g.quantity, g.unitPrice = big.NewRat(1, 1), mustRat(src.amount.FloatString(4))
		g.sources, g.dates = append(g.sources, src), append(g.dates, src.date)
	}

	ordered := make([]*workGroup, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	slices.SortFunc(ordered, compareGroups)
	lines := make([]draftLine, 0, len(ordered))
	held := make([][]heldSource, 0, len(ordered))
	for i, g := range ordered {
		l := draftLine{
			description: clip(g.textFn(g)), unit: g.unit, quantity: g.quantity, unitPrice: g.unitPrice,
			discount: new(big.Rat), vatCodeID: g.vatCodeID, sourcesGiven: true,
		}
		l.amounts = computeLine(l.quantity, l.unitPrice, l.discount)
		rows := make([]heldSource, 0, len(g.sources))
		for _, s := range g.sources {
			s.state, s.linePosition = sourceHeld, int32(i+1)
			rows = append(rows, s)
			l.sources = append(l.sources, s.ref())
		}
		lines, held = append(lines, l), append(held, rows)
	}
	return lines, held, nil
}
