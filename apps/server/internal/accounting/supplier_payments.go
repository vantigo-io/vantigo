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

// PayAccountingSupplierInvoice registers a payment of a posted supplier
// invoice (A19): the item and the invoice are taken FOR UPDATE, the amount
// held to what is left, and the voucher posted with its own source key. A
// date before the books' start or in a locked period is refused.
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
	by := callerID(ctx)
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		inv, err := q.LockSupplierInvoice(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if inv.State != "posted" || inv.OpenItemID == nil {
			return conflict(codeSupplierInvNotPosted, "The supplier invoice is not posted", "Post it before paying it.")
		}
		if err := q.LockOpenItem(ctx, *inv.OpenItemID); err != nil {
			return err
		}
		bal, err := q.OpenItemCurrencyBalance(ctx, *inv.OpenItemID)
		if err != nil {
			return err
		}
		balNOK, balCur := mustRat(bal.Balance), mustRat(bal.BalanceCurrency)
		if balNOK.Sign() == 0 {
			return conflict(codeSupplierInvSettled, "The supplier invoice is paid", "Nothing is left on it.")
		}
		foreign := inv.Currency != currency
		rate := mustRat(inv.ExchangeRate)
		settledNOK, settledCur := paid, paid
		if foreign {
			settledCur = abs(balCur)
			if b.AmountCurrency != nil && strings.TrimSpace(*b.AmountCurrency) != "" {
				c, ok := parseAmount(*b.AmountCurrency)
				if !ok || c.Sign() <= 0 {
					return invalidField(title, "amountCurrency", codeAmountInvalid, "A positive amount in "+inv.Currency+".")
				}
				settledCur = c
			}
			if settledCur.Cmp(abs(balCur)) > 0 {
				return conflict(codePaymentExceedsBalance, "More than is left on the invoice",
					money(abs(balCur))+" "+inv.Currency+" is left.")
			}
			settledNOK = docmath.Round2(new(big.Rat).Mul(settledCur, rate))
			if settledCur.Cmp(abs(balCur)) == 0 {
				// The last share settles what is left in NOK, so the item
				// ends at zero in both.
				settledNOK = abs(balNOK)
			}
		} else if paid.Cmp(abs(balNOK)) > 0 {
			return conflict(codePaymentExceedsBalance, "More than is left on the invoice", money(abs(balNOK))+" is left.")
		}
		bank, err := bankLine(ctx, q, title, b.BankAccountId)
		if err != nil {
			return err
		}
		language := s.language(ctx, q)
		books := salesBooks{language: language}
		text := books.text("Payment of supplier invoice %s", "Betaling av leverandørfaktura %s", inv.SupplierNumber)
		date := day(b.Date)
		v := Voucher{Kind: "supplier_payment", Date: date, DocumentDate: date, Description: truncate(text, 500),
			Source: &Source{Module: sourceAccounting, Kind: "supplier_payment", ID: inv.ID, Part: uuid.NewString()},
			Rule:   purchaseRulePayment, RuleVersion: purchaseRuleVersion, PostedBy: &by, At: s.now(),
			Lines: paymentLines(balNOK.Sign() < 0, settledNOK, paid, settledCur, foreign, inv.Currency, rate, inv.SupplierID,
				*inv.OpenItemID, bank, text)}
		posted, err := Post(ctx, tx, v)
		if err != nil {
			return err
		}
		if err := refuseMoved(ctx, q, posted, date); err != nil {
			return err
		}
		bankID := bank.AccountID
		if bankID == 0 {
			roles, err := q.RoleAccounts(ctx)
			if err != nil {
				return err
			}
			for _, r := range roles {
				if r.Role == roleBank {
					bankID = r.ID
				}
			}
		}
		_, err = q.InsertSupplierPayment(ctx, store.InsertSupplierPaymentParams{SupplierInvoiceID: inv.ID, PaidOn: date,
			AmountNok: money(paid), AmountCurrency: money(settledCur), BankAccountID: bankID, VoucherID: posted.ID, Now: s.now(),
			UserID: by})
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

// RemoveAccountingSupplierPayment removes a payment: its voucher reversed,
// line for line, dated the payment's day, while that day's period is open.
func (s *server) RemoveAccountingSupplierPayment(ctx context.Context, req gen.RemoveAccountingSupplierPaymentRequestObject) (gen.RemoveAccountingSupplierPaymentResponseObject, error) {
	by := callerID(ctx)
	err := s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		inv, err := q.LockSupplierInvoice(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		p, err := q.LockSupplierPayment(ctx, store.LockSupplierPaymentParams{ID: req.PaymentId, SupplierInvoiceID: req.Id})
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if p.RemovedAt != nil {
			return conflict(codePaymentRemoved, "The payment is removed already", "")
		}
		stored, err := storedVoucherOf(ctx, q, p.VoucherID)
		if err != nil {
			return err
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
			return err
		}
		if err := refuseMoved(ctx, q, posted, p.PaidOn); err != nil {
			return err
		}
		return q.MarkSupplierPaymentRemoved(ctx, store.MarkSupplierPaymentRemovedParams{ID: p.ID, Now: ptr(s.now()),
			UserID: &by, VoucherID: &posted.ID})
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
