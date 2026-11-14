package db_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// 00178_invoices_journal.sql's fact log (accounting design R1): a seq taken at
// commit, so seqs become visible in commit order and a reader paging by seq
// never passes one; append-only; a removal is a fact; the backfill of what
// was there before.

const journalVersion = 178

// journalPayment inserts a payment of invoice on q, registered at at.
func journalPayment(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, invoice int64, at time.Time) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at)
		VALUES ($1, DATE '2026-10-06', 1, 'NOK', gen_random_uuid(), $2) RETURNING id`, invoice, at).Scan(&id)
	return id, err
}

// journalFacts is the log as "kind:subject" in seq order.
func journalFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT kind || ':' || subject_id FROM invoices.journal_facts ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Two transactions register a payment each; the one that started first
// commits last. Its seq is the later one: the seq is taken at commit.
func TestInvoicesJournal_SeqsFollowCommitOrder(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	a, b := f.issued(7), f.issued(8)

	first, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(f.ctx) }()
	early, err := journalPayment(f.ctx, first, a, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	late, err := journalPayment(f.ctx, second, b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := journalFacts(t, f.ctx, f.pool); !equalStrings(got, []string{fmt.Sprintf("payment:%d", late)}) {
		t.Fatalf("after the second commit: %v, want its payment alone, at seq 1", got)
	}
	if err := first.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{fmt.Sprintf("payment:%d", late), fmt.Sprintf("payment:%d", early)}
	if got := journalFacts(t, f.ctx, f.pool); !equalStrings(got, want) {
		t.Errorf("after both: %v, want %v — commit order, not start order", got, want)
	}
	if next := f.text(`SELECT next_value::text FROM invoices.counters WHERE counter_name = 'journal'`); next != "3" {
		t.Errorf("the journal counter's next = %s, want 3", next)
	}
}

// Writers register payments on their own invoices with a random pause before
// each commit, a few rolling back, while a reader pages by seq the way a
// ledger's catch-up does. Every seq the reader sees is the one after the
// last it saw — a rolled-back writer gives its seq back, and no fact ever
// becomes visible behind one already read — and it ends having read every
// fact.
func TestInvoicesJournal_AReaderPagingBySeqNeverMissesAFact(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	const writers, each = 6, 15
	invoices := make([]int64, writers)
	for i := range invoices {
		invoices[i] = f.issued(int32(100 + i))
	}

	var wg sync.WaitGroup
	committed := make(chan int64, writers*each)
	errs := make(chan error, writers)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				tx, err := f.pool.Begin(f.ctx)
				if err != nil {
					errs <- err
					return
				}
				id, err := journalPayment(f.ctx, tx, invoices[w], time.Now())
				if err != nil {
					_ = tx.Rollback(f.ctx)
					errs <- err
					return
				}
				time.Sleep(time.Duration(rand.IntN(3)) * time.Millisecond)
				if i%5 == 4 {
					_ = tx.Rollback(f.ctx)
					continue
				}
				if err := tx.Commit(f.ctx); err != nil {
					errs <- err
					return
				}
				committed <- id
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done); close(errs) }()

	var last int64
	seen := map[int64]bool{}
	reading := true
	for reading {
		select {
		case <-done:
			reading = false
		default:
		}
		rows, err := f.pool.Query(f.ctx, `SELECT seq, subject_id FROM invoices.journal_facts WHERE seq > $1 ORDER BY seq LIMIT 7`, last)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var seq, subject int64
			if err := rows.Scan(&seq, &subject); err != nil {
				t.Fatal(err)
			}
			if seq != last+1 {
				t.Fatalf("read seq %d after %d: the reader would have passed %d", seq, last, last+1)
			}
			last, seen[subject] = seq, true
		}
		rows.Close()
	}
	// What committed after the reader's last read is read now.
	rows, err := f.pool.Query(f.ctx, `SELECT seq, subject_id FROM invoices.journal_facts WHERE seq > $1 ORDER BY seq`, last)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var seq, subject int64
		if err := rows.Scan(&seq, &subject); err != nil {
			t.Fatal(err)
		}
		if seq != last+1 {
			t.Fatalf("read seq %d after %d", seq, last)
		}
		last, seen[subject] = seq, true
	}
	rows.Close()
	for err := range errs {
		t.Fatal(err)
	}
	close(committed)
	n := 0
	for id := range committed {
		n++
		if !seen[id] {
			t.Errorf("payment %d committed and the reader never read its fact", id)
		}
	}
	if want := writers * each * 4 / 5; n != want || int(last) != want {
		t.Errorf("%d committed, last seq %d; want %d of each", n, last, want)
	}
}

// The log is append-only, and a removal is one more fact: of a payment and of
// a charge payment, once — blanking the note later writes none.
func TestInvoicesJournal_RemovalsAndAppendOnly(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	invoice := f.issued(7)
	payment, err := journalPayment(f.ctx, f.pool, invoice, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	charge := f.mustInsert("a charge payment", `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount, currency, source,
		registered_by_user_id, registered_at) VALUES ($1, DATE '2026-10-07', 35, 'NOK', 'manual', gen_random_uuid(), now()) RETURNING id`, invoice)
	f.mustExec("remove the payment", `UPDATE invoices.payments SET removed_at = '2026-10-08T10:00:00Z', removed_by_user_id = gen_random_uuid(),
		removal_reason = 'Feil' WHERE id = $1`, payment)
	f.mustExec("remove the charge payment", `UPDATE invoices.charge_payments SET removed_at = now(), removed_by_user_id = gen_random_uuid(),
		removal_reason = 'Feil' WHERE id = $1`, charge)
	f.mustExec("blank the note", `UPDATE invoices.payments SET note = '' WHERE id = $1`, payment)

	want := []string{
		fmt.Sprintf("payment:%d", payment), fmt.Sprintf("charge_payment:%d", charge),
		fmt.Sprintf("payment_removed:%d", payment), fmt.Sprintf("charge_payment_removed:%d", charge),
	}
	if got := journalFacts(t, f.ctx, f.pool); !equalStrings(got, want) {
		t.Errorf("facts = %v, want %v", got, want)
	}
	if got := f.text(`SELECT coalesce(business_date::text, '-') || ' ' || coalesce(removed_at::text, '-') FROM invoices.journal_facts
		WHERE kind = 'payment_removed'`); got != "- 2026-10-08 10:00:00+00" {
		t.Errorf("the removal's day columns = %q, want its moment and no date", got)
	}
	if got := f.text(`SELECT business_date::text FROM invoices.journal_facts WHERE kind = 'payment'`); got != "2026-10-06" {
		t.Errorf("the registration's day = %s, want its paid-on day", got)
	}
	for what, sql := range map[string]string{
		"an update": `UPDATE invoices.journal_facts SET business_date = DATE '2026-01-01' WHERE kind = 'payment'`,
		"a delete":  `DELETE FROM invoices.journal_facts WHERE kind = 'payment'`,
	} {
		if err := f.exec(sql); !refusedWith(err, "invoices: a journal fact is never changed") {
			t.Errorf("%s: %v, want the refusal", what, err)
		}
	}
	// A fact of the same kind for the same row twice is refused by its index,
	// whatever wrote it.
	if err := f.exec(`INSERT INTO invoices.journal_facts (seq, kind, subject_id, invoice_id, business_date, created_at)
		VALUES (99, 'payment', $1, $2, DATE '2026-10-06', now())`, payment, invoice); err == nil {
		t.Error("a second registration fact of one payment went in")
	}
}

// The backfill: a database with payments and charge payments, some removed,
// migrated to 00178, gets them in registration order and then their removals
// in removal order, numbered from 1, with the counter after the last.
func TestInvoicesJournal_TheBackfill(t *testing.T) {
	t.Parallel()
	url := testdb.URL(t)
	migrateTo(t, url, journalVersion-1)
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	f := &receivablesFixture{t: t, ctx: ctx, pool: pool, next: 1}
	invoice := f.issued(7)
	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	p1, err := journalPayment(ctx, pool, invoice, base.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := journalPayment(ctx, pool, invoice, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	c1 := f.mustInsert("a charge payment", `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount, currency, source,
		registered_by_user_id, registered_at) VALUES ($1, DATE '2026-10-06', 35, 'NOK', 'manual', gen_random_uuid(), $2) RETURNING id`,
		invoice, base.Add(3*time.Hour))
	f.mustExec("remove p1", `UPDATE invoices.payments SET removed_at = $2, removed_by_user_id = gen_random_uuid(), removal_reason = 'Feil'
		WHERE id = $1`, p1, base.Add(10*time.Hour))
	f.mustExec("remove c1", `UPDATE invoices.charge_payments SET removed_at = $2, removed_by_user_id = gen_random_uuid(), removal_reason = 'Feil'
		WHERE id = $1`, c1, base.Add(5*time.Hour))

	migrateTo(t, url, journalVersion)
	want := []string{
		fmt.Sprintf("payment:%d", p2), fmt.Sprintf("payment:%d", p1), fmt.Sprintf("charge_payment:%d", c1),
		fmt.Sprintf("charge_payment_removed:%d", c1), fmt.Sprintf("payment_removed:%d", p1),
	}
	if got := journalFacts(t, ctx, pool); !equalStrings(got, want) {
		t.Errorf("backfilled = %v, want %v", got, want)
	}
	if got := f.text(`SELECT string_agg(seq::text, ',' ORDER BY seq) FROM invoices.journal_facts`); got != "1,2,3,4,5" {
		t.Errorf("seqs = %s, want 1 to 5", got)
	}
	// The next fact follows the backfill.
	p3, err := journalPayment(ctx, pool, invoice, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := f.text(`SELECT seq::text FROM invoices.journal_facts WHERE kind = 'payment' AND subject_id = $1`, p3); got != "6" {
		t.Errorf("the first fact after the backfill has seq %s, want 6", got)
	}
	// Down and up again: the log and its counter go, and come back from the
	// rows as they are now.
	migrateTo(t, url, journalVersion-1)
	var counters int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices.counters WHERE counter_name = 'journal'`).Scan(&counters); err != nil || counters != 0 {
		t.Errorf("journal counters after down = %d (%v), want none", counters, err)
	}
	migrateTo(t, url, journalVersion)
	if got := journalFacts(t, ctx, pool); len(got) != 6 {
		t.Errorf("after down and up: %v, want the six facts again", got)
	}
}

// An empty installation gets no journal counter: the row is made by the
// first fact, as 'documents' is by the first issue.
func TestInvoicesJournal_NoCounterUntilAFact(t *testing.T) {
	t.Parallel()
	f := newReceivablesFixture(t)
	var n int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM invoices.counters WHERE counter_name = 'journal'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d journal counters on an empty installation, want none", n)
	}
	// A payment that rolls back leaves none either.
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journalPayment(f.ctx, tx, f.issued(7), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(f.ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM invoices.journal_facts`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d facts after a rollback (%v), want none", n, err)
	}
}
