package accounting

// The actions Needs attention has for Expenses' facts (R2): the amount in NOK
// of an expense in another currency, entered and posted; an expense of the
// supplier_invoice kind made into a draft supplier invoice; and an
// informational row acknowledged.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// openUnposted is a row under Needs attention that has not posted and is not
// resolved, of the reason given; 404 when there is no such row.
func openUnposted(ctx context.Context, q *store.Queries, id int64, reason string) (store.GetUnpostedRow, error) {
	row, err := q.GetUnposted(ctx, id)
	if noRows(err) {
		return row, notFound()
	}
	if err != nil {
		return row, err
	}
	if row.PostedAt != nil && !row.Informational {
		return row, conflict(codeUnpostedPosted, "The fact has posted", "")
	}
	if row.ResolvedAt != nil || row.Reason != reason {
		return row, conflict(codeUnpostedWrongReason, "The row is not waiting for this",
			"This action is for a row of reason "+reason+"; this one is "+row.Reason+".")
	}
	return row, nil
}

// EnterAccountingUnpostedNokAmounts keeps the amounts in NOK a person entered
// for an expense fact's lines in another currency, and posts the fact with
// them (R2): read again through Expenses' journal with no transaction open,
// posted in one transaction, and then what waited behind it.
func (s *server) EnterAccountingUnpostedNokAmounts(ctx context.Context, req gen.EnterAccountingUnpostedNokAmountsRequestObject) (gen.EnterAccountingUnpostedNokAmountsResponseObject, error) {
	const title = "The amounts cannot be entered"
	if req.Body == nil {
		return nil, invalidField(title, "body", "", "A body is required.")
	}
	q := store.New(s.deps.Pool)
	row, err := openUnposted(ctx, q, req.Id, reasonAmountInNOKNeeded)
	if err != nil {
		return nil, err
	}
	notes := parseLineNotes(row.NokAmounts)
	var f fieldErrors
	for i, a := range req.Body.Amounts {
		key := strconv.FormatInt(a.EntryId, 10)
		n, ok := notes[key]
		if !ok {
			f.add(fmt.Sprintf("amounts[%d].entryId", i), "Not one of the row's lines in another currency.")
			continue
		}
		amount, ok := parseAmount(a.AmountNok)
		if !ok || amount.Sign() <= 0 {
			f.addCoded(fmt.Sprintf("amounts[%d].amountNok", i), codeAmountInvalid, "A positive amount in NOK, with at most two decimals.")
			continue
		}
		n.AmountNOK = ptr(money(amount))
		notes[key] = n
	}
	if err := f.refusal(title); err != nil {
		return nil, err
	}
	data, err := json.Marshal(notes)
	if err != nil {
		return nil, err
	}
	if err := q.SetUnpostedNokAmounts(ctx, store.SetUnpostedNokAmountsParams{NokAmounts: string(data), ID: row.ID}); err != nil {
		return nil, err
	}
	row.NokAmounts = string(data)
	out, err := s.retryAndDependants(ctx, row)
	if err != nil {
		return nil, err
	}
	return gen.EnterAccountingUnpostedNokAmounts200JSONResponse(out), nil
}

// AcknowledgeAccountingUnposted resolves an informational row: it posted,
// and the person has seen what it says.
func (s *server) AcknowledgeAccountingUnposted(ctx context.Context, req gen.AcknowledgeAccountingUnpostedRequestObject) (gen.AcknowledgeAccountingUnpostedResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetUnposted(ctx, req.Id)
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	if !row.Informational || row.ResolvedAt != nil {
		return nil, conflict(codeUnpostedWrongReason, "Only an informational row is acknowledged",
			"A row that waits is fixed and retried; it is never waved away.")
	}
	if _, err := q.ResolveUnposted(ctx, store.ResolveUnpostedParams{ID: row.ID, Now: ptr(s.now())}); err != nil {
		return nil, err
	}
	row, err = q.GetUnposted(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.AcknowledgeAccountingUnposted200JSONResponse(unpostedView(row, nil)), nil
}

// MakeAccountingUnpostedSupplierInvoice makes a draft supplier invoice from
// an expense of Expenses' supplier_invoice kind (R2): its supplier matched by
// name among the active suppliers or created, one line with its amount on
// the category's account and, when its VAT matches an input rate, that
// rate's code, its date, due date, number and project, and its receipts
// copied through ExpenseJournal into the draft's files. The row is resolved
// in the transaction that makes the draft; the receipts are copied after it,
// one by one, and one that cannot be copied is left for the person to
// attach.
func (s *server) MakeAccountingUnpostedSupplierInvoice(ctx context.Context, req gen.MakeAccountingUnpostedSupplierInvoiceRequestObject) (gen.MakeAccountingUnpostedSupplierInvoiceResponseObject, error) {
	const title = "The supplier invoice cannot be made"
	q := store.New(s.deps.Pool)
	row, err := openUnposted(ctx, q, req.Id, reasonEnterAsSupplierInvoice)
	if err != nil {
		return nil, err
	}
	it, err := s.reread(ctx, row)
	if err != nil {
		return nil, err
	}
	if it == nil || it.expense == nil {
		return nil, invalidField(title, "id", "", "Expenses' journal no longer answers this fact.")
	}
	var line *contracts.ExpenseLine
	for i := range it.expense.Lines {
		if it.expense.Lines[i].Kind == contracts.ExpenseKindSupplierInvoice {
			line = &it.expense.Lines[i]
			break
		}
	}
	if line == nil {
		return nil, invalidField(title, "id", "", "The fact has no expense of the supplier invoice kind.")
	}
	gross, ok := parseAmount(line.Gross)
	if !ok {
		return nil, invalidField(title, "id", codeAmountInvalid, "The expense's amount is not an amount.")
	}
	supplierName := strings.TrimSpace(line.Supplier)
	if supplierName == "" {
		return nil, invalidField(title, "id", "", "The expense names no supplier.")
	}
	by := callerID(ctx)
	var id int64
	err = s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		b, err := readExpenseBooks(ctx, q, line.Date)
		if err != nil {
			return err
		}
		supplierID, err := q.SupplierByName(ctx, supplierName)
		if noRows(err) {
			supplierID, err = insertSupplier(ctx, q, supplierFields{name: truncate(supplierName, 200), country: "NO",
				currency: line.Currency, paymentTermsDays: 14, active: true}, s.now())
		}
		if err != nil {
			return err
		}
		account, deductible, _, err := b.expenseAccount(*line)
		if err != nil {
			return invalidField(title, "id", "", err.Error())
		}
		in := purchaseLineIn{AccountID: account, Amount: gross, Gross: true, ProjectID: line.ProjectID,
			Text: truncate(strings.TrimSpace(line.CategoryName), 500)}
		if vat, ok := parseAmount(line.VAT); ok && deductible && b.registered && line.Currency == currency {
			if code := inferredCode(b.rates, gross, vat); code != nil {
				in.TaxCodeID = &code.ID
			}
		}
		number := strings.TrimSpace(line.SupplierInvoiceNumber)
		if number == "" {
			number = fmt.Sprintf("EXP-%d", line.EntryID)
		}
		due := line.SupplierDueDate
		if due == nil {
			due = ptr(line.Date.AddDate(0, 0, 14))
		}
		d := draftInput{supplierID: supplierID, kind: "invoice", number: truncate(number, 60), invoiceDate: line.Date,
			dueDate: due, currency: line.Currency, rate: big.NewRat(1, 1), total: gross, totalNOK: gross,
			description: b.text("From expense %d", "Fra utgift %d", line.EntryID), lines: []purchaseLineIn{in}}
		if id, err = insertDraft(ctx, q, d, nil, &line.EntryID, by, s.now()); err != nil {
			return err
		}
		_, err = q.ResolveUnposted(ctx, store.ResolveUnpostedParams{ID: row.ID, Now: ptr(s.now()), SupplierInvoiceID: &id})
		return err
	})
	if c := duplicateConflict(err); c != nil {
		return nil, c
	}
	if err != nil {
		return nil, err
	}
	s.copyReceipts(ctx, id, line.EntryID, by)
	out, err := s.supplierInvoiceOne(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.MakeAccountingUnpostedSupplierInvoice201JSONResponse(out), nil
}

// copyReceipts copies an expense's receipts into a draft's files, through
// Expenses' journal, with no transaction open. A receipt that cannot be read
// or written is logged and left out.
func (s *server) copyReceipts(ctx context.Context, draft, entry int64, by uuid.UUID) {
	journal := s.deps.ExpenseJournal
	if journal == nil {
		return
	}
	receipts, err := journal.Attachments(ctx, entry)
	if err != nil {
		s.logger().WarnContext(ctx, "accounting: an expense's receipts could not be listed", "entry_id", entry, "error", err.Error())
		return
	}
	for _, r := range receipts {
		body, meta, err := journal.OpenAttachment(ctx, entry, r.ID)
		if err != nil {
			s.logger().WarnContext(ctx, "accounting: a receipt could not be opened", "attachment_id", r.ID, "error", err.Error())
			continue
		}
		data, err := io.ReadAll(io.LimitReader(body, maxAttachmentBytes+1))
		_ = body.Close()
		if err != nil || len(data) == 0 || len(data) > maxAttachmentBytes {
			s.logger().WarnContext(ctx, "accounting: a receipt could not be read", "attachment_id", r.ID)
			continue
		}
		if _, err := s.storeSupplierFile(ctx, draft, meta.FileName, meta.ContentType, data, by); err != nil {
			s.logger().WarnContext(ctx, "accounting: a receipt could not be kept on the draft", "attachment_id", r.ID, "error", err.Error())
		}
	}
}
