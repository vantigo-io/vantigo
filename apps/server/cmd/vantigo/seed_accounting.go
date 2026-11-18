package main

// The demo seed's books (accounting design; the reference's Set up the books
// onward): Accounting set up on the demo company, its opening balances, the
// demo's sales and expenses posted by the catch-up, suppliers and their
// invoices, the VAT terms that have ended closed and filed, and a bank
// statement of the last full month half reconciled — every write through the
// module's own operations (accounting.Operations and the bank's entry
// points), so the seed cannot write books the module's rules would refuse.
//
// It runs after the demo's one transaction has committed, once, on a fresh
// seed: seedAccountingSources adds what Invoices and Expenses lack inside that
// transaction, in their own seed style; seedDemoAccounting writes the books.

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"mime/multipart"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// demoExpenseCategories maps the demo expenses' descriptions (seedTimeAndExpenses)
// to Expenses' seeded categories, so the expense rules have something to place.
var demoExpenseCategories = map[string]string{
	"Flyreise Oslo–Bergen i forbindelse med kundeworkshop":    "Travel",
	"Hotellopphold under prosjektoppstart hos kunde":          "Accommodation",
	"Faglunsj og bevertning under styringsgruppemøte":         "Meals",
	"Programvarelisens og testsertifikater for prosjektmiljø": "Other",
}

// demoWrittenOffInvoice is the overdue demo invoice (seedInvoicesReceivables'
// index 23, the oldest overdue) that is written off as a loss with the VAT
// correction and then partly recovered.
const demoWrittenOffInvoice = int64(10001 + 23)

// seedAccountingSources writes, on the demo's transaction and in the Invoices
// and Expenses seeds' own style, what the books are to show beyond the plain
// demo: the demo expenses' categories, one approved expense in euros (which
// waits under Needs attention for its NOK amount), and a write-off of the
// oldest overdue invoice as a loss with its VAT corrected, partly recovered.
// The modules' journal triggers turn them into facts at commit.
func seedAccountingSources(ctx context.Context, tx pgx.Tx, now time.Time, userIDs []uuid.UUID) error {
	ownerID := userIDs[0]
	for description, category := range demoExpenseCategories {
		if _, err := tx.Exec(ctx, `
			UPDATE expenses.entries
			SET category_id = (SELECT id FROM expenses.categories WHERE name = $2)
			WHERE description = $1 AND category_id IS NULL
		`, description, category); err != nil {
			return err
		}
	}

	// A conference fee paid in euros: Expenses has no rate, so the books wait
	// for a person to enter its NOK amount.
	day := now.Format(time.DateOnly)
	if _, err := tx.Exec(ctx, `
		INSERT INTO expenses.entries (
			user_id, created_by_user_id, kind, entry_date, description, category_id,
			status, billable, markup_percent,
			gross_amount, currency,
			paid_by, supplier,
			submitted_at, decided_at, decided_by_user_id,
			revision, created_at, updated_at
		) VALUES (
			$1, $1, 'outlay', $2::date, 'Deltakeravgift Nordic Cloud Summit, Berlin',
			(SELECT id FROM expenses.categories WHERE name = 'Other'),
			'approved', false, 0,
			80.00, 'EUR',
			'employee', 'Nordic Cloud Summit e.V.',
			$3, $3, $4,
			1, $3, $3
		)
	`, userIDs[6], day, now, ownerID); err != nil {
		return err
	}

	// The write-off: the invoice's whole open amount, ten days ago, as a loss
	// with the VAT correction; a third of it came in a week later.
	var invoiceID int64
	var gross float64
	if err := tx.QueryRow(ctx, `SELECT id, gross_total FROM invoices.invoices WHERE number = $1`,
		demoWrittenOffInvoice).Scan(&invoiceID, &gross); err != nil {
		return fmt.Errorf("the invoice to write off: %w", err)
	}
	var writeOffID int64
	writtenOff := now.AddDate(0, 0, -10)
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices.write_offs (
			invoice_id, written_off_on, amount, amount_nok, currency, reason, vat_correction, registered_by_user_id, registered_at
		) VALUES ($1, $2::date, $3, $3, 'NOK', 'loss', true, $4, $5)
		RETURNING id
	`, invoiceID, writtenOff.Format(time.DateOnly), gross, ownerID, writtenOff).Scan(&writeOffID); err != nil {
		return err
	}
	recovered := now.AddDate(0, 0, -3)
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoices.payments (
			invoice_id, paid_on, amount, amount_nok, currency, source, reference, note, registered_by_user_id, registered_at,
			kind, write_off_id
		) VALUES ($1, $2::date, round($3::numeric / 3, 2), round($3::numeric / 3, 2), 'NOK', 'manual', 'INKASSO-1', 'Delvis innfridd via inkasso', $4, $5,
			'recovered', $6)
	`, invoiceID, recovered.Format(time.DateOnly), gross, ownerID, recovered, writeOffID); err != nil {
		return err
	}
	return nil
}

// demoBooks is one run of the books' seed: the module's operations, acting
// as the owner, and what it has learnt of the chart on the way.
type demoBooks struct {
	deps     module.Deps
	ops      gen.StrictServerInterface
	ctx      context.Context
	logger   *slog.Logger
	owner    uuid.UUID
	today    time.Time
	start    time.Time
	accounts map[string]int32
	taxCodes map[string]int32
	// paidTerms are the VAT terms whose payment is already in the books.
	paidTerms map[string]bool
	counts    map[string]int
}

// seedDemoAccounting sets up and fills the demo's books. It does nothing when
// Accounting or Invoices is not enabled, when Invoices holds no issued
// invoice, or when the books are already set up; without an object store the
// supplier invoices and the bank statement, which need one for their files,
// are left out and said so.
func seedDemoAccounting(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg *config.Config, now time.Time) error {
	if !slices.Contains(cfg.Modules, "accounting") || !slices.Contains(cfg.Modules, "invoices") {
		logger.Info("accounting demo: the accounting or invoices module is not enabled; skipping the books")
		return nil
	}
	var issued, setUp bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM invoices.invoices WHERE status = 'issued'),
		EXISTS (SELECT 1 FROM accounting.settings)`).Scan(&issued, &setUp); err != nil {
		return fmt.Errorf("check the books' state: %w", err)
	}
	if !issued || setUp {
		logger.Info("accounting demo: no issued invoices, or the books are already set up; skipping the books")
		return nil
	}
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT u.id FROM identity.users u
		JOIN identity.user_roles ur ON ur.user_id = u.id
		JOIN identity.roles r ON r.id = ur.role_id
		WHERE r.name = 'Owner' ORDER BY u.created_at LIMIT 1
	`).Scan(&owner); err != nil {
		return fmt.Errorf("read the owner: %w", err)
	}

	deps := module.Deps{Config: cfg, Pool: pool, Logger: logger, Clock: func() time.Time { return now }}
	access := identity.NewAccess(deps)
	deps.Access = access
	deps = module.WorkerDeps(deps, businessModules(access)...)
	b := &demoBooks{deps: deps, ops: accounting.Operations(deps), logger: logger, owner: owner,
		ctx:       contracts.WithPrincipal(ctx, contracts.Principal{UserID: owner}),
		paidTerms: map[string]bool{}, counts: map[string]int{}}
	today, err := deps.Calendar.Today(ctx, now)
	if err != nil {
		return err
	}
	b.today = today
	// The books start on the first of January — last year's while it is
	// January, so there is a history to show.
	year := today.Year()
	if today.Month() == time.January {
		year--
	}
	b.start = time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	storage := cfg.StorageProvider != ""

	steps := []struct {
		name string
		run  func() error
		skip bool
	}{
		{"set up the books", b.setUp, false},
		{"add the posting rules", b.postingRules, false},
		{"enter the opening balances", b.opening, false},
		{"post the sales and expenses", b.catchUp, false},
		{"add the suppliers and their invoices", b.purchases, !storage},
		{"close the VAT terms before the statement's month", func() error { return b.closeTerms(true) }, false},
		{"import and reconcile the bank statement", b.bank, !storage},
		{"close the remaining VAT terms", func() error { return b.closeTerms(false) }, false},
		{"file the VAT returns", b.fileTerms, false},
	}
	for _, step := range steps {
		if step.skip {
			continue
		}
		if err := step.run(); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	if !storage {
		logger.Info("accounting demo: no object store (STORAGE_PROVIDER) configured; the supplier invoices and the bank statement, which keep files, are left out")
	}
	attrs := []any{"books_start", b.start.Format(time.DateOnly)}
	for _, k := range []string{"suppliers", "supplier_invoices", "vat_terms_closed", "vat_terms_filed", "statement_lines", "lines_matched"} {
		attrs = append(attrs, k, b.counts[k])
	}
	var vouchers, attention int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounting.vouchers),
		(SELECT count(*) FROM accounting.unposted WHERE posted_at IS NULL AND resolved_at IS NULL)`).Scan(&vouchers, &attention); err != nil {
		return err
	}
	attrs = append(attrs, "vouchers", vouchers, "needs_attention", attention)
	logger.Info("accounting demo books applied", attrs...)
	return nil
}

// refused turns an operation's error into one that says what was refused.
func refused(what string, err error) error {
	if p, ok := accounting.RefusalProblem(err); ok {
		detail := ""
		if p.Detail != nil {
			detail = ": " + *p.Detail
		}
		if p.Errors != nil {
			detail += fmt.Sprintf(" %v", *p.Errors)
		}
		code := ""
		if p.Code != nil {
			code = *p.Code
		}
		return fmt.Errorf("%s: refused %s%s", what, code, detail)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// answer is an operation's response as the type it answers on success.
func answer[T any](what string, res any, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, refused(what, err)
	}
	out, ok := res.(T)
	if !ok {
		return zero, fmt.Errorf("%s: answered %T", what, res)
	}
	return out, nil
}

func apiDay(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t} }

func ptrOf[T any](v T) *T { return &v }

// øre is an amount in øre as the API's decimal.
func øre(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	return fmt.Sprintf("%s%d.%02d", sign, n/100, n%100)
}

// parseØre reads a decimal of at most two places as øre.
func parseØre(s string) (int64, error) {
	neg := strings.HasPrefix(s, "-")
	whole, frac, _ := strings.Cut(strings.TrimPrefix(s, "-"), ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("amount %q has more than two decimals", s)
	}
	frac = (frac + "00")[:2]
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", s, err)
	}
	if neg {
		n = -n
	}
	return n, nil
}

func (b *demoBooks) setUp() error {
	res, err := b.ops.SetUpAccountingBooks(b.ctx, gen.SetUpAccountingBooksRequestObject{Body: &gen.AccountingSetupInput{
		BooksStart: apiDay(b.start), FiscalYearStartMonth: ptrOf(int32(1)), Language: gen.Nb, VatTerm: gen.AccountingVatTermBimonthly}})
	if _, err := answer[gen.SetUpAccountingBooks201JSONResponse]("set up", res, err); err != nil {
		return err
	}
	return b.readChart()
}

// readChart reads the chart's accounts and the tax codes by number and code.
func (b *demoBooks) readChart() error {
	res, err := b.ops.ListAccountingAccounts(b.ctx, gen.ListAccountingAccountsRequestObject{})
	list, err := answer[gen.ListAccountingAccounts200JSONResponse]("list the accounts", res, err)
	if err != nil {
		return err
	}
	b.accounts = map[string]int32{}
	for _, a := range list.Items {
		b.accounts[a.Number] = a.Id
	}
	tres, err := b.ops.ListAccountingTaxCodes(b.ctx, gen.ListAccountingTaxCodesRequestObject{})
	codes, err := answer[gen.ListAccountingTaxCodes200JSONResponse]("list the tax codes", tres, err)
	if err != nil {
		return err
	}
	b.taxCodes = map[string]int32{}
	for _, c := range codes.Items {
		b.taxCodes[c.Code] = c.Id
	}
	return nil
}

// account is a chart account's id by number; the seed names only accounts
// the default chart has, or that it made itself.
func (b *demoBooks) account(number string) int32 {
	id, ok := b.accounts[number]
	if !ok {
		panic("accounting demo: the chart has no account " + number)
	}
	return id
}

func (b *demoBooks) taxCode(code string) *int32 {
	id, ok := b.taxCodes[code]
	if !ok {
		panic("accounting demo: the books have no tax code " + code)
	}
	return &id
}

// postingRules adds a consulting revenue account with a rule for hours, and
// an expense rule for each of Expenses' categories, VAT deductible where a
// Norwegian company may deduct it (not for meals with guests).
func (b *demoBooks) postingRules() error {
	res, err := b.ops.ListAccountingAccounts(b.ctx, gen.ListAccountingAccountsRequestObject{Params: gen.ListAccountingAccountsParams{Q: ptrOf("3000")}})
	list, err := answer[gen.ListAccountingAccounts200JSONResponse]("read account 3000", res, err)
	if err != nil {
		return err
	}
	var grouping *gen.AccountingGrouping
	for _, a := range list.Items {
		if a.Number == "3000" && len(a.Groupings) > 0 {
			grouping = &a.Groupings[len(a.Groupings)-1]
		}
	}
	ares, err := b.ops.CreateAccountingAccount(b.ctx, gen.CreateAccountingAccountRequestObject{Body: &gen.AccountingAccountInput{
		Number: "3050", Name: "Konsulenthonorarer", Kind: gen.AccountingAccountKind("income"), Grouping: grouping,
		DefaultTaxCodeId: b.taxCode("3")}})
	created, err := answer[gen.CreateAccountingAccount201JSONResponse]("add account 3050", ares, err)
	if err != nil {
		return err
	}
	b.accounts["3050"] = created.Id

	rules := []gen.AccountingPostingRuleInput{{Name: "Timer", Scope: gen.AccountingRuleScope("sales"),
		Match: gen.AccountingRuleMatch("line_kind"), LineKind: ptrOf("hours"), AccountId: created.Id}}
	rows, err := b.deps.Pool.Query(b.ctx, `SELECT id, name FROM expenses.categories WHERE active ORDER BY position, id`)
	if err != nil {
		return err
	}
	type category struct {
		ID   int32
		Name string
	}
	categories, err := pgx.CollectRows(rows, pgx.RowToStructByPos[category])
	if err != nil {
		return err
	}
	byCategory := map[string]struct {
		name, account string
		deductible    bool
	}{
		"Materials":          {"Materiell", "6500", true},
		"Subcontractor":      {"Underleverandører", "4500", true},
		"Equipment hire":     {"Leie av utstyr", "6400", true},
		"Travel":             {"Reise", "7165", true},
		"Accommodation":      {"Overnatting", "7165", true},
		"Meals":              {"Bevertning", "7350", false},
		"Phone and internet": {"Telefon og internett", "6995", true},
		"Other":              {"Andre utlegg", "7700", true},
	}
	for _, c := range categories {
		r, ok := byCategory[c.Name]
		if !ok {
			continue
		}
		rules = append(rules, gen.AccountingPostingRuleInput{Name: "Utlegg: " + r.name, Scope: gen.AccountingRuleScope("expense"),
			Match: gen.AccountingRuleMatch("expense_category"), ExpenseCategoryId: ptrOf(c.ID), AccountId: b.account(r.account),
			VatDeductible: ptrOf(r.deductible)})
	}
	for _, r := range rules {
		res, err := b.ops.CreateAccountingPostingRule(b.ctx, gen.CreateAccountingPostingRuleRequestObject{Body: &r})
		if _, err := answer[gen.CreateAccountingPostingRule201JSONResponse]("add the posting rule "+r.Name, res, err); err != nil {
			return err
		}
	}
	return nil
}

// opening enters a small consultancy's balances on the day before the books
// start — the bank, office machines, share capital, a bank loan and the
// equity earned before — and every invoice Invoices had open then, each as
// its own receivable, through the opening's own path.
func (b *demoBooks) opening() error {
	res, err := b.ops.GetAccountingOpening(b.ctx, gen.GetAccountingOpeningRequestObject{})
	open, err := answer[gen.GetAccountingOpening200JSONResponse]("read the opening", res, err)
	if err != nil {
		return err
	}
	const (
		bank      = int64(85_000_000)
		equipment = int64(18_000_000)
		capital   = int64(-10_000_000)
		loan      = int64(-40_000_000)
	)
	lines := []gen.AccountingOpeningLineInput{
		{AccountId: b.account("1920"), Amount: øre(bank), Text: ptrOf("Driftskonto")},
		{AccountId: b.account("1280"), Amount: øre(equipment), Text: ptrOf("Kontormaskiner og datautstyr")},
		{AccountId: b.account("2000"), Amount: øre(capital), Text: ptrOf("Aksjekapital")},
		{AccountId: b.account("2220"), Amount: øre(loan), Text: ptrOf("Lån i bank")},
	}
	receivables := int64(0)
	for _, inv := range open.OpenInvoices {
		amount, err := parseØre(inv.Amount)
		if err != nil {
			return err
		}
		receivables += amount
		lines = append(lines, gen.AccountingOpeningLineInput{AccountId: b.account("1500"), Amount: inv.Amount,
			CustomerId: ptrOf(inv.CustomerId), DocumentNumber: ptrOf(strconv.FormatInt(inv.Number, 10)),
			DocumentDate: ptrOf(inv.IssueDate), DueDate: ptrOf(inv.DueDate), SourceDocumentId: ptrOf(inv.DocumentId)})
	}
	lines = append(lines, gen.AccountingOpeningLineInput{AccountId: b.account("2050"),
		Amount: øre(-(bank + equipment + capital + loan + receivables)), Text: ptrOf("Opptjent egenkapital")})
	pres, err := b.ops.PostAccountingOpening(b.ctx, gen.PostAccountingOpeningRequestObject{Body: &gen.AccountingOpeningInput{Lines: lines}})
	_, err = answer[gen.PostAccountingOpening201JSONResponse]("post the opening", pres, err)
	return err
}

// catchUp runs the catch-up worker as its poll would: one run reads every
// stream to its end, the fixed clock never reaching the lease's margin.
func (b *demoBooks) catchUp() error {
	return accounting.NewCatchUpWorker(b.deps).RunOnce(b.ctx)
}

// dayOf is the day frac of the way from the books' start to today.
func (b *demoBooks) dayOf(frac float64) time.Time {
	span := int(b.today.Sub(b.start).Hours() / 24)
	return b.start.AddDate(0, 0, int(frac*float64(span)))
}

// demoSupplier is a supplier of the demo: fictional names, organisation
// numbers from the seed's own range (norwegianOrgNumber, past the customers').
type demoSupplier struct {
	key, name, city, postal, address string
	iban, bic, country               string
	currency                         string
	account, taxCode                 string
}

var demoSuppliers = []demoSupplier{
	{key: "rent", name: "Kvadratur Eiendom AS", address: "Tollbugata 17", postal: "0152", city: "Oslo", account: "6300"},
	{key: "telecom", name: "Fjellnett Telekom AS", address: "Strandkaien 4", postal: "5013", city: "Bergen", account: "6995", taxCode: "1"},
	{key: "software", name: "Skybrygge Programvare AS", address: "Brattørkaia 12", postal: "7010", city: "Trondheim", account: "6995", taxCode: "1"},
	{key: "accountant", name: "Ledgerhuset Regnskap AS", address: "Kirkegata 9", postal: "4006", city: "Stavanger", account: "6700", taxCode: "1"},
	{key: "equipment", name: "Kontorlandet Utstyr AS", address: "Industriveien 30", postal: "1337", city: "Sandvika", account: "6500", taxCode: "1"},
	{key: "power", name: "Lysverket Kraft AS", address: "Elveveien 2", postal: "3015", city: "Drammen", account: "6340", taxCode: "1"},
	{key: "cloud", name: "Nordlicht Cloud GmbH", address: "Am Kanal 21", postal: "10117", city: "Berlin", country: "DE",
		currency: "EUR", iban: "DE02100100100006820101", bic: "PBNKDEFFXXX", account: "6995", taxCode: "86"},
}

// demoSupplierInvoice is one supplier invoice of the demo: its day as a share
// of the books' span so far (or days before today, when ago is set), its
// gross in NOK øre (net for a reverse charge, in the currency's cents), and
// what happens to it after.
type demoSupplierInvoice struct {
	supplier, number, text string
	frac                   float64
	ago                    int // days before today, instead of frac
	amount                 int64
	rate                   string // NOK per unit, another currency
	payRate                string // the rate the bank paid at, another currency
	credits                string // the number of the invoice a credit note credits
	draft                  bool   // left a draft
	unpaid                 bool   // left owed
}

var demoSupplierInvoices = []demoSupplierInvoice{
	{supplier: "rent", number: "KE-1101", text: "Husleie kontor, 1. kvartal", frac: 0.0, amount: 4_500_000},
	{supplier: "power", number: "LK-88412", text: "Strøm kontor", frac: 0.08, amount: 437_500},
	{supplier: "telecom", number: "FT-20417", text: "Mobil og bredbånd", frac: 0.15, amount: 249_000},
	{supplier: "cloud", number: "NC-2026-0193", text: "Cloud platform subscription", frac: 0.25, amount: 120_000, rate: "11.52", payRate: "11.61"},
	{supplier: "rent", number: "KE-1188", text: "Husleie kontor, 2. kvartal", frac: 0.33, amount: 4_500_000},
	{supplier: "software", number: "SP-5521", text: "Lisenser prosjektverktøy, årsabonnement", frac: 0.4, amount: 1_250_000},
	{supplier: "accountant", number: "LR-3307", text: "Årsoppgjør og bistand", frac: 0.47, amount: 1_875_000},
	{supplier: "equipment", number: "KU-90412", text: "Bærbare PC-er og skjermer", frac: 0.55, amount: 3_125_000},
	{supplier: "telecom", number: "FT-20988", text: "Mobil og bredbånd", frac: 0.62, amount: 249_000},
	{supplier: "telecom", number: "FT-K-0412", text: "Kreditnota, feilfakturert abonnement", frac: 0.64, amount: 49_000, credits: "FT-20988"},
	{supplier: "rent", number: "KE-1265", text: "Husleie kontor, 3. kvartal", frac: 0.68, amount: 4_500_000},
	{supplier: "cloud", number: "NC-2026-0611", text: "Cloud platform subscription", frac: 0.82, amount: 120_000, rate: "11.48", payRate: "11.43"},
	{supplier: "accountant", number: "LR-3412", text: "Rådgivning MVA og rapportering", ago: 30, amount: 1_875_000, unpaid: true},
	{supplier: "power", number: "LK-90127", text: "Strøm kontor", ago: 10, amount: 412_500, unpaid: true},
	{supplier: "equipment", number: "KU-90877", text: "Møteromsskjerm og kabler", ago: 2, amount: 1_486_250, draft: true},
}

// purchases adds the suppliers and their invoices, each with a small PDF,
// posts all but the draft, and pays what is due by today on its due day.
func (b *demoBooks) purchases() error {
	suppliers := map[string]int64{}
	terms := int32(14)
	for i, s := range demoSuppliers {
		in := gen.AccountingSupplierInput{Name: s.name, AddressLine: ptrOf(s.address), PostalCode: ptrOf(s.postal),
			City: ptrOf(s.city), PaymentTermsDays: &terms, DefaultAccountId: ptrOf(b.account(s.account)),
			Email: ptrOf("faktura@" + slugifyDomain(s.key, "leverandor", i))}
		if s.taxCode != "" {
			in.DefaultTaxCodeId = b.taxCode(s.taxCode)
		}
		if s.country == "" {
			in.OrganisationNumber = ptrOf(norwegianOrgNumber(1000 + i))
			in.BankAccount = ptrOf(norwegianBankAccount(fmt.Sprintf("15062000%02d", i+1)))
		} else {
			in.Country, in.Iban, in.Bic = ptrOf(s.country), ptrOf(s.iban), ptrOf(s.bic)
		}
		if s.currency != "" {
			in.Currency = ptrOf(s.currency)
		}
		// A supplier's write checks the caller's keys itself (purchasing design
		// A12): the owner is asked as a user, as identity answers for them.
		res, err := b.ops.CreateAccountingSupplier(contracts.AsUser(b.ctx, b.owner), gen.CreateAccountingSupplierRequestObject{Body: &in})
		created, err := answer[gen.CreateAccountingSupplier201JSONResponse]("add the supplier "+s.name, res, err)
		if err != nil {
			return err
		}
		suppliers[s.key] = created.Id
		b.counts["suppliers"]++
	}
	supplierOf := map[string]demoSupplier{}
	for _, s := range demoSuppliers {
		supplierOf[s.key] = s
	}

	type payment struct {
		id     int64
		due    time.Time
		amount int64 // NOK øre
		cur    *string
	}
	var payments []payment
	posted := map[string]int64{}   // number → id
	balances := map[string]int64{} // number → NOK øre owed
	for _, inv := range demoSupplierInvoices {
		s := supplierOf[inv.supplier]
		date := b.dayOf(inv.frac)
		if inv.ago > 0 {
			date = b.today.AddDate(0, 0, -inv.ago)
		}
		due := date.AddDate(0, 0, 14)
		line := gen.AccountingSupplierInvoiceLineInput{AccountId: b.account(s.account), Amount: øre(inv.amount), Text: ptrOf(inv.text)}
		if s.taxCode != "" {
			line.TaxCodeId = b.taxCode(s.taxCode)
		}
		in := gen.AccountingSupplierInvoiceInput{SupplierId: suppliers[inv.supplier], Kind: gen.AccountingSupplierInvoiceKind("invoice"),
			InvoiceNumber: inv.number, InvoiceDate: apiDay(date), DueDate: ptrOf(apiDay(due)), Total: øre(inv.amount),
			Description: ptrOf(inv.text), Lines: []gen.AccountingSupplierInvoiceLineInput{line}}
		nok := inv.amount
		if inv.rate != "" {
			// A reverse charge is entered net; the payable is the net, at the
			// invoice's rate.
			line.Entry = ptrOf(gen.AccountingEntryMode("net"))
			in.Lines[0] = line
			in.Currency, in.ExchangeRate = ptrOf(s.currency), ptrOf(inv.rate)
			nok = atRate(inv.amount, inv.rate)
		}
		if inv.credits != "" {
			in.Kind = gen.AccountingSupplierInvoiceKind("credit_note")
			in.CreditsInvoiceId = ptrOf(posted[inv.credits])
		}
		res, err := b.ops.CreateAccountingSupplierInvoice(b.ctx, gen.CreateAccountingSupplierInvoiceRequestObject{Body: &in})
		created, err := answer[gen.CreateAccountingSupplierInvoice201JSONResponse]("enter the supplier invoice "+inv.number, res, err)
		if err != nil {
			return err
		}
		b.counts["supplier_invoices"]++
		body, boundary, err := demoInvoicePDF(s, inv, date, due)
		if err != nil {
			return err
		}
		ures, err := b.ops.UploadAccountingSupplierInvoiceFile(b.ctx, gen.UploadAccountingSupplierInvoiceFileRequestObject{
			Id: created.Id, Body: multipart.NewReader(bytes.NewReader(body), boundary)})
		if _, err := answer[gen.UploadAccountingSupplierInvoiceFile201JSONResponse]("attach the PDF of "+inv.number, ures, err); err != nil {
			return err
		}
		if inv.draft {
			continue
		}
		pres, err := b.ops.PostAccountingSupplierInvoice(b.ctx, gen.PostAccountingSupplierInvoiceRequestObject{Id: created.Id})
		if _, err := answer[gen.PostAccountingSupplierInvoice200JSONResponse]("post the supplier invoice "+inv.number, pres, err); err != nil {
			return err
		}
		posted[inv.number] = created.Id
		if inv.credits != "" {
			balances[inv.credits] -= nok
			continue
		}
		balances[inv.number] = nok
		if inv.unpaid || !due.Before(b.today) {
			continue
		}
		p := payment{id: created.Id, due: due}
		if inv.payRate != "" {
			p.amount = atRate(inv.amount, inv.payRate)
			p.cur = ptrOf(øre(inv.amount))
		}
		payments = append(payments, p)
	}
	// The payments, each on its due day, after every invoice and credit note
	// is in: a credited invoice is paid what is left of it.
	numberOf := map[int64]string{}
	for number, id := range posted {
		numberOf[id] = number
	}
	slices.SortStableFunc(payments, func(x, y payment) int { return x.due.Compare(y.due) })
	for _, p := range payments {
		amount := p.amount
		if amount == 0 {
			amount = balances[numberOf[p.id]]
		}
		res, err := b.ops.PayAccountingSupplierInvoice(b.ctx, gen.PayAccountingSupplierInvoiceRequestObject{Id: p.id,
			Body: &gen.AccountingSupplierPaymentInput{Date: apiDay(p.due), AmountNok: øre(amount), AmountCurrency: p.cur}})
		if _, err := answer[gen.PayAccountingSupplierInvoice201JSONResponse]("pay "+numberOf[p.id], res, err); err != nil {
			return err
		}
	}
	return nil
}

// atRate is a currency amount in cents at a rate as NOK øre, rounded half
// away from zero.
func atRate(cents int64, rate string) int64 {
	r, err := strconv.ParseFloat(rate, 64)
	if err != nil {
		panic(err)
	}
	v := float64(cents) * r
	if v < 0 {
		return -int64(-v + 0.5)
	}
	return int64(v + 0.5)
}

// statementMonth is the last full month before today: the bank statement's.
func (b *demoBooks) statementMonth() (time.Time, time.Time) {
	first := time.Date(b.today.Year(), b.today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	if first.Before(b.start) {
		first = b.start
	}
	return first, time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC)
}

// terms is the VAT terms as the module lists them, oldest first.
func (b *demoBooks) terms() ([]gen.AccountingVatTermItem, error) {
	res, err := b.ops.ListAccountingVatTerms(b.ctx, gen.ListAccountingVatTermsRequestObject{})
	list, err := answer[gen.ListAccountingVatTerms200JSONResponse]("list the VAT terms", res, err)
	return list.Terms, err
}

func termKey(t gen.AccountingVatTermItem) string { return fmt.Sprintf("%d-%d", t.Year, t.Number) }

// closeTerms closes, in order, every VAT term that has ended — before the
// statement's month only, or the rest — and pays each one closed before the
// statement's month whose due day is before it, on its due day, as a person
// would enter it from the bank.
func (b *demoBooks) closeTerms(beforeStatement bool) error {
	monthStart, _ := b.statementMonth()
	terms, err := b.terms()
	if err != nil {
		return err
	}
	for _, t := range terms {
		if !t.NeedsClosing || (beforeStatement && !t.PeriodEnd.Before(monthStart)) {
			continue
		}
		res, err := b.ops.CloseAccountingVatTerm(b.ctx, gen.CloseAccountingVatTermRequestObject{Year: t.Year, Number: t.Number,
			Body: &gen.AccountingVatCloseInput{}})
		if p, ok := accounting.RefusalProblem(err); ok && p.Code != nil && *p.Code == "vat_term_attention" {
			// The euro expense waits in this term (the seed's day in UTC is
			// the business day before): it stays open, and so do the rest.
			b.logger.Info("accounting demo: a VAT term with a fact under Needs attention is left open", "term", t.PeriodName)
			return nil
		}
		if _, err := answer[gen.CloseAccountingVatTerm200JSONResponse]("close the VAT term "+t.PeriodName, res, err); err != nil {
			return err
		}
		b.counts["vat_terms_closed"]++
		if !beforeStatement || !t.DueDate.Before(monthStart) {
			continue
		}
		closed, err := b.term(t.Year, t.Number)
		if err != nil {
			return err
		}
		if err := b.payTerm(closed, t.DueDate.Time); err != nil {
			return err
		}
	}
	return nil
}

func (b *demoBooks) term(year, number int32) (gen.AccountingVatTermItem, error) {
	terms, err := b.terms()
	if err != nil {
		return gen.AccountingVatTermItem{}, err
	}
	for _, t := range terms {
		if t.Year == year && t.Number == number {
			return t, nil
		}
	}
	return gen.AccountingVatTermItem{}, fmt.Errorf("no VAT term %d-%d", year, number)
}

// termTotal is a closed term's return total in øre: positive to pay.
func termTotal(t gen.AccountingVatTermItem) (int64, error) {
	if t.Total == nil {
		return 0, nil
	}
	return parseØre(*t.Total)
}

// payTerm enters a term's payment — or its refund — between the bank and
// the VAT settlement account, as a manual journal on day.
func (b *demoBooks) payTerm(t gen.AccountingVatTermItem, day time.Time) error {
	total, err := termTotal(t)
	if err != nil || total == 0 {
		return err
	}
	text := "Merverdiavgift " + t.PeriodName
	res, err := b.ops.PostAccountingVoucher(b.ctx, gen.PostAccountingVoucherRequestObject{Body: &gen.AccountingJournalInput{
		VoucherDate: apiDay(day), Description: text, Lines: []gen.AccountingJournalLineInput{
			{AccountId: b.account("2740"), Amount: øre(total), Text: ptrOf(text)},
			{AccountId: b.account("1920"), Amount: øre(-total), Text: ptrOf("Skatteetaten")}}}})
	if _, err := answer[gen.PostAccountingVoucher201JSONResponse]("pay the VAT term "+t.PeriodName, res, err); err != nil {
		return err
	}
	b.paidTerms[termKey(t)] = true
	return nil
}

// fileTerms marks every closed term but the latest as filed at Skatteetaten,
// a few days before its due day, with a made-up reference.
func (b *demoBooks) fileTerms() error {
	terms, err := b.terms()
	if err != nil {
		return err
	}
	var closed []gen.AccountingVatTermItem
	for _, t := range terms {
		if t.State == gen.AccountingVatTermState("closed") {
			closed = append(closed, t)
		}
	}
	for i, t := range closed {
		if i == len(closed)-1 {
			break
		}
		filed := t.DueDate.AddDate(0, 0, -3)
		if !filed.After(t.PeriodEnd.Time) {
			filed = t.PeriodEnd.AddDate(0, 0, 1)
		}
		if filed.After(b.today) {
			filed = b.today
		}
		res, err := b.ops.FileAccountingVatTerm(b.ctx, gen.FileAccountingVatTermRequestObject{Year: t.Year, Number: t.Number,
			Body: &gen.AccountingVatFilingInput{FiledAt: apiDay(filed), Reference: fmt.Sprintf("DEMO-MVA-%d-%d-%05d", t.Year, t.Number, 4711+i)}})
		if _, err := answer[gen.FileAccountingVatTerm200JSONResponse]("file the VAT term "+t.PeriodName, res, err); err != nil {
			return err
		}
		b.counts["vat_terms_filed"]++
	}
	return nil
}

// statementEntry is one entry of the generated camt.053.
type statementEntry struct {
	day          time.Time
	amount       int64 // øre, money in positive
	text         string
	family, sub  string // the bank transaction code's family and sub-family
	counterparty string
}

// bank adds the seller's account as the bank account on 1920, a rule for the
// bank's fees, and the last full month's statement as camt.053: every
// payment the books hold on 1920 that month, the VAT term the month pays, a
// fee and a card purchase no one has entered. About half the lines are
// accepted as suggested; the rest are left for a person to try.
func (b *demoBooks) bank() error {
	seller, err := b.deps.SalesTax.Seller(b.ctx)
	if err != nil {
		return err
	}
	number := strings.NewReplacer(" ", "", ".", "").Replace(seller.BankAccount)
	bankAccountID, err := accounting.AddBankAccount(b.ctx, b.deps, gen.AccountingBankAccountInput{Name: "Driftskonto",
		AccountNumber: number, Bic: ptrOf(seller.BIC), LedgerAccountId: b.account("1920")})
	if err != nil {
		return refused("add the bank account", err)
	}
	rres, err := b.ops.CreateAccountingBankRule(b.ctx, gen.CreateAccountingBankRuleRequestObject{Body: &gen.AccountingBankRuleInput{
		Name: "Bankgebyr", TextContains: ptrOf("gebyr"), Direction: ptrOf("out"), AccountId: b.account("7770"),
		BankAccountId: &bankAccountID, Description: ptrOf("Gebyr fra banken")}})
	if _, err := answer[gen.CreateAccountingBankRule201JSONResponse]("add the bank rule", rres, err); err != nil {
		return err
	}

	from, to := b.statementMonth()
	var opening int64
	if err := b.deps.Pool.QueryRow(b.ctx, `
		SELECT coalesce(sum(p.amount), 0) * 100 FROM accounting.postings p JOIN accounting.vouchers v ON v.id = p.voucher_id
		WHERE p.account_id = $1 AND (v.voucher_date < $2 OR v.kind = 'opening')
	`, b.account("1920"), from).Scan(&opening); err != nil {
		return err
	}
	rows, err := b.deps.Pool.Query(b.ctx, `
		SELECT v.voucher_date, (p.amount * 100)::bigint, v.description FROM accounting.postings p
		JOIN accounting.vouchers v ON v.id = p.voucher_id
		WHERE p.account_id = $1 AND v.voucher_date BETWEEN $2 AND $3 AND v.kind <> 'opening'
		ORDER BY v.voucher_date, v.id, p.line_no
	`, b.account("1920"), from, to)
	if err != nil {
		return err
	}
	var entries []statementEntry
	for rows.Next() {
		var e statementEntry
		if err := rows.Scan(&e.day, &e.amount, &e.text); err != nil {
			rows.Close()
			return err
		}
		e.family, e.sub = "RCDT", "ESCT"
		if e.amount < 0 {
			e.family = "ICDT"
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// The VAT the month pays: the latest term closed whose payment is not in
	// the books yet, on its due day or the month's last.
	terms, err := b.terms()
	if err != nil {
		return err
	}
	for i := len(terms) - 1; i >= 0; i-- {
		t := terms[i]
		if t.State != gen.AccountingVatTermState("closed") || b.paidTerms[termKey(t)] {
			continue
		}
		total, err := termTotal(t)
		if err != nil {
			return err
		}
		if total != 0 {
			payDay := t.DueDate.Time
			if payDay.After(to) {
				payDay = to
			}
			if payDay.Before(from) {
				payDay = from
			}
			entries = append(entries, statementEntry{day: payDay, amount: -total, text: "Skatteetaten MVA " + t.PeriodName,
				counterparty: "SKATTEETATEN", family: "ICDT", sub: "DMCT"})
		}
		break
	}
	entries = append(entries,
		statementEntry{day: from.AddDate(0, 0, 13), amount: -124_900, text: "VISA 4925 *1187 CLAS OHLSON OSLO CITY", family: "CCRD", sub: "POSD"},
		statementEntry{day: to, amount: -12_900, text: "Gebyr nettbank og betalinger", family: "MCOP", sub: "CHRG"})
	slices.SortStableFunc(entries, func(x, y statementEntry) int { return x.day.Compare(y.day) })

	file := camt053(number, seller.BIC, from, to, opening, entries)
	if _, err := accounting.ImportBankStatement(b.ctx, b.deps, fmt.Sprintf("kontoutskrift-%s.xml", from.Format("2006-01")),
		file, nil, b.owner); err != nil {
		return refused("import the bank statement", err)
	}

	lineRows, err := b.deps.Pool.Query(b.ctx, `SELECT id, text FROM accounting.statement_lines WHERE bank_account_id = $1
		ORDER BY booked_on, id`, bankAccountID)
	if err != nil {
		return err
	}
	type statementLine struct {
		ID   int64
		Text string
	}
	lines, err := pgx.CollectRows(lineRows, pgx.RowToStructByPos[statementLine])
	if err != nil {
		return err
	}
	b.counts["statement_lines"] = len(lines)
	for i, l := range lines {
		vat := strings.HasPrefix(l.Text, "Skatteetaten")
		if strings.Contains(l.Text, "Gebyr") || (!vat && i%2 == 1) {
			continue
		}
		keys, err := accounting.BankSuggestionKeys(b.ctx, b.deps, l.ID)
		if err != nil {
			return err
		}
		// The most certain suggestion a person could accept with one click: one
		// that needs what a person gives — a part payment's amount in the
		// invoice's currency — is refused without it and left for them, and the
		// next is tried. A possible duplicate and a set-aside line have none.
		matched := false
		for _, key := range keys {
			err := accounting.AcceptBankSuggestion(b.ctx, b.deps, l.ID, key, b.owner)
			if _, isRefusal := accounting.RefusalProblem(err); isRefusal {
				continue
			}
			if err != nil {
				return refused(fmt.Sprintf("accept the bank line %q", l.Text), err)
			}
			matched = true
			break
		}
		if matched {
			b.counts["lines_matched"]++
		}
	}
	return nil
}

// camt053 is a bank statement as the Norwegian banks send it, for one account
// and one month, with its booked opening and closing balances.
func camt053(account, bic string, from, to time.Time, opening int64, entries []statementEntry) []byte {
	var w bytes.Buffer
	esc := func(s string) string {
		var out bytes.Buffer
		_ = xml.EscapeText(&out, []byte(s))
		return out.String()
	}
	amount := func(n int64) (string, string) {
		if n < 0 {
			return øre(-n), "DBIT"
		}
		return øre(n), "CRDT"
	}
	closing := opening
	for _, e := range entries {
		closing += e.amount
	}
	balance := func(kind string, n int64, day time.Time) {
		a, ind := amount(n)
		fmt.Fprintf(&w, "      <Bal><Tp><CdOrPrtry><Cd>%s</Cd></CdOrPrtry></Tp><Amt Ccy=\"NOK\">%s</Amt><CdtDbtInd>%s</CdtDbtInd><Dt><Dt>%s</Dt></Dt></Bal>\n",
			kind, a, ind, day.Format(time.DateOnly))
	}
	id := fmt.Sprintf("%s-%s", account, from.Format("2006-01"))
	fmt.Fprintf(&w, `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt>
    <GrpHdr><MsgId>DEMO-%s</MsgId><CreDtTm>%sT06:00:00+01:00</CreDtTm></GrpHdr>
    <Stmt>
      <Id>%s</Id>
      <CreDtTm>%sT06:00:00+01:00</CreDtTm>
      <FrToDt><FrDtTm>%sT00:00:00+01:00</FrDtTm><ToDtTm>%sT23:59:59+01:00</ToDtTm></FrToDt>
      <Acct><Id><Othr><Id>%s</Id><SchmeNm><Cd>BBAN</Cd></SchmeNm></Othr></Id><Ccy>NOK</Ccy><Svcr><FinInstnId><BIC>%s</BIC></FinInstnId></Svcr></Acct>
`, id, to.AddDate(0, 0, 1).Format(time.DateOnly), id, to.AddDate(0, 0, 1).Format(time.DateOnly),
		from.Format(time.DateOnly), to.Format(time.DateOnly), account, esc(bic))
	balance("OPBD", opening, from)
	balance("CLBD", closing, to)
	for i, e := range entries {
		a, ind := amount(e.amount)
		day := e.day.Format(time.DateOnly)
		party := ""
		if e.counterparty != "" {
			role := "Dbtr"
			if e.amount < 0 {
				role = "Cdtr"
			}
			party = fmt.Sprintf("<RltdPties><%s><Nm>%s</Nm></%s></RltdPties>", role, esc(e.counterparty), role)
		}
		text := e.text
		if len(text) > 140 {
			text = text[:140]
		}
		fmt.Fprintf(&w, `      <Ntry>
        <Amt Ccy="NOK">%s</Amt><CdtDbtInd>%s</CdtDbtInd><Sts>BOOK</Sts>
        <BookgDt><Dt>%s</Dt></BookgDt><ValDt><Dt>%s</Dt></ValDt>
        <AcctSvcrRef>DEMO%s%04d</AcctSvcrRef>
        <BkTxCd><Domn><Cd>PMNT</Cd><Fmly><Cd>%s</Cd><SubFmlyCd>%s</SubFmlyCd></Fmly></Domn></BkTxCd>
        <NtryDtls><TxDtls><Refs><EndToEndId>NOTPROVIDED</EndToEndId></Refs>%s<RmtInf><Ustrd>%s</Ustrd></RmtInf></TxDtls></NtryDtls>
      </Ntry>
`, a, ind, day, day, strings.ReplaceAll(day, "-", ""), i+1, e.family, e.sub, party, esc(text))
	}
	w.WriteString("    </Stmt>\n  </BkToCstmrStmt>\n</Document>\n")
	return w.Bytes()
}

// demoInvoicePDF is a supplier invoice's file: a one-page PDF naming the
// supplier, the number, the days and the amount, in a multipart form's field
// file as the upload takes it.
func demoInvoicePDF(s demoSupplier, inv demoSupplierInvoice, date, due time.Time) ([]byte, string, error) {
	currency := "NOK"
	if s.currency != "" {
		currency = s.currency
	}
	kind := "Faktura"
	if inv.credits != "" {
		kind = "Kreditnota"
	}
	pdf := onePagePDF([]string{
		s.name,
		s.address + ", " + s.postal + " " + s.city,
		"",
		kind + " " + inv.number,
		"Fakturadato: " + date.Format("02.01.2006") + "   Forfall: " + due.Format("02.01.2006"),
		"",
		inv.text,
		"Å betale: " + currency + " " + strings.Replace(øre(inv.amount), ".", ",", 1),
		"",
		"Demodokument generert av Vantigo",
	})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", strings.ToLower(kind)+"-"+inv.number+".pdf")
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(pdf); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), mw.Boundary(), nil
}

// onePagePDF is a valid single-page A4 PDF of lines of Helvetica text, the
// text in WinAnsi (Latin-1 for the letters a Norwegian text needs).
func onePagePDF(lines []string) []byte {
	var content bytes.Buffer
	content.WriteString("BT /F1 12 Tf 72 770 Td 16 TL\n")
	for _, l := range lines {
		content.WriteString("(")
		for _, r := range l {
			switch {
			case r == '(' || r == ')' || r == '\\':
				content.WriteByte('\\')
				content.WriteRune(r)
			case r < 0x100:
				content.WriteByte(byte(r))
			default:
				content.WriteByte('?')
			}
		}
		content.WriteString(") Tj T*\n")
	}
	content.WriteString("ET\n")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}
