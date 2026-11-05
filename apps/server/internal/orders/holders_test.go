package orders_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// inTx runs fn on one transaction of the harness's pool, committed.
func inTx(t *testing.T, h *harness, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	ctx := context.Background()
	if err := pgx.BeginFunc(ctx, h.Pool(), func(tx pgx.Tx) error { return fn(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
}

// A merge re-points every quote and order of the absorbed customer (O29) and
// leaves the snapshot as it was; from == into writes nothing.
func TestCustomerReferenceHolder_RepointsQuotesAndOrders(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	q := createQuote(t, c, custPerson, productLine(varBolt, 1))
	o := confirmOrder(t, c, createOrder(t, c, custPerson, productLine(varBolt, 1)))
	holder := orders.Module().CustomerReferences(h.Deps())
	var moved []contracts.RepointedReferences
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		moved, err = holder.RepointCustomer(ctx, tx, custPerson, custBusiness)
		return err
	})
	if len(moved) != 4 || moved[0] != (contracts.RepointedReferences{Kind: "orders.quotes", Count: 1}) ||
		moved[1] != (contracts.RepointedReferences{Kind: "orders.orders", Count: 1}) ||
		moved[2] != (contracts.RepointedReferences{Kind: "orders.warnings"}) || moved[3] != (contracts.RepointedReferences{Kind: "orders.subscriptions"}) {
		t.Errorf("moved %+v; want one quote, one order and no warning", moved)
	}
	res := c.Do(http.MethodGet, orderPath(o.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	if got := orderOf(t, res); got.Customer.CustomerId != custBusiness || got.Customer.Name != "Ola Nordmann" {
		t.Errorf("order customer %d %q; want re-pointed to %d with the snapshot kept", got.Customer.CustomerId, got.Customer.Name, custBusiness)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.quotes WHERE id = $1 AND customer_id = $2`, q.Id, custBusiness); n != 1 {
		t.Error("the quote was not re-pointed")
	}
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		moved, err = holder.RepointCustomer(ctx, tx, custBusiness, custBusiness)
		return err
	})
	if moved[0].Count != 0 || moved[1].Count != 0 {
		t.Errorf("from == into moved %+v", moved)
	}
}

// The export hands over every quote and order of the person with its lines
// and sends; a customer with none has no section (O29).
func TestCustomerPersonalData_Export(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	createQuote(t, c, custPerson, productLine(varBolt, 1))
	o := confirmOrder(t, c, createOrder(t, c, custPerson, productLine(varBolt, 2), productLine(varFood, 1)))
	deliver(t, c, o, o.Lines[0].Id, 1)
	data := orders.Module().CustomerPersonalData(h.Deps())
	ctx := context.Background()
	section, err := data.ExportCustomerData(ctx, custPerson)
	if err != nil || section == nil {
		t.Fatalf("export: %v, %v", section, err)
	}
	body, _ := json.Marshal(section)
	var got struct {
		Quotes []struct {
			CustomerName string            `json:"customerName"`
			Lines        []json.RawMessage `json:"lines"`
		} `json:"quotes"`
		Orders []struct {
			Number int64 `json:"number"`
			Lines  []struct {
				SKU       string `json:"sku"`
				Delivered string `json:"delivered"`
			} `json:"lines"`
		} `json:"orders"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Quotes) != 1 || got.Quotes[0].CustomerName != "Ola Nordmann" || len(got.Quotes[0].Lines) != 1 ||
		len(got.Orders) != 1 || len(got.Orders[0].Lines) != 2 || got.Orders[0].Lines[0].Delivered != "1.000" {
		t.Errorf("export %s", body)
	}
	if none, err := data.ExportCustomerData(ctx, custBusiness); err != nil || none != nil {
		t.Errorf("a customer with nothing: %v, %v; want no section", none, err)
	}
}

// The erase (O29) anonymises every quote and every order never confirmed at
// once, blanks the sends' recipients and fails a queued one, keeps a
// confirmed order and records the erasure; the retention worker anonymises
// the kept order once its retention has run (O54) and completes the erasure.
// Run again, each reports nothing.
func TestCustomerPersonalData_EraseAndRetention(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	q := markSent(t, c, createQuote(t, c, custPerson, productLine(varBolt, 1)))
	draft := createOrder(t, c, custPerson, productLine(varBolt, 1))
	kept := confirmOrder(t, c, createOrder(t, c, custPerson, productLine(varBolt, 1)))
	queued, err := orders.QueueOrderMailing(context.Background(), h.Deps(), kept.Id, 1, "ola@example.com", [16]byte{1})
	if err != nil || !queued {
		t.Fatalf("queue: %v %v", queued, err)
	}
	data := orders.Module().CustomerPersonalData(h.Deps())
	var erased []contracts.ErasedData
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		erased, err = data.EraseCustomerData(ctx, tx, custPerson)
		return err
	})
	want := []contracts.ErasedData{
		{Kind: "orders.quotes", Count: 1}, {Kind: "orders.draftOrders", Count: 1}, {Kind: "orders.orders"}, {Kind: "orders.mailings", Count: 1},
		{Kind: "orders.warnings"}, {Kind: "orders.subscriptions"},
	}
	if len(erased) != len(want) {
		t.Fatalf("erased %+v, want %+v", erased, want)
	}
	for i := range want {
		if erased[i] != want[i] {
			t.Errorf("erased[%d] = %+v, want %+v", i, erased[i], want[i])
		}
	}
	name := func(table string, id int64) string {
		return strings.TrimSpace(oneString(t, h, `SELECT customer_name || '|' || coalesce(invoice_address_line1, '') || '|' || coalesce(customer_invoice_email, '') FROM orders.`+table+` WHERE id = $1`, id))
	}
	if got := name("quotes", q.Id); got != "||" {
		t.Errorf("quote snapshot %q, want blanked", got)
	}
	if got := name("orders", draft.Id); got != "||" {
		t.Errorf("draft snapshot %q, want blanked", got)
	}
	if got := name("orders", kept.Id); !strings.HasPrefix(got, "Ola Nordmann|") {
		t.Errorf("kept order snapshot %q, want kept", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings WHERE recipient = '' AND status = 'failed'`); n != 1 {
		t.Errorf("%d blanked and failed mailings, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.erasures WHERE customer_id = $1 AND completed_at IS NULL`, custPerson); n != 1 {
		t.Fatalf("%d open erasures, want 1", n)
	}
	if !strings.Contains(h.Logs(), "order_numbers") {
		t.Error("the kept order numbers were not logged")
	}

	// The retention has not run yet: the cycle keeps the confirmed order.
	ctx := context.Background()
	if won, n, err := orders.RunRetentionCycle(ctx, h.Deps()); err != nil || !won || n != 0 {
		t.Fatalf("retention before its time: won %v, %d, %v; want nothing", won, n, err)
	}
	// Cancelled in 2020: kept to 30 June 2024, so due now.
	h.Exec(t, `UPDATE orders.orders SET status = 'cancelled', cancelled_at = '2020-03-01T12:00:00Z' WHERE id = $1`, kept.Id)
	if won, n, err := orders.RunRetentionCycle(ctx, h.Deps()); err != nil || !won || n != 1 {
		t.Fatalf("retention: won %v, %d, %v; want the one order", won, n, err)
	}
	if got := name("orders", kept.Id); got != "||" {
		t.Errorf("kept order after retention %q, want blanked", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.erasures WHERE completed_at IS NOT NULL`); n != 1 {
		t.Error("the erasure was not completed")
	}
	if _, n, err := orders.RunRetentionCycle(ctx, h.Deps()); err != nil || n != 0 {
		t.Errorf("a second cycle: %d, %v; want nothing", n, err)
	}
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		erased, err = data.EraseCustomerData(ctx, tx, custPerson)
		return err
	})
	for _, e := range erased {
		if e.Count != 0 {
			t.Errorf("a second erase reported %+v", e)
		}
	}
}

func oneString(t *testing.T, h *harness, sql string, args ...any) string {
	t.Helper()
	var s string
	if err := h.Pool().QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// The retention's cutoff (O54): 3 years and 6 months after the end of the
// financial year.
func TestRetentionCutoff(t *testing.T) {
	t.Parallel()
	oslo, _ := time.LoadLocation("Europe/Oslo")
	for _, tc := range []struct{ now, want string }{
		{"2024-06-30T12:00:00Z", "2020-01-01"}, // 2019's ran out on 30 June 2023
		{"2024-07-01T12:00:00Z", "2021-01-01"}, // 2020's ran out on 30 June 2024
		{"2026-11-05T12:00:00Z", "2023-01-01"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := orders.RetentionCutoff(now, oslo).Format("2006-01-02"); got != tc.want {
			t.Errorf("cutoff at %s = %s, want %s", tc.now, got, tc.want)
		}
	}
}

// The variant holder (O30) holds a delete while a draft or sent quote or an
// order line not done names the variant, counting the documents; a type
// change is never held.
func TestVariantReferenceHolder_CountsOpenDocuments(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	holder := orders.Module().VariantReferences(h.Deps())
	held := func(purpose contracts.VariantHoldPurpose) *contracts.VariantHold {
		t.Helper()
		var out *contracts.VariantHold
		inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			out, err = holder.VariantHeld(ctx, tx, varBolt, purpose)
			return err
		})
		return out
	}
	if got := held(contracts.VariantHoldDelete); got != nil {
		t.Errorf("hold before any document %+v", got)
	}
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 2)))
	got := held(contracts.VariantHoldDelete)
	if got == nil || got.Kind != contracts.VariantHoldKindOpenDocuments || got.Documents != 2 {
		t.Errorf("hold %+v; want open_documents, 2", got)
	}
	if got := held(contracts.VariantHoldTypeChange); got != nil {
		t.Errorf("a type change was held %+v", got)
	}
	mustStatus(t, c.Do(http.MethodPost, quotePath(q.Id, "cancel"), gen.OrdersRevisionInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1}), http.StatusOK, "cancel the quote")
	deliver(t, c, o, o.Lines[0].Id, 2) // delivered, but not invoiced: still not done
	if got := held(contracts.VariantHoldDelete); got == nil || got.Documents != 1 {
		t.Errorf("hold %+v; want the order still open", got)
	}
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel-remaining"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 1}), http.StatusOK, "cancel remaining")
	h.Exec(t, `UPDATE orders.lines SET qty_invoiced = quantity WHERE owner_kind = 'order' AND owner_id = $1`, o.Id)
	if got := held(contracts.VariantHoldDelete); got != nil {
		t.Errorf("hold %+v after the order's line is done", got)
	}
}

// orders.queue_confirmation (O25), through the dispatcher: with
// confirmationOnConfirm set, a confirm queues one mailing to the contact,
// however often the dispatcher runs; the mail worker sends it with the
// stored confirmation. Off, it queues nothing.
func TestQueueConfirmation_ThroughTheDispatcher(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	admin := h.SignIn(t, settingsKey...)
	ch := h.channel
	mustStatus(t, admin.Do(http.MethodPut, settingsPath, gen.OrdersSettingsInput{
		Revision: 0, QuoteSeriesStart: 1, OrderSeriesStart: 1, QuoteValidityDays: 30, DefaultPaymentTermsDays: 14,
		MailTexts: []gen.OrdersMailText{}, SendChannelId: &ch, ConfirmationOnConfirm: true,
	}), http.StatusOK, "settings")
	c := h.SignIn(t, boss...)
	lines := []gen.OrdersLineInput{productLine(varBolt, 1)}
	res := c.Do(http.MethodPost, ordersPath, gen.OrdersOrderInput{ClientRequestId: [16]byte{9}, CustomerId: custBusiness, ContactId: i32(contactKari), Lines: &lines})
	mustStatus(t, res, http.StatusCreated, "create")
	o := confirmOrder(t, c, orderOf(t, res))

	ctx := context.Background()
	dispatch := events.NewDispatcher(h.Pool(), orders.Module().EventHandlers(h.Deps()), h.Deps().Logger, h.Now)
	for range 2 {
		if err := dispatch.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings WHERE owner_kind = 'order' AND owner_id = $1 AND status = 'queued' AND recipient = 'kari@acme.example' AND version = 1`, o.Id); n != 1 {
		t.Fatalf("%d queued mailings to the contact, want 1", n)
	}
	// A second delivery of the same event — a lost commit's retry — queues
	// nothing twice.
	h.Exec(t, `UPDATE platform.event_deliveries SET state = 'pending', next_attempt_at = '2000-01-01T00:00:00Z'`)
	if err := dispatch.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := h.Count(t, `SELECT count(*) FROM platform.event_deliveries WHERE state <> 'done'`); n != 0 {
		t.Fatalf("%d deliveries not done after the retry", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings WHERE owner_id = $1`, o.Id); n != 1 {
		t.Fatalf("%d mailings after the retry, want 1", n)
	}
	if n, err := orders.RunMailCycle(ctx, h.Deps()); err != nil || n != 1 {
		t.Fatalf("mail cycle: %d, %v", n, err)
	}
	if sent := h.mail.Sent(); len(sent) != 1 || sent[0].Out.To[0] != "kari@acme.example" || len(sent[0].Out.Attachments) != 1 {
		t.Errorf("sent %+v; want the confirmation to the contact", sent)
	}

	off := newHarness(t, harnessOptions{})
	oc := off.SignIn(t, boss...)
	plain := confirmOrder(t, oc, createOrder(t, oc, custBusiness, productLine(varBolt, 1)))
	if err := events.NewDispatcher(off.Pool(), orders.Module().EventHandlers(off.Deps()), off.Deps().Logger, off.Now).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := off.Count(t, `SELECT count(*) FROM orders.mailings WHERE owner_id = $1`, plain.Id); n != 0 {
		t.Errorf("%d mailings with the setting off, want none", n)
	}
}

// orders.quote_expiry (O20) moves a sent quote past its valid-until to
// expired, once; an expired quote can still be accepted.
func TestQuoteExpiry(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	fresh := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	h.Exec(t, `UPDATE orders.quotes SET valid_until = valid_until - 60 WHERE id = $1`, q.Id)
	ctx := context.Background()
	if n, err := orders.RunQuoteExpiryCycle(ctx, h.Deps()); err != nil || n != 1 {
		t.Fatalf("expiry: %d, %v; want one", n, err)
	}
	if n, _ := orders.RunQuoteExpiryCycle(ctx, h.Deps()); n != 0 {
		t.Errorf("a second cycle expired %d", n)
	}
	res := c.Do(http.MethodGet, quotePath(q.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	if quoteOf(t, res).Status != gen.OrdersQuoteStatusExpired {
		t.Error("the quote did not expire")
	}
	res = c.Do(http.MethodGet, quotePath(fresh.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	if quoteOf(t, res).Status != gen.OrdersQuoteStatusSent {
		t.Error("a quote still valid expired")
	}
	mustStatus(t, c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1, Into: gen.OrdersAcceptInputIntoDraftOrder}), http.StatusOK, "accept an expired quote")
}

// The workers never hold a connection while asking for another: each cycle
// finishes on a pool of one.
func TestWorkers_RunOnAPoolOfOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	markSent(t, c, createQuote(t, c, custPerson, productLine(varBolt, 1)))
	confirmOrder(t, c, createOrder(t, c, custPerson, productLine(varBolt, 1)))
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		_, err := orders.Module().CustomerPersonalData(h.Deps()).EraseCustomerData(ctx, tx, custPerson)
		return err
	})
	cfg := h.Pool().Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	d := h.DepsOn(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if won, _, err := orders.RunRetentionCycle(ctx, d); err != nil || !won {
		t.Errorf("retention on a pool of one: won %v, %v", won, err)
	}
	if _, err := orders.RunQuoteExpiryCycle(ctx, d); err != nil {
		t.Errorf("expiry on a pool of one: %v", err)
	}
	if _, err := orders.RunMailCycle(ctx, d); err != nil {
		t.Errorf("mail on a pool of one: %v", err)
	}
	// A second replica's cycle does nothing while the lease is held.
	conn, err := h.Pool().Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(context.Background(), `SELECT pg_try_advisory_lock($1)`, orders.RetentionLeaseKey).Scan(&locked); err != nil || !locked {
		t.Fatalf("take the lease: %v %v", locked, err)
	}
	if won, _, err := orders.RunRetentionCycle(ctx, d); err != nil || won {
		t.Errorf("a cycle while another holds the lease: won %v, %v", won, err)
	}
	_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, orders.RetentionLeaseKey)
}
