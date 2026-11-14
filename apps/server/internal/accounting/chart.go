package accounting

// The default chart, the grouping list and the standard tax codes (A3, A5,
// R13). Skatteetaten's næringsspesifikasjon list (grouping/2026.csv, the
// 2025–2026 list as published in its saf-t repository) carries a Norwegian
// and an English name for every code: the chart takes its names from it, and
// an account's grouping is its own number. The few accounts the list has no
// code for — the separate VAT accounts, Vipps clearing, rounding, bank fees
// and the opening balance — carry names of our own and the nearest code.

import (
	"bytes"
	"context"
	"embed"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

//go:embed grouping/*.csv
var groupingFiles embed.FS

// groupingCode is one code of a year's list.
type groupingCode struct {
	Category, Code, NameNb, NameEn string
}

// groupingList is one year's list in file order, and its codes by code for the
// categories a company outside banking and insurance uses.
type groupingList struct {
	year   int
	codes  []groupingCode
	byCode map[string]groupingCode
}

var (
	groupingOnce  sync.Once
	groupingLists []groupingList // by year
	groupingErr   error
)

// bankingCategory reports whether a category is one only banks, insurers or
// IFRS reporters use; their codes repeat the ordinary ones' and are not
// offered as a default.
func bankingCategory(category string) bool {
	return strings.Contains(category, "BankOgForsikring") || strings.HasPrefix(category, "bankOgForsikring") ||
		strings.Contains(category, "IFRS") || strings.Contains(category, "InnenBank")
}

// loadGroupings reads every embedded year's list once.
func loadGroupings() ([]groupingList, error) {
	groupingOnce.Do(func() {
		entries, err := groupingFiles.ReadDir("grouping")
		if err != nil {
			groupingErr = err
			return
		}
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".csv")
			year, err := strconv.Atoi(name)
			if err != nil {
				continue
			}
			data, err := groupingFiles.ReadFile("grouping/" + e.Name())
			if err != nil {
				groupingErr = err
				return
			}
			list, err := parseGroupingCSV(year, data)
			if err != nil {
				groupingErr = fmt.Errorf("accounting: grouping/%s: %w", e.Name(), err)
				return
			}
			groupingLists = append(groupingLists, list)
		}
		sort.Slice(groupingLists, func(i, j int) bool { return groupingLists[i].year < groupingLists[j].year })
	})
	return groupingLists, groupingErr
}

// parseGroupingCSV reads Skatteetaten's list: UTF-8 with a byte order mark,
// semicolons, GroupingCategory;CategoryDescriptionNOB;CategoryDescriptionENG;
// GroupingCode;CodeDescriptionNOB;CodeDescriptionENG.
func parseGroupingCSV(year int, data []byte) (groupingList, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.Comma = ';'
	r.FieldsPerRecord = -1
	list := groupingList{year: year, byCode: map[string]groupingCode{}}
	first := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return list, err
		}
		if first {
			first = false
			continue
		}
		if len(rec) < 6 || strings.TrimSpace(rec[3]) == "" {
			continue
		}
		c := groupingCode{Category: strings.TrimSpace(rec[0]), Code: strings.TrimSpace(rec[3]),
			NameNb: strings.TrimSpace(rec[4]), NameEn: strings.TrimSpace(rec[5])}
		list.codes = append(list.codes, c)
		if _, seen := list.byCode[c.Code]; !seen && !bankingCategory(c.Category) {
			list.byCode[c.Code] = c
		}
	}
	return list, nil
}

// groupingsFor is the list in force for year: the latest not after it, else
// the earliest.
func groupingsFor(year int) (groupingList, error) {
	lists, err := loadGroupings()
	if err != nil || len(lists) == 0 {
		return groupingList{}, fmt.Errorf("accounting: no grouping list: %w", err)
	}
	chosen := lists[0]
	for _, l := range lists {
		if l.year <= year {
			chosen = l
		}
	}
	return chosen, nil
}

// validGrouping reports whether category and code are a pair of the list in
// force for year.
func validGrouping(year int, category, code string) bool {
	list, err := groupingsFor(year)
	if err != nil {
		return false
	}
	for _, c := range list.codes {
		if c.Category == category && c.Code == code {
			return true
		}
	}
	return false
}

// chartAccount is one account of the default chart. code is its grouping
// code (its own number unless the list has none); nb and en override the
// list's names, and are set only where the list has no code of the number.
type chartAccount struct {
	number, code string
	role         string
	taxCode      string
	nb, en       string
}

// defaultChart is the default chart (A3): what a small Norwegian AS needs, in
// the four-digit space Norwegian accountants know.
var defaultChart = []chartAccount{
	// Fixed assets.
	{number: "1070"}, {number: "1080"}, {number: "1150"}, {number: "1205"}, {number: "1238"}, {number: "1280"},
	{number: "1290"}, {number: "1350"}, {number: "1370"}, {number: "1380"}, {number: "1390"},
	// Current assets.
	{number: "1400"},
	{number: "1500", role: "receivables"},
	{number: "1530"}, {number: "1565"}, {number: "1570"},
	{number: "1580", code: "1570", role: "clearing_vipps", nb: "Vipps-oppgjør", en: "Vipps clearing"},
	{number: "1780"}, {number: "1810"}, {number: "1900"},
	{number: "1920", role: "bank"},
	{number: "1950"},
	// Equity.
	{number: "2000"}, {number: "2010"}, {number: "2020"}, {number: "2030"},
	{number: "2050", role: "year_result"},
	{number: "2060", code: "2050", role: "opening", nb: "Åpningsbalanse, motkonto", en: "Opening balance, contra account"},
	{number: "2080"},
	// Long-term liabilities.
	{number: "2120"}, {number: "2180"}, {number: "2220"}, {number: "2250"}, {number: "2290"},
	// Current liabilities.
	{number: "2380"},
	{number: "2400", role: "payables"},
	{number: "2500"}, {number: "2510"}, {number: "2600"},
	{number: "2700", code: "2740", nb: "Utgående merverdiavgift, alminnelig sats", en: "Output VAT, regular rate"},
	{number: "2701", code: "2740", nb: "Utgående merverdiavgift, middels sats", en: "Output VAT, middle rate"},
	{number: "2702", code: "2740", nb: "Utgående merverdiavgift, lav sats", en: "Output VAT, low rate"},
	{number: "2703", code: "2740", nb: "Utgående merverdiavgift, råfisk", en: "Output VAT, raw fish"},
	{number: "2705", code: "2740", nb: "Beregnet merverdiavgift, omvendt avgiftsplikt og innførsel", en: "Calculated VAT, reverse charge and import"},
	{number: "2710", code: "2740", nb: "Inngående merverdiavgift, alminnelig sats", en: "Input VAT, regular rate"},
	{number: "2711", code: "2740", nb: "Inngående merverdiavgift, middels sats", en: "Input VAT, middle rate"},
	{number: "2712", code: "2740", nb: "Inngående merverdiavgift, lav sats", en: "Input VAT, low rate"},
	{number: "2713", code: "2740", nb: "Inngående merverdiavgift, råfisk", en: "Input VAT, raw fish"},
	{number: "2714", code: "2740", nb: "Inngående merverdiavgift, innførsel", en: "Input VAT, import"},
	{number: "2715", code: "2740", nb: "Inngående merverdiavgift, omvendt avgiftsplikt", en: "Input VAT, reverse charge"},
	{number: "2740", role: "vat_settlement"},
	{number: "2770"}, {number: "2790"}, {number: "2800"}, {number: "2900"},
	{number: "2910", role: "employee_payables"},
	{number: "2949"}, {number: "2950"}, {number: "2970"}, {number: "2990"},
	// Sales by VAT treatment and other operating income.
	{number: "3000", taxCode: "3"},
	{number: "3010", code: "3000", taxCode: "31", nb: "Salg og uttak med mva-plikt, middels sats", en: "Sales and withdrawals subject to VAT, middle rate"},
	{number: "3020", code: "3000", taxCode: "33", nb: "Salg og uttak med mva-plikt, lav sats", en: "Sales and withdrawals subject to VAT, low rate"},
	{number: "3006", taxCode: "3"}, {number: "3007", taxCode: "3"},
	{number: "3100", taxCode: "5"},
	{number: "3150", code: "3100", taxCode: "52", nb: "Utførsel av varer og tjenester", en: "Export of goods and services"},
	{number: "3160", code: "3100", taxCode: "51", nb: "Innenlandsk omsetning med omvendt avgiftsplikt", en: "Domestic sales, reverse charge"},
	{number: "3200", taxCode: "6"},
	{number: "3400"}, {number: "3600"}, {number: "3700", taxCode: "3"}, {number: "3880"},
	{number: "3900", role: "charge_income", taxCode: "7"},
	// Cost of goods.
	{number: "4005", taxCode: "1"}, {number: "4295"}, {number: "4500", taxCode: "1"},
	// Pay and employer tax.
	{number: "5000"}, {number: "5300"}, {number: "5400"}, {number: "5420"}, {number: "5900", taxCode: "1"},
	// Depreciation and other operating costs.
	{number: "6000"}, {number: "6050"}, {number: "6100", taxCode: "1"}, {number: "6200", taxCode: "1"},
	{number: "6300"}, {number: "6340", taxCode: "1"}, {number: "6350", taxCode: "1"}, {number: "6395", taxCode: "1"},
	{number: "6400", taxCode: "1"}, {number: "6440", taxCode: "1"}, {number: "6500", taxCode: "1"},
	{number: "6600", taxCode: "1"}, {number: "6695", taxCode: "1"}, {number: "6700", taxCode: "1"},
	{number: "6995", taxCode: "1"}, {number: "7000", taxCode: "1"}, {number: "7020", taxCode: "1"}, {number: "7040"},
	{number: "7155"}, {number: "7165", taxCode: "1"}, {number: "7295", taxCode: "1"}, {number: "7330", taxCode: "1"},
	{number: "7350"}, {number: "7370"}, {number: "7400"}, {number: "7420"}, {number: "7440"}, {number: "7490"},
	{number: "7500"}, {number: "7565", taxCode: "1"}, {number: "7600", taxCode: "1"}, {number: "7650", taxCode: "1"},
	{number: "7700", taxCode: "1"},
	{number: "7770", code: "7700", role: "payment_fees", nb: "Bank- og betalingsgebyrer", en: "Bank and payment fees"},
	{number: "7790", code: "7700", role: "rounding", nb: "Øreavrunding og små differanser", en: "Rounding and small differences"},
	{number: "7830", role: "bad_debt"},
	// Finance.
	{number: "8050"}, {number: "8060", role: "currency_gain"}, {number: "8079"}, {number: "8090"},
	{number: "8150"}, {number: "8160", role: "currency_loss"}, {number: "8179"},
	// Tax and the result.
	{number: "8300"}, {number: "8321"}, {number: "8322"}, {number: "8800"},
}

// systemRoles is every role, in the order the contract lists them. Every
// charge payment posts to charge_income; interest income is an ordinary
// account.
var systemRoles = []string{
	"receivables", "payables", "bank", "vat_settlement", "rounding", "bad_debt", "employee_payables", "opening",
	"year_result", "clearing_vipps", "charge_income", "currency_gain", "currency_loss", "payment_fees",
}

// kindOf is an account's kind from its number's class (A3, A25): 1 assets;
// 20 equity, 21–29 liabilities; 3 income; 4–7 expense; 80 financial income,
// 81 and above expense, 88 the result's appropriation with them.
func kindOf(number string) string {
	switch {
	case number[0] == '1':
		return "asset"
	case strings.HasPrefix(number, "20"):
		return "equity"
	case number[0] == '2':
		return "liability"
	case number[0] == '3', strings.HasPrefix(number, "80"):
		return "income"
	default:
		return "expense"
	}
}

// standardTaxCode is one standard tax code as this module seeds it (A5):
// direction, reverse charge, the VAT account's number (and the other side's
// for reverse charge), the 2026 rate, and whether the return reports it.
type standardTaxCode struct {
	code, direction string
	reverseCharge   bool
	vatAccount      string
	counterAccount  string
	rate            string
	notReported     bool
}

// standardTaxCodes is Skatteetaten's whole list with the 2026 rates — 25, 15,
// 12 and 11.11 % for raw fish, Invoices' own (00034). Names come from
// grouping/standard-tax-codes.csv with the rate after them. A
// reverse-charge code without deduction posts its input side onto the line's
// own account (vatAccount empty).
var standardTaxCodes = []standardTaxCode{
	{code: "0", direction: "none", notReported: true},
	{code: "1", direction: "input", vatAccount: "2710", rate: "25.00"},
	{code: "11", direction: "input", vatAccount: "2711", rate: "15.00"},
	{code: "12", direction: "input", vatAccount: "2713", rate: "11.11"},
	{code: "13", direction: "input", vatAccount: "2712", rate: "12.00"},
	{code: "14", direction: "input", vatAccount: "2714", rate: "25.00"},
	{code: "15", direction: "input", vatAccount: "2714", rate: "15.00"},
	{code: "20", direction: "none", notReported: true},
	{code: "21", direction: "none", rate: "25.00", notReported: true},
	{code: "22", direction: "none", rate: "15.00", notReported: true},
	{code: "3", direction: "output", vatAccount: "2700", rate: "25.00"},
	{code: "31", direction: "output", vatAccount: "2701", rate: "15.00"},
	{code: "32", direction: "output", vatAccount: "2703", rate: "11.11"},
	{code: "33", direction: "output", vatAccount: "2702", rate: "12.00"},
	{code: "5", direction: "output", rate: "0.00"},
	{code: "51", direction: "output", rate: "0.00"},
	{code: "52", direction: "output", rate: "0.00"},
	{code: "6", direction: "output", rate: "0.00"},
	{code: "7", direction: "none", notReported: true},
	{code: "81", direction: "input", reverseCharge: true, vatAccount: "2714", counterAccount: "2705", rate: "25.00"},
	{code: "82", direction: "input", reverseCharge: true, counterAccount: "2705", rate: "25.00"},
	{code: "83", direction: "input", reverseCharge: true, vatAccount: "2714", counterAccount: "2705", rate: "15.00"},
	{code: "84", direction: "input", reverseCharge: true, counterAccount: "2705", rate: "15.00"},
	{code: "85", direction: "input", reverseCharge: true, counterAccount: "2705", rate: "0.00"},
	{code: "86", direction: "input", reverseCharge: true, vatAccount: "2715", counterAccount: "2705", rate: "25.00"},
	{code: "87", direction: "input", reverseCharge: true, counterAccount: "2705", rate: "25.00"},
	{code: "88", direction: "input", reverseCharge: true, vatAccount: "2715", counterAccount: "2705", rate: "12.00"},
	{code: "89", direction: "input", reverseCharge: true, counterAccount: "2705", rate: "12.00"},
	{code: "91", direction: "input", reverseCharge: true, vatAccount: "2715", counterAccount: "2705", rate: "25.00"},
	{code: "92", direction: "input", reverseCharge: true, counterAccount: "2705", rate: "25.00"},
}

// taxCodeNames is the standard list's names by code, Norwegian and English.
func taxCodeNames() (map[string][2]string, error) {
	data, err := groupingFiles.ReadFile("grouping/standard-tax-codes.csv")
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.Comma = ';'
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	out := map[string][2]string{}
	for _, rec := range recs[1:] {
		if len(rec) >= 3 {
			out[strings.TrimSpace(rec[0])] = [2]string{strings.TrimSpace(rec[1]), strings.TrimSpace(rec[2])}
		}
	}
	return out, nil
}

// rateLabel is a rate as a name shows it: "25 %", "11,11 %" in Norwegian.
func rateLabel(rate, language string) string {
	label := strings.TrimSuffix(rate, ".00")
	if language == "nb" {
		label = strings.ReplaceAll(label, ".", ",")
	}
	return label + " %"
}

// seedBooks writes the default chart, its groupings, the standard tax codes
// with their 2026 rates and the system roles, named in language (R12), on the
// setup's transaction.
func seedBooks(ctx context.Context, q *store.Queries, language string, booksStart time.Time, now time.Time) error {
	list, err := groupingsFor(booksStart.Year())
	if err != nil {
		return err
	}
	ids := map[string]int32{}
	for _, a := range defaultChart {
		code := a.code
		if code == "" {
			code = a.number
		}
		g, ok := list.byCode[code]
		if !ok {
			return fmt.Errorf("accounting: the default chart's account %s names grouping code %s, which the %d list has not", a.number, code, list.year)
		}
		name := g.NameNb
		if language == "en" {
			name = g.NameEn
		}
		if a.nb != "" {
			name = a.nb
			if language == "en" {
				name = a.en
			}
		}
		var role *string
		if a.role != "" {
			role = ptr(a.role)
		}
		id, err := q.InsertAccount(ctx, store.InsertAccountParams{Number: a.number, Name: name, Kind: kindOf(a.number),
			SystemRole: role, Now: now})
		if err != nil {
			return fmt.Errorf("accounting: seed account %s: %w", a.number, err)
		}
		ids[a.number] = id
		if err := q.InsertGrouping(ctx, store.InsertGroupingParams{AccountID: id, FromYear: int32(booksStart.Year()),
			Category: g.Category, Code: g.Code}); err != nil {
			return err
		}
	}
	names, err := taxCodeNames()
	if err != nil {
		return err
	}
	// The 2026 rates hold from 2026, or from books_start when the books begin
	// earlier: they were the same in 2025.
	rateStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if booksStart.Before(rateStart) {
		rateStart = booksStart
	}
	codes := map[string]int32{}
	for _, t := range standardTaxCodes {
		n := names[t.code]
		name := n[0]
		if language == "en" {
			name = n[1]
		}
		if t.rate != "" && t.rate != "0.00" {
			name += ", " + rateLabel(t.rate, language)
		}
		var vat, counter *int32
		if t.vatAccount != "" {
			vat = ptr(ids[t.vatAccount])
		}
		if t.counterAccount != "" {
			counter = ptr(ids[t.counterAccount])
		}
		id, err := q.InsertTaxCode(ctx, store.InsertTaxCodeParams{Code: t.code, Name: name, Direction: t.direction,
			ReverseCharge: t.reverseCharge, VatAccountID: vat, CounterVatAccountID: counter, Reported: !t.notReported, Now: now})
		if err != nil {
			return fmt.Errorf("accounting: seed tax code %s: %w", t.code, err)
		}
		codes[t.code] = id
		if t.rate != "" {
			if err := q.InsertTaxRate(ctx, store.InsertTaxRateParams{TaxCodeID: id, RatePercent: t.rate,
				ValidFrom: rateStart, Now: now}); err != nil {
				return err
			}
		}
	}
	for _, a := range defaultChart {
		if a.taxCode != "" {
			if err := q.SetDefaultTaxCode(ctx, store.SetDefaultTaxCodeParams{ID: ids[a.number], TaxCodeID: ptr(codes[a.taxCode])}); err != nil {
				return err
			}
		}
	}
	return nil
}
