package accounting

// Supplier invoices (A18 as amended by R8): a draft is no voucher, and is
// edited and deleted freely; Post books it through the engine with its
// source key and makes its open item on payables; its files become the
// voucher's attachments. A mistake after posting is corrected by Reverse and
// copy. Post, the reversal and a draft's deletion publish
// accounting.supplier_invoice.posted, .reversed and .deleted on their own
// transactions (purchasing design P5, A79), and the list
// and the page carry Purchasing's match as a badge when it is on (P6). The same supplier's number twice is refused among the invoices that
// are not reversed.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/events"
)

const (
	defaultSupplierInvoiceLimit = 100
	maxSupplierInvoiceLimit     = 500
	maxSupplierInvoiceLines     = 200
	// rateScale is the decimals an exchange rate is kept to (numeric(12,6)).
	rateScale = 6
)

// draftInput is a supplier invoice's input checked: what a draft stores.
type draftInput struct {
	supplierID       int64
	kind             string
	number           string
	invoiceDate      time.Time
	dueDate          *time.Time
	currency         string
	rate, total      *big.Rat
	totalNOK         *big.Rat
	kid, reference   *string
	description      string
	creditsInvoiceID *int64
	lines            []purchaseLineIn
}

// rateText is a rate as the column keeps it.
func rateText(r *big.Rat) string { return r.FloatString(rateScale) }

// rateColumn is a rate for the nullable column: nil while it is not set.
func rateColumn(r *big.Rat) *string {
	if r == nil {
		return nil
	}
	return ptr(rateText(r))
}

// moneyColumn is an amount for a nullable column: nil while it is not set.
func moneyColumn(r *big.Rat) *string {
	if r == nil {
		return nil
	}
	return ptr(money(r))
}

// optRat is a nullable numeric column read as text: nil when empty.
func optRat(s string) *big.Rat {
	if s == "" {
		return nil
	}
	return mustRat(s)
}

// roundRate is a rate at six decimals, half away from zero.
func roundRate(r *big.Rat) *big.Rat {
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(rateScale), nil))
	scaled := new(big.Rat).Mul(r, scale)
	n := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	rem := new(big.Rat).Sub(scaled, new(big.Rat).SetInt(n))
	half := big.NewRat(1, 2)
	if rem.Cmp(half) >= 0 {
		n.Add(n, big.NewInt(1))
	} else if new(big.Rat).Neg(rem).Cmp(half) >= 0 {
		n.Sub(n, big.NewInt(1))
	}
	return new(big.Rat).Quo(new(big.Rat).SetInt(n), scale)
}

// checkDraft holds a supplier invoice's input to its rules (A18, R8): a
// supplier that exists (and is active for a new invoice), the supplier's
// number, the dates, a currency with its rate or its NOK total, a credited
// invoice that is a posted invoice of the same supplier, and every line's
// account and tax code. The lines need not sum to the total yet; Post holds
// them to it.
func checkDraft(ctx context.Context, q *store.Queries, b gen.AccountingSupplierInvoiceInput, isNew bool) (draftInput, error) {
	const title = "The supplier invoice cannot be saved"
	var f fieldErrors
	out := draftInput{supplierID: b.SupplierId, kind: string(b.Kind), number: strings.TrimSpace(b.InvoiceNumber),
		invoiceDate: day(b.InvoiceDate), description: strings.TrimSpace(deref(b.Description)),
		creditsInvoiceID: b.CreditsInvoiceId}
	sup, err := q.GetSupplier(ctx, b.SupplierId)
	switch {
	case noRows(err):
		f.add("supplierId", "No such supplier.")
	case err != nil:
		return out, err
	case isNew && !sup.Active:
		f.addCoded("supplierId", codeSupplierInactive, "The supplier is inactive.")
	}
	if out.kind != "invoice" && out.kind != "credit_note" {
		f.add("kind", "invoice or credit_note.")
	}
	if out.number == "" || len([]rune(out.number)) > 60 {
		f.add("invoiceNumber", "The supplier's number, 1 to 60 characters.")
	}
	if len([]rune(out.description)) > 500 {
		f.add("description", "At most 500 characters.")
	}
	out.kid = optional(b.Kid, "kid", 25, &f)
	out.reference = optional(b.Reference, "reference", 100, &f)
	out.currency = currency
	if err == nil && sup.Currency != "" {
		out.currency = sup.Currency
	}
	if b.Currency != nil && strings.TrimSpace(*b.Currency) != "" {
		out.currency = strings.ToUpper(strings.TrimSpace(*b.Currency))
	}
	if !currencyPattern.MatchString(out.currency) {
		f.add("currency", "A currency code of three letters (ISO 4217).")
	}
	total, ok := parseAmount(b.Total)
	if !ok || total.Sign() < 0 {
		f.addCoded("total", codeAmountInvalid, "A total of zero or more, with at most two decimals.")
		total = new(big.Rat)
	}
	out.total = total
	out.rate, out.totalNOK = big.NewRat(1, 1), new(big.Rat).Set(total)
	if out.currency != currency {
		var rate, nok *big.Rat
		if b.ExchangeRate != nil && strings.TrimSpace(*b.ExchangeRate) != "" {
			r, ok := new(big.Rat).SetString(strings.TrimSpace(*b.ExchangeRate))
			if !ok || r.Sign() <= 0 {
				f.add("exchangeRate", "NOK per unit, above zero.")
			} else {
				rate = roundRate(r)
			}
		}
		if b.TotalNok != nil && strings.TrimSpace(*b.TotalNok) != "" {
			n, ok := parseAmount(*b.TotalNok)
			if !ok || n.Sign() < 0 {
				f.addCoded("totalNok", codeAmountInvalid, "A total in NOK of zero or more, with at most two decimals.")
			} else {
				nok = n
			}
		}
		switch {
		case rate == nil && nok == nil:
			f.addCoded("exchangeRate", codeExchangeRateRequired, "An invoice in "+out.currency+" needs the rate or the total in NOK.")
		case rate != nil && nok == nil:
			out.rate, out.totalNOK = rate, docmath.Round2(new(big.Rat).Mul(total, rate))
		case rate == nil && total.Sign() == 0:
			f.addCoded("exchangeRate", codeExchangeRateRequired, "A total of zero has no rate to take from the NOK total.")
		case rate == nil:
			out.rate, out.totalNOK = roundRate(new(big.Rat).Quo(nok, total)), nok
		default:
			// Both given: they must agree, total × rate to the øre within an
			// øre per line, or one of them is mistyped (M1).
			diff := new(big.Rat).Sub(docmath.Round2(new(big.Rat).Mul(total, rate)), nok)
			slack := new(big.Rat).Mul(big.NewRat(1, 100), big.NewRat(int64(max(1, len(b.Lines))), 1))
			if diff.Abs(diff).Cmp(slack) > 0 {
				f.addCoded("totalNok", codeExchangeRateMismatch, "The total in NOK is not the total at the rate: "+
					money(docmath.Round2(new(big.Rat).Mul(total, rate)))+" at "+rateText(rate)+". Correct one of them, or give only one.")
			}
			out.rate, out.totalNOK = rate, nok
		}
	}
	if b.DueDate != nil {
		out.dueDate = ptr(day(*b.DueDate))
		if out.dueDate.Before(out.invoiceDate) {
			f.add("dueDate", "On or after the invoice date.")
		}
	} else if err == nil {
		out.dueDate = ptr(out.invoiceDate.AddDate(0, 0, int(sup.PaymentTermsDays)))
	}
	if b.CreditsInvoiceId != nil {
		if out.kind != "credit_note" {
			f.add("creditsInvoiceId", "Only a credit note credits an invoice.")
		} else {
			credited, err := q.GetSupplierInvoices(ctx, []int64{*b.CreditsInvoiceId})
			if err != nil {
				return out, err
			}
			if len(credited) != 1 || credited[0].Kind != "invoice" || credited[0].State != "posted" ||
				credited[0].SupplierID != b.SupplierId {
				f.add("creditsInvoiceId", "A posted invoice of the same supplier.")
			}
		}
	}
	if len(b.Lines) > maxSupplierInvoiceLines {
		f.add("lines", fmt.Sprintf("At most %d lines.", maxSupplierInvoiceLines))
	}
	var accountIDs, codeIDs []int32
	for _, l := range b.Lines {
		accountIDs = append(accountIDs, l.AccountId)
		if l.TaxCodeId != nil {
			codeIDs = append(codeIDs, *l.TaxCodeId)
		}
	}
	accounts, err := q.AccountsForPosting(ctx, accountIDs)
	if err != nil {
		return out, err
	}
	known := map[int32]store.AccountsForPostingRow{}
	for _, a := range accounts {
		known[a.ID] = a
	}
	codes, err := taxCodesOn(ctx, q, codeIDs, out.invoiceDate)
	if err != nil {
		return out, err
	}
	for i, l := range b.Lines {
		field := fmt.Sprintf("lines[%d]", i)
		a, ok := known[l.AccountId]
		switch {
		case !ok:
			f.addCoded(field+".accountId", codeAccountUnknown, "No such account.")
		case !a.Active:
			f.addCoded(field+".accountId", codeAccountInactive, "Account "+a.Number+" is inactive.")
		case a.SystemRole != nil && (*a.SystemRole == rolePayables || *a.SystemRole == roleReceivables):
			f.add(field+".accountId", "A line is a cost or an asset, not the party ledger itself.")
		}
		amount, ok := parseAmount(l.Amount)
		if !ok {
			f.addCoded(field+".amount", codeAmountInvalid, "An exact amount with at most two decimals.")
			continue
		}
		in := purchaseLineIn{AccountID: l.AccountId, TaxCodeID: l.TaxCodeId, Amount: amount,
			Gross: l.Entry == nil || *l.Entry == gen.Gross, ProjectID: l.ProjectId, Text: strings.TrimSpace(deref(l.Text))}
		if len([]rune(in.Text)) > 500 {
			f.add(field+".text", "At most 500 characters.")
		}
		var code *store.TaxCodesForPostingRow
		if l.TaxCodeId != nil {
			c, ok := codes[*l.TaxCodeId]
			if !ok {
				f.addCoded(field+".taxCodeId", codeTaxCodeUnknown, "No such active tax code.")
				continue
			}
			code = &c
		}
		if _, rule, msg := computePurchaseLine(in, code); rule != "" {
			f.addCoded(field+".taxCodeId", rule, msg)
		}
		out.lines = append(out.lines, in)
	}
	return out, f.refusal(title)
}

// taxCodesOn is the codes with those ids with their rate on day.
func taxCodesOn(ctx context.Context, q *store.Queries, ids []int32, on time.Time) (map[int32]store.TaxCodesForPostingRow, error) {
	out := map[int32]store.TaxCodesForPostingRow{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.TaxCodesForPosting(ctx, store.TaxCodesForPostingParams{Ids: ids, Day: on})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// duplicateConflict is the 409 of the duplicate guard, nil for any other
// error.
func duplicateConflict(err error) error {
	if violates(err, "ux_supplier_invoices_number") {
		return conflict(codeDuplicateSupplierInv, "The supplier's invoice is already entered",
			"Another invoice of this supplier, not reversed, has the same number.")
	}
	return nil
}

// writeDraftLines replaces a draft's lines.
func writeDraftLines(ctx context.Context, q *store.Queries, id int64, lines []purchaseLineIn) error {
	if err := q.DeleteSupplierInvoiceLines(ctx, id); err != nil {
		return err
	}
	for i, l := range lines {
		entry := "net"
		if l.Gross {
			entry = "gross"
		}
		if err := q.InsertSupplierInvoiceLine(ctx, store.InsertSupplierInvoiceLineParams{SupplierInvoiceID: id,
			Position: int32(i + 1), AccountID: l.AccountID, TaxCodeID: l.TaxCodeID, Amount: money(l.Amount), Entry: entry,
			ProjectID: l.ProjectID, Text: l.Text}); err != nil {
			return err
		}
	}
	return nil
}

// insertDraft writes a new draft and its lines on q.
func insertDraft(ctx context.Context, q *store.Queries, d draftInput, copiedFrom, expense *int64, by uuid.UUID, now time.Time) (int64, error) {
	id, err := q.InsertSupplierInvoice(ctx, store.InsertSupplierInvoiceParams{SupplierID: d.supplierID, Kind: d.kind,
		SupplierNumber: d.number, InvoiceDate: d.invoiceDate, DueDate: d.dueDate, Currency: d.currency,
		ExchangeRate: rateColumn(d.rate), Total: money(d.total), TotalNok: moneyColumn(d.totalNOK), Kid: d.kid, Reference: d.reference,
		Description: d.description, CreditsInvoiceID: d.creditsInvoiceID, CopiedFromID: copiedFrom,
		SourceExpenseEntryID: expense, Now: now, UserID: by})
	if err != nil {
		return 0, err
	}
	return id, writeDraftLines(ctx, q, id, d.lines)
}

// ListAccountingSupplierInvoices is a page of the supplier invoices.
func (s *server) ListAccountingSupplierInvoices(ctx context.Context, req gen.ListAccountingSupplierInvoicesRequestObject) (gen.ListAccountingSupplierInvoicesResponseObject, error) {
	p := req.Params
	limit, offset := int32(defaultSupplierInvoiceLimit), int32(0)
	if p.Limit != nil {
		limit = min(max(*p.Limit, 1), maxSupplierInvoiceLimit)
	}
	if p.Offset != nil && *p.Offset > 0 {
		offset = *p.Offset
	}
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	params := store.ListSupplierInvoiceIDsParams{SupplierID: p.SupplierId, DueFrom: datePtr(p.DueFrom), DueTo: datePtr(p.DueTo),
		Overdue: p.Overdue != nil && *p.Overdue, Today: today, RowLimit: limit, RowOffset: offset}
	if p.State != nil {
		params.State = ptr(string(*p.State))
	}
	if p.SupplierNumber != nil && strings.TrimSpace(*p.SupplierNumber) != "" {
		params.SupplierNumber = ptr(strings.TrimSpace(*p.SupplierNumber))
	}
	rows, err := store.New(s.deps.Pool).ListSupplierInvoiceIDs(ctx, params)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	out := gen.ListAccountingSupplierInvoices200JSONResponse{}
	for _, r := range rows {
		ids = append(ids, r.ID)
		out.Total = int32(r.Total)
	}
	if out.Items, err = s.supplierInvoices(ctx, ids); err != nil {
		return nil, err
	}
	return out, nil
}

// supplierInvoices is the invoices with those ids as the contract answers
// them, in the order given — with their lines computed, files, payments and
// the open item's balance.
func (s *server) supplierInvoices(ctx context.Context, ids []int64) ([]gen.AccountingSupplierInvoice, error) {
	out := []gen.AccountingSupplierInvoice{}
	if len(ids) == 0 {
		return out, nil
	}
	q := store.New(s.deps.Pool)
	rows, err := q.GetSupplierInvoices(ctx, ids)
	if err != nil {
		return nil, err
	}
	lines, err := q.SupplierInvoiceLines(ctx, ids)
	if err != nil {
		return nil, err
	}
	files, err := q.SupplierInvoiceFiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	payments, err := q.SupplierPayments(ctx, ids)
	if err != nil {
		return nil, err
	}
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	linesOf := map[int64][]store.SupplierInvoiceLinesRow{}
	var projectIDs []int32
	for _, l := range lines {
		linesOf[l.SupplierInvoiceID] = append(linesOf[l.SupplierInvoiceID], l)
		if l.ProjectID != nil {
			projectIDs = append(projectIDs, *l.ProjectID)
		}
	}
	projects := s.projectNames(ctx, projectIDs)
	filesOf := map[int64][]gen.AccountingAttachment{}
	for _, f := range files {
		filesOf[f.SupplierInvoiceID] = append(filesOf[f.SupplierInvoiceID], fileView(f))
	}
	paymentsOf := map[int64][]gen.AccountingSupplierPayment{}
	for _, p := range payments {
		v := gen.AccountingSupplierPayment{Id: p.ID, PaidOn: apiDate(p.PaidOn), AmountNok: money(mustRat(p.AmountNok)),
			AmountCurrency: money(mustRat(p.AmountCurrency)), BankAccountId: p.BankAccountID, BankAccountNumber: p.BankAccountNumber,
			Voucher:            gen.AccountingVoucherRef{Id: p.VoucherID, Display: display(p.VoucherYear, p.VoucherNumber)},
			CurrencyDifference: money(mustRat(p.CurrencyDifference)), RemovedAt: p.RemovedAt}
		if p.RemovalVoucherID != nil && p.RemovalYear != nil && p.RemovalNumber != nil {
			v.RemovalVoucher = &gen.AccountingVoucherRef{Id: *p.RemovalVoucherID, Display: display(*p.RemovalYear, *p.RemovalNumber)}
		}
		paymentsOf[p.SupplierInvoiceID] = append(paymentsOf[p.SupplierInvoiceID], v)
	}
	byID := map[int64]gen.AccountingSupplierInvoice{}
	for _, r := range rows {
		v, err := s.supplierInvoiceView(ctx, q, r, linesOf[r.ID], projects, today)
		if err != nil {
			return nil, err
		}
		v.Files = filesOf[r.ID]
		if v.Files == nil {
			v.Files = []gen.AccountingAttachment{}
		}
		v.Payments = paymentsOf[r.ID]
		if v.Payments == nil {
			v.Payments = []gen.AccountingSupplierPayment{}
		}
		byID[r.ID] = v
	}
	s.purchaseMatches(ctx, ids, byID)
	for _, id := range ids {
		if v, ok := byID[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// purchaseMatches puts on each invoice the purchase orders Purchasing has
// matched it to (contracts.SupplierInvoiceMatches, purchasing design P6),
// read on the pool with no transaction open: a badge and a link, absent when
// Purchasing is off or the invoice is matched to none. A failed read is
// logged and the badge left out; the invoice is the books', not Purchasing's.
func (s *server) purchaseMatches(ctx context.Context, ids []int64, byID map[int64]gen.AccountingSupplierInvoice) {
	if s.deps.SupplierInvoiceMatches == nil || len(ids) == 0 {
		return
	}
	got, err := s.deps.SupplierInvoiceMatches.Summary(ctx, ids)
	if err != nil {
		s.logger().WarnContext(ctx, "accounting: the purchase matches could not be read", "error", err.Error())
		return
	}
	for _, m := range got {
		v, ok := byID[m.InvoiceID]
		if !ok || len(m.Orders) == 0 {
			continue
		}
		match := gen.AccountingSupplierInvoiceMatch{Differences: int32(m.Differences), Orders: make([]gen.AccountingMatchedOrder, 0, len(m.Orders))}
		for _, o := range m.Orders {
			match.Orders = append(match.Orders, gen.AccountingMatchedOrder{Id: o.ID, Number: o.Number})
		}
		v.PurchaseMatch = &match
		byID[m.InvoiceID] = v
	}
}

// dateText is a nullable date column as YYYY-MM-DD, "" for none.
func dateText(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.DateOnly)
}

// fileView is a draft's file as the contract answers it.
func fileView(f store.AccountingSupplierInvoiceFile) gen.AccountingAttachment {
	return gen.AccountingAttachment{Id: f.ID, FileName: f.FileName, ContentType: f.ContentType, SizeBytes: f.SizeBytes,
		Sha256: f.Sha256, UploadedAt: f.UploadedAt}
}

// supplierInvoiceView is one invoice's row and lines as the contract answers
// them, each line's net, VAT and gross computed at the invoice's date.
func (s *server) supplierInvoiceView(ctx context.Context, q *store.Queries, r store.GetSupplierInvoicesRow,
	lines []store.SupplierInvoiceLinesRow, projects map[int32]string, today time.Time) (gen.AccountingSupplierInvoice, error) {
	out := gen.AccountingSupplierInvoice{Id: r.ID, SupplierId: r.SupplierID, SupplierName: r.SupplierName,
		SupplierNumber: r.SupplierSeq, Kind: gen.AccountingSupplierInvoiceKind(r.Kind), InvoiceNumber: r.SupplierNumber,
		InvoiceDate: apiDate(r.InvoiceDate), DueDate: apiDatePtr(r.DueDate), Currency: r.Currency,
		ExchangeRate: rateColumn(optRat(r.ExchangeRate)), Total: money(mustRat(r.Total)), TotalNok: moneyColumn(optRat(r.TotalNok)),
		Kid: r.Kid, Reference: r.Reference, Description: r.Description, State: gen.AccountingSupplierInvoiceState(r.State),
		OpenItemId: r.OpenItemID, CreditsInvoiceId: r.CreditsInvoiceID, CopiedFromId: r.CopiedFromID,
		SourceExpenseEntryId: r.SourceExpenseEntryID, Revision: r.Revision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		PostedAt: r.PostedAt, Lines: []gen.AccountingSupplierInvoiceLine{}}
	if r.VoucherID != nil && r.VoucherYear != nil && r.VoucherNumber != nil {
		out.Voucher = &gen.AccountingVoucherRef{Id: *r.VoucherID, Display: display(*r.VoucherYear, *r.VoucherNumber)}
	}
	if r.ReversalVoucherID != nil && r.ReversalYear != nil && r.ReversalNumber != nil {
		out.ReversalVoucher = &gen.AccountingVoucherRef{Id: *r.ReversalVoucherID, Display: display(*r.ReversalYear, *r.ReversalNumber)}
	}
	if r.State != "draft" {
		balance := mustRat(r.Balance)
		out.Balance, out.BalanceCurrency = ptr(money(balance)), ptr(money(mustRat(r.BalanceCurrency)))
		if r.State == "posted" && balance.Sign() == 0 {
			out.State = "paid"
		}
		out.Overdue = r.State == "posted" && balance.Sign() != 0 && r.DueDate != nil && r.DueDate.Before(today)
	}
	var codeIDs []int32
	for _, l := range lines {
		if l.TaxCodeID != nil {
			codeIDs = append(codeIDs, *l.TaxCodeID)
		}
	}
	codes, err := taxCodesOn(ctx, q, codeIDs, r.InvoiceDate)
	if err != nil {
		return out, err
	}
	sum, vat := new(big.Rat), new(big.Rat)
	for _, l := range lines {
		in := purchaseLineIn{AccountID: l.AccountID, TaxCodeID: l.TaxCodeID, Amount: mustRat(l.Amount), Gross: l.Entry == "gross",
			ProjectID: l.ProjectID, Text: l.Text}
		var code *store.TaxCodesForPostingRow
		if l.TaxCodeID != nil {
			if c, ok := codes[*l.TaxCodeID]; ok {
				code = &c
			}
		}
		computed, _, _ := computePurchaseLine(in, code)
		sum.Add(sum, computed.gross)
		vat.Add(vat, computed.vat)
		v := gen.AccountingSupplierInvoiceLine{Id: l.ID, Position: l.Position, AccountId: l.AccountID,
			AccountNumber: l.AccountNumber, AccountName: l.AccountName, TaxCodeId: l.TaxCodeID, TaxCode: l.TaxCode,
			Amount: money(in.Amount), Entry: gen.AccountingEntryMode(l.Entry), ProjectId: l.ProjectID, Text: l.Text,
			Net: money(computed.net), Vat: money(computed.vat), Gross: money(computed.gross)}
		if l.ProjectID != nil {
			if name, ok := projects[*l.ProjectID]; ok {
				v.ProjectName = &name
			}
		}
		out.Lines = append(out.Lines, v)
	}
	out.LinesTotal, out.Vat = money(sum), money(vat)
	return out, nil
}

// supplierInvoiceOne is one invoice as the contract answers it; 404 when
// none.
func (s *server) supplierInvoiceOne(ctx context.Context, id int64) (gen.AccountingSupplierInvoice, error) {
	got, err := s.supplierInvoices(ctx, []int64{id})
	if err != nil {
		return gen.AccountingSupplierInvoice{}, err
	}
	if len(got) != 1 {
		return gen.AccountingSupplierInvoice{}, notFound()
	}
	return got[0], nil
}

// GetAccountingSupplierInvoice is one supplier invoice.
func (s *server) GetAccountingSupplierInvoice(ctx context.Context, req gen.GetAccountingSupplierInvoiceRequestObject) (gen.GetAccountingSupplierInvoiceResponseObject, error) {
	out, err := s.supplierInvoiceOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingSupplierInvoice200JSONResponse(out), nil
}

// CreateAccountingSupplierInvoice enters a supplier invoice as a draft.
func (s *server) CreateAccountingSupplierInvoice(ctx context.Context, req gen.CreateAccountingSupplierInvoiceRequestObject) (gen.CreateAccountingSupplierInvoiceResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The supplier invoice cannot be saved", "body", "", "A body is required.")
	}
	var id int64
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		d, err := checkDraft(ctx, q, *req.Body, true)
		if err != nil {
			return err
		}
		id, err = insertDraft(ctx, q, d, nil, nil, callerID(ctx), s.now())
		return err
	})
	if c := duplicateConflict(err); c != nil {
		return nil, c
	}
	if err != nil {
		return nil, err
	}
	out, err := s.supplierInvoiceOne(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.CreateAccountingSupplierInvoice201JSONResponse(out), nil
}

// lockDraft takes a supplier invoice FOR UPDATE and refuses one that is not
// a draft.
func lockDraft(ctx context.Context, q *store.Queries, id int64) (store.LockSupplierInvoiceRow, error) {
	inv, err := q.LockSupplierInvoice(ctx, id)
	if noRows(err) {
		return inv, notFound()
	}
	if err != nil {
		return inv, err
	}
	if inv.State != "draft" {
		return inv, conflict(codeSupplierInvNotDraft, "The supplier invoice is not a draft",
			"A posted invoice is corrected by Reverse and copy.")
	}
	return inv, nil
}

// UpdateAccountingSupplierInvoice changes a draft under its revision.
func (s *server) UpdateAccountingSupplierInvoice(ctx context.Context, req gen.UpdateAccountingSupplierInvoiceRequestObject) (gen.UpdateAccountingSupplierInvoiceResponseObject, error) {
	const title = "The supplier invoice cannot be saved"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	if req.Body.Revision == nil {
		return nil, invalidField(title, "revision", "", "The revision read is required.")
	}
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := lockDraft(ctx, q, req.Id); err != nil {
			return err
		}
		d, err := checkDraft(ctx, q, *req.Body, false)
		if err != nil {
			return err
		}
		_, err = q.UpdateSupplierInvoice(ctx, store.UpdateSupplierInvoiceParams{ID: req.Id, Revision: *req.Body.Revision,
			SupplierID: d.supplierID, Kind: d.kind, SupplierNumber: d.number, InvoiceDate: d.invoiceDate, DueDate: d.dueDate,
			Currency: d.currency, ExchangeRate: rateColumn(d.rate), Total: money(d.total), TotalNok: moneyColumn(d.totalNOK), Kid: d.kid,
			Reference: d.reference, Description: d.description, CreditsInvoiceID: d.creditsInvoiceID, Now: s.now()})
		if noRows(err) {
			return conflict(codeRevisionConflict, "The supplier invoice has changed", "Read it again and repeat the change.")
		}
		if err != nil {
			return err
		}
		return writeDraftLines(ctx, q, req.Id, d.lines)
	})
	if c := duplicateConflict(err); c != nil {
		return nil, c
	}
	if err != nil {
		return nil, err
	}
	out, err := s.supplierInvoiceOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.UpdateAccountingSupplierInvoice200JSONResponse(out), nil
}

// DeleteAccountingSupplierInvoice deletes a draft with its lines and files;
// a file's object is deleted from the store once nothing names it.
func (s *server) DeleteAccountingSupplierInvoice(ctx context.Context, req gen.DeleteAccountingSupplierInvoiceRequestObject) (gen.DeleteAccountingSupplierInvoiceResponseObject, error) {
	var keys []string
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		inv, err := lockDraft(ctx, q, req.Id)
		if err != nil {
			return err
		}
		got, err := q.DeleteSupplierInvoice(ctx, req.Id)
		if err != nil {
			return err
		}
		for _, k := range got {
			if k != "" {
				keys = append(keys, k)
			}
		}
		// A draft made from an expense — or a copy of one — gives the
		// expense back to Needs attention: its approval's row, matched by the
		// entry, whichever draft the row last named (M2).
		if inv.SourceExpenseEntryID != nil {
			if err := q.ReopenUnpostedOfEntry(ctx, *inv.SourceExpenseEntryID); err != nil {
				return err
			}
		}
		// The deletion's event, on its own transaction and under the draft's
		// row lock, so a module that matched the draft lets it go
		// (purchasing design A79, rule 23).
		return events.Publish(ctx, tx, moduleName, contracts.SupplierInvoiceDeleted{InvoiceID: inv.ID, SupplierID: inv.SupplierID,
			Kind: inv.Kind, Number: inv.SupplierNumber, InvoiceDate: inv.InvoiceDate.Format(time.DateOnly),
			DueDate: dateText(inv.DueDate), Currency: inv.Currency, Total: money(mustRat(inv.Total)),
			DeletedAt: s.now()}, s.now())
	})
	if err != nil {
		return nil, err
	}
	s.dropObjects(ctx, keys)
	return gen.DeleteAccountingSupplierInvoice204Response{}, nil
}

// dropObjects deletes from the store each object no file and no attachment
// names any more. A failure is logged: the object is an orphan, not a loss.
func (s *server) dropObjects(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	st, err := s.objectStore()
	if err != nil {
		return
	}
	q := store.New(s.deps.Pool)
	cleanup := context.WithoutCancel(ctx)
	for _, k := range keys {
		used, err := q.ObjectKeyInUse(cleanup, k)
		if err != nil || used {
			continue
		}
		if err := st.Delete(cleanup, k); err != nil {
			s.logger().WarnContext(ctx, "accounting: an unused file could not be removed from the store", "error", err.Error())
		}
	}
}

// PostAccountingSupplierInvoice posts a draft (A18): with a file, and lines
// that sum to the total, through the engine with the invoice's source key.
// The voucher is the person's: a date before the books' start or in a locked
// period is refused, never moved. The draft's files become the voucher's
// attachments.
func (s *server) PostAccountingSupplierInvoice(ctx context.Context, req gen.PostAccountingSupplierInvoiceRequestObject) (gen.PostAccountingSupplierInvoiceResponseObject, error) {
	const title = "The supplier invoice cannot be posted"
	by := callerID(ctx)
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		inv, err := lockDraft(ctx, q, req.Id)
		if err != nil {
			return err
		}
		files, err := q.SupplierInvoiceFiles(ctx, []int64{inv.ID})
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return conflict(codeSupplierInvFileRequired, "The supplier invoice has no file",
				"Attach the invoice as received before posting it.")
		}
		if inv.ExchangeRate == "" {
			return invalidField(title, "exchangeRate", codeExchangeRateRequired, "Enter the rate or the total in NOK.")
		}
		if inv.CreditsInvoiceID != nil {
			if err := checkCredit(ctx, q, inv); err != nil {
				return err
			}
		}
		doc, err := s.purchaseDocOf(ctx, q, inv)
		if err != nil {
			return err
		}
		sum := new(big.Rat)
		for _, l := range doc.Lines {
			sum.Add(sum, l.gross)
		}
		if len(doc.Lines) == 0 {
			return invalidField(title, "lines", codeVoucherEmpty, "A supplier invoice has lines.")
		}
		if sum.Cmp(doc.Total) != 0 {
			return invalidField(title, "lines", codeSupplierInvTotal,
				"The lines come to "+money(sum)+", the total is "+money(doc.Total)+".")
		}
		v := purchaseVoucher(doc, s.language(ctx, q))
		v.PostedBy, v.At = &by, s.now()
		posted, err := Post(ctx, tx, v)
		if err != nil {
			return err
		}
		if err := refuseMoved(ctx, q, posted, inv.InvoiceDate); err != nil {
			return err
		}
		for _, f := range files {
			if _, err := q.InsertAttachment(ctx, store.InsertAttachmentParams{VoucherID: posted.ID, ObjectKey: f.ObjectKey,
				FileName: f.FileName, ContentType: f.ContentType, SizeBytes: f.SizeBytes, Sha256: f.Sha256, Now: f.UploadedAt,
				UserID: f.UploadedByUserID}); err != nil {
				return err
			}
		}
		item, err := q.VoucherOpenItem(ctx, posted.ID)
		if err != nil {
			return err
		}
		if err := q.MarkSupplierInvoicePosted(ctx, store.MarkSupplierInvoicePostedParams{ID: inv.ID, VoucherID: &posted.ID,
			OpenItemID: &item, Now: ptr(s.now()), UserID: &by}); err != nil {
			return err
		}
		// The posting's event, on its own transaction and under the
		// invoice's row lock (purchasing design P5, rule 23).
		return events.Publish(ctx, tx, moduleName, contracts.SupplierInvoicePosted{InvoiceID: inv.ID, SupplierID: inv.SupplierID,
			Kind: inv.Kind, Number: inv.SupplierNumber, InvoiceDate: inv.InvoiceDate.Format(time.DateOnly),
			DueDate: dateText(inv.DueDate), Currency: inv.Currency, Total: money(mustRat(inv.Total)),
			PostedAt: s.now()}, s.now())
	})
	if err != nil {
		return nil, err
	}
	out, err := s.supplierInvoiceOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PostAccountingSupplierInvoice200JSONResponse(out), nil
}

// checkCredit holds a credit note to the invoice it credits, as it stands
// when it is posted: the invoice and its open item are locked — in the order
// a payment takes them — so a payment or another credit note on the same
// item waits, and then the invoice must still be posted, be in the credit
// note's currency, and have at least the credit note's total left on it, in
// NOK and in its currency.
func checkCredit(ctx context.Context, q *store.Queries, inv store.LockSupplierInvoiceRow) error {
	credited, err := q.LockSupplierInvoice(ctx, *inv.CreditsInvoiceID)
	if err != nil {
		return err
	}
	if credited.State != "posted" || credited.OpenItemID == nil {
		return conflict(codeCreditNoteNotPosted, "The credited invoice is not posted",
			"The invoice this credit note credits is "+credited.State+". Credit a posted invoice, or post the credit note on its own.")
	}
	if credited.Currency != inv.Currency {
		return conflict(codeCreditNoteCurrency, "The credit note is in another currency",
			"The credited invoice is in "+credited.Currency+", the credit note in "+inv.Currency+". A credit note is in its invoice's currency.")
	}
	if err := q.LockOpenItem(ctx, *credited.OpenItemID); err != nil {
		return err
	}
	bal, err := q.OpenItemCurrencyBalance(ctx, *credited.OpenItemID)
	if err != nil {
		return err
	}
	left := new(big.Rat).Abs(mustRat(bal.Balance))
	if mustRat(inv.TotalNok).Cmp(left) > 0 {
		return conflict(codeCreditNoteExceeds, "The credit note is more than is left on the invoice",
			"NOK "+money(left)+" is left on the invoice; the credit note is NOK "+money(mustRat(inv.TotalNok))+".")
	}
	if inv.Currency != currency {
		leftCur := new(big.Rat).Abs(mustRat(bal.BalanceCurrency))
		if mustRat(inv.Total).Cmp(leftCur) > 0 {
			return conflict(codeCreditNoteExceeds, "The credit note is more than is left on the invoice",
				inv.Currency+" "+money(leftCur)+" is left on the invoice; the credit note is "+inv.Currency+" "+money(mustRat(inv.Total))+".")
		}
	}
	return nil
}

// language is the chart's language, en when the settings cannot be read.
func (s *server) language(ctx context.Context, q *store.Queries) string {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return "en"
	}
	return settings.ChartLanguage
}

// refuseMoved refuses, as a person's voucher would be, a voucher with a
// source the engine moved: asked for a day before the books' start, or in a
// locked period. A supplier invoice, its reversal and a payment are a
// person's acts, dated as the person says.
func refuseMoved(ctx context.Context, q *store.Queries, posted Posted, asked time.Time) error {
	if !posted.Moved {
		return nil
	}
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return err
	}
	if asked.Before(settings.BooksStart) {
		return invalidField("The voucher cannot be posted", "date", codeDateBeforeBooks,
			"The books start on "+settings.BooksStart.Format(time.DateOnly)+"; nothing is posted before it.")
	}
	return conflict(codePeriodLocked, "The period is locked",
		"The books are locked through "+deref(settings.LockedThrough).Format(time.DateOnly)+
			". Date it after the lock, or move the lock back.")
}

// purchaseDocOf reads what a supplier invoice's voucher is built from: its
// lines with their codes at the invoice's date, and for a credit note the
// open item of the invoice it credits.
func (s *server) purchaseDocOf(ctx context.Context, q *store.Queries, inv store.LockSupplierInvoiceRow) (purchaseDoc, error) {
	rows, err := q.SupplierInvoiceLines(ctx, []int64{inv.ID})
	if err != nil {
		return purchaseDoc{}, err
	}
	var codeIDs []int32
	for _, l := range rows {
		if l.TaxCodeID != nil {
			codeIDs = append(codeIDs, *l.TaxCodeID)
		}
	}
	codes, err := taxCodesOn(ctx, q, codeIDs, inv.InvoiceDate)
	if err != nil {
		return purchaseDoc{}, err
	}
	supplier, err := q.GetSupplier(ctx, inv.SupplierID)
	if err != nil {
		return purchaseDoc{}, err
	}
	doc := purchaseDoc{ID: inv.ID, CreditNote: inv.Kind == "credit_note", Number: inv.SupplierNumber, Supplier: inv.SupplierID,
		Currency: inv.Currency, Rate: mustRat(inv.ExchangeRate), Total: mustRat(inv.Total), TotalNOK: mustRat(inv.TotalNok),
		DocumentDate: inv.InvoiceDate, DueDate: inv.DueDate,
		Description: truncate(inv.SupplierNumber+" · "+supplier.Name, 500)}
	var f fieldErrors
	for i, l := range rows {
		in := purchaseLineIn{AccountID: l.AccountID, TaxCodeID: l.TaxCodeID, Amount: mustRat(l.Amount), Gross: l.Entry == "gross",
			ProjectID: l.ProjectID, Text: l.Text}
		var code *store.TaxCodesForPostingRow
		if l.TaxCodeID != nil {
			c, ok := codes[*l.TaxCodeID]
			if !ok {
				f.addCoded(fmt.Sprintf("lines[%d].taxCodeId", i), codeTaxCodeUnknown, "No such active tax code.")
				continue
			}
			code = &c
		}
		computed, rule, msg := computePurchaseLine(in, code)
		if rule != "" {
			f.addCoded(fmt.Sprintf("lines[%d].taxCodeId", i), rule, msg)
			continue
		}
		doc.Lines = append(doc.Lines, computed)
	}
	if err := f.refusal("The supplier invoice cannot be posted"); err != nil {
		return doc, err
	}
	if inv.CreditsInvoiceID != nil {
		credited, err := q.GetSupplierInvoices(ctx, []int64{*inv.CreditsInvoiceID})
		if err != nil {
			return doc, err
		}
		if len(credited) == 1 && credited[0].OpenItemID != nil {
			doc.CreditsOpenItemID = credited[0].OpenItemID
		}
	}
	return doc, nil
}

// purchaseVoucher is a supplier invoice's voucher: its lines (purchases.go),
// its kind and rule, and its source key.
func purchaseVoucher(d purchaseDoc, language string) Voucher {
	b := salesBooks{language: language}
	kind, rule := "supplier_invoice", purchaseRuleInvoice
	description := b.text("Supplier invoice %s", "Leverandørfaktura %s", d.Description)
	if d.CreditNote {
		kind, rule = "supplier_credit_note", purchaseRuleCreditNote
		description = b.text("Supplier credit note %s", "Leverandørkreditnota %s", d.Description)
	}
	return Voucher{Kind: kind, Date: d.DocumentDate, DocumentDate: d.DocumentDate, Description: truncate(description, 500),
		Source: &Source{Module: sourceAccounting, Kind: "supplier_invoice", ID: d.ID}, Rule: rule,
		RuleVersion: purchaseRuleVersion, Lines: purchaseVoucherLines(d)}
}

// ReverseAndCopyAccountingSupplierInvoice reverses a posted invoice's voucher,
// line for line, marks it reversed, and copies it to a new draft with the
// same lines and files (A18), which it answers. An invoice with a payment
// standing is not reversed: the payment is removed first.
func (s *server) ReverseAndCopyAccountingSupplierInvoice(ctx context.Context, req gen.ReverseAndCopyAccountingSupplierInvoiceRequestObject) (gen.ReverseAndCopyAccountingSupplierInvoiceResponseObject, error) {
	const title = "The supplier invoice cannot be reversed"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	reason := strings.TrimSpace(req.Body.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return nil, invalidField(title, "reason", "", "A reason of 1 to 500 characters.")
	}
	by := callerID(ctx)
	var copyID int64
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		inv, err := q.LockSupplierInvoice(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if inv.State != "posted" || inv.VoucherID == nil {
			return conflict(codeSupplierInvNotPosted, "The supplier invoice is not posted", "")
		}
		payments, err := q.SupplierPayments(ctx, []int64{inv.ID})
		if err != nil {
			return err
		}
		for _, p := range payments {
			if p.RemovedAt == nil {
				return conflict(codeSupplierInvHasPayments, "The supplier invoice has payments",
					"Remove its payments first; then reverse it.")
			}
		}
		// Nothing else may stand on the invoice's open item: a credit note,
		// a payment made through a credit note's row, a settlement. Turning
		// the invoice back under them would leave them on an item with
		// nothing to settle. A credit note's item is its invoice's, whose
		// own voucher is not in the way.
		if inv.OpenItemID != nil {
			if err := q.LockOpenItem(ctx, *inv.OpenItemID); err != nil {
				return err
			}
			own := []int64{*inv.VoucherID}
			if inv.CreditsInvoiceID != nil {
				credited, err := q.SupplierInvoiceState(ctx, *inv.CreditsInvoiceID)
				if err != nil {
					return err
				}
				if credited.VoucherID != nil {
					own = append(own, *credited.VoucherID)
				}
			}
			used, err := q.ItemHasOtherVouchers(ctx, store.ItemHasOtherVouchersParams{ItemID: *inv.OpenItemID, Own: own})
			if err != nil {
				return err
			}
			if used {
				return conflict(codeSupplierInvItemInUse, "Other vouchers stand on the supplier invoice",
					"A credit note, a payment or a settlement posts on this invoice's open item. Reverse or remove it first; then reverse the invoice.")
			}
		}
		stored, err := storedVoucherOf(ctx, q, *inv.VoucherID)
		if err != nil {
			return err
		}
		language := s.language(ctx, q)
		b := salesBooks{language: language}
		v := Voucher{Kind: "supplier_invoice", Date: day(req.Body.Date), DocumentDate: day(req.Body.Date),
			Description: truncate(b.text("Reversal of supplier invoice %s", "Tilbakeføring av leverandørfaktura %s",
				inv.SupplierNumber), 500),
			Source: &Source{Module: sourceAccounting, Kind: "supplier_invoice", ID: inv.ID, Part: "reversal"},
			Rule:   purchaseRuleReversal, RuleVersion: purchaseRuleVersion, PostedBy: &by, At: s.now()}
		if inv.Kind == "credit_note" {
			v.Kind = "supplier_credit_note"
		}
		turnBack(&v, stored, reason)
		posted, err := Post(ctx, tx, v)
		if err != nil {
			return err
		}
		if err := refuseMoved(ctx, q, posted, day(req.Body.Date)); err != nil {
			return err
		}
		if err := q.MarkSupplierInvoiceReversed(ctx, store.MarkSupplierInvoiceReversedParams{ID: inv.ID, VoucherID: &posted.ID,
			Now: s.now()}); err != nil {
			return err
		}
		rows, err := q.SupplierInvoiceLines(ctx, []int64{inv.ID})
		if err != nil {
			return err
		}
		d := draftInput{supplierID: inv.SupplierID, kind: inv.Kind, number: inv.SupplierNumber, invoiceDate: inv.InvoiceDate,
			dueDate: inv.DueDate, currency: inv.Currency, rate: optRat(inv.ExchangeRate), total: mustRat(inv.Total),
			totalNOK: optRat(inv.TotalNok), kid: inv.Kid, reference: inv.Reference, description: inv.Description,
			creditsInvoiceID: inv.CreditsInvoiceID}
		for _, l := range rows {
			d.lines = append(d.lines, purchaseLineIn{AccountID: l.AccountID, TaxCodeID: l.TaxCodeID, Amount: mustRat(l.Amount),
				Gross: l.Entry == "gross", ProjectID: l.ProjectID, Text: l.Text})
		}
		if copyID, err = insertDraft(ctx, q, d, &inv.ID, inv.SourceExpenseEntryID, by, s.now()); err != nil {
			return err
		}
		// A row under Needs attention that named the invoice names the copy:
		// deleting the copy then gives the expense back (M2).
		if err := q.RepointUnpostedDraft(ctx, store.RepointUnpostedDraftParams{CopyID: copyID, OriginalID: inv.ID}); err != nil {
			return err
		}
		files, err := q.SupplierInvoiceFiles(ctx, []int64{inv.ID})
		if err != nil {
			return err
		}
		for _, f := range files {
			if _, err := q.InsertSupplierInvoiceFile(ctx, store.InsertSupplierInvoiceFileParams{SupplierInvoiceID: copyID,
				ObjectKey: f.ObjectKey, FileName: f.FileName, ContentType: f.ContentType, SizeBytes: f.SizeBytes,
				Sha256: f.Sha256, Now: f.UploadedAt, UserID: f.UploadedByUserID}); err != nil {
				return err
			}
		}
		// The reversal's event, on its own transaction and under the
		// invoice's row lock, naming the copy made of it (P5, A15).
		return events.Publish(ctx, tx, moduleName, contracts.SupplierInvoiceReversed{InvoiceID: inv.ID, SupplierID: inv.SupplierID,
			Kind: inv.Kind, Number: inv.SupplierNumber, InvoiceDate: inv.InvoiceDate.Format(time.DateOnly),
			DueDate: dateText(inv.DueDate), Currency: inv.Currency, Total: money(mustRat(inv.Total)),
			CopyID: copyID, ReversedAt: s.now()}, s.now())
	})
	if c := duplicateConflict(err); c != nil {
		return nil, c
	}
	if err != nil {
		return nil, err
	}
	out, err := s.supplierInvoiceOne(ctx, copyID)
	if err != nil {
		return nil, err
	}
	return gen.ReverseAndCopyAccountingSupplierInvoice201JSONResponse(out), nil
}

// storedVoucherOf is a voucher's postings as lines to turn back, currencies
// included.
func storedVoucherOf(ctx context.Context, q *store.Queries, id int64) (*storedVoucher, error) {
	rows, err := q.VoucherPostingsForTurn(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &storedVoucher{id: id}
	for _, p := range rows {
		out.lines = append(out.lines, postingLine(p.AccountID, p.Amount, p.TaxCodeID, p.TaxRole, p.TaxBase, p.TaxSpecification,
			p.CustomerID, p.SupplierID, p.OpenItemID, p.ProjectID, p.Description, p.PostingRuleID, p.PostingRuleVersion,
			p.Currency, p.CurrencyAmount, p.ExchangeRate))
	}
	return out, nil
}

// postingLine is a stored posting as a Line.
func postingLine(account int32, amount string, taxCode *int32, taxRole *string, taxBase string, spec *string,
	customer *int32, supplier, item *int64, project *int32, text string, rule, version *int32,
	cur *string, curAmount, rate string) Line {
	l := Line{AccountID: account, Amount: mustRat(amount), TaxCodeID: taxCode, CustomerID: customer, SupplierID: supplier,
		OpenItemID: item, ProjectID: project, Text: text, TaxSpecification: deref(spec), PostingRuleID: rule,
		PostingRuleVersion: version}
	if taxRole != nil {
		l.TaxRole = *taxRole
	}
	if taxBase != "" {
		l.TaxBase = mustRat(taxBase)
	}
	if cur != nil && curAmount != "" && rate != "" {
		l.Currency, l.CurrencyAmount, l.ExchangeRate = *cur, mustRat(curAmount), mustRat(rate)
	}
	return l
}

// UploadAccountingSupplierInvoiceFile keeps a file on a draft: the object
// first, the row after it; the object is removed again if the row is
// refused.
func (s *server) UploadAccountingSupplierInvoiceFile(ctx context.Context, req gen.UploadAccountingSupplierInvoiceFileRequestObject) (gen.UploadAccountingSupplierInvoiceFileResponseObject, error) {
	file, err := readUpload(req.Body)
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	got, err := q.GetSupplierInvoices(ctx, []int64{req.Id})
	if err != nil {
		return nil, err
	}
	if len(got) != 1 {
		return nil, notFound()
	}
	if got[0].State != "draft" {
		return nil, conflict(codeSupplierInvNotDraft, "The supplier invoice is not a draft",
			"A posted invoice's files are its voucher's: attach more on the voucher.")
	}
	row, err := s.storeSupplierFile(ctx, req.Id, file.name, file.contentType, file.data, callerID(ctx))
	if err != nil {
		return nil, err
	}
	return gen.UploadAccountingSupplierInvoiceFile201JSONResponse(fileView(row)), nil
}

// storeSupplierFile writes a draft's file: the object, then its row under
// the draft's lock, refused when the draft is no longer one; the object is
// removed again when the row is.
func (s *server) storeSupplierFile(ctx context.Context, id int64, name, contentType string, data []byte, by uuid.UUID) (store.AccountingSupplierInvoiceFile, error) {
	if _, _, err := mime.ParseMediaType(contentType); err != nil || contentType == "" || contentType == "application/octet-stream" {
		contentType, _, _ = strings.Cut(http.DetectContentType(data), ";")
	}
	if len(contentType) > 100 {
		contentType = "application/octet-stream"
	}
	sum := sha256.Sum256(data)
	st, err := s.objectStore()
	if err != nil {
		return store.AccountingSupplierInvoiceFile{}, storageRefusal(err)
	}
	key := fmt.Sprintf("supplier-invoices/%d/%s", id, uuid.New().String())
	if err := st.Put(ctx, key, bytes.NewReader(data), contentType); err != nil {
		s.logger().ErrorContext(ctx, "accounting: a supplier invoice's file could not be written", "supplier_invoice_id", id, "error", err.Error())
		return store.AccountingSupplierInvoiceFile{}, storageRefusal(err)
	}
	var row store.AccountingSupplierInvoiceFile
	err = s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := lockDraft(ctx, q, id); err != nil {
			return err
		}
		var err error
		row, err = q.InsertSupplierInvoiceFile(ctx, store.InsertSupplierInvoiceFileParams{SupplierInvoiceID: id, ObjectKey: key,
			FileName: cleanFileName(name), ContentType: contentType, SizeBytes: int64(len(data)),
			Sha256: hex.EncodeToString(sum[:]), Now: s.now(), UserID: by})
		return err
	})
	if err != nil {
		if derr := st.Delete(context.WithoutCancel(ctx), key); derr != nil {
			s.logger().ErrorContext(ctx, "accounting: an orphaned file could not be removed", "error", derr.Error())
		}
		return row, err
	}
	return row, nil
}

// supplierFileDownload sets the headers a file is served with.
type supplierFileDownload struct {
	name string
	body gen.GetAccountingSupplierInvoiceFileResponseObject
}

func (r supplierFileDownload) VisitGetAccountingSupplierInvoiceFileResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": r.name}))
	return r.body.VisitGetAccountingSupplierInvoiceFileResponse(w)
}

// GetAccountingSupplierInvoiceFile streams one of a supplier invoice's files.
func (s *server) GetAccountingSupplierInvoiceFile(ctx context.Context, req gen.GetAccountingSupplierInvoiceFileRequestObject) (gen.GetAccountingSupplierInvoiceFileResponseObject, error) {
	f, err := store.New(s.deps.Pool).GetSupplierInvoiceFile(ctx, store.GetSupplierInvoiceFileParams{ID: req.FileId,
		SupplierInvoiceID: req.Id})
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	st, err := s.objectStore()
	if err != nil {
		return nil, storageRefusal(err)
	}
	body, err := st.Get(ctx, f.ObjectKey)
	if err != nil {
		s.logger().ErrorContext(ctx, "accounting: a supplier invoice's file could not be read", "file_id", f.ID, "error", err.Error())
		return nil, storageRefusal(err)
	}
	return supplierFileDownload{name: f.FileName, body: gen.GetAccountingSupplierInvoiceFile200AsteriskResponse{Body: body,
		ContentType: f.ContentType, ContentLength: f.SizeBytes}}, nil
}

// DeleteAccountingSupplierInvoiceFile removes a file from a draft; its
// object goes once nothing names it.
func (s *server) DeleteAccountingSupplierInvoiceFile(ctx context.Context, req gen.DeleteAccountingSupplierInvoiceFileRequestObject) (gen.DeleteAccountingSupplierInvoiceFileResponseObject, error) {
	var key string
	err := s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := lockDraft(ctx, q, req.Id); err != nil {
			return err
		}
		var err error
		key, err = q.DeleteSupplierInvoiceFile(ctx, store.DeleteSupplierInvoiceFileParams{ID: req.FileId, SupplierInvoiceID: req.Id})
		if noRows(err) {
			return notFound()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.dropObjects(ctx, []string{key})
	return gen.DeleteAccountingSupplierInvoiceFile204Response{}, nil
}
