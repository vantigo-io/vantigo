package purchasing_test

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
)

// fakeInvoices is contracts.SupplierInvoices over the invoices a test makes,
// in place of Accounting, which depguard keeps out of this package: a draft
// is entered net, its lines' nets their amounts; post and reverse change the
// state and publish Accounting's events into the outbox.
type fakeInvoices struct {
	mu       sync.Mutex
	next     int64
	invoices map[int64]*contracts.SupplierInvoice
	drafts   []contracts.SupplierInvoiceDraft
	refuse   error
	// afterCreate runs once a draft is made, outside the lock, before
	// CreateDraft answers: a test holds a registration there.
	afterCreate func()
}

func newFakeInvoices() *fakeInvoices {
	return &fakeInvoices{next: 7000, invoices: map[int64]*contracts.SupplierInvoice{}}
}

func (f *fakeInvoices) Invoice(ctx context.Context, id int64) (*contracts.SupplierInvoice, error) {
	lockedContractCalls.note(ctx, "fake SupplierInvoices.Invoice")
	f.mu.Lock()
	defer f.mu.Unlock()
	inv, ok := f.invoices[id]
	if !ok {
		return nil, nil
	}
	cp := *inv
	cp.Lines = slices.Clone(inv.Lines)
	return &cp, nil
}

func (f *fakeInvoices) CreateDraft(ctx context.Context, d contracts.SupplierInvoiceDraft) (int64, error) {
	lockedContractCalls.note(ctx, "fake SupplierInvoices.CreateDraft")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse != nil {
		return 0, f.refuse
	}
	for _, inv := range f.invoices {
		if inv.SupplierID == d.SupplierID && inv.Number == d.Number && inv.State != "reversed" {
			return 0, &contracts.SupplierInvoiceRefusal{Code: contracts.SupplierInvoiceDuplicate, Detail: "duplicate"}
		}
	}
	f.drafts = append(f.drafts, d)
	var lines []fakeLine
	for _, l := range d.Lines {
		lines = append(lines, fakeLine{text: l.Text, net: l.Amount})
	}
	currency := d.Currency
	if currency == "" {
		currency = "NOK"
	}
	inv := f.add(d.SupplierID, "invoice", d.Number, d.InvoiceDate, currency, d.Reference, lines...)
	if hook := f.afterCreate; hook != nil {
		f.mu.Unlock()
		hook()
		f.mu.Lock()
	}
	return inv.ID, nil
}

// fakeLine is a line a test gives an invoice.
type fakeLine struct{ text, net string }

func (f *fakeInvoices) add(supplier int64, kind, number string, date time.Time, currency, reference string, lines ...fakeLine) *contracts.SupplierInvoice {
	f.next++
	inv := &contracts.SupplierInvoice{ID: f.next, SupplierID: supplier, Kind: kind, Number: number, InvoiceDate: date,
		Currency: currency, Reference: reference, State: "draft"}
	total := new(big.Rat)
	for i, l := range lines {
		inv.Lines = append(inv.Lines, contracts.SupplierInvoiceLine{ID: f.next*100 + int64(i), Position: int32(i + 1), Text: l.text,
			AccountID: 4000, Entry: "net", Amount: l.net, Net: l.net, VAT: "0.00", Gross: l.net})
		r, _ := new(big.Rat).SetString(l.net)
		total.Add(total, r)
	}
	inv.Total = total.FloatString(2)
	f.invoices[inv.ID] = inv
	return inv
}

// enter is a draft a person typed in Accounting.
func (f *fakeInvoices) enter(supplier int64, kind, number, currency, reference string, lines ...fakeLine) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.add(supplier, kind, number, day(2026, 11, 10), currency, reference, lines...).ID
}

func (f *fakeInvoices) edit(id int64, fn func(*contracts.SupplierInvoice)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.invoices[id])
}

func (f *fakeInvoices) lastDraft() contracts.SupplierInvoiceDraft {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.drafts[len(f.drafts)-1]
}

func (f *fakeInvoices) draftCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.drafts)
}

// post marks the invoice posted and publishes accounting.supplier_invoice.posted.
func (f *fakeInvoices) post(t *testing.T, h *harness, id int64) {
	t.Helper()
	f.mu.Lock()
	inv := f.invoices[id]
	inv.State = "posted"
	ev := contracts.SupplierInvoicePosted{InvoiceID: id, SupplierID: inv.SupplierID, Kind: inv.Kind, Number: inv.Number,
		InvoiceDate: inv.InvoiceDate.Format(time.DateOnly), Currency: inv.Currency, Total: inv.Total, PostedAt: h.Now()}
	f.mu.Unlock()
	publishEvents(t, h, ev)
}

// reverse reverses a posted invoice into a draft copy with its lines, and
// publishes accounting.supplier_invoice.reversed naming the copy.
func (f *fakeInvoices) reverse(t *testing.T, h *harness, id int64) int64 {
	t.Helper()
	f.mu.Lock()
	inv := f.invoices[id]
	inv.State = "reversed"
	var lines []fakeLine
	for _, l := range inv.Lines {
		lines = append(lines, fakeLine{text: l.Text, net: l.Net})
	}
	cp := f.add(inv.SupplierID, inv.Kind, inv.Number, inv.InvoiceDate, inv.Currency, inv.Reference, lines...)
	cp.CopiedFromID = &inv.ID
	ev := contracts.SupplierInvoiceReversed{InvoiceID: id, SupplierID: inv.SupplierID, Kind: inv.Kind, Number: inv.Number,
		InvoiceDate: inv.InvoiceDate.Format(time.DateOnly), Currency: inv.Currency, Total: inv.Total, CopyID: cp.ID, ReversedAt: h.Now()}
	f.mu.Unlock()
	publishEvents(t, h, ev)
	return cp.ID
}

// remove deletes a draft and publishes accounting.supplier_invoice.deleted.
func (f *fakeInvoices) remove(t *testing.T, h *harness, id int64) {
	t.Helper()
	f.mu.Lock()
	inv := f.invoices[id]
	delete(f.invoices, id)
	ev := contracts.SupplierInvoiceDeleted{InvoiceID: id, SupplierID: inv.SupplierID, Kind: inv.Kind, Number: inv.Number,
		InvoiceDate: inv.InvoiceDate.Format(time.DateOnly), Currency: inv.Currency, Total: inv.Total, DeletedAt: h.Now()}
	f.mu.Unlock()
	publishEvents(t, h, ev)
}

// publishEvents writes Accounting's events into the outbox and runs this
// module's handlers through the platform's dispatcher.
func publishEvents(t *testing.T, h *harness, evs ...contracts.Event) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if err := events.Publish(ctx, tx, "accounting", ev, h.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	dispatch(t, h)
}

// dispatch runs the handlers over what the outbox holds.
func dispatch(t *testing.T, h *harness) {
	t.Helper()
	d := h.Deps()
	if err := events.NewDispatcher(h.Pool(), purchasing.Module().EventHandlers(d), d.Logger, h.Now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// deliverAgain hands the outbox's event id to the named handler once more,
// on a transaction of its own, as a redelivery would.
func deliverAgain(t *testing.T, h *harness, handler string, id int64) {
	t.Helper()
	ctx := context.Background()
	var ev events.Delivered
	if err := h.Pool().QueryRow(ctx, `SELECT id, name, version, module, aggregate_id, payload, occurred_at FROM platform.events WHERE id = $1`, id).
		Scan(&ev.ID, &ev.Name, &ev.Version, &ev.Module, &ev.AggregateID, &ev.Payload, &ev.OccurredAt); err != nil {
		t.Fatal(err)
	}
	for _, hd := range purchasing.Module().EventHandlers(h.Deps()) {
		if hd.Name != handler {
			continue
		}
		tx, err := h.Pool().Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := hd.Handle(ctx, tx, ev); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("no handler %s", handler)
}

// lastEventID is the outbox's newest event of a name.
func lastEventID(t *testing.T, h *harness, name string) int64 {
	t.Helper()
	var id int64
	if err := h.Pool().QueryRow(context.Background(), `SELECT max(id) FROM platform.events WHERE name = $1`, name).Scan(&id); err != nil {
		t.Fatal(fmt.Errorf("last event %s: %w", name, err))
	}
	return id
}
