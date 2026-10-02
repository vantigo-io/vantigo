package invoices

import (
	"context"
	"math/big"
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
