package invoices_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The buyer's VAT registration is snapshotted at the issue (§ 5-1-2's MVA
// suffix), and a credit note keeps its original's.
func TestIssue_SnapshotsTheBuyersVATRegistration(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.VATRegistered = true })
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	credit := issued(t, h, creditDraft(t, h, inv.ID).ID)
	plain := issued(t, h, createDraft(t, h, draftBody(customerNoTerms, line("A", 1, 100, vat25))).ID)
	for id, want := range map[int64]bool{inv.ID: true, credit.ID: true, plain.ID: false} {
		if got := modtest.One[bool](t, h.Harness, `SELECT buyer_vat_registered FROM invoices.invoices WHERE id = $1`, id); got != want {
			t.Errorf("document %d buyer_vat_registered = %v, want %v", id, got, want)
		}
	}
}
