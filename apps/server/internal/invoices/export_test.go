package invoices

import (
	"context"
	"math/big"
	"time"
)

// InLockedTx exposes inLockedTx to the external tests, whose contract-call
// hook uses it to tell a call made from inside one of this module's locked
// transactions from one made outside any.
var InLockedTx = inLockedTx

// SetContractCallHook installs the hook every call out of this module is
// reported to (contractscalls.go). The harness installs it once, from
// TestMain, before any test runs: a package-level hook written while other
// tests are making requests would be a data race of the tests' own making.
func SetContractCallHook(hook func(ctx context.Context, method string)) {
	contractCallHook = hook
}

// SetIssueAfterAllocation installs a hook the issue calls inside its
// transaction right after the number is allocated, and answers the function
// that removes it. A test using it does not run in parallel: the hook is the
// package's.
func SetIssueAfterAllocation(hook func(ctx context.Context, invoiceID int64) error) func() {
	issueAfterAllocation = hook
	return func() { issueAfterAllocation = nil }
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

// SetPDFModelBuilt installs a hook told, for every PDF laid out, its
// document's id, its watermark ("" for none) and whether it carries a
// number, and answers the function that removes it. A test using it does not
// run in parallel: the hook is the package's.
func SetPDFModelBuilt(hook func(invoiceID int64, watermark string, numbered bool)) func() {
	pdfModelBuilt = func(id int64, m pdfModel) {
		numbered := false
		for _, row := range m.meta {
			numbered = numbered || row[0] == labels["nb"].number || row[0] == labels["en"].number
		}
		hook(id, m.watermark, numbered)
	}
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
