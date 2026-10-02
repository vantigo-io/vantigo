package invoices_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	if got, want := erase(true), []contracts.ErasedData{
		{Kind: "invoices.drafts", Count: 3}, {Kind: "invoices.documents", Count: 0},
		{Kind: "invoices.payments", Count: 0}, {Kind: "invoices.deliveries", Count: 0},
	}; !slices.Equal(got, want) {
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
	if got, want := erase(true), []contracts.ErasedData{
		{Kind: "invoices.drafts", Count: 0}, {Kind: "invoices.documents", Count: 0},
		{Kind: "invoices.payments", Count: 0}, {Kind: "invoices.deliveries", Count: 0},
	}; !slices.Equal(got, want) {
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
			`{"paidOn":"2026-09-12","amount":"300.10","currency":"NOK","reference":"KID 0012345","note":"Delbetaling","registeredAt":"2026-09-12T12:00:00Z"},` +
			`{"paidOn":"2026-09-12","amount":"0.50","currency":"NOK","registeredAt":"2026-09-12T13:00:00Z",` +
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
// every one of their deliveries' recipients and deletes their drafts,
// reporting the four kinds in order; the payments — a bank reference naming
// the payer included — are looked at and kept, reported at 0. Another
// customer's deliveries keep their address. Run twice it finds nothing, and
// the marker keeps its first time. With the module disabled: the pool and
// the clock are all it needs.
func TestCustomerPersonalData_EraseBlanksDeliveriesAndReportsFourKinds(t *testing.T) {
	t.Parallel()
	h, _ := sendReady(t)
	doc := issued(t, h, createDraft(t, h, draftBody(customerPerson, line("Konsultasjon", 1, 1000, vat25))).ID)
	createDraft(t, h, draftBody(customerPerson, line("Utkast", 1, 100, vat25)))
	registered(t, h, doc.ID, map[string]any{"amount": 300, "paidOn": "2026-09-12", "reference": "Fra Kari Nordmann", "note": "Ringte"})
	sent(t, h, doc.ID, nil)
	sent(t, h, doc.ID, map[string]any{"recipient": "kari.privat@example.org"})
	acme := issuedAcme(t, h)
	sent(t, h, acme.ID, nil)
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

	if got, want := erase(), []contracts.ErasedData{
		{Kind: "invoices.drafts", Count: 1}, {Kind: "invoices.documents", Count: 0},
		{Kind: "invoices.payments", Count: 0}, {Kind: "invoices.deliveries", Count: 2},
	}; !slices.Equal(got, want) {
		t.Errorf("erased = %+v, want %+v", got, want)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.deliveries WHERE invoice_id = $1 AND recipient = ''`, doc.ID); n != 2 {
		t.Errorf("%d of the person's two deliveries blanked, want both", n)
	}
	if kept := getInvoice(t, h, doc.ID); len(kept.Deliveries) != 2 || kept.Deliveries[0].Recipient != "" || kept.Deliveries[0].Subject == "" {
		t.Errorf("the deliveries = %+v, want both kept, the subject too, with the address gone", kept.Deliveries)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT recipient FROM invoices.deliveries WHERE invoice_id = $1`, acme.ID); got != "faktura@acme.example" {
		t.Errorf("another customer's delivery = %q, want its address kept", got)
	}
	payment := `SELECT reference || '|' || note || '|' || amount::text || '|' || (removed_at IS NULL)::text FROM invoices.payments WHERE invoice_id = $1`
	if got := modtest.One[string](t, h.Harness, payment, doc.ID); got != "Fra Kari Nordmann|Ringte|300.00|true" {
		t.Errorf("the payment = %s, want it kept as registered", got)
	}
	marker := `SELECT erased_at FROM invoices.erased_customers WHERE customer_id = $1`
	if at := modtest.One[time.Time](t, h.Harness, marker, customerPerson); !at.Equal(erasedAt) {
		t.Errorf("the marker = %s, want the clock's %s", at, erasedAt)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.erased_customers WHERE customer_id = $1`, customerAcme); n != 0 {
		t.Error("another customer was marked erased")
	}

	h.Advance(time.Hour)
	if got, want := erase(), []contracts.ErasedData{
		{Kind: "invoices.drafts", Count: 0}, {Kind: "invoices.documents", Count: 0},
		{Kind: "invoices.payments", Count: 0}, {Kind: "invoices.deliveries", Count: 0},
	}; !slices.Equal(got, want) {
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
	if len(got.Deliveries) != 1 || got.Deliveries[0].Recipient != "" {
		t.Errorf("the send's deliveries = %+v, want one with no address", got.Deliveries)
	}
	if row := modtest.One[string](t, h.Harness, `SELECT recipient FROM invoices.deliveries WHERE invoice_id = $1`, inv.ID); row != "" {
		t.Errorf("the delivery row's recipient = %q, want '' — the erase has run past it", row)
	}
	restore()
	sendRefused(t, "a second send", sendAs(c, inv.ID, nil), http.StatusConflict, "customer_anonymised")
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
