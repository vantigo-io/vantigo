package accounting

// The posting rules (A14): which account a sales line's net is credited to,
// matched on exactly one of its line kind, its product's tax category or its
// standard tax code — in that order of precedence — and then the default
// rules, one per tax code, seeded with the books. A sales rule names an
// income account (R15). Each revenue line's posting names the rule and the
// version that placed it; changing a rule's name or account is a new
// version, kept in posting_rule_versions, and changes future vouchers only.
// A rule is archived, never deleted. An expense rule (scope expense, delivery
// C) matches an expense category of Expenses, names an expense account, and
// says whether the category's VAT may be deducted (R2); a change of that is
// a new version too.

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// The posting rules' refusal codes.
const (
	codeRuleNameTaken  = "posting_rule_name_taken"
	codeRuleMatchTaken = "posting_rule_match_taken"
	codeRuleDefault    = "posting_rule_default"
)

// defaultRule is a default rule seeded with the books: a tax code, the
// default chart's account its sales go to, and its name in both languages.
type defaultRule struct {
	taxCode, account string
	en, nb           string
}

// defaultRules are the seeded defaults by tax code (A14): taxable sales,
// exempt sales, sales outside the law and export, on the default chart's
// 3000-series accounts. Codes 0 and 7 — no VAT treatment, a seller that is
// not registered — go with the exempt sales.
var defaultRules = []defaultRule{
	{"3", "3000", "Taxable sales, standard rate (code 3)", "Avgiftspliktig salg, alminnelig sats (kode 3)"},
	{"31", "3010", "Taxable sales, middle rate (code 31)", "Avgiftspliktig salg, middels sats (kode 31)"},
	{"32", "3000", "Taxable sales, raw fish (code 32)", "Avgiftspliktig salg, råfisk (kode 32)"},
	{"33", "3020", "Taxable sales, low rate (code 33)", "Avgiftspliktig salg, lav sats (kode 33)"},
	{"5", "3100", "Exempt sales (code 5)", "Avgiftsfritt salg (kode 5)"},
	{"51", "3160", "Domestic sales, reverse charge (code 51)", "Innenlandsk salg, omvendt avgiftsplikt (kode 51)"},
	{"52", "3150", "Export (code 52)", "Utførsel (kode 52)"},
	{"6", "3200", "Sales outside the VAT Act (code 6)", "Salg utenfor merverdiavgiftsloven (kode 6)"},
	{"0", "3100", "Sales with no VAT treatment (code 0)", "Salg uten mva-behandling (kode 0)"},
	{"7", "3100", "Sales with no VAT (code 7)", "Salg uten mva (kode 7)"},
}

// lineKinds are the line kinds a rule may match (contracts.LineKind*).
var lineKinds = []string{
	contracts.LineKindHours, contracts.LineKindExpense, contracts.LineKindMilestone, contracts.LineKindSettlement,
	contracts.LineKindOrderLine, contracts.LineKindSubscriptionPeriod, contracts.LineKindDeduction, contracts.LineKindManual,
	contracts.LineKindMixed,
}

// seedDefaultRules writes the default rules on q, each whose account the chart
// has; a rule already there is left as it is, so seeding twice is harmless.
func seedDefaultRules(ctx context.Context, q *store.Queries, language string, now time.Time) error {
	for _, r := range defaultRules {
		account, err := q.AccountByNumber(ctx, r.account)
		if noRows(err) {
			continue
		}
		if err != nil {
			return err
		}
		name := r.en
		if language == "nb" {
			name = r.nb
		}
		if _, err := q.InsertPostingRule(ctx, store.InsertPostingRuleParams{Name: name, Scope: "sales", Match: "default",
			TaxCode: ptr(r.taxCode), AccountID: account, Now: now}); err != nil && !noRows(err) {
			return err
		}
	}
	return nil
}

// ensureDefaultRules seeds the default rules of books set up before they
// existed (migration 00183 cannot know the chart), once: a no-op when any
// default rule is there, and idempotent when two callers race.
func ensureDefaultRules(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	q := store.New(pool)
	n, err := q.CountDefaultRules(ctx)
	if err != nil || n > 0 {
		return err
	}
	return db.WithTx(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		settings, err := q.GetSettings(ctx)
		if noRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return seedDefaultRules(ctx, q, settings.ChartLanguage, now)
	})
}

// ruleView is a rule as the contract answers it.
func ruleView(r store.GetPostingRuleRow) gen.AccountingPostingRule {
	return gen.AccountingPostingRule{Id: r.ID, Name: r.Name, Scope: gen.AccountingRuleScope(r.Scope),
		Match: gen.AccountingRuleMatch(r.Match), LineKind: r.LineKind, TaxCategoryId: r.TaxCategoryID, TaxCode: r.TaxCode,
		AccountId: r.AccountID, AccountNumber: r.AccountNumber, AccountName: r.AccountName, Version: r.Version,
		Revision: r.Revision, UpdatedAt: r.UpdatedAt, ExpenseCategoryId: r.ExpenseCategoryID, VatDeductible: r.VatDeductible}
}

// ListAccountingPostingRules is every rule in force in precedence order. It
// writes nothing: the default rules are seeded by Set up the books, and for
// books set up before they existed by the catch-up worker (L1).
func (s *server) ListAccountingPostingRules(ctx context.Context, _ gen.ListAccountingPostingRulesRequestObject) (gen.ListAccountingPostingRulesResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListPostingRules(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingPostingRules200JSONResponse{Items: []gen.AccountingPostingRule{}}
	for _, r := range rows {
		out.Items = append(out.Items, ruleView(store.GetPostingRuleRow(r)))
	}
	return out, nil
}

// checkRuleAccount refuses an account that is not there, not active, or —
// for a sales rule — not an income account (R15): a sale's net is credited
// to income.
func checkRuleAccount(ctx context.Context, q *store.Queries, scope string, id int32) error {
	accounts, err := q.AccountsForPosting(ctx, []int32{id})
	if err != nil {
		return err
	}
	switch {
	case len(accounts) == 0:
		return invalidField("The rule cannot be saved", "accountId", codeAccountUnknown, "No such account.")
	case !accounts[0].Active:
		return invalidField("The rule cannot be saved", "accountId", codeAccountInactive, "Account "+accounts[0].Number+" is inactive.")
	case scope == "sales" && accounts[0].Kind != "income":
		return invalidField("The rule cannot be saved", "accountId", codeAccountKindMismatch,
			"A sales rule credits an income account; account "+accounts[0].Number+" is "+accounts[0].Kind+".")
	case scope == "expense" && accounts[0].Kind != "expense":
		return invalidField("The rule cannot be saved", "accountId", codeAccountKindMismatch,
			"An expense rule debits an expense account; account "+accounts[0].Number+" is "+accounts[0].Kind+".")
	}
	return nil
}

// ruleName is a rule's name trimmed, or the field error.
func ruleName(name string, f *fieldErrors) string {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		f.add("name", "A name of 1 to 100 characters.")
	}
	return name
}

// ruleConflict is the 409 of a unique index a rule's write met, nil for any
// other error.
func ruleConflict(err error) error {
	switch {
	case violates(err, "ux_posting_rules_name"):
		return conflict(codeRuleNameTaken, "Another rule has that name", "")
	case violates(err, "ux_posting_rules_match"):
		return conflict(codeRuleMatchTaken, "A rule with that match exists", "Change that rule's account instead.")
	}
	return nil
}

// CreateAccountingPostingRule adds a rule.
func (s *server) CreateAccountingPostingRule(ctx context.Context, req gen.CreateAccountingPostingRuleRequestObject) (gen.CreateAccountingPostingRuleResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The rule cannot be saved", "body", "", "A body is required.")
	}
	b := *req.Body
	var f fieldErrors
	name := ruleName(b.Name, &f)
	params := store.InsertPostingRuleParams{Name: name, Scope: string(b.Scope), Match: string(b.Match), AccountID: b.AccountId, Now: s.now()}
	q := store.New(s.deps.Pool)
	switch b.Scope {
	case gen.AccountingRuleScopeSales:
		if b.Match == gen.ExpenseCategory {
			f.add("match", "A sales rule matches line_kind, tax_category or tax_code.")
		}
	case gen.AccountingRuleScopeExpense:
		if b.Match != gen.ExpenseCategory {
			f.add("match", "An expense rule matches expense_category.")
		}
		params.VatDeductible = ptr(b.VatDeductible == nil || *b.VatDeductible)
	default:
		f.add("scope", "sales or expense.")
	}
	switch b.Match {
	case gen.ExpenseCategory:
		if b.ExpenseCategoryId == nil || *b.ExpenseCategoryId <= 0 {
			f.add("expenseCategoryId", "An expense category from Expenses.")
		}
		params.ExpenseCategoryID = b.ExpenseCategoryId
	case gen.LineKind:
		if b.LineKind == nil || !slices.Contains(lineKinds, *b.LineKind) {
			f.add("lineKind", "One of "+strings.Join(lineKinds, ", ")+".")
		}
		params.LineKind = b.LineKind
	case gen.TaxCategory:
		if b.TaxCategoryId == nil || *b.TaxCategoryId <= 0 {
			f.add("taxCategoryId", "A tax category from Products.")
		}
		params.TaxCategoryID = b.TaxCategoryId
	case gen.TaxCode:
		codes, err := q.TaxCodesByCode(ctx)
		if err != nil {
			return nil, err
		}
		if b.TaxCode == nil || !slices.ContainsFunc(codes, func(c store.TaxCodesByCodeRow) bool { return c.Code == *b.TaxCode && c.Active }) {
			f.addCoded("taxCode", codeTaxCodeUnknown, "An active standard tax code of the books.")
		}
		params.TaxCode = b.TaxCode
	default:
		f.add("match", "line_kind, tax_category, tax_code or expense_category; the default rules are seeded with the books.")
	}
	if err := f.refusal("The rule cannot be saved"); err != nil {
		return nil, err
	}
	if err := checkRuleAccount(ctx, q, string(b.Scope), b.AccountId); err != nil {
		return nil, err
	}
	id, err := q.InsertPostingRuleStrict(ctx, store.InsertPostingRuleStrictParams(params))
	if c := ruleConflict(err); c != nil {
		return nil, c
	}
	if err != nil {
		return nil, err
	}
	row, err := q.GetPostingRule(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.CreateAccountingPostingRule201JSONResponse(ruleView(row)), nil
}

// UpdateAccountingPostingRule changes a rule's name and account under its
// revision; a changed name or account is a new version.
func (s *server) UpdateAccountingPostingRule(ctx context.Context, req gen.UpdateAccountingPostingRuleRequestObject) (gen.UpdateAccountingPostingRuleResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The rule cannot be saved", "body", "", "A body is required.")
	}
	q := store.New(s.deps.Pool)
	current, err := q.GetPostingRule(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	var f fieldErrors
	name := ruleName(req.Body.Name, &f)
	if err := f.refusal("The rule cannot be saved"); err != nil {
		return nil, err
	}
	if req.Body.AccountId != current.AccountID {
		if err := checkRuleAccount(ctx, q, current.Scope, req.Body.AccountId); err != nil {
			return nil, err
		}
	}
	params := store.UpdatePostingRuleParams{ID: req.Id, Name: name, AccountID: req.Body.AccountId,
		Revision: req.Body.Revision, Now: s.now()}
	if current.Scope == "expense" {
		params.VatDeductible = req.Body.VatDeductible
	}
	_, err = q.UpdatePostingRule(ctx, params)
	if c := ruleConflict(err); c != nil {
		return nil, c
	}
	if noRows(err) {
		return nil, conflict(codeRevisionConflict, "The rule has changed", "Read it again and repeat the change.")
	}
	if err != nil {
		return nil, err
	}
	row, err := q.GetPostingRule(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.UpdateAccountingPostingRule200JSONResponse(ruleView(row)), nil
}

// DeleteAccountingPostingRule archives a rule that is not a default one
// (R15): it matches nothing from now on, and its versions stay, so every
// posting it placed still names it.
func (s *server) DeleteAccountingPostingRule(ctx context.Context, req gen.DeleteAccountingPostingRuleRequestObject) (gen.DeleteAccountingPostingRuleResponseObject, error) {
	q := store.New(s.deps.Pool)
	current, err := q.GetPostingRule(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	if current.Match == "default" {
		return nil, conflict(codeRuleDefault, "A default rule is not archived", "Point it at another account instead.")
	}
	if _, err := q.ArchivePostingRule(ctx, store.ArchivePostingRuleParams{ID: req.Id, Now: ptr(s.now())}); err != nil {
		return nil, err
	}
	s.logger().InfoContext(ctx, "accounting: a posting rule was archived", "rule", current.Name, "status", http.StatusNoContent)
	return gen.DeleteAccountingPostingRule204Response{}, nil
}
