package invoices

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// D4: the wizard's lines — five groupings, the split by unit price, the
// order and the text in both languages — from the work, its projects and its
// people, with no database.

var (
	kari = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	ola  = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
)

func groupingDay(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func textPtr(s string) *string { return &s }

// apollo is project 41, code P-41, the one the September work is on.
func groupingProjects() map[int32]contracts.ProjectEntry {
	return map[int32]contracts.ProjectEntry{
		41: {ID: 41, Code: "P-41", Name: "Apollo", BillingType: "time-and-materials"},
		42: {ID: 42, Code: "A-7", Name: "Borealis", BillingType: "time-and-materials"},
		43: {ID: 43, Code: "Z-1", Name: "Cetus", BillingType: "fixed-price"},
	}
}

func groupingPeople() map[uuid.UUID]string {
	return map[uuid.UUID]string{kari: "Kari Nordmann", ola: "Ola Hansen"}
}

// september is a month of work on Apollo: Kari's 4 h on the 1st and 2 h on
// the 30th, Ola's 3.5 h on the 2nd, all at 1 200; an outlay, a mileage of
// 90 km at 4.50 and a supplier invoice; and a milestone.
func september() workSelection {
	utvikling, overtid := int32(5), int32(6)
	return workSelection{
		hours: []contracts.BillableHour{
			{ID: 1, Revision: 1, ProjectID: 41, UserID: kari, Date: groupingDay("2026-09-01"), HoursHundredths: 400,
				BillRate: "1200.00", Currency: "NOK", WorkTypeID: &utvikling, WorkTypeName: "Utvikling", Amount: "4800.00000000"},
			{ID: 2, Revision: 1, ProjectID: 41, UserID: ola, Date: groupingDay("2026-09-02"), HoursHundredths: 350,
				BillRate: "1200.00", Currency: "NOK", WorkTypeID: &utvikling, WorkTypeName: "Utvikling", Amount: "4200.00000000"},
			{ID: 3, Revision: 1, ProjectID: 41, UserID: kari, Date: groupingDay("2026-09-30"), HoursHundredths: 200,
				BillRate: "1200.00", Currency: "NOK", WorkTypeID: &overtid, WorkTypeName: "Overtid", Amount: "2400.00000000"},
		},
		expenses: []contracts.BillableExpense{
			{ID: 11, ProjectID: 41, Kind: "outlay", Date: groupingDay("2026-09-03"), Description: "Hotell Bergen",
				NetAmount: "1500.00", BillAmount: "1500.00", Currency: "NOK"},
			{ID: 12, ProjectID: 41, Kind: "mileage", Date: groupingDay("2026-09-04"), Description: "Oslo–Drammen",
				NetAmount: "405.00", DistanceKm: textPtr("90.0"), BillRatePerKm: textPtr("4.50"), BillAmount: "405.00", Currency: "NOK"},
			{ID: 13, ProjectID: 41, Kind: "supplier_invoice", Date: groupingDay("2026-09-05"), Description: "Stillas",
				Supplier: " Byggmakker AS ", SupplierInvoiceNumber: "F-77", NetAmount: "2000.00", BillAmount: "2000.00", Currency: "NOK"},
		},
		milestones: []contracts.BillableMilestone{
			{ID: 21, Revision: 2, ProjectID: 41, Name: "Fase 1", ReadyAt: time.Date(2026, 9, 9, 22, 30, 0, 0, time.UTC),
				Amount: "10000.00", Currency: "NOK"},
		},
	}
}

var groupingCodes = workVatCodes{hours: 1, expenses: 2, milestones: 3}

// lineSummary is one line as "description | unit | quantity × price | vat | sources".
func lineSummary(l draftLine, rows []heldSource) string {
	refs := make([]string, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, fmt.Sprintf("%s:%d", r.kind, r.id))
	}
	slices.Sort(refs)
	return fmt.Sprintf("%s | %s | %s × %s | %d | %s", l.description, l.unit,
		l.quantity.FloatString(3), l.unitPrice.FloatString(4), l.vatCodeID, strings.Join(refs, " "))
}

func groupedSummaries(t *testing.T, language, grouping string, sel workSelection) []string {
	t.Helper()
	lines, held, err := groupLines(language, grouping, groupingProjects(), groupingPeople(), sel, groupingCodes)
	if err != nil {
		t.Fatalf("groupLines(%s, %s): %v", language, grouping, err)
	}
	if len(lines) != len(held) {
		t.Fatalf("%d lines and %d sets of sources", len(lines), len(held))
	}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		for _, r := range held[i] {
			if r.linePosition != int32(i+1) || r.state != sourceHeld {
				t.Errorf("line %d's source %s:%d at position %d, state %q", i+1, r.kind, r.id, r.linePosition, r.state)
			}
		}
		out = append(out, lineSummary(l, held[i]))
	}
	return out
}

// Each grouping's lines and their text, in Norwegian and in English: hours by
// the project, the work type, the person, the day or the entry; expenses by
// their kind (each by itself when itemised); a milestone always its own line.
func TestGroupLines_EachGroupingsLinesAndTexts(t *testing.T) {
	t.Parallel()
	expensesNB := []string{
		"Kjøregodtgjørelse, Apollo, 4. sep. 2026 |  | 1.000 × 405.0000 | 2 | expenses.entry:12",
		"Viderefakturerte kostnader, Apollo, 3. sep. 2026 |  | 1.000 × 1500.0000 | 2 | expenses.entry:11",
		"Viderefakturert leverandørfaktura Byggmakker AS F-77 |  | 1.000 × 2000.0000 | 2 | expenses.entry:13",
	}
	expensesEN := []string{
		"Mileage, Apollo, 4 Sep 2026 |  | 1.000 × 405.0000 | 2 | expenses.entry:12",
		"Re-billed costs, Apollo, 3 Sep 2026 |  | 1.000 × 1500.0000 | 2 | expenses.entry:11",
		"Re-billed supplier invoice Byggmakker AS F-77 |  | 1.000 × 2000.0000 | 2 | expenses.entry:13",
	}
	milestone := "Fase 1 |  | 1.000 × 10000.0000 | 3 | projects.milestone:21"
	cases := []struct {
		language, grouping string
		want               []string
	}{
		{"nb", groupingProject, append(append([]string{
			"Konsulenttimer, Apollo, september 2026 | timer | 9.500 × 1200.0000 | 1 | time.entry:1 time.entry:2 time.entry:3",
		}, expensesNB...), milestone)},
		{"en", groupingProject, append(append([]string{
			"Consulting hours, Apollo, September 2026 | hours | 9.500 × 1200.0000 | 1 | time.entry:1 time.entry:2 time.entry:3",
		}, expensesEN...), milestone)},
		{"nb", groupingWorkType, append(append([]string{
			"Konsulenttimer, Apollo, 30. sep. 2026 – Overtid | timer | 2.000 × 1200.0000 | 1 | time.entry:3",
			"Konsulenttimer, Apollo, 1.–2. sep. 2026 – Utvikling | timer | 7.500 × 1200.0000 | 1 | time.entry:1 time.entry:2",
		}, expensesNB...), milestone)},
		{"en", groupingWorkType, append(append([]string{
			"Consulting hours, Apollo, 30 Sep 2026 – Overtid | hours | 2.000 × 1200.0000 | 1 | time.entry:3",
			"Consulting hours, Apollo, 1–2 Sep 2026 – Utvikling | hours | 7.500 × 1200.0000 | 1 | time.entry:1 time.entry:2",
		}, expensesEN...), milestone)},
		{"nb", groupingPerson, append(append([]string{
			"Konsulenttimer, Apollo, september 2026 – Kari Nordmann | timer | 6.000 × 1200.0000 | 1 | time.entry:1 time.entry:3",
			"Konsulenttimer, Apollo, 2. sep. 2026 – Ola Hansen | timer | 3.500 × 1200.0000 | 1 | time.entry:2",
		}, expensesNB...), milestone)},
		{"en", groupingPerson, append(append([]string{
			"Consulting hours, Apollo, September 2026 – Kari Nordmann | hours | 6.000 × 1200.0000 | 1 | time.entry:1 time.entry:3",
			"Consulting hours, Apollo, 2 Sep 2026 – Ola Hansen | hours | 3.500 × 1200.0000 | 1 | time.entry:2",
		}, expensesEN...), milestone)},
		{"nb", groupingDate, append(append([]string{
			"Konsulenttimer, Apollo, 1. sep. 2026 | timer | 4.000 × 1200.0000 | 1 | time.entry:1",
			"Konsulenttimer, Apollo, 2. sep. 2026 | timer | 3.500 × 1200.0000 | 1 | time.entry:2",
			"Konsulenttimer, Apollo, 30. sep. 2026 | timer | 2.000 × 1200.0000 | 1 | time.entry:3",
		}, expensesNB...), milestone)},
		{"en", groupingDate, append(append([]string{
			"Consulting hours, Apollo, 1 Sep 2026 | hours | 4.000 × 1200.0000 | 1 | time.entry:1",
			"Consulting hours, Apollo, 2 Sep 2026 | hours | 3.500 × 1200.0000 | 1 | time.entry:2",
			"Consulting hours, Apollo, 30 Sep 2026 | hours | 2.000 × 1200.0000 | 1 | time.entry:3",
		}, expensesEN...), milestone)},
		{"nb", groupingItemised, []string{
			"Konsulenttimer, Apollo, 1. sep. 2026 – Kari Nordmann – Utvikling | timer | 4.000 × 1200.0000 | 1 | time.entry:1",
			"Konsulenttimer, Apollo, 2. sep. 2026 – Ola Hansen – Utvikling | timer | 3.500 × 1200.0000 | 1 | time.entry:2",
			"Konsulenttimer, Apollo, 30. sep. 2026 – Kari Nordmann – Overtid | timer | 2.000 × 1200.0000 | 1 | time.entry:3",
			"Hotell Bergen |  | 1.000 × 1500.0000 | 2 | expenses.entry:11",
			"Kjøregodtgjørelse, Apollo, 4. sep. 2026 – Oslo–Drammen | km | 90.000 × 4.5000 | 2 | expenses.entry:12",
			"Viderefakturert leverandørfaktura Byggmakker AS F-77 |  | 1.000 × 2000.0000 | 2 | expenses.entry:13",
			milestone,
		}},
		{"en", groupingItemised, []string{
			"Consulting hours, Apollo, 1 Sep 2026 – Kari Nordmann – Utvikling | hours | 4.000 × 1200.0000 | 1 | time.entry:1",
			"Consulting hours, Apollo, 2 Sep 2026 – Ola Hansen – Utvikling | hours | 3.500 × 1200.0000 | 1 | time.entry:2",
			"Consulting hours, Apollo, 30 Sep 2026 – Kari Nordmann – Overtid | hours | 2.000 × 1200.0000 | 1 | time.entry:3",
			"Hotell Bergen |  | 1.000 × 1500.0000 | 2 | expenses.entry:11",
			"Mileage, Apollo, 4 Sep 2026 – Oslo–Drammen | km | 90.000 × 4.5000 | 2 | expenses.entry:12",
			"Re-billed supplier invoice Byggmakker AS F-77 |  | 1.000 × 2000.0000 | 2 | expenses.entry:13",
			milestone,
		}},
	}
	for _, c := range cases {
		t.Run(c.language+"/"+c.grouping, func(t *testing.T) {
			t.Parallel()
			got := groupedSummaries(t, c.language, c.grouping, september())
			if !slices.Equal(got, c.want) {
				t.Errorf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
			for _, s := range got {
				if strings.Contains(strings.ToLower(s), "utlegg") {
					t.Errorf("a line says utlegg, which is not supported: %s", s)
				}
			}
		})
	}
}

// Two supplier invoices of one project are one line under a grouping, named
// as a group; mileage grouped is 1 at the sum, never kilometres.
func TestGroupLines_GroupedExpensesAreOneLinePerKind(t *testing.T) {
	t.Parallel()
	sel := september()
	sel.hours, sel.milestones = nil, nil
	sel.expenses = append(sel.expenses,
		contracts.BillableExpense{ID: 14, ProjectID: 41, Kind: "supplier_invoice", Date: groupingDay("2026-09-08"),
			Supplier: "Rør AS", SupplierInvoiceNumber: "9", NetAmount: "500.00", BillAmount: "550.00", Currency: "NOK"},
		contracts.BillableExpense{ID: 15, ProjectID: 41, Kind: "mileage", Date: groupingDay("2026-09-09"),
			NetAmount: "45.00", DistanceKm: textPtr("10"), BillRatePerKm: textPtr("4.50"), BillAmount: "45.00", Currency: "NOK"})
	want := []string{
		"Kjøregodtgjørelse, Apollo, 4.–9. sep. 2026 |  | 1.000 × 450.0000 | 2 | expenses.entry:12 expenses.entry:15",
		"Viderefakturerte kostnader, Apollo, 3. sep. 2026 |  | 1.000 × 1500.0000 | 2 | expenses.entry:11",
		"Viderefakturerte leverandørfakturaer, Apollo, 5.–8. sep. 2026 |  | 1.000 × 2550.0000 | 2 | expenses.entry:13 expenses.entry:14",
	}
	if got := groupedSummaries(t, "nb", groupingProject, sel); !slices.Equal(got, want) {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A line has one unit price: two people at two rates on one project are two
// lines under project, and an hour with a multiplier is billed at the rate
// times the multiplier, rounded half away from zero to four decimals. Where
// that rounding reaches the øre, the line's net differs from Time's exact
// amount — line_differs_from_sources says so.
func TestGroupLines_SplitByEffectiveRateAndTheFourDecimals(t *testing.T) {
	t.Parallel()
	multiplier := "133.33"
	sel := workSelection{hours: []contracts.BillableHour{
		{ID: 1, ProjectID: 41, UserID: kari, Date: groupingDay("2026-09-01"), HoursHundredths: 400, BillRate: "1200.00",
			Currency: "NOK", Amount: "4800"},
		{ID: 2, ProjectID: 41, UserID: ola, Date: groupingDay("2026-09-02"), HoursHundredths: 300, BillRate: "1000.00",
			Currency: "NOK", Amount: "3000"},
		{ID: 3, ProjectID: 41, UserID: kari, Date: groupingDay("2026-09-03"), HoursHundredths: 200, BillRate: "1200.00",
			Currency: "NOK", Amount: "2400"},
		// 100.33 × 133.33 % = 133.769989, billed at 133.7700; 3.5 h are
		// 468.19496150 exactly, 468.19 to the øre, and 3.5 × 133.77 = 468.20.
		{ID: 4, ProjectID: 41, UserID: ola, Date: groupingDay("2026-09-04"), HoursHundredths: 350, BillRate: "100.33",
			BillMultiplierPercent: &multiplier, Currency: "NOK", Amount: "468.19496150"},
	}}
	lines, held, err := groupLines("nb", groupingProject, groupingProjects(), groupingPeople(), sel, groupingCodes)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i, l := range lines {
		got = append(got, lineSummary(l, held[i]))
	}
	want := []string{
		"Konsulenttimer, Apollo, 4. sep. 2026 | timer | 3.500 × 133.7700 | 1 | time.entry:4",
		"Konsulenttimer, Apollo, 2. sep. 2026 | timer | 3.000 × 1000.0000 | 1 | time.entry:2",
		"Konsulenttimer, Apollo, 1.–3. sep. 2026 | timer | 6.000 × 1200.0000 | 1 | time.entry:1 time.entry:3",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !lineDiffers(lines[0].amounts.net, held[0]) {
		t.Errorf("the 133.7700 line nets %s against 468.19496150 exactly, and does not differ", lines[0].amounts.net.FloatString(2))
	}
	for i := 1; i < len(lines); i++ {
		if lineDiffers(lines[i].amounts.net, held[i]) {
			t.Errorf("line %d nets %s and differs from its sources", i+1, lines[i].amounts.net.FloatString(2))
		}
	}
	if held[0][0].amount.FloatString(8) != "468.19496150" {
		t.Errorf("the source's amount = %s, want Time's exact 468.19496150", held[0][0].amount.FloatString(8))
	}
}

// Lines are ordered by the project's code, then the kind — hours, expenses,
// milestones — then the key, whatever order the work arrived in.
func TestGroupLines_TheOrder(t *testing.T) {
	t.Parallel()
	sel := workSelection{
		milestones: []contracts.BillableMilestone{
			{ID: 22, ProjectID: 41, Name: "Fase 2", ReadyAt: groupingDay("2026-09-20"), Amount: "5000", Currency: "NOK"},
			{ID: 21, ProjectID: 41, Name: "Fase 1", ReadyAt: groupingDay("2026-09-10"), Amount: "5000", Currency: "NOK"},
			{ID: 31, ProjectID: 42, Name: "Oppstart", ReadyAt: groupingDay("2026-09-01"), Amount: "100", Currency: "NOK"},
		},
		expenses: []contracts.BillableExpense{
			{ID: 11, ProjectID: 41, Kind: "outlay", Date: groupingDay("2026-09-03"), BillAmount: "10", Currency: "NOK"},
			{ID: 32, ProjectID: 42, Kind: "outlay", Date: groupingDay("2026-09-03"), BillAmount: "10", Currency: "NOK"},
		},
		hours: []contracts.BillableHour{
			{ID: 2, ProjectID: 41, UserID: ola, Date: groupingDay("2026-09-02"), HoursHundredths: 100, BillRate: "900", Currency: "NOK", Amount: "900"},
			{ID: 1, ProjectID: 41, UserID: kari, Date: groupingDay("2026-09-01"), HoursHundredths: 100, BillRate: "1000", Currency: "NOK", Amount: "1000"},
			{ID: 33, ProjectID: 42, UserID: kari, Date: groupingDay("2026-09-01"), HoursHundredths: 100, BillRate: "1000", Currency: "NOK", Amount: "1000"},
		},
	}
	lines, held, err := groupLines("nb", groupingDate, groupingProjects(), groupingPeople(), sel, groupingCodes)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := range lines {
		got = append(got, fmt.Sprintf("%s:%d", held[i][0].kind, held[i][0].id))
	}
	// Borealis (A-7) before Apollo (P-41); in each, hours by day, then the
	// expense, then the milestones by the day they became ready.
	want := []string{
		"time.entry:33", "expenses.entry:32", "projects.milestone:31",
		"time.entry:1", "time.entry:2", "expenses.entry:11", "projects.milestone:21", "projects.milestone:22",
	}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// The period a line names: a whole calendar month by name, else its days —
// one day, days of one month, of two months, of two years.
func TestGroupLines_ThePeriodText(t *testing.T) {
	t.Parallel()
	cases := []struct{ from, to, nb, en string }{
		{"2026-09-01", "2026-09-30", "september 2026", "September 2026"},
		{"2026-02-01", "2026-02-28", "februar 2026", "February 2026"},
		{"2026-09-03", "2026-09-03", "3. sep. 2026", "3 Sep 2026"},
		{"2026-09-01", "2026-09-15", "1.–15. sep. 2026", "1–15 Sep 2026"},
		{"2026-09-02", "2026-09-30", "2.–30. sep. 2026", "2–30 Sep 2026"},
		{"2026-08-28", "2026-09-03", "28. aug.–3. sep. 2026", "28 Aug – 3 Sep 2026"},
		{"2025-12-28", "2026-01-03", "28. des. 2025–3. jan. 2026", "28 Dec 2025 – 3 Jan 2026"},
		{"2026-03-01", "2026-03-31", "mars 2026", "March 2026"},
	}
	for _, c := range cases {
		from, to := groupingDay(c.from), groupingDay(c.to)
		if got := periodText("nb", from, to); got != c.nb {
			t.Errorf("nb %s–%s = %q, want %q", c.from, c.to, got, c.nb)
		}
		if got := periodText("en", from, to); got != c.en {
			t.Errorf("en %s–%s = %q, want %q", c.from, c.to, got, c.en)
		}
	}
	if got := lineLanguage("en"); got != "en" {
		t.Errorf("lineLanguage(en) = %q", got)
	}
	for _, l := range []string{"", "nb", "nn", "sv"} {
		if got := lineLanguage(l); got != "nb" {
			t.Errorf("lineLanguage(%q) = %q, want nb", l, got)
		}
	}
}

// The next coarser grouping, itemised → date → person → work_type → project;
// none past project, none for a grouping this module does not know.
func TestSuggestCoarser(t *testing.T) {
	t.Parallel()
	for g, want := range map[string]string{
		groupingItemised: groupingDate, groupingDate: groupingPerson, groupingPerson: groupingWorkType,
		groupingWorkType: groupingProject, groupingProject: "", "weekly": "",
	} {
		if got := suggestCoarser(g); got != want {
			t.Errorf("suggestCoarser(%q) = %q, want %q", g, got, want)
		}
	}
}
