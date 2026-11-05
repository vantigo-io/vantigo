package orders

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// InOpenTx exposes inOpenTx to the external tests, whose contract-call hook
// uses it to tell a call made from inside one of this module's transactions
// from one made outside any.
var InOpenTx = inOpenTx

// LockVariantsCall is the name the hook hears the one in-transaction call by.
const LockVariantsCall = lockVariantsCall

// SetContractCallHook installs the hook every call out of this module is
// reported to (contractscalls.go), once, from TestMain.
func SetContractCallHook(hook func(ctx context.Context, method string)) {
	contractCallHook = hook
}

// PDFModel is the words of one PDF, as the tests read them: the title and
// subtitle, the line table's header and rows, the optional block's rows, the
// VAT rows and the totals.
type PDFModel struct {
	Watermark, Title, Subtitle string
	Header                     []string
	Rows, OptionalRows         [][]string
	VATRows                    [][]string
	Totals                     [][2]string
	Meta                       [][2]string
}

// SetPDFModelHook installs fn to hear every PDF model built, once, from
// TestMain; the hook is shared by every test.
func SetPDFModelHook(fn func(PDFModel)) {
	pdfModelBuilt = func(m pdfModel) {
		out := PDFModel{Watermark: m.watermark, Title: m.title, Subtitle: m.subtitle, Header: m.header,
			VATRows: m.vatRows, Totals: m.totals, Meta: m.meta}
		for _, r := range m.rows {
			out.Rows = append(out.Rows, r.cells)
		}
		for _, r := range m.optionalRows {
			out.OptionalRows = append(out.OptionalRows, r.cells)
		}
		fn(out)
	}
}

// RunMailCycle runs one cycle of the orders.mail worker over d and answers
// how many mailings it claimed.
func RunMailCycle(ctx context.Context, d module.Deps) (int, error) {
	s, err := newServer(d)
	if err != nil {
		return 0, err
	}
	return (&mailWorker{s: s}).cycle(ctx), nil
}

// StoreOrderConfirmation renders an order's confirmation, stores it and
// records it at the order's version — what the orders lane does on confirm —
// and answers the document's id.
func StoreOrderConfirmation(ctx context.Context, d module.Deps, orderID int64) (int64, error) {
	s, err := newServer(d)
	if err != nil {
		return 0, err
	}
	q := store.New(d.Pool)
	o, err := q.OrderByID(ctx, orderID)
	if err != nil {
		return 0, err
	}
	lines, err := linesOf(ctx, q, ownerOrder, orderID)
	if err != nil {
		return 0, err
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return 0, err
	}
	body, err := renderOrderConfirmation(o, lines, seller, d.Clock(), false)
	if err != nil {
		return 0, err
	}
	stored, err := s.storeDocument(ctx, ownerOrder, o.ID, o.Number, docConfirmation, o.Version, body)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		doc, err := recordDocument(ctx, txq, ownerOrder, o.ID, docConfirmation, o.Version, stored, d.Clock())
		id = doc.ID
		return err
	})
	return id, err
}

// QueueOrderMailing queues the automatic send of an order's confirmation at
// its version, as the orders.queue_confirmation handler does, and answers
// whether a row was queued.
func QueueOrderMailing(ctx context.Context, d module.Deps, orderID int64, version int32, recipient string, event uuid.UUID) (bool, error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queued, err := queueMailing(ctx, store.New(tx), ownerOrder, orderID, version, nil, recipient, event, d.Clock())
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return queued, nil
}

// MailBackoff exposes the worker's backoff.
func MailBackoff(attempts int32) time.Duration { return mailBackoff(attempts) }

// RunRetentionCycle runs one cycle of the orders.retention worker over d and
// answers whether it won the lease and how many orders it anonymised.
func RunRetentionCycle(ctx context.Context, d module.Deps) (bool, int64, error) {
	s, err := newServer(d)
	if err != nil {
		return false, 0, err
	}
	return (&retentionWorker{s: s}).cycle(ctx)
}

// RetentionLeaseKey is the retention worker's advisory lease.
const RetentionLeaseKey = retentionLeaseKey

// RetentionCutoff exposes the retention's cutoff.
func RetentionCutoff(now time.Time, zone *time.Location) time.Time { return retentionCutoff(now, zone) }

// RunQuoteExpiryCycle runs one cycle of the orders.quote_expiry worker over d
// and answers how many quotes it expired.
func RunQuoteExpiryCycle(ctx context.Context, d module.Deps) (int64, error) {
	s, err := newServer(d)
	if err != nil {
		return 0, err
	}
	return (&quoteExpiryWorker{s: s}).cycle(ctx)
}

// CreateChannelOrder runs the create command as a channel would (O27, O28):
// an order for customer of one catalog line, its origin channel and external
// id given; created is false when the channel's reference made one already.
func CreateChannelOrder(ctx context.Context, d module.Deps, customerID int32, channel, externalID string, variant int32, qty float64) (int64, bool, error) {
	s, err := newServer(d)
	if err != nil {
		return 0, false, err
	}
	var f fieldErrors
	in := headerInput{CustomerID: customerID}
	reads, err := s.readHeader(ctx, in, nil)
	if err != nil {
		return 0, false, err
	}
	h := resolveHeader(&f, in, nil, reads)
	if f.failed() {
		return 0, false, fmt.Errorf("header: %v", f.errs)
	}
	priced, err := s.priceOrderInputs(ctx, []lineInput{{kind: kindProduct, variantID: &variant, quantity: &qty}}, nil, h, false, false)
	if err != nil {
		return 0, false, err
	}
	o, _, created, err := s.createOrderOnce(ctx, CreateOrder{
		Origin: OrderOrigin{Channel: channel, ExternalID: &externalID}, Header: h, Lines: priced,
	})
	return o.ID, created, err
}
