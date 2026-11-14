package accounting

// Reconciliation's suggestions (A28, R5, R11, A29): for an open statement
// line, what it may be, most certain first, each with its reason. They are
// computed from the books alone — no call out — on the pool for the screen
// and again in the accepting transaction, where a suggestion that no longer
// holds is refused as stale. The kinds, in the order they are tried:
//
//  1. feed_payment — a payment Invoices registered from the same bank line:
//     the voucher's bank reference is the line's bank id or reference.
//  2. posting — an unmatched posting on the ledger account of the same
//     amount, dated within ten days, the nearest first.
//  3. batch — several of Invoices' payments on the account whose sum is the
//     line: by the batch's own transactions when camt.053 details them,
//     else by the payments of one day and one source, the booking day first.
//     Never a search over subsets: each group is a sum read once.
//  4. supplier_invoice — a posted supplier invoice owed, by its KID or its
//     number in the text, or its amount with the supplier's bank account.
//  5. vipps_payout — money in from Vipps: the line from clearing_vipps, what
//     the clearing holds beyond it to payment_fees.
//  6. vat — money to or from Skatteetaten that is the VAT settlement
//     account's balance or a closed term's settlement.
//  7. transfer — the opposite line on another bank account's statement, or
//     money to or from another bank account's number.
//  8. rule — a bank rule whose words the text or counterparty holds.
//
// A line that looks like a customer payment — money in with a KID, or a
// batch — that none of the first three explains is never coded here: its
// suggestion is Invoices' bank import (R11).

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
)

// The system's bank rules and their versions: what each voucher a match
// posts is. The reference page documents each.
const (
	bankRuleVipps    = "bank.vipps_payout"
	bankRuleVAT      = "bank.vat_settlement"
	bankRuleTransfer = "bank.transfer"
	bankRuleRule     = "bank.rule"
	bankRuleCoded    = "bank.coded"
	bankRuleUndone   = "bank.match_undone"

	bankRuleVersion int32 = 1

	// The sources of the vouchers a match posts and of their reversals.
	sourceBankMatch       = "bank_match"
	sourceBankMatchUndone = "bank_match_undone"
)

// The match kinds.
const (
	matchPosting  = "posting"
	matchFeed     = "feed_payment"
	matchBatch    = "batch"
	matchSupplier = "supplier_invoice"
	matchVipps    = "vipps_payout"
	matchVAT      = "vat"
	matchTransfer = "transfer"
	matchRule     = "rule"
	matchCoded    = "coded"
	// suggestInvoices is a customer payment's pointer to Invoices; it is
	// never accepted.
	suggestInvoices = "invoices"
)

// The windows the suggestions look in.
const (
	postingWindowDays  = 10
	batchWindowDays    = 3
	transferWindowDays = 3
	maxSuggestions     = 8
)

// The roles the bank's vouchers post to besides the bank account's own.
const (
	rolePaymentFees   = "payment_fees"
	roleVATSettlement = "vat_settlement"
)

// stmtLine is a statement line as the matching reads it.
type stmtLine struct {
	id, statementID, bankAccountID int64
	booked                         time.Time
	value                          *time.Time
	amount                         *big.Rat
	text, reference, kid           string
	cpName, cpAccount, bankID      string
	kind, state, source            string
	reversal                       bool
	batch                          []gen.AccountingBankBatchItem
}

func lineFromRow(r store.GetStatementLineRow) stmtLine {
	l := stmtLine{id: r.ID, statementID: r.StatementID, bankAccountID: r.BankAccountID, booked: r.BookedOn, value: r.ValueOn,
		amount: mustRat(r.Amount), text: r.Text, reference: r.Reference, kid: deref(r.Kid), cpName: r.CounterpartyName,
		cpAccount: r.CounterpartyAccount, bankID: deref(r.BankID), kind: r.Kind, state: r.State, source: r.StatementSource,
		reversal: r.Reversal}
	l.batch = batchOf(r.Batch)
	return l
}

func batchOf(raw []byte) []gen.AccountingBankBatchItem {
	if len(raw) == 0 {
		return nil
	}
	var out []gen.AccountingBankBatchItem
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// customerPayment is whether the line looks like a customer payment (R11):
// money in with a KID, or a batch.
func (l stmtLine) customerPayment() bool {
	return l.amount.Sign() > 0 && (l.kid != "" || len(l.batch) > 0)
}

// words is the line's text and counterparty, lower case, for the word
// tests.
func (l stmtLine) words() string {
	return strings.ToLower(l.text + " " + l.cpName + " " + l.reference)
}

// bankAccountOf is the bank account a line is on, as matching reads it.
type bankAccountOf struct {
	id     int64
	name   string
	number string
	ledger int32
}

// candidate is an unmatched posting on the ledger account.
type candidate struct {
	store.UnmatchedPostingsRow
	amount *big.Rat
}

// suggestion is one thing a line may be.
type suggestion struct {
	key, kind, certainty, reason string
	exact                        bool
	postings                     []candidate
	// voucher is what accepting posts, nil when it links postings that are
	// there; lines are its postings, shown before.
	voucher *Voucher
	// supplierInvoice and paid are a supplier payment's: through its own
	// path, not voucher.
	supplierInvoice *int64
	paid            *big.Rat
	otherLine       *int64
	otherLedger     int32
	ruleID          *int64
	ruleVersion     int32
}

// certaintyRank orders the suggestions: exact, likely, possible.
func certaintyRank(c string) int {
	switch c {
	case "exact":
		return 0
	case "likely":
		return 1
	}
	return 2
}

// suggester is what suggesting reads for one bank account, once: the
// account's unmatched postings around the lines, the open supplier invoices,
// the active rules, the roles and the accounts.
type suggester struct {
	q          *store.Queries
	account    bankAccountOf
	language   string
	candidates []candidate
	invoices   []store.OpenSupplierInvoicesRow
	rules      []store.ListBankRulesRow
	roles      map[string]int32
	others     []store.ListBankAccountsRow
	balances   map[string]*big.Rat
	terms      []store.ClosedTermSettlementsRow
	termsRead  bool
}

// newSuggester reads what the suggestions of lines booked from..to need.
func newSuggester(ctx context.Context, q *store.Queries, account bankAccountOf, from, to time.Time) (*suggester, error) {
	s := &suggester{q: q, account: account, roles: map[string]int32{}, balances: map[string]*big.Rat{}}
	rows, err := q.UnmatchedPostings(ctx, store.UnmatchedPostingsParams{AccountID: account.ledger,
		FromDay: from.AddDate(0, 0, -postingWindowDays), ToDay: to.AddDate(0, 0, postingWindowDays)})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		s.candidates = append(s.candidates, candidate{UnmatchedPostingsRow: r, amount: mustRat(r.Amount)})
	}
	if s.invoices, err = q.OpenSupplierInvoices(ctx); err != nil {
		return nil, err
	}
	if s.rules, err = q.ListBankRules(ctx); err != nil {
		return nil, err
	}
	roles, err := q.RoleAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		s.roles[r.Role] = r.ID
	}
	if s.others, err = q.ListBankAccounts(ctx); err != nil {
		return nil, err
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	s.language = settings.ChartLanguage
	return s, nil
}

// text is the description in the chart's language.
func (s *suggester) text(en, nb string, args ...any) string {
	return salesBooks{language: s.language}.text(en, nb, args...)
}

// balance is a role's account's balance at the end of a day.
func (s *suggester) balance(ctx context.Context, role string, through time.Time) (*big.Rat, int32, error) {
	id, ok := s.roles[role]
	if !ok {
		return nil, 0, nil
	}
	key := role + "@" + through.Format(time.DateOnly)
	if b, ok := s.balances[key]; ok {
		return b, id, nil
	}
	text, err := s.q.AccountBalanceThrough(ctx, store.AccountBalanceThroughParams{AccountID: id, Through: through})
	if err != nil {
		return nil, 0, err
	}
	b := mustRat(text)
	s.balances[key] = b
	return b, id, nil
}

// suggest is the line's suggestions, most certain first.
func (s *suggester) suggest(ctx context.Context, l stmtLine) ([]suggestion, error) {
	var out []suggestion
	used := map[int64]bool{}
	sameAmount := s.sameAmount(l)
	// 1. The payment Invoices registered from the same bank line.
	for _, c := range sameAmount {
		ref := deref(c.BankReference)
		if ref == "" || (ref != l.bankID && ref != l.reference) {
			continue
		}
		used[c.ID] = true
		out = append(out, suggestion{key: fmt.Sprintf("ref:%d", c.ID), kind: matchFeed, certainty: "exact", exact: true,
			reason: s.text("The payment Invoices registered from the same bank transaction (%s): voucher %s.",
				"Innbetalingen Fakturering registrerte fra samme banktransaksjon (%s): bilag %s.", ref, display(c.FiscalYear, c.Number)),
			postings: []candidate{c}})
	}
	// 2. A posting of the same amount, the nearest day first.
	var near []candidate
	for _, c := range sameAmount {
		if !used[c.ID] {
			near = append(near, c)
		}
	}
	sort.SliceStable(near, func(i, j int) bool {
		return daysApart(near[i].VoucherDate, l.booked) < daysApart(near[j].VoucherDate, l.booked)
	})
	sameDay := 0
	for _, c := range near {
		if daysApart(c.VoucherDate, l.booked) == 0 {
			sameDay++
		}
	}
	for i, c := range near {
		if i >= 5 {
			break
		}
		d := daysApart(c.VoucherDate, l.booked)
		certainty := "possible"
		switch {
		case d == 0:
			certainty = "exact"
		case d <= 3:
			certainty = "likely"
		}
		reason := s.text("The same amount, posted the same day: voucher %s, %s.", "Samme beløp, bokført samme dag: bilag %s, %s.",
			display(c.FiscalYear, c.Number), c.Description)
		if d != 0 {
			reason = s.text("The same amount, posted %d days apart: voucher %s, %s.", "Samme beløp, bokført %d dager fra: bilag %s, %s.",
				d, display(c.FiscalYear, c.Number), c.Description)
		}
		out = append(out, suggestion{key: fmt.Sprintf("posting:%d", c.ID), kind: matchPosting, certainty: certainty,
			exact: d == 0 && sameDay == 1, reason: reason, postings: []candidate{c}})
	}
	// 3. A batch of Invoices' payments.
	if b := s.batch(l, used); b != nil {
		out = append(out, *b)
	}
	explained := len(out) > 0
	if l.customerPayment() && !explained {
		out = append(out, suggestion{key: suggestInvoices, kind: suggestInvoices, certainty: "likely",
			reason: s.text("A customer payment that Invoices has not registered: import the bank file in Invoices, or register it on the invoice there.",
				"En innbetaling fra en kunde som Fakturering ikke har registrert: importer bankfilen i Fakturering, eller registrer den på fakturaen der.")})
	}
	if !l.customerPayment() {
		// 4. A supplier invoice.
		out = append(out, s.supplierInvoices(ctx, l)...)
		// 5. A Vipps payout.
		v, err := s.vipps(ctx, l)
		if err != nil {
			return nil, err
		}
		if v != nil {
			out = append(out, *v)
		}
		// 6. VAT.
		vat, err := s.vat(ctx, l)
		if err != nil {
			return nil, err
		}
		if vat != nil {
			out = append(out, *vat)
		}
		// 7. A transfer.
		tr, err := s.transfers(ctx, l)
		if err != nil {
			return nil, err
		}
		out = append(out, tr...)
		// 8. A bank rule.
		rs, err := s.ruleSuggestions(ctx, l)
		if err != nil {
			return nil, err
		}
		out = append(out, rs...)
	}
	sort.SliceStable(out, func(i, j int) bool { return certaintyRank(out[i].certainty) < certaintyRank(out[j].certainty) })
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	// Only a lone first suggestion that is exact is bulk-accepted.
	for i := range out {
		if i > 0 || (len(out) > 1 && out[1].certainty == "exact") {
			out[i].exact = false
		}
	}
	return out, nil
}

// sameAmount is the candidates of the line's amount within the window.
func (s *suggester) sameAmount(l stmtLine) []candidate {
	var out []candidate
	for _, c := range s.candidates {
		if c.amount.Cmp(l.amount) == 0 && daysApart(c.VoucherDate, l.booked) <= postingWindowDays {
			out = append(out, c)
		}
	}
	return out
}

// daysApart is how many days lie between two days.
func daysApart(a, b time.Time) int {
	d := int(a.Sub(b).Hours() / 24)
	if d < 0 {
		return -d
	}
	return d
}

// isInvoicesPayment is whether a candidate is a payment Invoices registered:
// a customer payment's voucher of its sales journal.
func isInvoicesPayment(c candidate) bool {
	return deref(c.SourceModule) == sourceInvoices && deref(c.SourceKind) == "payment" && c.amount.Sign() > 0
}

// batch is a group of Invoices' payments whose sum is the line, bounded: the
// batch's own transactions one by one, else the payments of one day — and of
// one source, then of any — from the booking day back three days.
func (s *suggester) batch(l stmtLine, used map[int64]bool) *suggestion {
	if l.amount.Sign() <= 0 {
		return nil
	}
	var pool []candidate
	for _, c := range s.candidates {
		if !used[c.ID] && isInvoicesPayment(c) && daysApart(c.VoucherDate, l.booked) <= postingWindowDays {
			pool = append(pool, c)
		}
	}
	if len(pool) < 2 {
		return nil
	}
	sum := func(cs []candidate) *big.Rat {
		t := new(big.Rat)
		for _, c := range cs {
			t.Add(t, c.amount)
		}
		return t
	}
	group := func(cs []candidate, certainty, reason string) *suggestion {
		ids := make([]string, 0, len(cs))
		for _, c := range cs {
			ids = append(ids, strconv.FormatInt(c.ID, 10))
		}
		slices.Sort(ids)
		return &suggestion{key: "batch:" + strings.Join(ids, ","), kind: matchBatch, certainty: certainty, reason: reason, postings: cs}
	}
	if len(l.batch) > 1 {
		// Each of the batch's transactions to one payment of its amount, the
		// nearest day first: a matching, not a search.
		taken := map[int64]bool{}
		var picked []candidate
		for _, item := range l.batch {
			want, ok := parseAmount(item.Amount)
			if !ok {
				return nil
			}
			best := -1
			for i, c := range pool {
				if taken[c.ID] || c.amount.Cmp(want) != 0 {
					continue
				}
				if best < 0 || daysApart(c.VoucherDate, l.booked) < daysApart(pool[best].VoucherDate, l.booked) {
					best = i
				}
			}
			if best < 0 {
				return nil
			}
			taken[pool[best].ID] = true
			picked = append(picked, pool[best])
		}
		if sum(picked).Cmp(l.amount) != 0 {
			return nil
		}
		g := group(picked, "exact", s.text("The batch's %d payments, each one Invoices registered: %s in all.",
			"Bunten med %d innbetalinger, hver registrert i Fakturering: %s i alt.", len(picked), money(l.amount)))
		g.exact = true
		return g
	}
	for back := 0; back <= batchWindowDays; back++ {
		d := l.booked.AddDate(0, 0, -back)
		bySource := map[string][]candidate{}
		var all []candidate
		for _, c := range pool {
			if c.VoucherDate.Equal(d) {
				bySource[deref(c.BankSource)] = append(bySource[deref(c.BankSource)], c)
				all = append(all, c)
			}
		}
		sources := make([]string, 0, len(bySource))
		for k := range bySource {
			sources = append(sources, k)
		}
		slices.Sort(sources)
		for _, src := range sources {
			cs := bySource[src]
			if len(cs) >= 2 && sum(cs).Cmp(l.amount) == 0 {
				return group(cs, "likely", s.text("%d payments Invoices registered on %s from one bank file: %s in all.",
					"%d innbetalinger Fakturering registrerte %s fra én bankfil: %s i alt.", len(cs), d.Format(time.DateOnly), money(l.amount)))
			}
		}
		if len(all) >= 2 && sum(all).Cmp(l.amount) == 0 {
			return group(all, "possible", s.text("%d payments Invoices registered on %s: %s in all.",
				"%d innbetalinger Fakturering registrerte %s: %s i alt.", len(all), d.Format(time.DateOnly), money(l.amount)))
		}
	}
	return nil
}

// supplierInvoices is the supplier invoices an outgoing line may pay: by KID,
// by the invoice's number or reference in the text, or by the amount and the
// supplier's bank account.
func (s *suggester) supplierInvoices(ctx context.Context, l stmtLine) []suggestion {
	if l.amount.Sign() >= 0 {
		return nil
	}
	paid := neg(l.amount)
	words := l.words()
	var out []suggestion
	for _, inv := range s.invoices {
		owed := neg(mustRat(inv.Balance))
		foreign := inv.Currency != currency
		certainty, why := "", ""
		switch {
		case l.kid != "" && deref(inv.Kid) == l.kid:
			certainty = "likely"
			if !foreign && owed.Cmp(paid) == 0 {
				certainty = "exact"
			}
			why = s.text("Its KID %s", "KID-en %s", l.kid)
		case !foreign && owed.Cmp(paid) == 0 && inv.SupplierBankAccount != nil && *inv.SupplierBankAccount == l.cpAccount && l.cpAccount != "":
			certainty, why = "likely", s.text("The amount, to the supplier's account %s", "Beløpet, til leverandørens konto %s", l.cpAccount)
		case foreign && inv.SupplierBankAccount != nil && *inv.SupplierBankAccount == l.cpAccount && l.cpAccount != "":
			certainty, why = "possible", s.text("To the supplier's account %s", "Til leverandørens konto %s", l.cpAccount)
		case len(inv.SupplierNumber) >= 3 && strings.Contains(words, strings.ToLower(inv.SupplierNumber)):
			certainty, why = "likely", s.text("Its number %s in the text", "Nummeret %s i teksten", inv.SupplierNumber)
		case inv.Reference != nil && len(*inv.Reference) >= 3 && strings.Contains(words, strings.ToLower(*inv.Reference)):
			certainty, why = "likely", s.text("Its reference %s in the text", "Referansen %s i teksten", *inv.Reference)
		case !foreign && owed.Cmp(paid) == 0:
			certainty, why = "possible", s.text("The amount owed", "Beløpet som skyldes")
		default:
			continue
		}
		if !foreign && paid.Cmp(owed) > 0 {
			continue
		}
		id := inv.ID
		sg := suggestion{key: fmt.Sprintf("supplier:%d", inv.ID), kind: matchSupplier, certainty: certainty,
			reason: s.text("%s: supplier invoice %s from %s, %s %s owed — registered as its payment.",
				"%s: leverandørfaktura %s fra %s, %s %s skyldes — registreres som betaling av den.", why, inv.SupplierNumber,
				inv.SupplierName, money(abs(owedInCurrency(ctx, s.q, inv))), inv.Currency),
			supplierInvoice: &id, paid: paid}
		sg.voucher = s.supplierVoucherShown(ctx, inv, paid)
		out = append(out, sg)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// owedInCurrency is what is left on a supplier invoice in its own currency.
func owedInCurrency(ctx context.Context, q *store.Queries, inv store.OpenSupplierInvoicesRow) *big.Rat {
	if inv.Currency == currency || inv.OpenItemID == nil {
		return mustRat(inv.Balance)
	}
	b, err := q.OpenItemCurrencyBalance(ctx, *inv.OpenItemID)
	if err != nil {
		return mustRat(inv.Balance)
	}
	return mustRat(b.BalanceCurrency)
}

// supplierVoucherShown is the voucher a supplier payment would post, for the
// screen: payables by what it settles, the bank by what it took, the
// currency difference to its role — as paymentLines builds it, settling
// all that is left.
func (s *suggester) supplierVoucherShown(ctx context.Context, inv store.OpenSupplierInvoicesRow, paid *big.Rat) *Voucher {
	settled := paid
	if inv.Currency != currency {
		settled = neg(mustRat(inv.Balance))
	}
	lines := paymentLines(true, settled, paid, owedInCurrency(ctx, s.q, inv), false, "", nil, inv.SupplierID,
		deref(inv.OpenItemID), Line{AccountID: s.account.ledger}, "")
	return &Voucher{Kind: "supplier_payment", Lines: lines}
}

// vipps is a Vipps payout (R11): money in whose text names Vipps, taken
// from the clearing account; what the clearing holds through the day before
// beyond it — or through the day itself, when the day before holds less — is
// Vipps' fees.
func (s *suggester) vipps(ctx context.Context, l stmtLine) (*suggestion, error) {
	if l.amount.Sign() <= 0 || !strings.Contains(l.words(), "vipps") {
		return nil, nil
	}
	clearing, id, err := s.balance(ctx, roleClearingVipps, l.booked.AddDate(0, 0, -1))
	if err != nil || clearing == nil {
		return nil, err
	}
	through := l.booked.AddDate(0, 0, -1)
	if clearing.Cmp(l.amount) < 0 {
		if clearing, _, err = s.balance(ctx, roleClearingVipps, l.booked); err != nil {
			return nil, err
		}
		through = l.booked
	}
	if clearing.Cmp(l.amount) < 0 {
		return nil, nil
	}
	fees := sub(clearing, l.amount)
	lines := []Line{{AccountID: s.account.ledger, Amount: new(big.Rat).Set(l.amount)}}
	if fees.Sign() > 0 {
		lines = append(lines, Line{Role: rolePaymentFees, Amount: fees})
	}
	lines = append(lines, Line{AccountID: id, Amount: neg(clearing)})
	certainty := "likely"
	if fees.Sign() == 0 {
		certainty = "exact"
	}
	return &suggestion{key: "vipps:" + money(clearing) + "@" + through.Format(time.DateOnly), kind: matchVipps, certainty: certainty,
		reason: s.text("A Vipps payout: the clearing account holds %s through %s, so %s are Vipps' fees.",
			"En utbetaling fra Vipps: oppgjørskontoen har %s til og med %s, så %s er Vipps' gebyrer.", money(clearing),
			through.Format(time.DateOnly), money(fees)),
		voucher: &Voucher{Kind: "bank", Rule: bankRuleVipps, Description: s.text("Vipps payout", "Utbetaling fra Vipps"), Lines: lines}}, nil
}

// vat is a VAT payment or refund: the line is the VAT settlement account's
// balance through its day, or a closed term's settlement (A22), and moves it
// to the bank.
func (s *suggester) vat(ctx context.Context, l stmtLine) (*suggestion, error) {
	balance, id, err := s.balance(ctx, roleVATSettlement, l.booked)
	if err != nil || balance == nil {
		return nil, err
	}
	why := ""
	if balance.Sign() != 0 && balance.Cmp(l.amount) == 0 {
		why = s.text("the VAT settlement account's balance %s", "saldoen %s på oppgjørskontoen for merverdiavgift", money(balance))
	} else {
		if !s.termsRead {
			if s.terms, err = s.q.ClosedTermSettlements(ctx, id); err != nil {
				return nil, err
			}
			s.termsRead = true
		}
		for _, t := range s.terms {
			if mustRat(t.Amount).Cmp(l.amount) == 0 {
				why = s.text("the settlement of VAT term %d %d", "oppgjøret av mva-termin %d %d", t.Term, t.Year)
				break
			}
		}
	}
	if why == "" {
		return nil, nil
	}
	certainty := "possible"
	if w := l.words(); strings.Contains(w, "skatteetaten") || strings.Contains(w, "skatt") || strings.Contains(w, "mva") {
		certainty = "likely"
	}
	description := s.text("VAT paid to Skatteetaten", "Merverdiavgift betalt til Skatteetaten")
	if l.amount.Sign() > 0 {
		description = s.text("VAT refunded by Skatteetaten", "Merverdiavgift tilbakebetalt fra Skatteetaten")
	}
	return &suggestion{key: "vat:" + money(l.amount), kind: matchVAT, certainty: certainty,
		reason: s.text("A VAT payment or refund: the amount is %s.", "En betaling eller tilbakebetaling av merverdiavgift: beløpet er %s.", why),
		voucher: &Voucher{Kind: "bank", Rule: bankRuleVAT, Description: description, Lines: []Line{
			{AccountID: s.account.ledger, Amount: new(big.Rat).Set(l.amount)},
			{AccountID: id, Amount: neg(l.amount)},
		}}}, nil
}

// transfers is a transfer between two of the company's bank accounts: the
// opposite line on another's statement within three days, or money to or
// from another's number.
func (s *suggester) transfers(ctx context.Context, l stmtLine) ([]suggestion, error) {
	rows, err := s.q.OpenLinesNear(ctx, store.OpenLinesNearParams{BankAccountID: s.account.id, Amount: money(neg(l.amount)),
		FromDay: l.booked.AddDate(0, 0, -transferWindowDays), ToDay: l.booked.AddDate(0, 0, transferWindowDays), Near: l.booked})
	if err != nil {
		return nil, err
	}
	var out []suggestion
	for _, r := range rows {
		other := r.ID
		certainty := "likely"
		if daysApart(r.BookedOn, l.booked) == 0 {
			certainty = "exact"
		}
		out = append(out, suggestion{key: fmt.Sprintf("transfer:line:%d", r.ID), kind: matchTransfer, certainty: certainty,
			reason: s.text("A transfer: %s on %s's statement on %s.", "En overføring: %s på kontoutskriften til %s %s.",
				money(neg(l.amount)), r.BankAccountName, r.BookedOn.Format(time.DateOnly)),
			otherLine: &other, otherLedger: r.LedgerAccountID, voucher: s.transferVoucher(l, r.LedgerAccountID)})
		if len(out) >= 2 {
			return out, nil
		}
	}
	if len(out) == 0 && l.cpAccount != "" {
		for _, o := range s.others {
			if o.ID == s.account.id || !o.Active || o.AccountNumber != l.cpAccount {
				continue
			}
			out = append(out, suggestion{key: fmt.Sprintf("transfer:account:%d", o.ID), kind: matchTransfer, certainty: "likely",
				reason: s.text("A transfer to or from %s (%s), whose statement does not show it yet.",
					"En overføring til eller fra %s (%s), som ikke har den på kontoutskriften ennå.", o.Name, o.AccountNumber),
				otherLedger: o.LedgerAccountID, voucher: s.transferVoucher(l, o.LedgerAccountID)})
		}
	}
	return out, nil
}

func (s *suggester) transferVoucher(l stmtLine, other int32) *Voucher {
	return &Voucher{Kind: "bank", Rule: bankRuleTransfer, Description: s.text("Transfer between bank accounts", "Overføring mellom bankkontoer"),
		Lines: []Line{{AccountID: s.account.ledger, Amount: new(big.Rat).Set(l.amount)}, {AccountID: other, Amount: neg(l.amount)}}}
}

// ruleSuggestions is each active bank rule the line matches, as the coded
// voucher it proposes: the bank's amount gross on the rule's account with its
// tax code, through the manual journal's tax computation.
func (s *suggester) ruleSuggestions(ctx context.Context, l stmtLine) ([]suggestion, error) {
	var out []suggestion
	text, party := strings.ToLower(l.text+" "+l.reference), strings.ToLower(l.cpName)
	for _, r := range s.rules {
		switch {
		case !r.Active:
			continue
		case r.BankAccountID != nil && *r.BankAccountID != s.account.id:
			continue
		case r.Direction == "in" && l.amount.Sign() < 0, r.Direction == "out" && l.amount.Sign() > 0:
			continue
		case r.TextContains != nil && !strings.Contains(text, strings.ToLower(*r.TextContains)):
			continue
		case r.CounterpartyContains != nil && !strings.Contains(party, strings.ToLower(*r.CounterpartyContains)):
			continue
		}
		description := r.Description
		if description == "" {
			description = r.Name
		}
		input := gen.AccountingJournalInput{VoucherDate: openapi_types.Date{Time: l.booked}, Description: description,
			Entry: ptr(gen.Gross), Lines: []gen.AccountingJournalLineInput{
				{AccountId: s.account.ledger, Amount: money(l.amount)},
				{AccountId: r.AccountID, Amount: money(neg(l.amount)), TaxCodeId: r.TaxCodeID, ProjectId: r.ProjectID, Text: ptr(r.Name)},
			}}
		postings, err := expandJournal(ctx, s.q, input)
		if err != nil {
			// A rule whose account or code no longer holds proposes nothing.
			continue
		}
		v := &Voucher{Kind: "bank", Rule: bankRuleRule, Description: truncate(description, 500)}
		for _, p := range postings {
			v.Lines = append(v.Lines, p.line)
		}
		id := r.ID
		out = append(out, suggestion{key: fmt.Sprintf("rule:%d:%d", r.ID, r.Version), kind: matchRule, certainty: "likely",
			reason:  s.text("The bank rule %s: to account %s.", "Bankregelen %s: til konto %s.", r.Name, r.AccountNumber),
			voucher: v, ruleID: &id, ruleVersion: r.Version})
		if len(out) >= 2 {
			break
		}
	}
	return out, nil
}

// suggestionView is a suggestion as the contract answers it, its voucher's
// lines named.
func suggestionView(sg suggestion, names map[int32]store.AccountsForPostingRow, codes map[int32]string, roles map[string]int32) gen.AccountingBankSuggestion {
	out := gen.AccountingBankSuggestion{Key: sg.key, Kind: sg.kind, Certainty: sg.certainty, Exact: sg.exact, Reason: sg.reason,
		Postings: []gen.AccountingBankMatchedPosting{}, Lines: []gen.AccountingBankProposedLine{}, SupplierInvoiceId: sg.supplierInvoice,
		OtherLineId: sg.otherLine, BankRuleId: sg.ruleID}
	for _, c := range sg.postings {
		out.Postings = append(out.Postings, candidateView(c))
	}
	if sg.voucher != nil {
		for _, l := range sg.voucher.Lines {
			id := l.AccountID
			if id == 0 {
				id = roles[l.Role]
			}
			pl := gen.AccountingBankProposedLine{AccountId: id, Amount: money(l.Amount), TaxCodeId: l.TaxCodeID}
			if a, ok := names[id]; ok {
				pl.AccountNumber, pl.AccountName = a.Number, a.Name
			}
			if l.TaxCodeID != nil {
				if c, ok := codes[*l.TaxCodeID]; ok {
					pl.TaxCode = ptr(c)
				}
			}
			out.Lines = append(out.Lines, pl)
		}
	}
	return out
}

func candidateView(c candidate) gen.AccountingBankMatchedPosting {
	return gen.AccountingBankMatchedPosting{PostingId: c.ID, VoucherId: c.VoucherID, VoucherDisplay: display(c.FiscalYear, c.Number),
		VoucherDate: apiDate(c.VoucherDate), VoucherKind: c.Kind, Description: firstNonEmpty(c.PostingText, c.Description),
		Amount: money(c.amount)}
}
