package invoices

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// InLockedTx exposes inLockedTx to the external tests, whose contract-call
// hook uses it to tell a call made from inside one of this module's locked
// transactions from one made outside any.
var InLockedTx = inLockedTx

// SetContractCallHook installs the hook every call out of this module is
// reported to (contractscalls.go), with whether it is a transaction-bound
// command. The harness installs it once, from TestMain, before any test runs:
// a package-level hook written while other tests are making requests would be
// a data race of the tests' own making.
func SetContractCallHook(hook func(ctx context.Context, method string, txBound bool)) {
	contractCallHook = hook
}

// NoteContractCall and NoteTxCommand are the two reports every call out of
// the module makes, for the test that drives the harness's recorder through
// them.
var (
	NoteContractCall = noteContractCall
	NoteTxCommand    = noteTxCommand
)

// LockedContext marks ctx as withLockedTx does, for that same test.
func LockedContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, lockedTxKey{}, true)
}

// SetIssueAfterAllocation installs a hook the issue calls inside its
// transaction right after the number is allocated, with that transaction, and
// answers the function that removes it. A test using it does not run in
// parallel: the hook is the package's.
func SetIssueAfterAllocation(hook func(ctx context.Context, tx pgx.Tx, invoiceID int64) error) func() {
	issueAfterAllocation = hook
	return func() { issueAfterAllocation = nil }
}

// SetIssueBeforeLock installs a hook the issue calls after its reads before
// the transaction and before the transaction begins, and answers the function
// that removes it. A test using it does not run in parallel: the hook is the
// package's.
func SetIssueBeforeLock(hook func(ctx context.Context, invoiceID int64)) func() {
	issueBeforeLock = hook
	return func() { issueBeforeLock = nil }
}

// SetFromWorkBeforeInsert installs a hook the wizard (POST /invoices/from-work)
// calls inside its transaction after LiveSourcesElsewhere found the work free
// and before it inserts the holds, with the draft's id, and answers the
// function that removes it. A race test parks one wizard there while another
// commits. A test using it does not run in parallel: the hook is the
// package's.
func SetFromWorkBeforeInsert(hook func(ctx context.Context, invoiceID int64)) func() {
	fromWorkBeforeInsert = hook
	return func() { fromWorkBeforeInsert = nil }
}

// SetSaveAfterLines installs a hook a draft's save calls inside its
// transaction right after the lines are written, with the draft's id, and
// answers the function that removes it. A test using it does not run in
// parallel: the hook is the package's.
func SetSaveAfterLines(hook func(ctx context.Context, invoiceID int64)) func() {
	saveAfterLines = hook
	return func() { saveAfterLines = nil }
}

// SetCreditAfterCopy installs a hook a credit-note draft's creation calls
// inside its transaction right after the original's lines are copied, with
// the credit note's id, and answers the function that removes it. A test
// using it does not run in parallel: the hook is the package's.
func SetCreditAfterCopy(hook func(ctx context.Context, invoiceID int64)) func() {
	creditAfterCopy = hook
	return func() { creditAfterCopy = nil }
}

// SetPaymentAfterLock installs a hook both payment writes — a registration
// and a removal — call inside their transaction right after the invoice is
// locked, and answers the function that removes it. A race test holds one
// side there while it starts the other. A test using it does not run in
// parallel: the hook is the package's.
func SetPaymentAfterLock(hook func(ctx context.Context, invoiceID int64)) func() {
	paymentAfterLock = hook
	return func() { paymentAfterLock = nil }
}

// SetCreditIssueAfterOriginalLock installs a hook a credit note's issue calls
// inside its transaction right after it has locked the original, with the
// original's id, and answers the function that removes it. A test using it
// does not run in parallel: the hook is the package's.
func SetCreditIssueAfterOriginalLock(hook func(ctx context.Context, invoiceID int64)) func() {
	creditIssueAfterOriginalLock = hook
	return func() { creditIssueAfterOriginalLock = nil }
}

// SetPreviewRendered installs a hook every preview reports its VAT total to,
// as a two-decimal string, and answers the function that removes it. A test
// using it does not run in parallel: the hook is the package's.
func SetPreviewRendered(hook func(invoiceID int64, vatTotal string)) func() {
	previewRendered = func(id int64, vat *big.Rat) { hook(id, vat.FloatString(2)) }
	return func() { previewRendered = nil }
}

// SetAfterPDFRender installs a hook the store-once path calls with each
// rendered PDF, answering the bytes it stores, and answers the function that
// removes it. A test using it does not run in parallel: the hook is the
// package's.
func SetAfterPDFRender(hook func(ctx context.Context, body []byte) []byte) func() {
	afterPDFRender = hook
	return func() { afterPDFRender = nil }
}

// PDFModel is what SetPDFModelBuilt reports of one PDF's model: its
// watermark ("" for none), whether it carries a number, and its timesheet
// block (nil for none).
type PDFModel struct {
	Watermark string
	Numbered  bool
	Timesheet *PDFTimesheet
}

// PDFTimesheet is a model's timesheet block as it prints it (invoices work
// design D5): its title, its column header, its rows and its totals — one
// per person, then the whole.
type PDFTimesheet struct {
	Title  string
	Header []string
	Rows   [][]string
	Totals [][2]string
}

// SetPDFModelBuilt installs a hook told, for every PDF laid out, its
// document's id and what its model says (PDFModel), and answers the function
// that removes it. A test using it does not run in parallel: the hook is the
// package's.
func SetPDFModelBuilt(hook func(invoiceID int64, m PDFModel)) func() {
	pdfModelBuilt = func(id int64, m pdfModel) {
		report := PDFModel{Watermark: m.watermark}
		for _, row := range m.meta {
			report.Numbered = report.Numbered || row[0] == labels["nb"].number || row[0] == labels["en"].number
		}
		if t := m.timesheet; t != nil {
			report.Timesheet = &PDFTimesheet{Title: t.title, Header: t.header, Rows: t.rows, Totals: t.totals}
		}
		hook(id, report)
	}
	return func() { pdfModelBuilt = nil }
}

// SetPDFModelText installs a hook told, for every PDF laid out, its
// document's id, its meta rows and its line rows as printed, and answers the
// function that removes it. A test using it does not run in parallel: the
// hook is the package's, shared with SetPDFModelBuilt.
func SetPDFModelText(hook func(invoiceID int64, meta [][2]string, lines [][]string)) func() {
	pdfModelBuilt = func(id int64, m pdfModel) { hook(id, m.meta, m.lines) }
	return func() { pdfModelBuilt = nil }
}

// DocumentState is documentState, the Go mirror of invoices.document_state,
// for the test that holds the two to each other.
var DocumentState = documentState

// InvoiceStates is the five states an issued invoice can be in, the ones the
// list filters by.
var InvoiceStates = invoiceStates

// SetBeforeDeliveryWrite installs a hook a send calls right before it writes
// its delivery row — after the mail went, on an uncancellable context, before
// the row's own timeout starts — and answers the function that removes it. A race test holds the
// send there while an erase runs. A test using it does not run in parallel:
// the hook is the package's.
func SetBeforeDeliveryWrite(hook func(ctx context.Context, invoiceID int64)) func() {
	beforeDeliveryWrite = hook
	return func() { beforeDeliveryWrite = nil }
}

// SetBeforeTransmissionInsert installs a hook a send as EHF calls inside its
// transaction right before it inserts the transmission — after the document's
// lock, the credentials' FOR SHARE and the judgment under them — and answers
// the function that removes it. A race test holds one send there while it
// starts another, or a DELETE of the credentials. A test using it does not run
// in parallel: the hook is the package's.
func SetBeforeTransmissionInsert(hook func(ctx context.Context, invoiceID int64)) func() {
	beforeTransmissionInsert = hook
	return func() { beforeTransmissionInsert = nil }
}

// SetSendEhfWithoutLock makes a send as EHF read the document inside its
// transaction without locking it, so a race test can show the partial unique
// index refusing a second transmission on its own, and answers the function
// that restores the lock. A test using it does not run in parallel.
func SetSendEhfWithoutLock() func() {
	sendEhfWithoutLock = true
	return func() { sendEhfWithoutLock = false }
}

// SetEhfCallTimeout bounds every provider call the EHF workers make by d, so
// a test reaches a transport timeout without waiting thirty seconds, and
// answers the function that restores it. A test using it does not run in
// parallel: the bound is the package's.
func SetEhfCallTimeout(d time.Duration) func() {
	ehfCallTimeout = d
	return func() { ehfCallTimeout = ehfDefaultCallTimeout }
}

// SetEhfAfterCall installs a hook the invoices-ehf worker calls after every
// provider call and before it records the outcome, with the transmission's
// id, and answers the function that removes it. A test moves the row there to
// show the completion is a no-op once the lease changed. A test using it does
// not run in parallel: the hook is the package's.
func SetEhfAfterCall(hook func(ctx context.Context, transmissionID int64)) func() {
	ehfAfterCall = hook
	return func() { ehfAfterCall = nil }
}

// RePullNote is rePullNote over db, the sources given as parallel kinds and
// ids, for the test of the note the wizard suggests when it pulls released
// work again.
func RePullNote(ctx context.Context, db store.DBTX, language string, kinds []string, ids []int64) (string, error) {
	refs := make([]sourceRef, 0, len(ids))
	for i, id := range ids {
		refs = append(refs, sourceRef{kind: contracts.WorkSourceKind(kinds[i]), id: id})
	}
	return rePullNote(ctx, store.New(db), language, refs)
}

// SetLockTaken installs the lock-order seam (locks.go): every lock a new
// path takes through locks.go's helpers is reported to hook, in order, with
// what it locked and the row's key. It answers the function that removes it.
// A test using it does not run in parallel: the seam is the package's.
func SetLockTaken(hook func(ctx context.Context, what, key string)) func() {
	lockTaken = hook
	return func() { lockTaken = nil }
}

// LockForTest takes the lock of the helper named what (lockInvoice,
// lockInvoicesDescending, lockBankTransaction, lockImportAccounts,
// lockImportAccount, lockReminder, lockPrintBatch, shareCustomerDocuments or
// lockPolicies) on tx — the caller's transaction, on a connection of the
// caller's own, never one of the harness's pool — with keys as the helper
// takes them: ids in decimal, accounts as their eleven digits, customer ids.
// lockImportAccounts sets an account it inserts to ocr.
func LockForTest(ctx context.Context, tx pgx.Tx, what string, keys ...string) error {
	txq := store.New(tx)
	ints := func(bits int) ([]int64, error) {
		ids := make([]int64, 0, len(keys))
		for _, k := range keys {
			id, err := strconv.ParseInt(k, 10, bits)
			if err != nil {
				return nil, fmt.Errorf("invoices: %s key %q: %w", what, k, err)
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	one := func(bits int) (int64, error) {
		if len(keys) != 1 {
			return 0, fmt.Errorf("invoices: %s takes one key, got %d", what, len(keys))
		}
		ids, err := ints(bits)
		if err != nil {
			return 0, err
		}
		return ids[0], nil
	}
	var err error
	switch what {
	case "lockInvoice", "lockBankTransaction", "lockReminder", "lockPrintBatch":
		var id int64
		if id, err = one(64); err != nil {
			return err
		}
		switch what {
		case "lockInvoice":
			_, err = lockInvoice(ctx, txq, id)
		case "lockBankTransaction":
			_, err = lockBankTransaction(ctx, txq, id)
		case "lockReminder":
			_, err = lockReminder(ctx, txq, id)
		default:
			_, err = lockPrintBatch(ctx, txq, id)
		}
	case "lockInvoicesDescending":
		var ids []int64
		if ids, err = ints(64); err != nil {
			return err
		}
		_, err = lockInvoicesDescending(ctx, txq, ids)
	case "lockImportAccounts":
		_, err = lockImportAccounts(ctx, txq, keys, "ocr", uuid.Nil, time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC))
	case "lockImportAccount":
		if len(keys) != 1 {
			return fmt.Errorf("invoices: %s takes one key, got %d", what, len(keys))
		}
		_, err = lockImportAccount(ctx, txq, keys[0])
	case "shareCustomerDocuments":
		var id int64
		if id, err = one(32); err != nil {
			return err
		}
		err = shareCustomerDocuments(ctx, txq, int32(id))
	case "lockPolicies":
		var ids []int64
		if ids, err = ints(32); err != nil {
			return err
		}
		customers := make([]int32, 0, len(ids))
		for _, id := range ids {
			customers = append(customers, int32(id))
		}
		err = lockPolicies(ctx, txq, customers)
	default:
		return fmt.Errorf("invoices: no lock helper %q", what)
	}
	return err
}

// SetBankImportAfterInsert installs a hook a bank import calls inside its
// transaction after every insert and before the commit, with the file's id;
// an error it answers rolls the import back. It answers the function that
// removes it. A race test holds one import there while it starts another. A
// test using it does not run in parallel: the hook is the package's.
func SetBankImportAfterInsert(hook func(ctx context.Context, bankFileID int64) error) func() {
	bankImportAfterInsert = hook
	return func() { bankImportAfterInsert = nil }
}

// SetPolicyAfterLock installs a hook a reminder policy's PUT calls inside its
// transaction right after it has locked the policy row — after the
// customer's documents and the row, before the write — with the customer's
// id, and answers the function that removes it. An error rolls the PUT back.
// A race test parks a PUT there while a merge starts. A test using it does
// not run in parallel: the hook is the package's.
func SetPolicyAfterLock(hook func(ctx context.Context, customerID int32) error) func() {
	policyAfterLock = hook
	return func() { policyAfterLock = nil }
}

// EngineSettingsForTest is what the reminder engine is handed of the
// settings and the rates — reminderSettings and ratesOf over RatesFor — read
// on db, for the test that pins the mapping.
func EngineSettingsForTest(ctx context.Context, db store.DBTX) (reminderrules.Settings, []reminderrules.Rate, error) {
	q := store.New(db)
	settings, _, err := (&server{}).reminderSettings(ctx, q)
	if err != nil {
		return reminderrules.Settings{}, nil, err
	}
	rows, err := q.RatesFor(ctx)
	if err != nil {
		return reminderrules.Settings{}, nil, err
	}
	rates, err := ratesOf(rows)
	return settings, rates, err
}
