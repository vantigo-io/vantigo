package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// counterWhere is the documents counter's row, for probes and raw locks.
const counterWhere = `counter_name = 'documents'`

// issuedNumbers is every issued document's number, ascending.
func issuedNumbers(t *testing.T, h *harness) []int64 {
	t.Helper()
	rows, err := h.Pool().Query(context.Background(), `SELECT number FROM invoices.invoices WHERE number IS NOT NULL ORDER BY number`)
	if err != nil {
		t.Fatalf("read the numbers: %v", err)
	}
	numbers, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatalf("read the numbers: %v", err)
	}
	return numbers
}

// TestQuickInvoice_LockOrder: the quick invoice's transaction holds its
// document (inserted, so its own), then the settings row FOR SHARE, then the
// counter (D17, D18) — as the issue does. With the settings row held by a raw
// FOR UPDATE, the quick invoice has inserted its draft (the seam has run)
// and waits on the settings row alone, the counter still free; with the
// counter held, it waits on the counter alone, holding the settings FOR
// SHARE. Not parallel: the seam is the package's.
func TestQuickInvoice_LockOrder(t *testing.T) {
	h := raceHarness(t)
	saveSeller(t, h, completeSeller(1))
	issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID) // the counter row
	probeConn := ownConn(t, h)
	var inserted atomic.Int64
	restore := invoices.SetQuickAfterInsert(func(_ context.Context, invoiceID int64) error {
		inserted.Store(invoiceID)
		return nil
	})
	defer restore()
	client := quicker(t, h)
	quick := func(ctx context.Context) <-chan *modtest.Response {
		answered := make(chan *modtest.Response, 1)
		go func() {
			answered <- client.Do(http.MethodPost, quickPath, quickBody(customerAcme, line("A", 1, 100, vat25)), modtest.Context(ctx))
		}()
		return answered
	}

	t.Run("the document before the settings", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		inserted.Store(0)
		settings := holdRow(t, h, `SELECT 1 FROM invoices.settings WHERE id = 1 FOR UPDATE`)
		answered := quick(ctx)
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{settings.pid}) {
			t.Fatalf("pg_blocking_pids(the quick invoice) = %v, want the settings' holder %d", got, settings.pid)
		}
		if inserted.Load() == 0 {
			t.Error("the quick invoice waits on the settings before it inserted its document")
		}
		if err := probe(probeConn, modeUpdate, "invoices.counters", counterWhere); err != nil {
			t.Errorf("the counter while the quick invoice waits on the settings: %v, want it free", err)
		}
		settings.release(t)
		if res := <-answered; res.Status != http.StatusCreated {
			t.Errorf("the quick invoice = %d %s, want 201", res.Status, res.Body)
		}
	})

	t.Run("the settings before the counter", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		inserted.Store(0)
		counter := holdRow(t, h, `SELECT 1 FROM invoices.counters WHERE `+counterWhere+` FOR UPDATE`)
		answered := quick(ctx)
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{counter.pid}) {
			t.Fatalf("pg_blocking_pids(the quick invoice) = %v, want the counter's holder %d", got, counter.pid)
		}
		if inserted.Load() == 0 {
			t.Error("the quick invoice waits on the counter before it inserted its document")
		}
		if mode := heldMode(t, probeConn, "invoices.settings", "id = 1"); mode != modeShare {
			t.Errorf("the quick invoice holds the settings %q while it waits on the counter, want FOR SHARE", mode)
		}
		counter.release(t)
		if res := <-answered; res.Status != http.StatusCreated {
			t.Errorf("the quick invoice = %d %s, want 201", res.Status, res.Body)
		}
	})
	if got := issuedNumbers(t, h); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Errorf("numbers = %v, want 1, 2, 3", got)
	}
}

// TestQuickInvoice_RacesIssue (D17, D18): a quick invoice and an ordinary
// issue share the one series, both ways, on a pool of two.
//
// The quick invoice parked on SetQuickAfterInsert — its draft inserted, not
// yet issued — holds neither the settings row nor the counter, so an
// ordinary issue of another draft takes both and commits; released, the
// quick invoice takes the next number.
//
// The ordinary issue parked on SetIssueAfterAllocation holds the counter; a
// quick invoice waits on it — pg_blocking_pids names the issue — and takes
// the next number once the issue commits.
//
// Consecutive numbers with no gap, never 40P01, pg_stat_database.deadlocks
// unchanged. Not parallel: the seams are the package's.
func TestQuickInvoice_RacesIssue(t *testing.T) {
	h := raceHarness(t)
	saveSeller(t, h, completeSeller(1))
	issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID) // number 1, the counter row
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	noDeadlock := func(t *testing.T) {
		t.Helper()
		if after := deadlocks(t, probeConn); after != before {
			t.Errorf("deadlocks = %d, was %d: Postgres broke a deadlock", after, before)
		}
	}
	quickClient, issueClient := quicker(t, h), issuer(t, h)
	numberOf := func(t *testing.T, res *modtest.Response, quick bool) int64 {
		t.Helper()
		want := http.StatusOK
		if quick {
			want = http.StatusCreated
		}
		if res.Status != want {
			t.Fatalf("= %d %s, want %d — never 40P01", res.Status, res.Body, want)
		}
		if quick {
			var got quickJSON
			res.JSON(&got)
			return *got.Invoice.Number
		}
		var got invoiceJSON
		res.JSON(&got)
		return *got.Number
	}

	t.Run("the quick invoice parked after its insert", func(t *testing.T) {
		t.Cleanup(func() { noDeadlock(t) })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		other := createDraft(t, h, draftBody(customerAcme, line("B", 1, 100, vat25))).ID
		parked, release := make(chan struct{}), make(chan struct{})
		var once atomic.Bool
		restore := invoices.SetQuickAfterInsert(func(ctx context.Context, _ int64) error {
			if once.Swap(true) {
				return nil
			}
			close(parked)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		defer restore()
		answered := make(chan *modtest.Response, 1)
		go func() {
			answered <- quickClient.Do(http.MethodPost, quickPath, quickBody(customerAcme, line("Q", 1, 100, vat25)), modtest.Context(ctx))
		}()
		select {
		case <-parked:
		case <-ctx.Done():
			t.Fatal("the quick invoice never reached the seam")
		}
		idleInTransaction(t, probeConn)
		if err := probe(probeConn, modeUpdate, "invoices.counters", counterWhere); err != nil {
			t.Errorf("the counter while the quick invoice is parked: %v, want it free", err)
		}
		if err := probe(probeConn, modeUpdate, "invoices.settings", "id = 1"); err != nil {
			t.Errorf("the settings while the quick invoice is parked: %v, want them free", err)
		}
		issueRes := issueClient.Do(http.MethodPost, issuePath(other), map[string]any{}, modtest.Context(ctx))
		if n := numberOf(t, issueRes, false); n != 2 {
			t.Errorf("the ordinary issue = %d, want 2", n)
		}
		close(release)
		if n := numberOf(t, <-answered, true); n != 3 {
			t.Errorf("the quick invoice = %d, want 3, the next", n)
		}
	})

	t.Run("the issue holding the counter", func(t *testing.T) {
		t.Cleanup(func() { noDeadlock(t) })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		other := createDraft(t, h, draftBody(customerAcme, line("C", 1, 100, vat25))).ID
		parked, release := make(chan uint32, 1), make(chan struct{})
		restore := invoices.SetIssueAfterAllocation(func(ctx context.Context, tx pgx.Tx, id int64) error {
			if id != other {
				return nil
			}
			var pid uint32
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			parked <- pid
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		defer restore()
		issued := make(chan *modtest.Response, 1)
		go func() {
			issued <- issueClient.Do(http.MethodPost, issuePath(other), map[string]any{}, modtest.Context(ctx))
		}()
		var issuePid uint32
		select {
		case issuePid = <-parked:
		case <-ctx.Done():
			t.Fatal("the issue never reached its allocation")
		}
		answered := make(chan *modtest.Response, 1)
		go func() {
			answered <- quickClient.Do(http.MethodPost, quickPath, quickBody(customerAcme, line("Q", 1, 100, vat25)), modtest.Context(ctx))
		}()
		waiter := newWaiter(t, probeConn)
		if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{issuePid}) {
			t.Errorf("pg_blocking_pids(the quick invoice) = %v, want the issue %d", got, issuePid)
		}
		close(release)
		if n := numberOf(t, <-issued, false); n != 4 {
			t.Errorf("the ordinary issue = %d, want 4", n)
		}
		if n := numberOf(t, <-answered, true); n != 5 {
			t.Errorf("the quick invoice = %d, want 5, the next", n)
		}
	})

	if got := issuedNumbers(t, h); !slices.Equal(got, []int64{1, 2, 3, 4, 5}) {
		t.Errorf("numbers = %v, want 1 to 5 with no gap", got)
	}
	if n := counterNext(t, h); n != 6 {
		t.Errorf("the counter's next = %d, want 6", n)
	}
}

// TestQuickInvoice_RequestIdDoubleSubmit (D17, after review): two quick
// invoices with one requestId at once. The first, parked on
// SetQuickAfterInsert, holds the id's key uncommitted; the second waits on
// it — pg_blocking_pids names the first — and, once the first commits, finds
// the id taken and answers 200 with the first's invoice. One document, one
// number, never 40P01. Not parallel: the seam is the package's.
func TestQuickInvoice_RequestIdDoubleSubmit(t *testing.T) {
	h := raceHarness(t)
	saveSeller(t, h, completeSeller(1))
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	parked, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	restore := invoices.SetQuickAfterInsert(func(ctx context.Context, _ int64) error {
		if once.Swap(true) {
			return nil
		}
		close(parked)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer restore()
	body := quickBody(customerAcme, line("Utrykning", 1, 950, vat25))
	body["requestId"] = "0b7a3f4e-1c2d-4e5f-8a9b-0c1d2e3f4a5b"
	firstClient, secondClient := quicker(t, h), quicker(t, h)

	firstDone := make(chan *modtest.Response, 1)
	go func() { firstDone <- firstClient.Do(http.MethodPost, quickPath, body, modtest.Context(ctx)) }()
	select {
	case <-parked:
	case <-ctx.Done():
		t.Fatal("the first never reached the seam")
	}
	first := idleInTransaction(t, probeConn)
	secondDone := make(chan *modtest.Response, 1)
	go func() { secondDone <- secondClient.Do(http.MethodPost, quickPath, body, modtest.Context(ctx)) }()
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{first}) {
		t.Errorf("pg_blocking_pids(the second) = %v, want the first %d", got, first)
	}
	close(release)

	a, b := <-firstDone, <-secondDone
	if a.Status != http.StatusCreated || b.Status != http.StatusOK {
		t.Fatalf("first %d %s, second %d %s; want 201 and 200 — never 40P01", a.Status, a.Body, b.Status, b.Body)
	}
	var x, y quickJSON
	a.JSON(&x)
	b.JSON(&y)
	if x.Invoice.ID != y.Invoice.ID || *y.Invoice.Number != 1 {
		t.Errorf("first %d, second %d (number %d); want one invoice, number 1", x.Invoice.ID, y.Invoice.ID, *y.Invoice.Number)
	}
	if n, next := documentCount(t, h), counterNext(t, h); n != 1 || next != 2 {
		t.Errorf("documents %d, counter %d; want 1 and 2", n, next)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks = %d, was %d", after, before)
	}
}

// TestQuickInvoice_RequestIdFirstRefused (D17, after review): the first of
// two quick invoices with one requestId, parked on SetQuickAfterInsert with
// the id's key uncommitted, is refused afterwards (buyer_incomplete) and
// rolls back while the second waits on the key — pg_blocking_pids names the
// first. The second then takes the id and issues its own invoice: the next
// number, no gap, never 40P01, deadlocks unchanged. Not parallel: the seam
// is the package's.
func TestQuickInvoice_RequestIdFirstRefused(t *testing.T) {
	h := raceHarness(t)
	saveSeller(t, h, completeSeller(1))
	issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID) // number 1
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	parked, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	restore := invoices.SetQuickAfterInsert(func(ctx context.Context, _ int64) error {
		if once.Swap(true) {
			return nil
		}
		close(parked)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer restore()
	const requestID = "3c9d8e7f-6a5b-4c3d-9e2f-1a0b9c8d7e6f"
	refusedBody := quickBody(customerNoAddress, line("Utrykning", 1, 950, vat25))
	refusedBody["requestId"] = requestID
	goodBody := quickBody(customerAcme, line("Utrykning", 1, 950, vat25))
	goodBody["requestId"] = requestID
	firstClient, secondClient := quicker(t, h), quicker(t, h)

	firstDone := make(chan *modtest.Response, 1)
	go func() { firstDone <- firstClient.Do(http.MethodPost, quickPath, refusedBody, modtest.Context(ctx)) }()
	select {
	case <-parked:
	case <-ctx.Done():
		t.Fatal("the first never reached the seam")
	}
	first := idleInTransaction(t, probeConn)
	secondDone := make(chan *modtest.Response, 1)
	go func() { secondDone <- secondClient.Do(http.MethodPost, quickPath, goodBody, modtest.Context(ctx)) }()
	waiter := newWaiter(t, probeConn)
	if got := blockersOf(t, probeConn, waiter); !slices.Equal(got, []uint32{first}) {
		t.Errorf("pg_blocking_pids(the second) = %v, want the first %d", got, first)
	}
	close(release)

	a, b := <-firstDone, <-secondDone
	if a.Status != http.StatusConflict || problemOf(t, a).Code != "buyer_incomplete" {
		t.Errorf("the first = %d %s, want 409 buyer_incomplete", a.Status, a.Body)
	}
	if b.Status != http.StatusCreated {
		t.Fatalf("the second = %d %s, want 201 — its own invoice, never 40P01", b.Status, b.Body)
	}
	var got quickJSON
	b.JSON(&got)
	if *got.Invoice.Number != 2 || got.Invoice.CustomerID != customerAcme {
		t.Errorf("the second = number %d for customer %d, want 2 for Acme", *got.Invoice.Number, got.Invoice.CustomerID)
	}
	if n := modtest.One[int64](t, h.Harness, `SELECT invoice_id FROM invoices.quick_requests WHERE request_id = $1`, requestID); n != got.Invoice.ID {
		t.Errorf("the id is kept for invoice %d, want the second's %d", n, got.Invoice.ID)
	}
	if nums := issuedNumbers(t, h); !slices.Equal(nums, []int64{1, 2}) {
		t.Errorf("numbers = %v, want 1 and 2 with no gap", nums)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("deadlocks = %d, was %d", after, before)
	}
}
