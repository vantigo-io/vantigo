package accounting

// The specifications (A11, R13): the journal, an account, the customers and
// the suppliers with aged balances, the trial balance, profit and loss and the
// balance sheet — computed from the postings by voucher date when asked for,
// one table shape for all, with when it was produced, and as CSV.

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

// maxJournalDays is the longest period the journal answers.
const maxJournalDays = 366

// maxReportLines is the most postings the journal or an account's
// specification lists; the queries read one more, so a longer one is refused
// (report_too_large) rather than cut short. A variable for the tests.
var maxReportLines = 50000

// tooLarge is the refusal of a report over maxReportLines.
func tooLarge() error {
	return invalidField("The report cannot be made", "to", codeReportTooLarge,
		fmt.Sprintf("More than %d postings: choose a shorter period.", maxReportLines))
}

// reportInput is a report's parameters, the contract's two operations' alike.
type reportInput struct {
	kind                   gen.AccountingReportKind
	from, to               time.Time
	compareFrom, compareTo *time.Time
	accountID, customerID  *int32
	supplierID             *int64
	projectID              *int32
}

// table is a report under construction.
type table struct {
	gen.AccountingReport
	language string
}

// words are a report's words in the books' language.
var words = map[string]map[string]string{
	"en": {
		"journal": "Journal", "account": "Account specification", "customers": "Customers", "suppliers": "Suppliers",
		"trial_balance": "Trial balance", "profit_and_loss": "Profit and loss", "balance_sheet": "Balance sheet",
		"voucher": "Voucher", "date": "Date", "description": "Description", "accountCol": "Account", "text": "Text",
		"taxCode": "Tax code", "debit": "Debit", "credit": "Credit", "balance": "Balance", "name": "Name",
		"opening": "Opening balance", "closing": "Closing balance", "amount": "Amount", "compare": "Comparison",
		"party": "Party", "document": "Document", "documentDate": "Document date", "dueDate": "Due date",
		"notDue": "Not due", "d1": "1–30 days", "d31": "31–60 days", "d61": "61–90 days", "d91": "Over 90 days",
		"total": "Total", "unallocated": "Unallocated", "movement": "Movement in the period",
		"earlierResult": "Earlier years' result, not yet closed",
		"opIncome":      "Operating income", "goods": "Cost of goods", "payroll": "Payroll costs", "otherCosts": "Other operating costs",
		"opResult": "Operating result", "finIncome": "Financial income", "finCosts": "Financial costs",
		"preTax": "Ordinary result before tax", "tax": "Tax and appropriations", "result": "Result",
		"fixedAssets": "Fixed assets", "currentAssets": "Current assets", "assets": "Total assets",
		"equity": "Equity", "yearResult": "Result for the year, not yet closed", "longTerm": "Long-term liabilities",
		"otherAssets": "Other assets", "otherLiabilities": "Other liabilities", "otherResult": "Other income and costs",
		"shortTerm": "Current liabilities", "equityAndLiabilities": "Total equity and liabilities",
		"period": "Period", "produced": "Produced",
	},
	"nb": {
		"journal": "Bokføringsspesifikasjon", "account": "Kontospesifikasjon", "customers": "Kunder", "suppliers": "Leverandører",
		"trial_balance": "Saldobalanse", "profit_and_loss": "Resultatregnskap", "balance_sheet": "Balanse",
		"voucher": "Bilag", "date": "Dato", "description": "Beskrivelse", "accountCol": "Konto", "text": "Tekst",
		"taxCode": "Mva-kode", "debit": "Debet", "credit": "Kredit", "balance": "Saldo", "name": "Navn",
		"opening": "Inngående saldo", "closing": "Utgående saldo", "amount": "Beløp", "compare": "Sammenligning",
		"party": "Part", "document": "Dokument", "documentDate": "Dokumentdato", "dueDate": "Forfall",
		"notDue": "Ikke forfalt", "d1": "1–30 dager", "d31": "31–60 dager", "d61": "61–90 dager", "d91": "Over 90 dager",
		"total": "Sum", "unallocated": "Uspesifisert", "movement": "Bevegelse i perioden",
		"earlierResult": "Tidligere års resultat, ikke avsluttet",
		"opIncome":      "Driftsinntekter", "goods": "Varekostnad", "payroll": "Lønnskostnad", "otherCosts": "Annen driftskostnad",
		"opResult": "Driftsresultat", "finIncome": "Finansinntekter", "finCosts": "Finanskostnader",
		"preTax": "Ordinært resultat før skatt", "tax": "Skatt og disponeringer", "result": "Resultat",
		"fixedAssets": "Anleggsmidler", "currentAssets": "Omløpsmidler", "assets": "Sum eiendeler",
		"equity": "Egenkapital", "yearResult": "Årets resultat, ikke avsluttet", "longTerm": "Langsiktig gjeld",
		"otherAssets": "Andre eiendeler", "otherLiabilities": "Annen gjeld", "otherResult": "Andre inntekter og kostnader",
		"shortTerm": "Kortsiktig gjeld", "equityAndLiabilities": "Sum egenkapital og gjeld",
		"period": "Periode", "produced": "Laget",
	},
}

func (t *table) w(key string) string { return words[t.language][key] }

func (t *table) column(key, label string, typ gen.AccountingReportColumnType) {
	t.Columns = append(t.Columns, gen.AccountingReportColumn{Key: key, Label: label, Type: typ})
}

func (t *table) row(style gen.AccountingReportRowStyle, depth int32, cells map[string]string) *gen.AccountingReportRow {
	t.Rows = append(t.Rows, gen.AccountingReportRow{Style: style, Depth: depth, Cells: cells})
	return &t.Rows[len(t.Rows)-1]
}

// GetAccountingReport is one report.
func (s *server) GetAccountingReport(ctx context.Context, req gen.GetAccountingReportRequestObject) (gen.GetAccountingReportResponseObject, error) {
	p := req.Params
	t, err := s.report(ctx, reportInput{kind: req.Report, from: day(p.From), to: day(p.To), compareFrom: datePtr(p.CompareFrom),
		compareTo: datePtr(p.CompareTo), accountID: p.AccountId, customerID: p.CustomerId, supplierID: p.SupplierId, projectID: p.ProjectId})
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingReport200JSONResponse(t.AccountingReport), nil
}

// csvDownload serves a report's file as an attachment, never cached.
type csvDownload struct {
	name string
	body gen.ExportAccountingReport200TextcsvResponse
}

func (r csvDownload) VisitExportAccountingReportResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.name+`"`)
	return r.body.VisitExportAccountingReportResponse(w)
}

// ExportAccountingReport is one report as CSV: semicolons, a byte order mark,
// CRLF, the decimal comma and the formula guard on text — the form a
// Norwegian spreadsheet opens without asking, Invoices' own.
func (s *server) ExportAccountingReport(ctx context.Context, req gen.ExportAccountingReportRequestObject) (gen.ExportAccountingReportResponseObject, error) {
	p := req.Params
	t, err := s.report(ctx, reportInput{kind: req.Report, from: day(p.From), to: day(p.To), compareFrom: datePtr(p.CompareFrom),
		compareTo: datePtr(p.CompareTo), accountID: p.AccountId, customerID: p.CustomerId, supplierID: p.SupplierId, projectID: p.ProjectId})
	if err != nil {
		return nil, err
	}
	data := t.csv()
	name := fmt.Sprintf("%s-%s-%s.csv", strings.ReplaceAll(string(t.Kind), "_", "-"), t.From.Format(time.DateOnly), t.To.Format(time.DateOnly))
	return csvDownload{name: name, body: gen.ExportAccountingReport200TextcsvResponse{Body: bytes.NewReader(data),
		ContentLength: int64(len(data))}}, nil
}

// report builds a report from the postings, in one snapshot.
func (s *server) report(ctx context.Context, in reportInput) (*table, error) {
	if in.from.After(in.to) {
		return nil, invalidField("The report cannot be made", "from", codePeriodInvalid, "from is on or before to.")
	}
	if in.compareFrom != nil && in.compareTo != nil && in.compareFrom.After(*in.compareTo) {
		return nil, invalidField("The report cannot be made", "compareFrom", codePeriodInvalid, "compareFrom is on or before compareTo.")
	}
	if (in.compareFrom == nil) != (in.compareTo == nil) {
		return nil, invalidField("The report cannot be made", "compareTo", codePeriodInvalid, "A comparison has both its days.")
	}
	if in.kind == gen.Journal && in.to.Sub(in.from) > maxJournalDays*24*time.Hour {
		return nil, invalidField("The report cannot be made", "to", codePeriodInvalid, "The journal covers at most 366 days.")
	}
	if in.kind == gen.Account && in.accountID == nil {
		return nil, invalidField("The report cannot be made", "accountId", codeReportAccountNeeded, "Name the account.")
	}
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	t := &table{language: settings.ChartLanguage}
	t.Kind, t.From, t.To = in.kind, apiDate(in.from), apiDate(in.to)
	t.CompareFrom, t.CompareTo = apiDatePtr(in.compareFrom), apiDatePtr(in.compareTo)
	t.Columns, t.Rows = []gen.AccountingReportColumn{}, []gen.AccountingReportRow{}
	t.Title = t.w(string(in.kind))
	month := int32(settings.FiscalYearStartMonth)
	var parties []int32
	err = s.withReadTx(ctx, func(q *store.Queries) error {
		switch in.kind {
		case gen.Journal:
			return t.journal(ctx, q, in)
		case gen.Account:
			return t.accountSpec(ctx, q, in, month)
		case gen.Customers, gen.Suppliers:
			var err error
			parties, err = t.parties(ctx, q, in)
			return err
		case gen.TrialBalance:
			return t.trialBalance(ctx, q, in, month)
		case gen.ProfitAndLoss:
			return t.profitAndLoss(ctx, q, in, month)
		case gen.BalanceSheet:
			return t.balanceSheet(ctx, q, in, month)
		}
		return invalidField("The report cannot be made", "report", "", "No such report.")
	})
	if err != nil {
		return nil, err
	}
	if len(parties) > 0 {
		names := s.customerNames(ctx, parties)
		for i := range t.Rows {
			if id, ok := t.Rows[i].Cells["customerId"]; ok {
				var n int32
				_, _ = fmt.Sscan(id, &n)
				if name, ok := names[n]; ok {
					t.Rows[i].Cells["party"] = id + " " + name
				}
				delete(t.Rows[i].Cells, "customerId")
			}
		}
	}
	t.ProducedAt = s.now()
	return t, nil
}

// debitCredit splits a signed amount into its debit and credit cells.
func debitCredit(cells map[string]string, amount *big.Rat) {
	if amount.Sign() >= 0 {
		cells["debit"], cells["credit"] = money(amount), ""
	} else {
		cells["debit"], cells["credit"] = "", money(neg(amount))
	}
}

func (t *table) journal(ctx context.Context, q *store.Queries, in reportInput) error {
	t.column("voucher", t.w("voucher"), gen.Text)
	t.column("date", t.w("date"), gen.Date)
	t.column("description", t.w("description"), gen.Text)
	t.column("account", t.w("accountCol"), gen.Text)
	t.column("taxCode", t.w("taxCode"), gen.Text)
	t.column("text", t.w("text"), gen.Text)
	t.column("debit", t.w("debit"), gen.Amount)
	t.column("credit", t.w("credit"), gen.Amount)
	rows, err := q.JournalPostings(ctx, store.JournalPostingsParams{FromDate: in.from, ToDate: in.to, ProjectID: in.projectID,
		RowLimit: int32(maxReportLines + 1)})
	if err != nil {
		return err
	}
	if len(rows) > maxReportLines {
		return tooLarge()
	}
	debit, credit := new(big.Rat), new(big.Rat)
	for _, r := range rows {
		a := mustRat(r.Amount)
		if a.Sign() > 0 {
			debit.Add(debit, a)
		} else {
			credit.Sub(credit, a)
		}
		cells := map[string]string{"voucher": display(r.FiscalYear, r.Number), "date": r.VoucherDate.Format(time.DateOnly),
			"description": r.VoucherDescription, "account": r.AccountNumber + " " + r.AccountName, "taxCode": deref(r.TaxCode),
			"text": r.Description}
		debitCredit(cells, a)
		row := t.row(gen.Line, 0, cells)
		row.VoucherId = ptr(r.VoucherID)
	}
	t.row(gen.Total, 0, map[string]string{"voucher": t.w("total"), "debit": money(debit), "credit": money(credit)})
	return nil
}

func (t *table) accountSpec(ctx context.Context, q *store.Queries, in reportInput, month int32) error {
	a, err := q.GetAccount(ctx, *in.accountID)
	if noRows(err) {
		return invalidField("The report cannot be made", "accountId", codeAccountUnknown, "No such account.")
	}
	if err != nil {
		return err
	}
	t.Title += ": " + a.Number + " " + a.Name
	t.column("date", t.w("date"), gen.Date)
	t.column("voucher", t.w("voucher"), gen.Text)
	t.column("description", t.w("description"), gen.Text)
	t.column("text", t.w("text"), gen.Text)
	t.column("debit", t.w("debit"), gen.Amount)
	t.column("credit", t.w("credit"), gen.Amount)
	t.column("balance", t.w("balance"), gen.Amount)
	opening, err := q.AccountOpening(ctx, store.AccountOpeningParams{AccountID: a.ID, FromDate: in.from,
		FromYearStart: a.Kind == "income" || a.Kind == "expense", YearStart: fiscalYearStart(in.from, month), ProjectID: in.projectID})
	if err != nil {
		return err
	}
	balance := mustRat(opening)
	t.row(gen.Subtotal, 0, map[string]string{"description": t.w("opening"), "balance": money(balance)}).AccountId = &a.ID
	rows, err := q.AccountPostings(ctx, store.AccountPostingsParams{AccountID: a.ID, FromDate: in.from, ToDate: in.to, ProjectID: in.projectID,
		RowLimit: int32(maxReportLines + 1)})
	if err != nil {
		return err
	}
	if len(rows) > maxReportLines {
		return tooLarge()
	}
	debit, credit := new(big.Rat), new(big.Rat)
	for _, r := range rows {
		amount := mustRat(r.Amount)
		balance.Add(balance, amount)
		if amount.Sign() > 0 {
			debit.Add(debit, amount)
		} else {
			credit.Sub(credit, amount)
		}
		cells := map[string]string{"date": r.VoucherDate.Format(time.DateOnly), "voucher": display(r.FiscalYear, r.Number),
			"description": r.VoucherDescription, "text": r.Description, "balance": money(balance)}
		debitCredit(cells, amount)
		t.row(gen.Line, 0, cells).VoucherId = ptr(r.VoucherID)
	}
	t.row(gen.Total, 0, map[string]string{"description": t.w("closing"), "debit": money(debit), "credit": money(credit),
		"balance": money(balance)}).AccountId = &a.ID
	return nil
}

// agedBucket is the aged-balance column of an item due on due at day.
func agedBucket(due *time.Time, at time.Time) string {
	if due == nil || !at.After(*due) {
		return "notDue"
	}
	days := int(at.Sub(*due).Hours() / 24)
	switch {
	case days <= 30:
		return "d1"
	case days <= 60:
		return "d31"
	case days <= 90:
		return "d61"
	default:
		return "d91"
	}
}

var agedColumns = []string{"notDue", "d1", "d31", "d61", "d91"}

// parties is the customers or the suppliers report: every item with a balance
// at to, or a movement in the period, aged by its due date at to, per party.
// It answers the customers to name.
func (t *table) parties(ctx context.Context, q *store.Queries, in reportInput) ([]int32, error) {
	kind := "customer"
	if in.kind == gen.Suppliers {
		kind = "supplier"
	}
	t.column("party", t.w("party"), gen.Text)
	t.column("document", t.w("document"), gen.Text)
	t.column("documentDate", t.w("documentDate"), gen.Date)
	t.column("dueDate", t.w("dueDate"), gen.Date)
	t.column("movement", t.w("movement"), gen.Amount)
	t.column("balance", t.w("balance"), gen.Amount)
	for _, c := range agedColumns {
		t.column(c, t.w(c), gen.Amount)
	}
	rows, err := q.PartyItemsAt(ctx, store.PartyItemsAtParams{Kind: kind, FromDate: in.from, ToDate: in.to,
		CustomerID: in.customerID, SupplierID: in.supplierID, ProjectID: in.projectID})
	if err != nil {
		return nil, err
	}
	var ids []int32
	total := map[string]*big.Rat{}
	partyTotal := map[string]*big.Rat{}
	var current int64 = -1
	flush := func() {
		if current < 0 {
			return
		}
		cells := map[string]string{"document": t.w("total")}
		for k, v := range partyTotal {
			cells[k] = money(v)
		}
		t.row(gen.Subtotal, 0, cells)
	}
	accumulate := func(m map[string]*big.Rat, key string, v *big.Rat) {
		if m[key] == nil {
			m[key] = new(big.Rat)
		}
		m[key].Add(m[key], v)
	}
	for _, r := range rows {
		party := deref(r.SupplierID)
		if r.CustomerID != nil {
			party = int64(*r.CustomerID)
		}
		if party != current {
			flush()
			current = party
			partyTotal = map[string]*big.Rat{}
			cells := map[string]string{"party": fmt.Sprint(party)}
			if r.CustomerID != nil {
				ids = append(ids, *r.CustomerID)
				cells["customerId"] = fmt.Sprint(party)
			}
			t.row(gen.Heading, 0, cells)
		}
		balance, movement := mustRat(r.Balance), mustRat(r.Movement)
		document := deref(r.DocumentNumber)
		if r.Unallocated {
			document = t.w("unallocated")
		}
		cells := map[string]string{"document": document, "balance": money(balance), "movement": money(movement)}
		if r.DocumentDate != nil {
			cells["documentDate"] = r.DocumentDate.Format(time.DateOnly)
		}
		if r.DueDate != nil {
			cells["dueDate"] = r.DueDate.Format(time.DateOnly)
		}
		bucket := agedBucket(r.DueDate, in.to)
		cells[bucket] = money(balance)
		t.row(gen.Line, 1, cells)
		for _, m := range []map[string]*big.Rat{partyTotal, total} {
			accumulate(m, "balance", balance)
			accumulate(m, "movement", movement)
			accumulate(m, bucket, balance)
		}
	}
	flush()
	cells := map[string]string{"party": t.w("total")}
	for k, v := range total {
		cells[k] = money(v)
	}
	t.row(gen.Total, 0, cells)
	return ids, nil
}

// totalsAt is AccountTotals for a period, by account id.
func totalsAt(ctx context.Context, q *store.Queries, from, to time.Time, month int32, project *int32) ([]store.AccountTotalsRow, error) {
	return q.AccountTotals(ctx, store.AccountTotalsParams{FromDate: from, ToDate: to, YearStart: fiscalYearStart(from, month), ProjectID: project})
}

func (t *table) trialBalance(ctx context.Context, q *store.Queries, in reportInput, month int32) error {
	t.column("account", t.w("accountCol"), gen.Text)
	t.column("name", t.w("name"), gen.Text)
	t.column("opening", t.w("opening"), gen.Amount)
	t.column("debit", t.w("debit"), gen.Amount)
	t.column("credit", t.w("credit"), gen.Amount)
	t.column("closing", t.w("closing"), gen.Amount)
	rows, err := totalsAt(ctx, q, in.from, in.to, month, in.projectID)
	if err != nil {
		return err
	}
	compare := map[int32]string{}
	if in.compareFrom != nil {
		t.column("compare", t.w("compare"), gen.Amount)
		cmp, err := totalsAt(ctx, q, *in.compareFrom, *in.compareTo, month, in.projectID)
		if err != nil {
			return err
		}
		for _, r := range cmp {
			compare[r.ID] = money(mustRat(r.Closing))
		}
	}
	sums := map[string]*big.Rat{"opening": new(big.Rat), "debit": new(big.Rat), "credit": new(big.Rat), "closing": new(big.Rat)}
	for _, r := range rows {
		cells := map[string]string{"account": r.Number, "name": r.Name, "opening": money(mustRat(r.Opening)),
			"debit": money(mustRat(r.Debit)), "credit": money(mustRat(r.Credit)), "closing": money(mustRat(r.Closing))}
		if in.compareFrom != nil {
			cells["compare"] = compare[r.ID]
		}
		for _, k := range []string{"opening", "debit", "credit", "closing"} {
			sums[k].Add(sums[k], mustRat(map[string]string{"opening": r.Opening, "debit": r.Debit, "credit": r.Credit, "closing": r.Closing}[k]))
		}
		row := t.row(gen.Line, 0, cells)
		row.AccountId = ptr(r.ID)
	}
	// The income and expense accounts open at the fiscal year's start, so the
	// result of the years before, not yet closed to equity, is its own line.
	earlier, err := q.UnclosedResult(ctx, store.UnclosedResultParams{ToDate: fiscalYearStart(in.from, month).AddDate(0, 0, -1), ProjectID: in.projectID})
	if err != nil {
		return err
	}
	if e := mustRat(earlier); e.Sign() != 0 {
		t.row(gen.Line, 0, map[string]string{"name": t.w("earlierResult"), "opening": money(e), "closing": money(e)})
		sums["opening"].Add(sums["opening"], e)
		sums["closing"].Add(sums["closing"], e)
	}
	t.row(gen.Total, 0, map[string]string{"name": t.w("total"), "opening": money(sums["opening"]), "debit": money(sums["debit"]),
		"credit": money(sums["credit"]), "closing": money(sums["closing"])})
	return nil
}

// statementGroup is a group of a statement's accounts by number prefix. The
// group with other set takes every account no other group of the statement
// does, so none is left out (M3); it is shown only when it has one.
type statementGroup struct {
	key      string
	prefixes []string
	other    bool
}

func inGroup(number string, g statementGroup) bool {
	for _, p := range g.prefixes {
		if strings.HasPrefix(number, p) {
			return true
		}
	}
	return false
}

// groupOf is the group of groups an account number falls in.
func groupOf(number string, groups []statementGroup) string {
	other := ""
	for _, g := range groups {
		if g.other {
			other = g.key
		} else if inGroup(number, g) {
			return g.key
		}
	}
	return other
}

// statement adds groups of accounts, each line's amount from value, with a
// subtotal after each group and, after the groups named in after, a running
// total under its own name. It answers the grand total.
func (t *table) statement(groups []statementGroup, after map[string]string, rows []store.AccountTotalsRow,
	value func(store.AccountTotalsRow) *big.Rat, compare map[int32]*big.Rat, withCompare bool) (*big.Rat, *big.Rat) {
	running, runningCmp := new(big.Rat), new(big.Rat)
	for _, g := range groups {
		sum, sumCmp := new(big.Rat), new(big.Rat)
		var lines []store.AccountTotalsRow
		for _, r := range rows {
			if groupOf(r.Number, groups) == g.key {
				lines = append(lines, r)
			}
		}
		if g.other && len(lines) == 0 {
			if label, ok := after[g.key]; ok {
				cells := map[string]string{"name": t.w(label), "amount": money(running)}
				if withCompare {
					cells["compare"] = money(runningCmp)
				}
				t.row(gen.Total, 0, cells)
			}
			continue
		}
		if len(lines) > 0 || withCompare {
			t.row(gen.Heading, 0, map[string]string{"name": t.w(g.key)})
		}
		for _, r := range lines {
			v := value(r)
			if v.Sign() == 0 && (compare[r.ID] == nil || compare[r.ID].Sign() == 0) {
				continue
			}
			sum.Add(sum, v)
			cells := map[string]string{"account": r.Number, "name": r.Name, "amount": money(v)}
			if withCompare {
				c := compare[r.ID]
				if c == nil {
					c = new(big.Rat)
				}
				sumCmp.Add(sumCmp, c)
				cells["compare"] = money(c)
			}
			t.row(gen.Line, 1, cells).AccountId = ptr(r.ID)
		}
		cells := map[string]string{"name": t.w("total") + " " + strings.ToLower(t.w(g.key)), "amount": money(sum)}
		if withCompare {
			cells["compare"] = money(sumCmp)
		}
		t.row(gen.Subtotal, 0, cells)
		running.Add(running, sum)
		runningCmp.Add(runningCmp, sumCmp)
		if label, ok := after[g.key]; ok {
			cells := map[string]string{"name": t.w(label), "amount": money(running)}
			if withCompare {
				cells["compare"] = money(runningCmp)
			}
			t.row(gen.Total, 0, cells)
		}
	}
	return running, runningCmp
}

func (t *table) statementColumns(withCompare bool) {
	t.column("account", t.w("accountCol"), gen.Text)
	t.column("name", t.w("name"), gen.Text)
	t.column("amount", t.w("amount"), gen.Amount)
	if withCompare {
		t.column("compare", t.w("compare"), gen.Amount)
	}
}

// profitAndLoss is the period's movement on the income and expense accounts by
// class, shown with income positive and costs negative.
func (t *table) profitAndLoss(ctx context.Context, q *store.Queries, in reportInput, month int32) error {
	withCompare := in.compareFrom != nil
	t.statementColumns(withCompare)
	rows, err := totalsAt(ctx, q, in.from, in.to, month, in.projectID)
	if err != nil {
		return err
	}
	value := func(r store.AccountTotalsRow) *big.Rat { return neg(mustRat(r.Movement)) }
	compare := map[int32]*big.Rat{}
	if withCompare {
		cmp, err := totalsAt(ctx, q, *in.compareFrom, *in.compareTo, month, in.projectID)
		if err != nil {
			return err
		}
		for _, r := range cmp {
			if r.Kind == "income" || r.Kind == "expense" {
				compare[r.ID] = value(r)
			}
		}
	}
	var pl []store.AccountTotalsRow
	for _, r := range rows {
		if r.Kind == "income" || r.Kind == "expense" {
			pl = append(pl, r)
		}
	}
	groups := []statementGroup{
		{key: "opIncome", prefixes: []string{"3"}}, {key: "goods", prefixes: []string{"4"}}, {key: "payroll", prefixes: []string{"5"}},
		{key: "otherCosts", prefixes: []string{"6", "7"}}, {key: "finIncome", prefixes: []string{"80"}},
		{key: "finCosts", prefixes: []string{"81"}}, {key: "tax", prefixes: []string{"82", "83", "84", "85", "86", "87", "88", "89"}},
		{key: "otherResult", other: true},
	}
	t.statement(groups, map[string]string{"otherCosts": "opResult", "finCosts": "preTax", "otherResult": "result"}, pl, value, compare, withCompare)
	return nil
}

// balanceSheet is the balances at to: the asset accounts as they stand, the
// equity and liability accounts shown positive, each side by its accounts'
// kind and grouped by class, with an Other group for an account no group
// takes, so the sheet always balances (M3). The result not yet closed is two
// computed lines in equity (R13, M2): the earlier fiscal years' and this
// one's.
func (t *table) balanceSheet(ctx context.Context, q *store.Queries, in reportInput, month int32) error {
	withCompare := in.compareFrom != nil
	t.statementColumns(withCompare)
	type side struct {
		assets, equity, debts []store.AccountTotalsRow
		earlier, year         *big.Rat
	}
	at := func(to time.Time) (side, error) {
		var out side
		rows, err := totalsAt(ctx, q, to, to, month, in.projectID)
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			switch r.Kind {
			case "asset":
				out.assets = append(out.assets, r)
			case "equity":
				out.equity = append(out.equity, r)
			case "liability":
				out.debts = append(out.debts, r)
			}
		}
		yearStart := fiscalYearStart(to, month)
		earlier, err := q.UnclosedResult(ctx, store.UnclosedResultParams{ToDate: yearStart.AddDate(0, 0, -1), ProjectID: in.projectID})
		if err != nil {
			return out, err
		}
		total, err := q.UnclosedResult(ctx, store.UnclosedResultParams{ToDate: to, ProjectID: in.projectID})
		if err != nil {
			return out, err
		}
		out.earlier = neg(mustRat(earlier))
		out.year = sub(neg(mustRat(total)), out.earlier)
		return out, nil
	}
	cur, err := at(in.to)
	if err != nil {
		return err
	}
	var cmp side
	if withCompare {
		if cmp, err = at(*in.compareTo); err != nil {
			return err
		}
	} else {
		cmp = side{earlier: new(big.Rat), year: new(big.Rat)}
	}
	assetValue := func(r store.AccountTotalsRow) *big.Rat { return mustRat(r.Closing) }
	claimValue := func(r store.AccountTotalsRow) *big.Rat { return neg(mustRat(r.Closing)) }
	byID := func(rows []store.AccountTotalsRow, value func(store.AccountTotalsRow) *big.Rat) map[int32]*big.Rat {
		out := map[int32]*big.Rat{}
		for _, r := range rows {
			out[r.ID] = value(r)
		}
		return out
	}
	total := func(name string, amount, compare *big.Rat) {
		cells := map[string]string{"name": name, "amount": money(amount)}
		if withCompare {
			cells["compare"] = money(compare)
		}
		t.row(gen.Total, 0, cells)
	}
	assets, assetsCmp := t.statement([]statementGroup{
		{key: "fixedAssets", prefixes: []string{"10", "11", "12", "13"}},
		{key: "currentAssets", prefixes: []string{"14", "15", "16", "17", "18", "19"}},
		{key: "otherAssets", other: true},
	}, nil, cur.assets, assetValue, byID(cmp.assets, assetValue), withCompare)
	total(t.w("assets"), assets, assetsCmp)

	// Equity: its accounts, then the two computed result lines, then its
	// subtotal.
	equity, equityCmp := new(big.Rat), new(big.Rat)
	t.row(gen.Heading, 0, map[string]string{"name": t.w("equity")})
	equityCompare := byID(cmp.equity, claimValue)
	line := func(account *store.AccountTotalsRow, name string, amount, compare *big.Rat) {
		if compare == nil {
			compare = new(big.Rat)
		}
		equity.Add(equity, amount)
		equityCmp.Add(equityCmp, compare)
		c := map[string]string{"name": name, "amount": money(amount)}
		if withCompare {
			c["compare"] = money(compare)
		}
		row := t.row(gen.Line, 1, c)
		if account != nil {
			row.Cells["account"] = account.Number
			row.AccountId = ptr(account.ID)
		}
	}
	for i, r := range cur.equity {
		v := claimValue(r)
		if v.Sign() == 0 && (equityCompare[r.ID] == nil || equityCompare[r.ID].Sign() == 0) {
			continue
		}
		line(&cur.equity[i], r.Name, v, equityCompare[r.ID])
	}
	line(nil, t.w("earlierResult"), cur.earlier, cmp.earlier)
	line(nil, t.w("yearResult"), cur.year, cmp.year)
	c := map[string]string{"name": t.w("total") + " " + strings.ToLower(t.w("equity")), "amount": money(equity)}
	if withCompare {
		c["compare"] = money(equityCmp)
	}
	t.row(gen.Subtotal, 0, c)
	debts, debtsCmp := t.statement([]statementGroup{
		{key: "longTerm", prefixes: []string{"21", "22"}},
		{key: "shortTerm", prefixes: []string{"23", "24", "25", "26", "27", "28", "29"}},
		{key: "otherLiabilities", other: true},
	}, nil, cur.debts, claimValue, byID(cmp.debts, claimValue), withCompare)
	total(t.w("equityAndLiabilities"), add(equity, debts), add(equityCmp, debtsCmp))
	return nil
}

// csv is the report as the file: its title, period and production time, a
// blank line, the header and the rows.
func (t *table) csv() []byte {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, []csvValue{{text: t.Title, guard: true}})
	writeCSVRow(&b, []csvValue{{text: t.w("period")}, {text: t.From.Format(time.DateOnly)}, {text: t.To.Format(time.DateOnly)}})
	writeCSVRow(&b, []csvValue{{text: t.w("produced")}, {text: t.ProducedAt.UTC().Format(time.RFC3339)}})
	b.WriteString(csvLineEnd)
	header := make([]csvValue, 0, len(t.Columns))
	for _, c := range t.Columns {
		header = append(header, csvValue{text: c.Label, guard: true})
	}
	writeCSVRow(&b, header)
	for _, r := range t.Rows {
		cells := make([]csvValue, 0, len(t.Columns))
		for _, c := range t.Columns {
			v := r.Cells[c.Key]
			if c.Type == gen.Amount {
				cells = append(cells, csvValue{text: strings.Replace(v, ".", ",", 1)})
			} else {
				cells = append(cells, csvValue{text: v, guard: c.Type == gen.Text})
			}
		}
		writeCSVRow(&b, cells)
	}
	return b.Bytes()
}
