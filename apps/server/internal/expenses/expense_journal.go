package expenses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the module's contracts.ExpenseJournal (accounting design A20,
// R2): the fact log (expenses.journal_facts, migration 00184) in seq order,
// each fact with the lines its trigger snapshotted at commit and their
// categories' current names, and an expense's receipts for a ledger to copy.
//
// It serves from this module's own tables and object store and nothing else,
// on the pool, and holds no lock.

// expenseJournal is this module's contracts.ExpenseJournal.
type expenseJournal struct {
	q          *store.Queries
	objects    storage.ObjectStore
	objectsErr error
}

var _ contracts.ExpenseJournal = (*expenseJournal)(nil)

// newExpenseJournal is Module's ExpenseJournal: the constructor Compose and
// Workers call. It takes the pool and the module's object store.
func newExpenseJournal(d module.Deps) contracts.ExpenseJournal {
	objects, err := moduleObjectStore(d)
	return &expenseJournal{q: store.New(d.Pool), objects: objects, objectsErr: err}
}

// journalLine is one line of a fact's snapshot, as the trigger writes it.
type journalLine struct {
	EntryID               int64   `json:"entryId"`
	Kind                  string  `json:"kind"`
	Date                  string  `json:"date"`
	CategoryID            *int32  `json:"categoryId"`
	PaidBy                *string `json:"paidBy"`
	Currency              string  `json:"currency"`
	Gross                 string  `json:"gross"`
	VAT                   *string `json:"vat"`
	ProjectID             *int32  `json:"projectId"`
	Supplier              *string `json:"supplier"`
	SupplierInvoiceNumber *string `json:"supplierInvoiceNumber"`
	SupplierDueDate       *string `json:"supplierDueDate"`
}

// journalDay is a snapshot's date as UTC midnight.
func journalDay(text string) (time.Time, error) {
	return time.ParseInLocation(time.DateOnly, text, time.UTC)
}

// Facts is up to limit facts after afterSeq, in seq order.
func (j *expenseJournal) Facts(ctx context.Context, afterSeq int64, limit int) ([]contracts.ExpenseFact, error) {
	limit = min(max(limit, 1), contracts.MaxExpenseJournalPage)
	rows, err := j.q.JournalFacts(ctx, store.JournalFactsParams{AfterSeq: afterSeq, RowLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("expenses: read the journal's facts: %w", err)
	}
	out := make([]contracts.ExpenseFact, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	lines := make([][]journalLine, len(rows))
	var categories []int32
	for i, r := range rows {
		if err := json.Unmarshal([]byte(r.Lines), &lines[i]); err != nil {
			return nil, fmt.Errorf("expenses: decode the lines of fact %d: %w", r.Seq, err)
		}
		for _, l := range lines[i] {
			if l.CategoryID != nil {
				categories = append(categories, *l.CategoryID)
			}
		}
	}
	names := map[int32]string{}
	if len(categories) > 0 {
		got, err := j.q.JournalCategoryNames(ctx, categories)
		if err != nil {
			return nil, fmt.Errorf("expenses: read the journal's categories: %w", err)
		}
		for _, c := range got {
			names[c.ID] = c.Name
		}
	}
	for i, r := range rows {
		f := contracts.ExpenseFact{Seq: r.Seq, Occurrence: r.Occurrence, Subject: r.Subject, SubjectID: r.SubjectID,
			UserID: r.UserID, Date: r.BusinessDate.Time, UndoesSeq: r.UndoesSeq, ReimbursementReference: r.ReimbursementReference,
			Lines: make([]contracts.ExpenseLine, 0, len(lines[i]))}
		for _, l := range lines[i] {
			d, err := journalDay(l.Date)
			if err != nil {
				return nil, fmt.Errorf("expenses: the date of fact %d's line %d: %w", r.Seq, l.EntryID, err)
			}
			line := contracts.ExpenseLine{EntryID: l.EntryID, Kind: l.Kind, Date: d, CategoryID: l.CategoryID,
				PaidBy: derefString(l.PaidBy), Currency: l.Currency, Gross: l.Gross, VAT: derefString(l.VAT), ProjectID: l.ProjectID,
				Supplier: derefString(l.Supplier), SupplierInvoiceNumber: derefString(l.SupplierInvoiceNumber)}
			if l.CategoryID != nil {
				line.CategoryName = names[*l.CategoryID]
			}
			if l.SupplierDueDate != nil {
				due, err := journalDay(*l.SupplierDueDate)
				if err != nil {
					return nil, fmt.Errorf("expenses: the due date of fact %d's line %d: %w", r.Seq, l.EntryID, err)
				}
				line.SupplierDueDate = &due
			}
			f.Lines = append(f.Lines, line)
		}
		out = append(out, f)
	}
	return out, nil
}

// attachmentOf is a receipt row as the contract answers it.
func attachmentOf(a store.ExpensesAttachment) contracts.ExpenseAttachment {
	return contracts.ExpenseAttachment{ID: a.ID, EntryID: a.EntryID, FileName: a.FileName, ContentType: a.ContentType,
		SizeBytes: a.SizeBytes}
}

// Attachments is one expense's receipts, oldest first.
func (j *expenseJournal) Attachments(ctx context.Context, entryID int64) ([]contracts.ExpenseAttachment, error) {
	rows, err := j.q.ListAttachmentsForEntries(ctx, []int64{entryID})
	if err != nil {
		return nil, fmt.Errorf("expenses: read the receipts of %d: %w", entryID, err)
	}
	out := make([]contracts.ExpenseAttachment, 0, len(rows))
	for _, r := range rows {
		out = append(out, attachmentOf(r))
	}
	return out, nil
}

// OpenAttachment opens one of an expense's receipts from the module's own
// object store.
func (j *expenseJournal) OpenAttachment(ctx context.Context, entryID, attachmentID int64) (io.ReadCloser, contracts.ExpenseAttachment, error) {
	row, err := j.q.GetAttachment(ctx, attachmentID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.EntryID != entryID) {
		return nil, contracts.ExpenseAttachment{}, contracts.ErrExpenseAttachmentNotFound
	}
	if err != nil {
		return nil, contracts.ExpenseAttachment{}, fmt.Errorf("expenses: read receipt %d: %w", attachmentID, err)
	}
	if j.objectsErr != nil {
		return nil, contracts.ExpenseAttachment{}, j.objectsErr
	}
	body, err := j.objects.Get(ctx, row.ObjectKey)
	if err != nil {
		return nil, contracts.ExpenseAttachment{}, fmt.Errorf("expenses: open receipt %d: %w", attachmentID, err)
	}
	return body, attachmentOf(row), nil
}
