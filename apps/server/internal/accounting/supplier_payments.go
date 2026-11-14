package accounting

// Paying suppliers (A19 as amended by R5, R8): a payment registered on a
// posted supplier invoice moves its open item toward zero against a bank
// ledger account, in part or in whole. For an invoice in another currency
// the person gives the share settled in the currency (default what is
// left), and the difference between the NOK the bank took and that share at
// the invoice's rate goes to currency_loss or currency_gain. A payment is
// removed, while its period is open, by its voucher reversed. paid is the
// open item's balance at zero, never a stored state.

import (
	"context"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
)

// The roles a payment's currency difference posts to (R8).
const (
	roleCurrencyGain = "currency_gain"
	roleCurrencyLoss = "currency_loss"
)

// bankLine is the bank side of a payment or a settlement: the given account,
// held to an active asset account, or the bank role's.
func bankLine(ctx context.Context, q *store.Queries, title string, id *int32) (Line, error) {
	if id == nil {
		return Line{Role: roleBank}, nil
	}
	accounts, err := q.AccountsForPosting(ctx, []int32{*id})
	if err != nil {
		return Line{}, err
	}
	if len(accounts) != 1 || accounts[0].Kind != "asset" || !accounts[0].Active {
		return Line{}, invalidField(title, "bankAccountId", codeBankAccountInvalid, "An active asset account.")
	}
	return Line{AccountID: *id}, nil
}

// paymentLines is a payment's postings (A19, R8): the item moved toward zero
// by the share settled in NOK — on an invoice owed (balance below zero) a
// debit of payables, on a credit (above zero) a credit — the bank the other
// way by what it took, and the difference, if any, on currency_loss (more
// NOK went than the share was worth) or currency_gain.
func paymentLines(owed bool, settledNOK, paidNOK, settledCurrency *big.Rat, foreign bool, cur string, rate *big.Rat,
	supplier, item int64, bank Line, text string) []Line {
	toItem := new(big.Rat).Set(settledNOK)
	if !owed {
		toItem = neg(settledNOK)
	}
	payable := Line{Role: rolePayables, Amount: toItem, SupplierID: &supplier, OpenItemID: &item, Text: text}
	if foreign {
		curAmount := new(big.Rat).Set(settledCurrency)
		if !owed {
			curAmount = neg(curAmount)
		}
		payable.Currency, payable.CurrencyAmount, payable.ExchangeRate = cur, curAmount, new(big.Rat).Set(rate)
	}
	bank.Text = text
	if !owed {
		bank.Amount = new(big.Rat).Set(paidNOK)
	} else {
		bank.Amount = neg(paidNOK)
	}
	lines := []Line{payable, bank}
	// The lines so far sum to toItem + bank; the difference balances them.
	diff := neg(add(toItem, bank.Amount))
	switch diff.Sign() {
	case 1:
		lines = append(lines, Line{Role: roleCurrencyLoss, Amount: diff, Text: text})
	case -1:
		lines = append(lines, Line{Role: roleCurrencyGain, Amount: diff, Text: text})
	}
	return lines
}

// supplierPayment is a payment to register on a supplier invoice: the day,
// the NOK the bank took, the share settled in the invoice's currency ("" is
// what is left) and the bank ledger account (nil is the bank role's).
type supplierPayment struct {
	date           time.Time
	paidNOK        *big.Rat
	amountCurrency string
	bankAccountID  *int32
	by             uuid.UUID
	at             time.Time
}

// paySupplierInvoice registers a payment of a posted supplier invoice on tx
// (A19): the invoice and then its item taken FOR UPDATE, the amount held to
// what is left, and the voucher posted with its own source key, refused when
// the engine would move it. It is the one path a payment takes, from the
// invoice's page and from a bank line's match (A28). It answers the voucher
// and the payment's id.
func (s *server) paySupplierInvoice(ctx context.Context, tx pgx.Tx, q *store.Queries, invoiceID int64, in supplierPayment) (Posted, int64, error) {
	const title = "The payment cannot be registered"
	paid := in.paidNOK
	inv, err := q.LockSupplierInvoice(ctx, invoiceID)
	if noRows(err) {
		return Posted{}, 0, notFound()
	}
	if err != nil {
		return Posted{}, 0, err
	}
	if inv.State != "posted" || inv.OpenItemID == nil {
		return Posted{}, 0, conflict(codeSupplierInvNotPosted, "The supplier invoice is not posted", "Post it before paying it.")
	}
	if err := q.LockOpenItem(ctx, *inv.OpenItemID); err != nil {
		return Posted{}, 0, err
	}
	bal, err := q.OpenItemCurrencyBalance(ctx, *inv.OpenItemID)
	if err != nil {
		return Posted{}, 0, err
	}
	balNOK, balCur := mustRat(bal.Balance), mustRat(bal.BalanceCurrency)
	if balNOK.Sign() == 0 {
		return Posted{}, 0, conflict(codeSupplierInvSettled, "The supplier invoice is paid", "Nothing is left on it.")
	}
	foreign := inv.Currency != currency
	rate := mustRat(inv.ExchangeRate)
	settledNOK, settledCur := paid, paid
	if foreign {
		settledCur = abs(balCur)
		if strings.TrimSpace(in.amountCurrency) != "" {
			c, ok := parseAmount(in.amountCurrency)
			if !ok || c.Sign() <= 0 {
				return Posted{}, 0, invalidField(title, "amountCurrency", codeAmountInvalid, "A positive amount in "+inv.Currency+".")
			}
			settledCur = c
		}
		if settledCur.Cmp(abs(balCur)) > 0 {
			return Posted{}, 0, conflict(codePaymentExceedsBalance, "More than is left on the invoice",
				money(abs(balCur))+" "+inv.Currency+" is left.")
		}
		settledNOK = docmath.Round2(new(big.Rat).Mul(settledCur, rate))
		if settledCur.Cmp(abs(balCur)) == 0 {
			// The last share settles what is left in NOK, so the item
			// ends at zero in both.
			settledNOK = abs(balNOK)
		}
	} else if paid.Cmp(abs(balNOK)) > 0 {
		return Posted{}, 0, conflict(codePaymentExceedsBalance, "More than is left on the invoice", money(abs(balNOK))+" is left.")
	}
	bank, err := bankLine(ctx, q, title, in.bankAccountID)
	if err != nil {
		return Posted{}, 0, err
	}
	language := s.language(ctx, q)
	books := salesBooks{language: language}
	text := books.text("Payment of supplier invoice %s", "Betaling av leverandørfaktura %s", inv.SupplierNumber)
	v := Voucher{Kind: "supplier_payment", Date: in.date, DocumentDate: in.date, Description: truncate(text, 500),
		Source: &Source{Module: sourceAccounting, Kind: "supplier_payment", ID: inv.ID, Part: uuid.NewString()},
		Rule:   purchaseRulePayment, RuleVersion: purchaseRuleVersion, PostedBy: &in.by, At: in.at,
		Lines: paymentLines(balNOK.Sign() < 0, settledNOK, paid, settledCur, foreign, inv.Currency, rate, inv.SupplierID,
			*inv.OpenItemID, bank, text)}
	posted, err := Post(ctx, tx, v)
	if err != nil {
		return Posted{}, 0, err
	}
	if err := refuseMoved(ctx, q, posted, in.date); err != nil {
		return Posted{}, 0, err
	}
	bankID := bank.AccountID
	if bankID == 0 {
		roles, err := q.RoleAccounts(ctx)
		if err != nil {
			return Posted{}, 0, err
		}
		for _, r := range roles {
			if r.Role == roleBank {
				bankID = r.ID
			}
		}
	}
	id, err := q.InsertSupplierPayment(ctx, store.InsertSupplierPaymentParams{SupplierInvoiceID: inv.ID, PaidOn: in.date,
		AmountNok: money(paid), AmountCurrency: money(settledCur), BankAccountID: bankID, VoucherID: posted.ID, Now: in.at,
		UserID: in.by})
	return posted, id, err
}

// PayAccountingSupplierInvoice registers a payment of a posted supplier
// invoice (A19) through paySupplierInvoice. A date before the books' start or
// in a locked period is refused.
func (s *server) PayAccountingSupplierInvoice(ctx context.Context, req gen.PayAccountingSupplierInvoiceRequestObject) (gen.PayAccountingSupplierInvoiceResponseObject, error) {
	const title = "The payment cannot be registered"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	b := *req.Body
	paid, ok := parseAmount(b.AmountNok)
	if !ok || paid.Sign() <= 0 {
		return nil, invalidField(title, "amountNok", codeAmountInvalid, "A positive amount in NOK, with at most two decimals.")
	}
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		_, _, err := s.paySupplierInvoice(ctx, tx, q, req.Id, supplierPayment{date: day(b.Date), paidNOK: paid,
			amountCurrency: deref(b.AmountCurrency), bankAccountID: b.BankAccountId, by: callerID(ctx), at: s.now()})
		return err
	})
	if err != nil {
		return nil, err
	}
	out, err := s.supplierInvoiceOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PayAccountingSupplierInvoice201JSONResponse(out), nil
}

// removeSupplierPayment removes a payment on tx: its voucher reversed, line
// for line, dated the payment's day, while that day's period is open. It is
// the one path, from the invoice's page and from undoing a bank line's match.
func (s *server) removeSupplierPayment(ctx context.Context, tx pgx.Tx, q *store.Queries, invoiceID, paymentID int64, by uuid.UUID) (Posted, error) {
	inv, err := q.LockSupplierInvoice(ctx, invoiceID)
	if noRows(err) {
		return Posted{}, notFound()
	}
	if err != nil {
		return Posted{}, err
	}
	p, err := q.LockSupplierPayment(ctx, store.LockSupplierPaymentParams{ID: paymentID, SupplierInvoiceID: invoiceID})
	if noRows(err) {
		return Posted{}, notFound()
	}
	if err != nil {
		return Posted{}, err
	}
	if p.RemovedAt != nil {
		return Posted{}, conflict(codePaymentRemoved, "The payment is removed already", "")
	}
	stored, err := storedVoucherOf(ctx, q, p.VoucherID)
	if err != nil {
		return Posted{}, err
	}
	books := salesBooks{language: s.language(ctx, q)}
	v := Voucher{Kind: "supplier_payment", Date: p.PaidOn, DocumentDate: p.PaidOn,
		Description: truncate(books.text("Payment of supplier invoice %s removed", "Betaling av leverandørfaktura %s fjernet",
			inv.SupplierNumber), 500),
		Source: &Source{Module: sourceAccounting, Kind: "supplier_payment_removed", ID: p.ID},
		Rule:   purchaseRulePaymentRemoved, RuleVersion: purchaseRuleVersion, PostedBy: &by, At: s.now()}
	turnBack(&v, stored, books.text("Payment removed", "Betaling fjernet"))
	posted, err := Post(ctx, tx, v)
	if err != nil {
		return Posted{}, err
	}
	if err := refuseMoved(ctx, q, posted, p.PaidOn); err != nil {
		return Posted{}, err
	}
	return posted, q.MarkSupplierPaymentRemoved(ctx, store.MarkSupplierPaymentRemovedParams{ID: p.ID, Now: ptr(s.now()),
		UserID: &by, VoucherID: &posted.ID})
}

// RemoveAccountingSupplierPayment removes a payment through
// removeSupplierPayment.
func (s *server) RemoveAccountingSupplierPayment(ctx context.Context, req gen.RemoveAccountingSupplierPaymentRequestObject) (gen.RemoveAccountingSupplierPaymentResponseObject, error) {
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		_, err := s.removeSupplierPayment(ctx, tx, q, req.Id, req.PaymentId, callerID(ctx))
		return err
	})
	if err != nil {
		return nil, err
	}
	out, err := s.supplierInvoiceOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.RemoveAccountingSupplierPayment200JSONResponse(out), nil
}
