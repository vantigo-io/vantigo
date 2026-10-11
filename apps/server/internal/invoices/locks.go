package invoices

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// The receivables' row locks (invoices payments and reminders design D18).
// Every lock a new path takes goes through one of these helpers, which take
// it with the statement of queries/locks.sql (or LockInvoice) and report it
// to the lock-order seam, so each path's test can pin its order: own row
// first, then invoices in descending id, never an invoice and then a bank
// line, an account row or a print batch. The existing paths keep
// txq.LockInvoice; they are not D18's subject (plan reading 30).

// lockTaken reports every lock a new path takes, in order, to a test; nil in production.
// key is the row's id in decimal, or the account's 11 digits.
var lockTaken func(ctx context.Context, what, key string)

// noteLock reports one lock taken to the seam, when a test installed it.
func noteLock(ctx context.Context, what, key string) {
	if hook := lockTaken; hook != nil {
		hook(ctx, what, key)
	}
}

// lockInvoice takes one document FOR UPDATE, reported "invoice".
func lockInvoice(ctx context.Context, txq *store.Queries, id int64) (store.InvoicesInvoice, error) {
	inv, err := txq.LockInvoice(ctx, id)
	if err != nil {
		return inv, err
	}
	noteLock(ctx, "invoice", strconv.FormatInt(id, 10))
	return inv, nil
}

// lockInvoicesDescending takes documents FOR UPDATE in descending id, each
// reported "invoice" in the order taken. Ids that name no document lock
// nothing.
func lockInvoicesDescending(ctx context.Context, txq *store.Queries, ids []int64) ([]store.InvoicesInvoice, error) {
	invs, err := txq.LockInvoicesDescending(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: lock invoices %v: %w", ids, err)
	}
	for _, inv := range invs {
		noteLock(ctx, "invoice", strconv.FormatInt(inv.ID, 10))
	}
	return invs, nil
}

// lockBankTransaction takes one bank line FOR NO KEY UPDATE, reported
// "bank_transaction".
func lockBankTransaction(ctx context.Context, txq *store.Queries, id int64) (store.InvoicesBankTransaction, error) {
	line, err := txq.LockBankTransaction(ctx, id)
	if err != nil {
		return line, err
	}
	noteLock(ctx, "bank_transaction", strconv.FormatInt(id, 10))
	return line, nil
}

// lockImportAccounts is a bank import's first statements (D3 step 7.1): the
// accounts not seen before inserted with format, by and at, then every
// account read FOR SHARE, each reported "account" in account order.
func lockImportAccounts(ctx context.Context, txq *store.Queries, accounts []string, format string, by uuid.UUID, at time.Time) ([]store.InvoicesBankImportAccount, error) {
	if err := txq.UpsertImportAccounts(ctx, store.UpsertImportAccountsParams{
		Format: format, SetByUserID: by, SetAt: at, Accounts: accounts,
	}); err != nil {
		return nil, fmt.Errorf("invoices: insert the import's accounts: %w", err)
	}
	rows, err := txq.ShareImportAccounts(ctx, accounts)
	if err != nil {
		return nil, fmt.Errorf("invoices: lock the import's accounts: %w", err)
	}
	for _, row := range rows {
		noteLock(ctx, "account", row.Account)
	}
	return rows, nil
}

// lockImportAccount takes one account FOR UPDATE, reported "account".
func lockImportAccount(ctx context.Context, txq *store.Queries, account string) (store.InvoicesBankImportAccount, error) {
	row, err := txq.LockImportAccount(ctx, account)
	if err != nil {
		return row, err
	}
	noteLock(ctx, "account", account)
	return row, nil
}

// lockReminder takes one letter FOR NO KEY UPDATE, reported "reminder".
func lockReminder(ctx context.Context, txq *store.Queries, id int64) (store.InvoicesReminder, error) {
	letter, err := txq.LockReminder(ctx, id)
	if err != nil {
		return letter, err
	}
	noteLock(ctx, "reminder", strconv.FormatInt(id, 10))
	return letter, nil
}

// lockPrintBatch takes one print batch FOR NO KEY UPDATE, reported
// "print_batch".
func lockPrintBatch(ctx context.Context, txq *store.Queries, id int64) (store.InvoicesReminderPrintBatch, error) {
	batch, err := txq.LockPrintBatch(ctx, id)
	if err != nil {
		return batch, err
	}
	noteLock(ctx, "print_batch", strconv.FormatInt(id, 10))
	return batch, nil
}

// lockCollectionRate takes one collection rate FOR UPDATE, reported
// "collection_rate" (the DELETE, D6).
func lockCollectionRate(ctx context.Context, txq *store.Queries, id int64) (store.InvoicesCollectionRate, error) {
	row, err := txq.LockCollectionRate(ctx, id)
	if err != nil {
		return row, err
	}
	noteLock(ctx, "collection_rate", strconv.FormatInt(id, 10))
	return row, nil
}

// shareOwnPrintBatch reads print batch id FOR SHARE, reported
// "print_batch", and answers whether it is closed — posted or reprinted: the
// first lock of each of the batch's letters' transactions while it is
// printed, so a posting or a reprint (FOR NO KEY UPDATE) never runs while a
// letter is being printed into the batch (D18).
func shareOwnPrintBatch(ctx context.Context, txq *store.Queries, id int64) (closed bool, err error) {
	if closed, err = txq.ShareOwnPrintBatch(ctx, id); err != nil {
		return false, fmt.Errorf("invoices: share print batch %d: %w", id, err)
	}
	noteLock(ctx, "print_batch", strconv.FormatInt(id, 10))
	return closed, nil
}

// shareCollectionRates reads FOR KEY SHARE the collection rates a letter
// dated day relies on — of each kind, the row in force on day, the rows
// DeleteInvoicesCollectionRatesById judges "used" — each reported
// "collection_rate" in id order (a print batch's letter, D6, plan reading
// 6). A DELETE takes its row FOR UPDATE, which waits for this share until
// the letter's transaction commits, and then sees the letter printed.
//
// It answers the rows it shared: the statement decides "in force" on its own
// snapshot, so a row added after it is not shared, and the caller reads the
// rows in force again once it has judged (CollectionRatesInForce) and judges
// again when they differ.
func shareCollectionRates(ctx context.Context, txq *store.Queries, day time.Time) ([]int64, error) {
	ids, err := txq.ShareCollectionRatesInForce(ctx, pgDate(day))
	if err != nil {
		return nil, fmt.Errorf("invoices: share the collection rates in force on %s: %w", day.Format(time.DateOnly), err)
	}
	for _, id := range ids {
		noteLock(ctx, "collection_rate", strconv.FormatInt(id, 10))
	}
	return ids, nil
}

// shareCustomerDocuments reads one customer's documents FOR SHARE, newest
// first, each reported "document" (D7's policy PUT, plan reading 11).
func shareCustomerDocuments(ctx context.Context, txq *store.Queries, customerID int32) error {
	ids, err := txq.ShareCustomerDocuments(ctx, customerID)
	if err != nil {
		return fmt.Errorf("invoices: share customer %d's documents: %w", customerID, err)
	}
	for _, id := range ids {
		noteLock(ctx, "document", strconv.FormatInt(id, 10))
	}
	return nil
}

// lockPolicies takes the reminder policies of customerIDs FOR UPDATE in
// customer id order, each row reported "policy" (the merge, D7).
func lockPolicies(ctx context.Context, txq *store.Queries, customerIDs []int32) error {
	ids, err := txq.LockPolicies(ctx, customerIDs)
	if err != nil {
		return fmt.Errorf("invoices: lock the reminder policies of %v: %w", customerIDs, err)
	}
	for _, id := range ids {
		noteLock(ctx, "policy", strconv.FormatInt(int64(id), 10))
	}
	return nil
}
