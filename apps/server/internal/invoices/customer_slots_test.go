package invoices_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// inTx runs fn in a transaction the test owns — the merge's or the
// anonymisation's position — and commits it or rolls it back as told.
func inTx(t *testing.T, h *harness, commit bool, fn func(pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(tx)
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
}

// disabledDeps is what Compose hands a module MODULES leaves out: no
// directory, no contract, no object store — only the platform.
func disabledDeps(h *harness) module.Deps {
	return module.Deps{Pool: h.Pool(), Clock: h.Now, Config: h.Deps().Config}
}

// The merge holder (D10) moves every document of the absorbed customer —
// drafts and issued — leaves the snapshots, reports invoices.invoices, and
// works with the module disabled; a rolled-back merge moved nothing.
func TestCustomerReferences_RepointMovesDraftsAndIssuedDocuments(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	issuedDoc := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	draft := createDraft(t, h, draftBody(customerAcme, line("B", 1, 100, vat25)))
	other := createDraft(t, h, draftBody(customerPerson, line("C", 1, 100, vat25)))
	holder := invoices.Module().CustomerReferences(disabledDeps(h))
	h.Advance(time.Hour)

	var moved []contracts.RepointedReferences
	inTx(t, h, false, func(tx pgx.Tx) {
		var err error
		if moved, err = holder.RepointCustomer(context.Background(), tx, customerAcme, customerNoTerms); err != nil {
			t.Fatalf("RepointCustomer: %v", err)
		}
	})
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE customer_id = $1`, customerAcme); n != 2 || moved[0].Count != 2 {
		t.Fatalf("after a rollback = %d still Acme's, reported %+v; want both there and 2 reported", n, moved)
	}

	inTx(t, h, true, func(tx pgx.Tx) {
		var err error
		if moved, err = holder.RepointCustomer(context.Background(), tx, customerAcme, customerNoTerms); err != nil {
			t.Fatalf("RepointCustomer: %v", err)
		}
	})
	if want := []contracts.RepointedReferences{{Kind: "invoices.invoices", Count: 2}}; !slices.Equal(moved, want) {
		t.Errorf("moved = %+v, want %+v", moved, want)
	}
	after := getInvoice(t, h, issuedDoc.ID)
	if after.CustomerID != customerNoTerms || after.Buyer.Name != issuedDoc.Buyer.Name || after.Revision != issuedDoc.Revision {
		t.Errorf("the issued document = customer %d buyer %q revision %d; want moved, its snapshot and revision kept",
			after.CustomerID, after.Buyer.Name, after.Revision)
	}
	if d := getInvoice(t, h, draft.ID); d.CustomerID != customerNoTerms || d.Revision != draft.Revision+1 {
		t.Errorf("the draft = customer %d revision %d; want moved and its revision on", d.CustomerID, d.Revision)
	}
	if o := getInvoice(t, h, other.ID); o.CustomerID != customerPerson {
		t.Error("another customer's draft moved")
	}

	inTx(t, h, true, func(tx pgx.Tx) {
		same, err := holder.RepointCustomer(context.Background(), tx, customerPerson, customerPerson)
		if err != nil || !slices.Equal(same, []contracts.RepointedReferences{{Kind: "invoices.invoices", Count: 0}}) {
			t.Errorf("from == into = %+v, %v; want a zero", same, err)
		}
	})
	if o := getInvoice(t, h, other.ID); o.Revision != other.Revision {
		t.Errorf("from == into wrote the draft: revision %d", o.Revision)
	}
}

// A merge racing a credit note's issue never deadlocks (D10): the issue
// holds the credit note and is about to lock its older original, and the
// merge — the holder inside the customers module's transaction — starts
// then. An UPDATE alone would lock the original first and wait on the credit
// note, a deadlock Postgres breaks by aborting one; locking newest first, the
// merge waits on the credit note holding nothing, and both finish. Not
// parallel: the issue hook is the package's.
func TestCustomerReferences_ARepointRacingACreditNoteIssueNeverDeadlocks(t *testing.T) {
	h := readyToIssue(t)
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 2, 1000, vat25))).ID)
	credit := creditDraft(t, h, original.ID)
	holder := invoices.Module().CustomerReferences(disabledDeps(h))

	mergeErr := make(chan error, 1)
	restore := invoices.SetIssueAfterAllocation(func(_ context.Context, id int64) error {
		if id != credit.ID {
			return nil
		}
		go func() {
			ctx := context.Background()
			tx, err := h.Pool().Begin(ctx)
			if err != nil {
				mergeErr <- err
				return
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := holder.RepointCustomer(ctx, tx, customerAcme, customerNoTerms); err != nil {
				mergeErr <- err
				return
			}
			mergeErr <- tx.Commit(ctx)
		}()
		// The merge is waiting on a document this issue holds before the
		// issue goes on to lock the original.
		waitForALockWaiter(t, h)
		return nil
	})
	res := issueWith(t, h, credit.ID, "")
	restore()
	if res.Status != http.StatusOK {
		t.Fatalf("the credit note's issue = %d %s, want 200 — a deadlock aborts one side", res.Status, res.Body)
	}
	if err := <-mergeErr; err != nil {
		t.Fatalf("the merge = %v, want it committed — a deadlock aborts one side", err)
	}
	after, note := getInvoice(t, h, original.ID), getInvoice(t, h, credit.ID)
	if after.CustomerID != customerNoTerms || note.CustomerID != customerNoTerms {
		t.Errorf("customers = original %d, credit note %d; want both moved to %d", after.CustomerID, note.CustomerID, customerNoTerms)
	}
	if note.Status != "issued" || after.CreditedAmount == nil || *after.CreditedAmount != note.GrossTotal || *after.UncreditedAmount != 0 {
		t.Errorf("after the race = credit note %s, original credited %v uncredited %v; want issued and the original credited in full",
			note.Status, after.CreditedAmount, after.UncreditedAmount)
	}
}

// The export (D10): nil when nothing is held; every issued document and draft
// otherwise, the internal notes included, each issued one with its whole buyer
// snapshot, a place of delivery when one is set, and a credit note naming the
// invoice it credits.
func TestCustomerPersonalData_Export(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	if none, err := data.ExportCustomerData(context.Background(), customerPerson); err != nil || none != nil {
		t.Fatalf("a customer with nothing = %v, %v; want nil", none, err)
	}
	h.customers.edit(customerPerson, func(p *contracts.CustomerBillingProfile) { p.InvoiceAddress.Region = "Vestland" })
	body := draftBody(customerPerson, line("Konsultasjon", 1.5, 800, vat25))
	body["internalNote"] = "Ringte to ganger"
	body["deliveryAddress"] = map[string]any{"line1": "Hytta", "postalCode": "3580", "city": "Geilo", "country": "NO"}
	issued(t, h, createDraft(t, h, body).ID)
	body["internalNote"] = "Utkast til neste måned"
	delete(body, "deliveryAddress")
	createDraft(t, h, body)

	export := func(customer int32) string {
		t.Helper()
		section, err := data.ExportCustomerData(context.Background(), customer)
		if err != nil {
			t.Fatalf("ExportCustomerData: %v", err)
		}
		raw, _ := json.Marshal(section)
		return string(raw)
	}
	raw := export(customerPerson)
	for _, want := range []string{
		`"documents":[{"number":1,"kind":"invoice","issueDate":"2026-09-12"`, `"grossTotal":"1500.00"`,
		`"buyer":{"customerNumber":10002,"type":"person","name":"Kari Nordmann",` +
			`"address":{"line1":"Hjemveien 5","postalCode":"5003","city":"Bergen","region":"Vestland","country":"NO"},"language":"en"}`,
		`"deliveryAddress":{"line1":"Hytta","postalCode":"3580","city":"Geilo","country":"NO"}`, `"internalNote":"Ringte to ganger"`,
		`"vatRatePercent":"25.00"`, `"drafts":[{"kind":"invoice"`, `"internalNote":"Utkast til neste måned"`, `"quantity":"1.500"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("export %s has no %s", raw, want)
		}
	}
	if n := strings.Count(raw, `"deliveryAddress"`); n != 1 {
		t.Errorf("export %s has %d places of delivery, want the issued one's only", raw, n)
	}
	if n := strings.Count(raw, `"buyer"`); n != 1 {
		t.Errorf("export %s has %d buyers, want the issued one's only: a draft has no snapshot", raw, n)
	}

	// A business's snapshot carries what a person's does not, and a credit
	// note names the invoice it credits.
	original := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	creditDraft(t, h, original.ID)
	raw = export(customerAcme)
	for _, want := range []string{
		`"organisationNumber":"923609016"`, `"peppolId":"0192:923609016"`, `"gln":"7080000000001"`,
		`"drafts":[{"kind":"credit_note","credits":{"number":2,"issueDate":"2026-09-12"}`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("export %s has no %s", raw, want)
		}
	}
}

// The erase (D10): the person's drafts deleted and counted as
// invoices.drafts, the issued documents kept and reported at 0, a second run
// zeros; a rolled-back anonymisation keeps the drafts.
func TestCustomerPersonalData_EraseDeletesDraftsAndKeepsDocuments(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	doc := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25))).ID)
	createDraft(t, h, draftBody(customerPerson, line("B", 1, 100, vat25)))
	createDraft(t, h, draftBody(customerPerson, line("C", 1, 100, vat25)))
	credit := creditDraft(t, h, doc.ID)
	createDraft(t, h, draftBody(customerAcme, line("D", 1, 100, vat25)))
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	erase := func(commit bool) []contracts.ErasedData {
		var erased []contracts.ErasedData
		inTx(t, h, commit, func(tx pgx.Tx) {
			var err error
			if erased, err = data.EraseCustomerData(context.Background(), tx, customerPerson); err != nil {
				t.Fatalf("EraseCustomerData: %v", err)
			}
		})
		return erased
	}

	erase(false)
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE customer_id = $1 AND status = 'draft'`, customerPerson); n != 3 {
		t.Fatalf("drafts after a rolled-back erase = %d, want 3", n)
	}
	if got, want := erase(true), []contracts.ErasedData{{Kind: "invoices.drafts", Count: 3}, {Kind: "invoices.documents", Count: 0}}; !slices.Equal(got, want) {
		t.Errorf("erased = %+v, want %+v", got, want)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE id = $1`, credit.ID); n != 0 {
		t.Error("a credit-note draft survived: a draft of either kind is erased")
	}
	if kept := getInvoice(t, h, doc.ID); kept.Buyer.Name != "Kari Nordmann" {
		t.Errorf("the issued document = %+v, want kept with its snapshot", kept.Buyer)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.invoices WHERE customer_id = $1`, customerAcme); n != 1 {
		t.Error("another customer's draft was erased")
	}
	if got, want := erase(true), []contracts.ErasedData{{Kind: "invoices.drafts", Count: 0}, {Kind: "invoices.documents", Count: 0}}; !slices.Equal(got, want) {
		t.Errorf("a second erase = %+v, want zeros", got)
	}
}
