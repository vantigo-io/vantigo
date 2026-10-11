package invoices_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
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
	if want := []contracts.RepointedReferences{
		{Kind: "invoices.invoices", Count: 2}, {Kind: "invoices.customerReminderPolicies", Count: 0},
	}; !slices.Equal(moved, want) {
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
		if err != nil || !slices.Equal(same, []contracts.RepointedReferences{
			{Kind: "invoices.invoices", Count: 0}, {Kind: "invoices.customerReminderPolicies", Count: 0},
		}) {
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
	restore := invoices.SetIssueAfterAllocation(func(_ context.Context, _ pgx.Tx, id int64) error {
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
		// issue goes on to lock the original. The handler's goroutine:
		// t.Errorf, never a t.Fatal.
		if err := awaitLockWaiter(h); err != nil {
			t.Errorf("the merge: %v", err)
			return err
		}
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
// snapshot, a place of delivery when one is set, and empty payments,
// deliveries and transmissions when it has none; and a credit note naming
// the invoice it credits.
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
		`"payments":[],"deliveries":[],"transmissions":[],"chargePayments":[],"chargeWaivers":[],"manualDeliveries":[],` +
			`"reminders":[],"holds":[],"collectionHandoffs":[]}`,
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
	if got, want := erase(true), eraseReport(3, 0, 0, 0); !slices.Equal(got, want) {
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
	if got, want := erase(true), eraseReport(0, 0, 0, 0); !slices.Equal(got, want) {
		t.Errorf("a second erase = %+v, want zeros", got)
	}
}

// The export carries each issued document's payments and deliveries
// (payments and delivery design D6): every registration, a removed one with
// its removal, the amounts as exact decimal text, and every send's
// recipient, time and subject. A draft has neither.
func TestCustomerPersonalData_ExportCarriesPaymentsAndDeliveries(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	doc := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Konsultasjon", 1, 1000, vat25))).ID)
	createDraft(t, h, draftBody(customerPerson, line("Utkast", 1, 100, vat25)))
	registered(t, h, doc.ID, map[string]any{"amount": 300.1, "paidOn": "2026-09-12", "reference": "KID 0012345", "note": "Delbetaling"})
	h.Advance(time.Hour)
	mistake := registered(t, h, doc.ID, pay(0.5, "2026-09-12")).Payments[1].ID
	h.Advance(time.Hour)
	if res := payer(t, h).Do(http.MethodPost, removalPath(doc.ID, mistake), map[string]any{"reason": "Feil beløp"}); res.Status != http.StatusOK {
		t.Fatalf("the removal = %d %s, want 200", res.Status, res.Body)
	}
	subject, _ := json.Marshal(sent(t, h, doc.ID, nil).Deliveries[0].Subject)

	section, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).ExportCustomerData(context.Background(), customerPerson)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	b, _ := json.Marshal(section)
	raw := string(b)
	for _, want := range []string{
		`"payments":[` +
			`{"paidOn":"2026-09-12","amount":"300.10","currency":"NOK","source":"manual","reference":"KID 0012345","note":"Delbetaling","registeredAt":"2026-09-12T12:00:00Z"},` +
			`{"paidOn":"2026-09-12","amount":"0.50","currency":"NOK","source":"manual","registeredAt":"2026-09-12T13:00:00Z",` +
			`"removedAt":"2026-09-12T14:00:00Z","removalReason":"Feil beløp"}]`,
		`"deliveries":[{"recipient":"kari@example.org","sentAt":"2026-09-12T14:00:00Z","subject":` + string(subject) + `}]`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("export %s has no %s", raw, want)
		}
	}
	for _, key := range []string{`"payments"`, `"deliveries"`} {
		if n := strings.Count(raw, key); n != 1 {
			t.Errorf("export %s has %d %s, want the issued document's only: a draft has neither", raw, n, key)
		}
	}
}

// The erase (D6) locks the person's documents, writes the marker, blanks
// every one of their deliveries' recipients and every one of their payments'
// notes — live and removed — and deletes their drafts, reporting the four
// kinds in order, invoices.payments as the notes blanked; the payments are
// kept otherwise, a bank reference naming the payer included. Another
// customer's deliveries keep their address and its payments their note. Run
// twice it finds nothing, and the marker keeps its first time. With the
// module disabled: the pool and the clock are all it needs.
func TestCustomerPersonalData_EraseBlanksDeliveriesAndReportsFiveKinds(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	doc := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Konsultasjon", 1, 1000, vat25))).ID)
	createDraft(t, h, draftBody(customerPerson, line("Utkast", 1, 100, vat25)))
	registered(t, h, doc.ID, map[string]any{"amount": 300, "paidOn": "2026-09-12", "reference": "Fra Kari Nordmann", "note": "Ringte"})
	registered(t, h, doc.ID, map[string]any{"amount": 100, "paidOn": "2026-09-12"})
	removed := registered(t, h, doc.ID, map[string]any{"amount": 50, "paidOn": "2026-09-12", "note": "Kari sa det var feil"}).Payments
	removedID := removed[len(removed)-1].ID
	if res := payer(t, h).Do(http.MethodPost, removalPath(doc.ID, removedID), map[string]any{"reason": "Feil beløp"}); res.Status != http.StatusOK {
		t.Fatalf("remove payment %d = %d %s", removedID, res.Status, res.Body)
	}
	sent(t, h, doc.ID, nil)
	sent(t, h, doc.ID, map[string]any{"recipient": "kari.privat@example.org"})
	acme := issuedAcme(t, h)
	sent(t, h, acme.ID, nil)
	registered(t, h, acme.ID, map[string]any{"amount": 100, "paidOn": "2026-09-12", "note": "Acme ringte"})
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	h.Advance(time.Hour)
	erasedAt := h.Now()
	erase := func() []contracts.ErasedData {
		var erased []contracts.ErasedData
		inTx(t, h, true, func(tx pgx.Tx) {
			var err error
			if erased, err = data.EraseCustomerData(context.Background(), tx, customerPerson); err != nil {
				t.Fatalf("EraseCustomerData: %v", err)
			}
		})
		return erased
	}

	if got, want := erase(), eraseReport(1, 2, 2, 0); !slices.Equal(got, want) {
		t.Errorf("erased = %+v, want %+v", got, want)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.deliveries WHERE invoice_id = $1 AND recipient = ''`, doc.ID); n != 2 {
		t.Errorf("%d of the person's two deliveries blanked, want both", n)
	}
	if kept := readAs(t, sender(t, h), doc.ID); len(kept.Deliveries) != 2 || addressOf(kept.Deliveries[0]) != "" || kept.Deliveries[0].Subject == "" {
		t.Errorf("the deliveries = %+v, want both kept, the subject too, with the address gone", kept.Deliveries)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT recipient FROM invoices.deliveries WHERE invoice_id = $1`, acme.ID); got != "faktura@acme.example" {
		t.Errorf("another customer's delivery = %q, want its address kept", got)
	}
	payments := `SELECT string_agg(reference || '|' || note || '|' || amount::text || '|' || (removed_at IS NULL)::text, ' ' ORDER BY id)
		FROM invoices.payments WHERE invoice_id = $1`
	if got := modtest.One[string](t, h.Harness, payments, doc.ID); got != "Fra Kari Nordmann||300.00|true ||100.00|true ||50.00|false" {
		t.Errorf("the payments = %s, want all three kept, the bank's reference too, with the notes gone", got)
	}
	if got := modtest.One[string](t, h.Harness, payments, acme.ID); got != "|Acme ringte|100.00|true" {
		t.Errorf("another customer's payment = %s, want its note kept", got)
	}
	marker := `SELECT erased_at FROM invoices.erased_customers WHERE customer_id = $1`
	if at := modtest.One[time.Time](t, h.Harness, marker, customerPerson); !at.Equal(erasedAt) {
		t.Errorf("the marker = %s, want the clock's %s", at, erasedAt)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.erased_customers WHERE customer_id = $1`, customerAcme); n != 0 {
		t.Error("another customer was marked erased")
	}

	h.Advance(time.Hour)
	if got, want := erase(), eraseReport(0, 0, 0, 0); !slices.Equal(got, want) {
		t.Errorf("a second erase = %+v, want zeros", got)
	}
	if at := modtest.One[time.Time](t, h.Harness, marker, customerPerson); !at.Equal(erasedAt) {
		t.Errorf("the marker after a second erase = %s, want its first time %s", at, erasedAt)
	}
}

// The race the marker closes (D6): a send reads the person's address while
// an anonymisation runs and has not committed — so the send is not refused —
// mails it, and writes its row while the erase still holds the person's
// documents. The row's insert waits on the document; once the erase commits,
// the trigger's check, made after the wait, sees the marker and the row keeps
// no address. A second send is refused customer_anonymised. Not parallel:
// the delivery hook is the package's.
func TestCustomerPersonalData_ASendRacingAnUncommittedEraseLeavesNoAddress(t *testing.T) {
	h, fake := sendReady(t)
	inv := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Konsultasjon", 1, 1000, vat25))).ID)
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	c := sender(t, h)
	ctx := context.Background()

	e, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = e.Rollback(ctx) }()
	if _, err := data.EraseCustomerData(ctx, e, customerPerson); err != nil {
		t.Fatalf("EraseCustomerData: %v", err)
	}

	reached, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseSend := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseSend()
	restore := invoices.SetBeforeDeliveryWrite(func(_ context.Context, id int64) {
		if id != inv.ID {
			return
		}
		close(reached)
		<-release
	})
	defer restore()

	answered := make(chan *modtest.Response, 1)
	go func() { answered <- sendAs(c, inv.ID, nil) }()
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the send never reached its row")
	}
	if mails := fake.mails(); len(mails) != 1 || mails[0].out.To[0] != "kari@example.org" {
		t.Fatalf("mails = %+v, want the one to Kari: the uncommitted erase refuses nothing", mails)
	}
	releaseSend()
	if err := awaitDeliveryInsertWaiting(h); err != nil {
		t.Fatalf("the send's row: %v", err)
	}
	if err := e.Commit(ctx); err != nil {
		t.Fatalf("commit the erase: %v", err)
	}

	var res *modtest.Response
	select {
	case res = <-answered:
	case <-time.After(20 * time.Second):
		t.Fatal("the send never answered")
	}
	if res.Status != http.StatusOK {
		t.Fatalf("the send = %d %s, want 200: the mail went", res.Status, res.Body)
	}
	var got invoiceJSON
	res.JSON(&got)
	if len(got.Deliveries) != 1 || addressOf(got.Deliveries[0]) != "" {
		t.Errorf("the send's deliveries = %+v, want one with no address", got.Deliveries)
	}
	if row := modtest.One[string](t, h.Harness, `SELECT recipient FROM invoices.deliveries WHERE invoice_id = $1`, inv.ID); row != "" {
		t.Errorf("the delivery row's recipient = %q, want '' — the erase has run past it", row)
	}
	restore()
	sendRefused(t, "a second send", sendAs(c, inv.ID, nil), http.StatusConflict, "customer_anonymised")
}

// The marker committed between a send's directory read and its row (D4 step
// 8, D6): the send was not refused — the customer was not anonymised when it
// looked — and the mail went, but the erase has run past the deliveries by
// the time the row is written, so the row's trigger blanks the address. The
// send still answers 200: the mail went. Not parallel: the delivery hook is
// the package's.
func TestCustomerPersonalData_ASendWhoseCustomerIsErasedBeforeItsRowLeavesNoAddress(t *testing.T) {
	h, fake := sendReady(t)
	inv := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Konsultasjon", 1, 1000, vat25))).ID)
	marked := make(chan error, 1)
	restore := invoices.SetBeforeDeliveryWrite(func(ctx context.Context, id int64) {
		if id != inv.ID {
			return
		}
		_, err := h.Pool().Exec(ctx, `INSERT INTO invoices.erased_customers (customer_id, erased_at) VALUES ($1, now())`, customerPerson)
		marked <- err
	})
	defer restore()

	res := sendAs(sender(t, h), inv.ID, nil)
	restore()
	select {
	case err := <-marked:
		if err != nil {
			t.Fatalf("mark the customer erased: %v", err)
		}
	default:
		t.Fatal("the send never reached its row")
	}
	if res.Status != http.StatusOK {
		t.Fatalf("the send = %d %s, want 200: the mail went", res.Status, res.Body)
	}
	if mails := fake.mails(); len(mails) != 1 || mails[0].out.To[0] != "kari@example.org" {
		t.Errorf("mails = %+v, want the one to Kari", mails)
	}
	var got invoiceJSON
	res.JSON(&got)
	if len(got.Deliveries) != 1 || addressOf(got.Deliveries[0]) != "" {
		t.Errorf("the send's deliveries = %+v, want one with no address", got.Deliveries)
	}
	if row := modtest.One[string](t, h.Harness, `SELECT recipient FROM invoices.deliveries WHERE invoice_id = $1`, inv.ID); row != "" {
		t.Errorf("the delivery row's recipient = %q, want '' — the marker was committed before it", row)
	}
}

// awaitDeliveryInsertWaiting polls until a delivery's INSERT in h's database
// is waiting on a lock — its trigger's FOR SHARE on a document an erase
// holds.
func awaitDeliveryInsertWaiting(h *harness) error {
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := h.Pool().QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%INSERT INTO invoices.deliveries%'`).Scan(&n); err != nil {
			return fmt.Errorf("read the lock waiters: %w", err)
		}
		if n > 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("no delivery insert ever waited on a lock")
}

// exportedTransmissionJSON is one transmission as the export writes it.
type exportedTransmissionJSON struct {
	ID                  int64   `json:"id"`
	DocumentType        string  `json:"documentType"`
	Status              string  `json:"status"`
	Provider            string  `json:"provider"`
	IdempotencyKey      string  `json:"idempotencyKey"`
	ReceiverParticipant string  `json:"receiverParticipant"`
	UblSha256           string  `json:"ublSha256"`
	QueuedAt            string  `json:"queuedAt"`
	SubmittedAt         *string `json:"submittedAt"`
	DeliveredAt         *string `json:"deliveredAt"`
	FailedAt            *string `json:"failedAt"`
	CancelledAt         *string `json:"cancelledAt"`
	ResolutionNote      *string `json:"resolutionNote"`
	Reason              *string `json:"reason"`
}

// The export (EHF and KID design D12) carries every transmission of each
// issued document, the oldest first — the provider, the state and its
// times, the receiver, the idempotency key and the UBL's hash, a person's
// resolution — and the reason only as the wire answers it, redacted: no
// e-mail address or participant id from the provider's words, and no bytes.
// An issued document never sent has none; a draft has no key.
func TestCustomerSlots_ExportCarriesTransmissions(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	doc := issuedAcme(t, h)
	first := sentAsEhf(t, h, doc.ID).Ehf.Transmissions[0]
	h.Advance(time.Hour)
	failedAt := h.Now()
	h.Exec(t, `UPDATE invoices.transmissions SET status = 'failed', failed_at = $2,
		last_error = 'Refused by 0192:923609016, ask ola@acme.example', resolution_note = 'Avvist av mottaker', resolved_by_user_id = $3
		WHERE id = $1`, first.ID, failedAt, uuid.New())
	h.Advance(time.Hour)
	second := sentAsEhf(t, h, doc.ID).Ehf.Transmissions[0]
	never := issuedAcme(t, h)
	createDraft(t, h, draftBody(customerAcme, line("Utkast", 1, 100, vat25)))

	section, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).ExportCustomerData(context.Background(), customerAcme)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	raw, _ := json.Marshal(section)
	var file struct {
		Documents []struct {
			Number        int64                       `json:"number"`
			Transmissions *[]exportedTransmissionJSON `json:"transmissions"`
		} `json:"documents"`
		Drafts []map[string]json.RawMessage `json:"drafts"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("export %s: %v", raw, err)
	}
	if len(file.Documents) != 2 || len(file.Drafts) != 1 {
		t.Fatalf("export %s: want two documents and a draft", raw)
	}
	byNumber := map[int64]*[]exportedTransmissionJSON{}
	for _, d := range file.Documents {
		byNumber[d.Number] = d.Transmissions
	}
	if got := byNumber[*never.Number]; got == nil || len(*got) != 0 {
		t.Errorf("the document never sent = %v, want transmissions: []", got)
	}
	got := byNumber[*doc.Number]
	if got == nil || len(*got) != 2 {
		t.Fatalf("export %s: want the sent document's two transmissions", raw)
	}
	failed, queued := (*got)[0], (*got)[1]
	queuedAt, err := time.Parse(time.RFC3339Nano, first.QueuedAt)
	if err != nil {
		t.Fatalf("the wire's queuedAt %q: %v", first.QueuedAt, err)
	}
	if failed.ID != first.ID || failed.Status != "failed" || failed.DocumentType != txRow(t, h, first.ID).DocumentType || failed.Provider != "storecove" ||
		failed.IdempotencyKey != first.IdempotencyKey || failed.ReceiverParticipant != "0192:923609016" ||
		failed.UblSha256 != first.UblSha256 || !sameInstant(t, failed.QueuedAt, queuedAt) ||
		failed.FailedAt == nil || !sameInstant(t, *failed.FailedAt, failedAt) ||
		failed.ResolutionNote == nil || *failed.ResolutionNote != "Avvist av mottaker" {
		t.Errorf("the first transmission = %+v, want transmission %d failed at %s with its resolution, as the wire answered it %+v",
			failed, first.ID, failedAt, first)
	}
	if failed.Reason == nil || *failed.Reason != "Refused by <participant>, ask <e-mail>" {
		t.Errorf("the reason = %v, want the provider's words redacted", failed.Reason)
	}
	if queued.ID != second.ID || queued.Status != "queued" || queued.SubmittedAt != nil || queued.FailedAt != nil ||
		queued.CancelledAt != nil || queued.DeliveredAt != nil || queued.ResolutionNote != nil || queued.Reason != nil {
		t.Errorf("the second transmission = %+v, want transmission %d queued and nothing more", queued, second.ID)
	}
	for _, leak := range []string{"ola@acme.example", "Refused by 0192", "ubl_object_key", "documents/", "providerRef", "lease"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("export %s carries %q", raw, leak)
		}
	}
	if _, ok := file.Drafts[0]["transmissions"]; ok {
		t.Errorf("the draft = %v, want no transmissions key", file.Drafts[0])
	}
}

// The erase (D12) cancels every queued transmission that was never
// attempted — one a worker holds a lease on too, since the worker stamps its
// marker only on a row still queued (TestEhfWorker_ACancelledClaimMakesNoPost)
// — and reports them as invoices.transmissions; every other row is the sales
// record and is kept untouched: a queued one whose crash marker is set (its
// bytes may have reached the provider), and every submitted, delivered,
// failed and unconfirmed one; another customer's queued row too. Run twice it
// reports zero.
func TestCustomerSlots_EraseCancelsOnlyNeverAttempted(t *testing.T) {
	t.Parallel()
	h, _ := ehfReady(t)
	doc := func() int64 { return issuedAcme(t, h).ID }
	never := insertTransmission(t, h, doc())
	attempted := insertTransmission(t, h, doc())
	h.Exec(t, `UPDATE invoices.transmissions SET submit_attempted_at = $2 WHERE id = $1`, attempted, h.Now())
	leased := insertTransmission(t, h, doc())
	// The lease outlives both erases below: it does not save the row.
	h.Exec(t, `UPDATE invoices.transmissions SET lease_id = 'worker', lease_until = $2 WHERE id = $1`, leased, h.Now().Add(3*time.Hour))
	kept := map[int64]string{attempted: "queued"}
	shared := doc()
	kept[plantTransmissionOn(t, h, shared, "failed")] = "failed"
	kept[plantTransmissionOn(t, h, shared, "unconfirmed")] = "unconfirmed"
	for _, status := range []string{"submitted", "delivered"} {
		kept[plantTransmissionOn(t, h, doc(), status)] = status
	}
	other := insertTransmission(t, h, issued(t, h, createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25))).ID).ID)
	kept[other] = "queued"
	snapshot := `SELECT to_jsonb(t)::text FROM invoices.transmissions t WHERE id = $1`
	before := map[int64]string{}
	for id := range kept {
		before[id] = modtest.One[string](t, h.Harness, snapshot, id)
	}
	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	h.Advance(time.Hour)
	erasedAt := h.Now()
	erase := func() []contracts.ErasedData {
		var erased []contracts.ErasedData
		inTx(t, h, true, func(tx pgx.Tx) {
			var err error
			if erased, err = data.EraseCustomerData(context.Background(), tx, customerAcme); err != nil {
				t.Fatalf("EraseCustomerData: %v", err)
			}
		})
		return erased
	}
	transmissions := func(erased []contracts.ErasedData) []contracts.ErasedData {
		return slices.DeleteFunc(slices.Clone(erased), func(e contracts.ErasedData) bool { return e.Kind != "invoices.transmissions" })
	}

	if got, want := transmissions(erase()), []contracts.ErasedData{{Kind: "invoices.transmissions", Count: 2}}; !slices.Equal(got, want) {
		t.Errorf("erased = %+v, want %+v", got, want)
	}
	for what, id := range map[string]int64{"never-attempted": never, "leased never-attempted": leased} {
		if row := txRow(t, h, id); row.Status != "cancelled" || row.CancelledAt == nil || !row.CancelledAt.Equal(erasedAt) {
			t.Errorf("the %s row = %s cancelled at %v, want cancelled at the clock's %s", what, row.Status, row.CancelledAt, erasedAt)
		}
	}
	for id, status := range kept {
		if after := modtest.One[string](t, h.Harness, snapshot, id); after != before[id] {
			t.Errorf("the %s transmission %d changed: %s, want untouched %s", status, id, after, before[id])
		}
	}
	h.Advance(time.Hour)
	if got, want := transmissions(erase()), []contracts.ErasedData{{Kind: "invoices.transmissions", Count: 0}}; !slices.Equal(got, want) {
		t.Errorf("a second erase = %+v, want %+v", got, want)
	}
	if row := txRow(t, h, never); row.CancelledAt == nil || !row.CancelledAt.Equal(erasedAt) {
		t.Errorf("after a second erase the cancelled row's time = %v, want its first %s", row.CancelledAt, erasedAt)
	}
}

// The erase takes a draft's holds and its timesheet with it — the cascade
// from the deleted draft (invoices work design D2, D5) — and keeps an issued
// document's: the live index (ux_line_sources_live) is free afterwards, so
// the draft's work is selectable again, while the issued invoice's work stays
// invoiced. Neither holder is called: deleting a draft releases nothing a
// source module stamped.
func TestErase_ADraftsHoldsAndTimesheetGoWithIt(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	f := newWorkFixture(t, holders.options()...)
	h := f.h
	inv := issued(t, h, tsFromWork(t, h, withSheet, workSrc("time.entry", workHour42, 1)).ID)
	draft := tsFromWork(t, h, withSheet, allSeptember()...)
	if n := h.Count(t, `SELECT count(*) FROM invoices.line_sources WHERE invoice_id = $1`, draft.ID); n != 5 || len(draft.TimesheetRows) != 2 {
		t.Fatalf("the draft holds %d sources and %d timesheet rows, want 5 and 2", n, len(draft.TimesheetRows))
	}
	calls := len(holders.calledOrder())

	data := invoices.Module().CustomerPersonalData(disabledDeps(h))
	inTx(t, h, true, func(tx pgx.Tx) {
		if _, err := data.EraseCustomerData(context.Background(), tx, customerAcme); err != nil {
			t.Fatalf("EraseCustomerData: %v", err)
		}
	})
	for table, want := range map[string]int{"line_sources": 0, "timesheet_rows": 0} {
		if n := h.Count(t, `SELECT count(*) FROM invoices.`+table+` WHERE invoice_id = $1`, draft.ID); n != want {
			t.Errorf("the draft's %s = %d rows after the erase, want %d", table, n, want)
		}
	}
	if got := modtest.One[[]string](t, h.Harness, `SELECT array_agg(state ORDER BY id) FROM invoices.line_sources WHERE invoice_id = $1`, inv.ID); !slices.Equal(got, []string{"invoiced"}) {
		t.Errorf("the issued invoice's sources = %v after the erase, want its one invoiced row kept", got)
	}
	if got := storedRows(t, h, inv.ID); len(got) != 1 {
		t.Errorf("the issued invoice's timesheet = %v after the erase, want its row kept", got)
	}
	if n := len(holders.calledOrder()); n != calls {
		t.Errorf("the erase called a holder %d times, want none", n-calls)
	}

	again := tsFromWork(t, h, withSheet, allSeptember()...)
	if n := h.Count(t, `SELECT count(*) FROM invoices.line_sources WHERE invoice_id = $1 AND state = 'held'`, again.ID); n != 5 {
		t.Errorf("the work pulled again holds %d sources, want all 5: the live index still held the erased draft's", n)
	}
	refusedFromWork(t, h, fromWorkBody(customerAcme, workSrc("time.entry", workHour42, 1)), "source_held_elsewhere")
}

// The export names each document's project as it printed it — its
// projectReference, the code the document snapshotted (D9) — beside its
// timesheet (D5): an issued invoice of one project's work and a draft of
// another's each carry their own — the issued one the code it printed, not the
// project's code since — and a draft spanning two projects and one made by
// hand carry neither key.
func TestExport_TheProjectAndTheTimesheet(t *testing.T) {
	t.Parallel()
	f := newWorkFixture(t, newFakeHolders().options()...)
	h := f.h
	issuedOne := issued(t, h, tsFromWork(t, h, withSheet, project41Work()...).ID)
	tsFromWork(t, h, withSheet, workSrc("time.entry", workHour42, 1))
	f.billable.putExpense(contracts.BillableExpense{ID: 903, Revision: 1, ProjectID: project42, Kind: "outlay", Date: wDay("2026-09-04"),
		Description: "Ferje", NetAmount: "300.00", BillAmount: "300.00", Currency: "NOK"})
	tsFromWork(t, h, nil, workSrc("expenses.entry", workMileage, 1), workSrc("expenses.entry", 903, 1))
	f.projects.edit(project41, func(p *contracts.ProjectEntry) { p.Code = "P-41-NY" })
	createDraft(t, h, draftBody(customerAcme, line("For hånd", 1, 100, vat25)))

	type exportedJSON struct {
		Number           *int64          `json:"number"`
		ProjectReference *string         `json:"projectReference"`
		Timesheet        json.RawMessage `json:"timesheet"`
	}
	var section struct {
		Documents []exportedJSON `json:"documents"`
		Drafts    []exportedJSON `json:"drafts"`
	}
	raw := exportOf(t, h, customerAcme)
	if err := json.Unmarshal([]byte(raw), &section); err != nil {
		t.Fatalf("export %s: %v", raw, err)
	}
	str := func(s *string) string {
		if s == nil {
			return "<absent>"
		}
		return *s
	}
	if len(section.Documents) != 1 || section.Documents[0].Number == nil || *section.Documents[0].Number != *issuedOne.Number ||
		str(section.Documents[0].ProjectReference) != "P-41" {
		t.Fatalf("documents = %s, want invoice %d naming the code it printed, P-41", raw, *issuedOne.Number)
	}
	if want := `[{"position":1,"personLabel":"KN","date":"2026-09-01","hours":"4.00","description":"Project 41"},` +
		`{"position":2,"personLabel":"OH","date":"2026-09-02","hours":"3.50","description":"Project 41"}]`; string(section.Documents[0].Timesheet) != want {
		t.Errorf("the issued invoice's timesheet = %s, want %s", section.Documents[0].Timesheet, want)
	}
	drafts := []string{}
	for _, d := range section.Drafts {
		drafts = append(drafts, str(d.ProjectReference)+" "+string(d.Timesheet))
	}
	slices.Sort(drafts)
	if want := []string{"<absent> ", "<absent> ",
		`P-42 [{"position":1,"personLabel":"KN","date":"2026-09-03","hours":"2.00","description":"Project 42"}]`,
	}; !slices.Equal(drafts, want) {
		t.Errorf("drafts = %q, want %q: project 42's naming it with its timesheet, the two-project one and the one by hand neither", drafts, want)
	}
	if n := strings.Count(raw, `"projectReference"`); n != 2 {
		t.Errorf("export %s names %d projects, want the issued invoice's and project 42's draft's", raw, n)
	}
}

// eraseReport is the erase's report in its order (D6; invoices payments and
// reminders design D19): the drafts, the documents at 0, the payment notes,
// the deliveries and the transmissions as given, then the receivables' eight
// kinds, each 0 unless more names it.
func eraseReport(drafts, payments, deliveries, transmissions int64, more ...contracts.ErasedData) []contracts.ErasedData {
	out := []contracts.ErasedData{
		{Kind: "invoices.drafts", Count: drafts}, {Kind: "invoices.documents", Count: 0},
		{Kind: "invoices.payments", Count: payments}, {Kind: "invoices.deliveries", Count: deliveries},
		{Kind: "invoices.transmissions", Count: transmissions},
		{Kind: "invoices.reminders"}, {Kind: "invoices.chargePayments"}, {Kind: "invoices.chargeWaivers"},
		{Kind: "invoices.manualDeliveries"}, {Kind: "invoices.invoiceHolds"}, {Kind: "invoices.collectionHandoffs"},
		{Kind: "invoices.bankTransactions"}, {Kind: "invoices.customerReminderPolicies"},
	}
	for _, m := range more {
		for i := range out {
			if out[i].Kind == m.Kind {
				out[i].Count = m.Count
			}
		}
	}
	return out
}

// receivables is one of every receivable planted on an issued invoice of a
// customer (D19), each with a note or an address, at the harness's clock:
// its letters in every status the erase treats apart, and the bank lines its
// money came from.
type receivables struct {
	invoice                                                                  int64
	queued, withdrawnEarlier, sentLetter, printed, awaiting, failed, sending int64
	resolvedLine, eventOnlyLine, matchedLine, chargeLine                     int64
}

// plantReceivables plants receivables on a new issued invoice of customer,
// number number, its letters to recipient.
func plantReceivables(t *testing.T, h *harness, customer int32, number int64, recipient string) receivables {
	t.Helper()
	at := h.Now()
	r := receivables{invoice: plantOverdue(t, h, overdueSpec{number: number, customer: customer, issue: "2026-07-01", due: "2026-08-03"})}
	run := plantRun(t, h)
	letter := func(sequence int, status, channel, to, extraCols, extraVals string, args ...any) int64 {
		return plantID(t, h, `
			INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, recipient, language, created_at,
			    created_by_user_id, status`+extraCols+`)
			VALUES ($1, $2, $3, 'reminder', $4, $5, 'nb', $6, gen_random_uuid(), $7`+extraVals+`)
			RETURNING id`, append([]any{r.invoice, run, sequence, channel, to, at, status}, args...)...)
	}
	facts := `, sent_on, deadline, regime, principal_open, credited, fee_kind, fee, charges_earlier, interest,
		interest_waived, interest_paid, total`
	factValues := `, DATE '2026-08-20', DATE '2026-09-03', 'inkassolov_1988', 1000, 0, 'reminder_fee', 35, 0, 0, 0, 0, 1035`
	r.queued = letter(1, "queued", "email", recipient, "", "")
	r.withdrawnEarlier = letter(2, "withdrawn", "email", recipient, ", withdrawn_at, withdrawal_reason", ", $6, 'on_hold'")
	r.sentLetter = letter(3, "sent", "email", recipient, facts+", sent_at, message_id, pdf_object_key, pdf_sha256",
		factValues+", $6, 'reminder-secret-message@vantigo.invalid', 'reminders/secret-object-key.pdf', repeat('f', 64)")
	batch := plantID(t, h, `INSERT INTO invoices.reminder_print_batches (post_on, created_at, created_by_user_id)
		VALUES (DATE '2026-09-14', $1, gen_random_uuid()) RETURNING id`, at)
	r.printed = letter(4, "printed", "paper", "", facts+", print_batch_id", factValues+", $8", batch)
	r.awaiting = letter(5, "awaiting_print", "paper", "", "", "")
	r.failed = letter(6, "failed", "email", recipient, ", failed_at, last_error", ", $6, '554 secret mailbox full'")
	r.sending = letter(7, "queued", "email", recipient, facts+", lease_id, lease_until", factValues+", 'lease-1', $6::timestamptz + interval '2 hours'")

	file := plantID(t, h, `
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
		    ignored, ignored_kinds, first_booked_on, last_booked_on, uploaded_by_user_id, uploaded_at)
		VALUES ('camt054', md5(random()::text) || md5(random()::text), md5(random()::text), 'bank-files/e.xml', 400,
		    ARRAY['15032080119'], 4, 0, '{}', DATE '2026-08-10', DATE '2026-08-10', gen_random_uuid(), $1) RETURNING id`, at)
	line := func(status, note string) int64 {
		resolution := map[string]any{"resolved": "applied", "matched": nil}[status]
		return plantID(t, h, `
			INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on, amount, currency,
			    debtor_name, debtor_account, remittance_text, fingerprint, ordinal, status, resolution, resolved_by_user_id,
			    resolved_at, resolution_note)
			VALUES ($1, md5(random()::text), 'camt054', '15032080119', 'credit', DATE '2026-08-10', 500, 'NOK', 'Kari Nordmann',
			    '12345678903', 'Faktura 1', md5(random()::text) || md5(random()::text), 1, $2, $3::text,
			    CASE WHEN $3::text IS NULL THEN NULL ELSE gen_random_uuid() END, CASE WHEN $3::text IS NULL THEN NULL ELSE $4::timestamptz END, $5)
			RETURNING id`, file, status, resolution, at, note)
	}
	event := func(lineID int64, event, note string) {
		h.Exec(t, `INSERT INTO invoices.bank_transaction_events (bank_transaction_id, event, note, by_user_id, at)
			VALUES ($1, $2, $3, gen_random_uuid(), $4)`, lineID, event, note, at)
	}
	payment := func(lineID int64, removed bool) {
		h.Exec(t, `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, source, bank_transaction_id,
			    registered_by_user_id, registered_at, removed_at, removed_by_user_id, removal_reason)
			VALUES ($1, DATE '2026-08-10', 500, 'NOK', 'camt054', $2, gen_random_uuid(), $3,
			    CASE WHEN $4 THEN $3::timestamptz END, CASE WHEN $4 THEN gen_random_uuid() END, CASE WHEN $4 THEN 'Reversert' END)`,
			r.invoice, lineID, at, removed)
	}
	r.resolvedLine = line("resolved", "Kari ringte om innbetalingen")
	payment(r.resolvedLine, false)
	event(r.resolvedLine, "applied", "Kari ringte")
	r.eventOnlyLine = line("resolved", "")
	payment(r.eventOnlyLine, true)
	event(r.eventOnlyLine, "applied", "Avtalt med Kari")
	r.matchedLine = line("matched", "")
	payment(r.matchedLine, true)
	event(r.matchedLine, "reversed", "Banken tok den tilbake")
	r.chargeLine = line("resolved", "Gebyr fra Kari")

	h.Exec(t, `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount, currency, source, bank_transaction_id, note,
		    registered_by_user_id, registered_at, removed_at, removed_by_user_id, removal_reason)
		VALUES ($1, DATE '2026-09-01', 35, 'NOK', 'manual', NULL, 'Betalte gebyret', gen_random_uuid(), $2, NULL, NULL, NULL),
		       ($1, DATE '2026-09-01', 10, 'NOK', 'manual', NULL, 'Feil', gen_random_uuid(), $2, $2, gen_random_uuid(), 'Feil beløp'),
		       ($1, DATE '2026-08-10', 5, 'NOK', 'camt054', $3, '', gen_random_uuid(), $2, NULL, NULL, NULL)`,
		r.invoice, at, r.chargeLine)
	h.Exec(t, `INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount, reason, note, waived_by_user_id, waived_at)
		VALUES ($1, $2, 'fee', 35, 'goodwill', 'Kari klaget', gen_random_uuid(), $3)`, r.invoice, r.sentLetter, at)
	h.Exec(t, `INSERT INTO invoices.manual_deliveries (invoice_id, kind, delivered_on, note, recorded_by_user_id, recorded_at,
		    removed_at, removed_by_user_id, removal_reason)
		VALUES ($1, 'handed_over', DATE '2026-07-01', 'Levert i hånd', gen_random_uuid(), $2, NULL, NULL, NULL),
		       ($1, 'posted', DATE '2026-07-02', 'Feil dag', gen_random_uuid(), $2, $2, gen_random_uuid(), 'Feil dag')`, r.invoice, at)
	h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id, lifted_at,
		    lifted_by_user_id, lift_note, charges_allowed)
		VALUES ($1, 'disputed', 'Bestridt', $2, gen_random_uuid(), $2, gen_random_uuid(), 'Avklart', true),
		       ($1, 'disputed', 'Bestridt igjen', $2, gen_random_uuid(), NULL, NULL, NULL, NULL)`, r.invoice, at)
	h.Exec(t, `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, agency_reference, note, created_at,
		    created_by_user_id, withdrawn_on, withdrawn_by_user_id, withdrawal_reason)
		VALUES ($1, DATE '2026-09-10', 'Inkasso AS', 'K-1', 'Overlevert', $2, gen_random_uuid(), DATE '2026-09-11', gen_random_uuid(), 'Betalt'),
		       ($1, DATE '2026-09-11', 'Inkasso AS', 'K-2', 'Overlevert igjen', $2, gen_random_uuid(), NULL, NULL, NULL)`, r.invoice, at)
	return r
}

// TestExport_CarriesEveryReceivable: each issued document carries, beside
// its payments, its charge payments, waivers, manual deliveries, letters,
// holds and hand-offs, each with its note, dates, amounts as exact decimal
// text and a letter's recipient (D19); each payment its source, and an
// imported one the bank line's date, debtor name, debtor account and text.
func TestExport_CarriesEveryReceivable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantReceivables(t, h, customerPerson, 1, "kari@example.org")
	section, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).ExportCustomerData(context.Background(), customerPerson)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	b, _ := json.Marshal(section)
	raw := string(b)
	at := `"2026-09-12T12:00:00Z"`
	bankLine := `"bankLine":{"bookedOn":"2026-08-10","debtorName":"Kari Nordmann","debtorAccount":"12345678903","text":"Faktura 1"}`
	for _, want := range []string{
		`{"paidOn":"2026-08-10","amount":"500.00","currency":"NOK","source":"camt054","registeredAt":` + at + `,` + bankLine + `}`,
		`{"paidOn":"2026-08-10","amount":"500.00","currency":"NOK","source":"camt054","registeredAt":` + at + `,"removedAt":` + at +
			`,"removalReason":"Reversert",` + bankLine + `}`,
		`"chargePayments":[{"paidOn":"2026-08-10","amount":"5.00","currency":"NOK","source":"camt054","registeredAt":` + at + `,` +
			bankLine + `},` +
			`{"paidOn":"2026-09-01","amount":"35.00","currency":"NOK","source":"manual","note":"Betalte gebyret","registeredAt":` + at + `},` +
			`{"paidOn":"2026-09-01","amount":"10.00","currency":"NOK","source":"manual","note":"Feil","registeredAt":` + at +
			`,"removedAt":` + at + `,"removalReason":"Feil beløp"}]`,
		`"chargeWaivers":[{"reminderSequence":3,"kind":"fee","amount":"35.00","reason":"goodwill","note":"Kari klaget","waivedAt":` + at + `}]`,
		`"manualDeliveries":[{"kind":"handed_over","deliveredOn":"2026-07-01","note":"Levert i hånd","recordedAt":` + at + `},` +
			`{"kind":"posted","deliveredOn":"2026-07-02","note":"Feil dag","recordedAt":` + at + `,"removedAt":` + at + `,"removalReason":"Feil dag"}]`,
		`{"sequence":3,"level":"reminder","announcesCollection":false,"channel":"email","recipient":"kari@example.org","language":"nb",` +
			`"status":"sent","createdAt":` + at + `,"sentOn":"2026-08-20","deadline":"2026-09-03","regime":"inkassolov_1988",` +
			`"principalOpen":"1000.00","credited":"0.00","feeKind":"reminder_fee","fee":"35.00","chargesEarlier":"0.00","interest":"0.00",` +
			`"interestWaived":"0.00","interestPaid":"0.00","total":"1035.00","sentAt":` + at + `}`,
		`{"sequence":2,"level":"reminder","announcesCollection":false,"channel":"email","recipient":"kari@example.org","language":"nb",` +
			`"status":"withdrawn","createdAt":` + at + `,"withdrawnAt":` + at + `,"withdrawalReason":"on_hold"}`,
		`{"sequence":5,"level":"reminder","announcesCollection":false,"channel":"paper","recipient":"","language":"nb",` +
			`"status":"awaiting_print","createdAt":` + at + `}`,
		`"holds":[{"kind":"disputed","note":"Bestridt","placedAt":` + at + `,"liftedAt":` + at + `,"liftNote":"Avklart","chargesAllowed":true},` +
			`{"kind":"disputed","note":"Bestridt igjen","placedAt":` + at + `}]`,
		`"collectionHandoffs":[{"handedOn":"2026-09-10","agency":"Inkasso AS","agencyReference":"K-1","note":"Overlevert","createdAt":` + at +
			`,"withdrawnOn":"2026-09-11","withdrawalReason":"Betalt"},` +
			`{"handedOn":"2026-09-11","agency":"Inkasso AS","agencyReference":"K-2","note":"Overlevert igjen","createdAt":` + at + `}]`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("export %s has no %s", raw, want)
		}
	}
	if n := strings.Count(raw, `"sequence":`); n != 7 {
		t.Errorf("the export has %d letters, want all seven, in every status", n)
	}
	for _, kept := range []string{"secret-message", "secret-object-key", "ffffffff", "secret mailbox", "messageId", "pdf", "lastError"} {
		if strings.Contains(raw, kept) {
			t.Errorf("the export carries %q: a letter's Message-ID, PDF key and hash and SMTP error are never exported", kept)
		}
	}
}

// eraseOf erases customer with deps' slot in a transaction it commits.
func eraseOf(t *testing.T, h *harness, d module.Deps, customer int32) []contracts.ErasedData {
	t.Helper()
	var erased []contracts.ErasedData
	inTx(t, h, true, func(tx pgx.Tx) {
		var err error
		if erased, err = invoices.Module().CustomerPersonalData(d).EraseCustomerData(context.Background(), tx, customer); err != nil {
			t.Fatalf("EraseCustomerData: %v", err)
		}
	})
	return erased
}

// TestErase_DoesEachAndReportsEach: after today's steps and in D19's order,
// the erase withdraws every letter in flight customer_anonymised — queued,
// awaiting print, failed — but leaves the printed one to the posting and the
// one being sent to become sent, naming both in a warning; blanks every
// letter's recipient in every status, one withdrawn earlier by a hold and a
// sent one among them (B1); blanks the notes of the charge payments, the
// waivers, the manual deliveries, the holds and their lifts and the
// hand-offs; blanks the resolution note of the resolved bank lines its
// payments and charge payments came from, and their events' notes, but
// nothing of a line not resolved; and deletes the policy. Each kind reports
// what it changed. Another customer's are all kept.
func TestErase_DoesEachAndReportsEach(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kari := plantReceivables(t, h, customerPerson, 1, "kari@example.org")
	acme := plantReceivables(t, h, customerAcme, 2, "faktura@acme.example")
	plantPolicy(t, h, customerPerson, "none", "Kari er syk")
	var logged bytes.Buffer
	d := disabledDeps(h)
	d.Logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	h.Advance(time.Hour)

	if got, want := eraseOf(t, h, d, customerPerson), eraseReport(0, 0, 0, 0,
		contracts.ErasedData{Kind: "invoices.reminders", Count: 6}, contracts.ErasedData{Kind: "invoices.chargePayments", Count: 2},
		contracts.ErasedData{Kind: "invoices.chargeWaivers", Count: 1}, contracts.ErasedData{Kind: "invoices.manualDeliveries", Count: 2},
		contracts.ErasedData{Kind: "invoices.invoiceHolds", Count: 2}, contracts.ErasedData{Kind: "invoices.collectionHandoffs", Count: 2},
		contracts.ErasedData{Kind: "invoices.bankTransactions", Count: 3},
		contracts.ErasedData{Kind: "invoices.customerReminderPolicies", Count: 1},
	); !slices.Equal(got, want) {
		t.Errorf("erased = %+v, want %+v", got, want)
	}

	letters := `SELECT string_agg(sequence || ' ' || status || ' ' || coalesce(withdrawal_reason, '-') || ' ' ||
		CASE WHEN recipient = '' THEN 'blank' ELSE recipient END, ', ' ORDER BY sequence) FROM invoices.reminders WHERE invoice_id = $1`
	if got := modtest.One[string](t, h.Harness, letters, kari.invoice); got != "1 withdrawn customer_anonymised blank, "+
		"2 withdrawn on_hold blank, 3 sent - blank, 4 printed - blank, 5 withdrawn customer_anonymised blank, "+
		"6 withdrawn customer_anonymised blank, 7 queued - blank" {
		t.Errorf("the person's letters = %s; want those in flight withdrawn, the printed one and the one being sent left, "+
			"and every recipient blank", got)
	}
	if got := modtest.One[string](t, h.Harness, letters, acme.invoice); strings.Count(got, "faktura@acme.example") != 5 ||
		strings.Contains(got, "customer_anonymised") {
		t.Errorf("another customer's letters = %s, want untouched", got)
	}
	log := logged.String()
	for _, id := range []int64{kari.printed, kari.sending} {
		if !strings.Contains(log, "level=WARN") || !strings.Contains(log, strconv.FormatInt(id, 10)) {
			t.Errorf("the warning %q does not name letter %d, left by the erase", log, id)
		}
	}

	notes := `SELECT
		(SELECT string_agg(note, '|' ORDER BY id) FROM invoices.charge_payments WHERE invoice_id = $1) || '/' ||
		(SELECT string_agg(note, '|' ORDER BY id) FROM invoices.charge_waivers WHERE invoice_id = $1) || '/' ||
		(SELECT string_agg(note, '|' ORDER BY id) FROM invoices.manual_deliveries WHERE invoice_id = $1) || '/' ||
		(SELECT string_agg(note || ',' || coalesce(lift_note, '-'), '|' ORDER BY id) FROM invoices.invoice_holds WHERE invoice_id = $1) || '/' ||
		(SELECT string_agg(note || ',' || agency_reference, '|' ORDER BY id) FROM invoices.collection_handoffs WHERE invoice_id = $1)`
	if got := modtest.One[string](t, h.Harness, notes, kari.invoice); got != "||//|/,|,-/,K-1|,K-2" {
		t.Errorf("the person's notes = %s, want every one blank, the agency's references kept", got)
	}
	if got := modtest.One[string](t, h.Harness, notes, acme.invoice); got != "Betalte gebyret|Feil|/Kari klaget/Levert i hånd|Feil dag/"+
		"Bestridt,Avklart|Bestridt igjen,-/Overlevert,K-1|Overlevert igjen,K-2" {
		t.Errorf("another customer's notes = %s, want kept", got)
	}

	lines := `SELECT string_agg(t.status || ' ' || CASE WHEN t.resolution_note = '' THEN 'blank' ELSE t.resolution_note END || ' ' ||
		coalesce((SELECT string_agg(CASE WHEN e.note = '' THEN 'blank' ELSE e.note END, ',') FROM invoices.bank_transaction_events e
		    WHERE e.bank_transaction_id = t.id), '-') || ' ' || t.debtor_name, '; ' ORDER BY t.id)
		FROM invoices.bank_transactions t WHERE t.id = ANY($1)`
	if got := modtest.One[string](t, h.Harness, lines, []int64{kari.resolvedLine, kari.eventOnlyLine, kari.matchedLine, kari.chargeLine}); got !=
		"resolved blank blank Kari Nordmann; resolved blank blank Kari Nordmann; "+
			"matched blank Banken tok den tilbake Kari Nordmann; resolved blank - Kari Nordmann" {
		t.Errorf("the person's bank lines = %s; want the resolved ones' notes and events blank, the matched one's event "+
			"kept, the bank's payer data kept", got)
	}
	if got := modtest.One[string](t, h.Harness, lines, []int64{acme.resolvedLine, acme.chargeLine}); got !=
		"resolved Kari ringte om innbetalingen Kari ringte Kari Nordmann; resolved Gebyr fra Kari - Kari Nordmann" {
		t.Errorf("another customer's bank lines = %s, want kept", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payments WHERE invoice_id = $1`, kari.invoice); n != 3 {
		t.Errorf("the person's payments = %d, want all three kept", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.customer_reminder_policies WHERE customer_id = $1`, customerPerson); n != 0 {
		t.Error("the person's reminder policy was kept")
	}
}

// TestErase_TwiceReportsZeros: a second erase finds nothing of the
// receivables either.
func TestErase_TwiceReportsZeros(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantReceivables(t, h, customerPerson, 1, "kari@example.org")
	plantPolicy(t, h, customerPerson, "none", "")
	eraseOf(t, h, disabledDeps(h), customerPerson)
	h.Advance(time.Hour)
	if got, want := eraseOf(t, h, disabledDeps(h), customerPerson), eraseReport(0, 0, 0, 0); !slices.Equal(got, want) {
		t.Errorf("a second erase = %+v, want zeros", got)
	}
}

// startErase erases customer in a transaction of its own under a 10-second
// deadline on its own goroutine, committing it, and answers its outcome.
func startErase(h *harness, customer int32) <-chan error {
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx, err := h.Pool().Begin(ctx)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).EraseCustomerData(ctx, tx, customer); err != nil {
			done <- err
			return
		}
		done <- tx.Commit(ctx)
	}()
	return done
}

// erased waits for an erase started with startErase and fails the test
// unless it committed.
func erased(t *testing.T, what string, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("%s never finished", what)
	}
}

// TestErase_RacesQueueApply: an apply on an exception line of the person's
// invoice, parked after its line lock — the line FOR NO KEY UPDATE, no
// invoice. The line is linked to a removed payment of the person and carries
// a note no flow writes on an open line today, so only the erase's
// "resolved" guard keeps it off the line (reading 54): the erase holds the
// person's documents, blanks the resolved line's note and commits without
// waiting on the apply's line; released, the apply locks the invoice and
// resolves its line. Both finish; no deadlock.
func TestErase_RacesQueueApply(t *testing.T) {
	h, _ := matchHarness(t, modtest.WithPoolMaxConns(2))
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	_, ids := camtLines(t, h, c, "ERASE-1", noKidEntry(6, 100, "Til faktura", "DONE"), noKidEntry(6, 400, "Til faktura", "OPEN"))
	done := applyBody(allocate(inv, 100))
	done["note"] = "Acme ringte"
	acted(t, c, ids["DONE"], "apply", done)
	h.Exec(t, `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, source, bank_transaction_id,
		    registered_by_user_id, registered_at, removed_at, removed_by_user_id, removal_reason)
		VALUES ($1, DATE '2026-10-06', 400, 'NOK', 'camt054', $2, gen_random_uuid(), now(), now(), gen_random_uuid(), 'Feil')`, inv.ID, ids["OPEN"])
	h.Exec(t, `UPDATE invoices.bank_transactions SET resolution_note = 'Acme sa det var deres' WHERE id = $1`, ids["OPEN"])
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	hook, parked := parkEach()
	restore := invoices.SetQueueAfterLineLock(hook)
	defer restore()

	applyDone := startAction(c, ids["OPEN"], "apply", applyBody(allocate(inv, 400)))
	p := waitParked(t, "the apply", parked)
	if got := heldMode(t, probeConn, "invoices.bank_transactions", "id = $1", ids["OPEN"]); got != modeNoKeyUpdate {
		t.Errorf("the open line is held %q, want FOR NO KEY UPDATE", got)
	}
	erased(t, "the erase beside the parked apply", startErase(h, customerAcme))
	close(p.release)
	if res := finished(t, "the apply", applyDone, http.StatusOK); res.Status == http.StatusOK {
		var l queueLineJSON
		res.JSON(&l)
		if l.Status != "resolved" {
			t.Errorf("the apply = %s, want resolved", l.Status)
		}
	}
	if got := modtest.One[string](t, h.Harness, `SELECT resolution_note FROM invoices.bank_transactions WHERE id = $1`, ids["DONE"]); got != "" {
		t.Errorf("the resolved line's note = %q, want blank", got)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// TestErase_DoesNotWaitOnAPrintedLetter: a reprint holds a batch's printed
// letters without their invoice (Task 13); the erase blanks only a recipient
// that is not blank already, and a paper letter's always is, so it never
// waits on one — here held FOR NO KEY UPDATE on a raw connection — and the
// order documents → letters cannot meet letters → documents.
func TestErase_DoesNotWaitOnAPrintedLetter(t *testing.T) {
	h := raceHarness(t)
	id := plantOverdue(t, h, overdueSpec{number: 1, customer: customerPerson, issue: "2026-07-01", due: "2026-08-03"})
	printed := plantLetterIn(t, h, id, 1, "printed")
	queued := queuedLetter(t, h, id, 2)
	raw := holdRow(t, h, `SELECT 1 FROM invoices.reminders WHERE id = $1 FOR NO KEY UPDATE`, printed)
	erased(t, "the erase beside a held printed letter", startErase(h, customerPerson))
	raw.release(t)
	if s := letterStateOf(t, h, printed); s.status != "printed" {
		t.Errorf("the printed letter = %+v, want left for the posting", s)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT status || ' ' || recipient FROM invoices.reminders WHERE id = $1`, queued); got != "withdrawn " {
		t.Errorf("the queued letter = %q, want withdrawn with no recipient", got)
	}
}

// TestErase_ALetterRacingTheEraseIsInsertedBlank: a run's item parked after
// its invoice lock while the erase waits on the person's documents; released,
// the letter is inserted with the reminder address and committed, and the
// erase, locking after it, withdraws it and blanks its address. Reversed — the
// erase holding the documents with its marker written — a letter inserted
// meanwhile waits on the trigger's FOR SHARE of its invoice and, once the
// erase commits, the trigger reads the marker and writes it with no address.
func TestErase_ALetterRacingTheEraseIsInsertedBlank(t *testing.T) {
	h := runReady(t, "", modtest.WithPoolMaxConns(2))
	id := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	hook, parked := parkEach()
	restore := invoices.SetRunItemAfterLock(hook)
	runDone := startRequest(payer(t, h), http.MethodPost, reminderRunsPath, runBody(yes, item(id, "reminder")))
	p := waitParked(t, "the run's item", parked)
	eraseDone := startErase(h, customerAcme)
	eraser := newWaiter(t, probeConn)
	if got := heldMode(t, probeConn, "invoices.invoices", "id = $1", id); got != modeUpdate {
		t.Errorf("the invoice is held %q by the run's item, want FOR UPDATE", got)
	}
	close(p.release)
	restore()
	r := made(t, "the run", finished(t, "the run", runDone, http.StatusCreated))
	erased(t, "the erase", eraseDone)
	if len(r.Created) != 1 {
		t.Fatalf("the run = %+v, want one letter", r)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT status || ' ' || coalesce(withdrawal_reason, '-') || ' ' || recipient
		FROM invoices.reminders WHERE id = $1`, r.Created[0].ID); got != "withdrawn customer_anonymised " {
		t.Errorf("the letter the erase waited for = %q (eraser pid %d), want withdrawn with no address", got, eraser)
	}

	// Reversed: the erase of Kari holds her documents and has written the
	// marker; a letter inserted for her waits on its invoice, then is
	// written blank.
	kari := deliveredOn(t, h, 2, customerPerson, "2026-08-03")
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := invoices.Module().CustomerPersonalData(disabledDeps(h)).EraseCustomerData(ctx, tx, customerPerson); err != nil {
		t.Fatalf("EraseCustomerData: %v", err)
	}
	inserter := ownConn(t, h)
	run := plantRun(t, h)
	inserted := make(chan error, 1)
	var letter int64
	go func() {
		inserted <- inserter.QueryRow(ctx, `
			INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, recipient, language, created_at,
			    created_by_user_id, status)
			VALUES ($1, $2, 1, 'reminder', 'email', 'kari@example.org', 'nb', now(), gen_random_uuid(), 'queued')
			RETURNING id`, kari, run).Scan(&letter)
	}()
	if got := blockersOf(t, probeConn, newWaiter(t, probeConn)); len(got) != 1 {
		t.Errorf("the insert waits on %v, want the erase alone", got)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-inserted:
		if err != nil {
			t.Fatalf("the insert: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the insert never finished")
	}
	if got := modtest.One[string](t, h.Harness, `SELECT recipient FROM invoices.reminders WHERE id = $1`, letter); got != "" {
		t.Errorf("the letter inserted after the marker = %q, want no address", got)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// eventNotes is line id's events as "event note", oldest first.
func eventNotes(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return texts(t, h, `SELECT event || ' ' || note FROM invoices.bank_transaction_events WHERE bank_transaction_id = $1 ORDER BY id`, id)
}

// appliedThenReopened applies a no-KID line of 300 to inv with staff's
// note, removes the payment it registered and reopens the line: an
// exception again, still linked to the person's (removed) payment, its
// applied event keeping the note.
func appliedThenReopened(t *testing.T, h *harness, c *modtest.Client, inv invoiceJSON, msgID string) int64 {
	t.Helper()
	_, ids := camtLines(t, h, c, msgID, noKidEntry(6, 300, "Til faktura", msgID))
	line := ids[msgID]
	body := applyBody(allocate(inv, 300))
	body["note"] = "Acme ringte om denne"
	acted(t, c, line, "apply", body)
	if res := payer(t, h).Do(http.MethodPost, removalPath(inv.ID, paymentOf(t, h, line)), map[string]any{"reason": "Feil faktura"}); res.Status != http.StatusOK {
		t.Fatalf("remove the payment = %d %s", res.Status, res.Body)
	}
	if l := acted(t, c, line, "reopen", nil); l.Status != "exception" {
		t.Fatalf("the reopened line = %s, want an exception", l.Status)
	}
	return line
}

// TestErase_BlanksAReopenedLinesEarlierEventNote: a line applied with a
// note, its payment removed and the line reopened is an exception again —
// open, its resolution note cleared by the reopen — but linked to the
// person's removed payment, and its applied event keeps staff's words. The
// erase blanks them: events are locked by nothing, so the line's status
// does not matter; only a reversed event, the system's own words, is kept.
func TestErase_BlanksAReopenedLinesEarlierEventNote(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	line := appliedThenReopened(t, h, c, inv, "REOPEN-1")
	if got := eventNotes(t, h, line); !slices.Contains(got, "applied Acme ringte om denne") {
		t.Fatalf("the line's events = %v, want the applied one with its note", got)
	}
	erased := eraseOf(t, h, disabledDeps(h), customerAcme)
	if got := eventNotes(t, h, line); slices.ContainsFunc(got, func(e string) bool { return strings.HasSuffix(e, "Acme ringte om denne") }) {
		t.Errorf("the reopened line's events after the erase = %v, want every note blank", got)
	}
	if !slices.Contains(erased, contracts.ErasedData{Kind: "invoices.bankTransactions", Count: 1}) {
		t.Errorf("erased = %+v, want the line counted", erased)
	}
	if got := stateOf(t, h, line); got != "exception no_kid" {
		t.Errorf("the line = %s, want still the queue's", got)
	}
}

// TestErase_AQueueActionAfterTheEraseWritesNoNote: once the person is
// anonymised, a queue action resolving a line linked to their invoices —
// an apply to their invoice, a dismissal of a reopened line their removed
// payment came from — keeps no note on the line nor on its event: the erase
// blanked such notes once, and a later action does not write one back.
// A line linked to nobody keeps its note.
func TestErase_AQueueActionAfterTheEraseWritesNoNote(t *testing.T) {
	t.Parallel()
	h, _ := matchHarness(t)
	inv := kidInvoice(t, h)
	toMatchDay(h)
	c := importer(t, h)
	reopened := appliedThenReopened(t, h, c, inv, "LATER-1")
	_, ids := camtLines(t, h, c, "LATER-2", noKidEntry(6, 200, "Til faktura", "APPLY"), noKidEntry(6, 50, "Renter", "OTHER"))
	eraseOf(t, h, disabledDeps(h), customerAcme)

	body := applyBody(allocate(inv, 200))
	body["note"] = "Acme ringte etterpå"
	acted(t, c, ids["APPLY"], "apply", body)
	acted(t, c, reopened, "dismiss", map[string]any{"note": "Acme sa den var feil"})
	acted(t, c, ids["OTHER"], "dismiss", map[string]any{"note": "Renter fra banken"})
	notes := `SELECT resolution_note FROM invoices.bank_transactions WHERE id = $1`
	for _, l := range []int64{ids["APPLY"], reopened} {
		if got := modtest.One[string](t, h.Harness, notes, l); got != "" {
			t.Errorf("line %d's note after the erase = %q, want none", l, got)
		}
		for _, e := range eventNotes(t, h, l) {
			if strings.Contains(e, "Acme") {
				t.Errorf("line %d's events = %v, want no note of staff's after the erase", l, eventNotes(t, h, l))
			}
		}
	}
	if got := modtest.One[string](t, h.Harness, notes, ids["OTHER"]); got != "Renter fra banken" {
		t.Errorf("a line linked to nobody = %q, want its note kept", got)
	}
}
