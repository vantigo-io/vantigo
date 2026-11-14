package db_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// 00184_expenses_journal.sql's fact log (accounting design A20, R2): a seq
// taken at commit from its own counter, append-only, an occurrence per
// change of a standalone expense or a travel claim, the lines snapshotted,
// an event published with each fact, the backfill of what was approved and
// reimbursed before it, and a Down that refuses while it holds a fact.

const expensesJournalVersion = 184

// expenseEntry inserts a standalone outlay of 1 250.00 on q and answers its id.
func expenseEntry(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, status string, decidedAt *time.Time) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `INSERT INTO expenses.entries (user_id, created_by_user_id, kind, entry_date, description,
		paid_by, currency, gross_amount, vat_amount, status, decided_at, created_at, updated_at)
		VALUES (gen_random_uuid(), gen_random_uuid(), 'outlay', DATE '2026-03-10', 'Kabel', 'employee', 'NOK', 1250.00, 250.00,
		$1, $2, now(), now()) RETURNING id`, status, decidedAt).Scan(&id)
	return id, err
}

// expenseFacts is the log as "subject.occurrence:id" in seq order.
func expenseFacts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT subject || '.' || occurrence || ':' || subject_id FROM expenses.journal_facts ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func expensesJournalPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	url := testdb.MigratedURL(t)
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

// Each occurrence is a fact, with the lines as they stood: an approval, its
// undoing (naming the approval it undoes), a change in the draft, the next
// approval with the changed amount, a reimbursement with its own day and
// reference and its undoing; a claim's facts carry every line, and a line of
// a claim makes none of its own. Each fact publishes its event.
func TestExpensesJournal_OccurrencesAndTheirLines(t *testing.T) {
	t.Parallel()
	pool, ctx := expensesJournalPool(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	entry, err := expenseEntry(ctx, pool, "submitted", nil)
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE expenses.entries SET status = 'approved', decided_at = now() WHERE id = $1`, entry)
	exec(`UPDATE expenses.entries SET status = 'draft', decided_at = NULL WHERE id = $1`, entry)
	exec(`UPDATE expenses.entries SET gross_amount = 500.00, vat_amount = NULL WHERE id = $1`, entry)
	exec(`UPDATE expenses.entries SET status = 'approved', decided_at = now() WHERE id = $1`, entry)
	exec(`UPDATE expenses.entries SET reimbursed_at = now(), reimbursement_date = DATE '2026-03-31',
		reimbursement_reference = 'Lønn mars' WHERE id = $1`, entry)
	exec(`UPDATE expenses.entries SET reimbursed_at = NULL, reimbursement_date = NULL, reimbursement_reference = NULL WHERE id = $1`, entry)

	var claim int64
	if err := pool.QueryRow(ctx, `INSERT INTO expenses.claims (user_id, created_by_user_id, purpose, departure_at, return_at,
		status, created_at, updated_at) VALUES (gen_random_uuid(), gen_random_uuid(), 'Bergen', '2026-04-01T06:00:00Z',
		'2026-04-02T20:00:00Z', 'submitted', now(), now()) RETURNING id`).Scan(&claim); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO expenses.entries (user_id, created_by_user_id, claim_id, kind, entry_date, description, currency,
		gross_amount, status, created_at, updated_at) SELECT user_id, user_id, id, 'mileage', DATE '2026-04-01', 'Tur', 'NOK', 530.00,
		'approved', now(), now() FROM expenses.claims WHERE id = $1`, claim)
	exec(`INSERT INTO expenses.entries (user_id, created_by_user_id, claim_id, kind, entry_date, description, currency,
		gross_amount, created_at, updated_at) SELECT user_id, user_id, id, 'per_diem', DATE '2026-04-02', 'Diett', 'NOK', 736.00,
		now(), now() FROM expenses.claims WHERE id = $1`, claim)
	exec(`UPDATE expenses.claims SET status = 'approved', decided_at = now() WHERE id = $1`, claim)

	want := []string{
		fmt.Sprintf("entry.approved:%d", entry), fmt.Sprintf("entry.unapproved:%d", entry),
		fmt.Sprintf("entry.approved:%d", entry), fmt.Sprintf("entry.reimbursed:%d", entry),
		fmt.Sprintf("entry.reimbursement_undone:%d", entry), fmt.Sprintf("claim.approved:%d", claim),
	}
	if got := expenseFacts(t, ctx, pool); !equalStrings(got, want) {
		t.Fatalf("facts = %v, want %v", got, want)
	}
	text := func(sql string, args ...any) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&s); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return s
	}
	if got := text(`SELECT string_agg((lines->0->>'gross') || '/' || coalesce(lines->0->>'vat', '-'), ',' ORDER BY seq)
		FROM expenses.journal_facts WHERE subject = 'entry' AND occurrence = 'approved'`); got != "1250.00/250.00,500.00/-" {
		t.Errorf("approved lines = %s, want each approval's own amount", got)
	}
	if got := text(`SELECT undoes_seq::text FROM expenses.journal_facts WHERE occurrence = 'unapproved'`); got != "1" {
		t.Errorf("the unapproval undoes %s, want 1", got)
	}
	if got := text(`SELECT business_date::text || ' ' || reimbursement_reference || ' ' || coalesce(undoes_seq::text, '')
		FROM expenses.journal_facts WHERE occurrence = 'reimbursed'`); got != "2026-03-31 Lønn mars " {
		t.Errorf("reimbursed = %q", got)
	}
	if got := text(`SELECT business_date::text || ' ' || reimbursement_reference || ' ' || undoes_seq::text
		FROM expenses.journal_facts WHERE occurrence = 'reimbursement_undone'`); got != "2026-03-31 Lønn mars 4" {
		t.Errorf("reimbursement undone = %q", got)
	}
	if got := text(`SELECT business_date::text || ' ' || jsonb_array_length(lines)::text || ' ' ||
		(lines->0->>'kind') || ' ' || (lines->1->>'kind') FROM expenses.journal_facts WHERE subject = 'claim'`); got != "2026-04-02 2 mileage per_diem" {
		t.Errorf("claim fact = %q", got)
	}
	if got := text(`SELECT string_agg(name, ',' ORDER BY id) FROM platform.events WHERE module = 'expenses'`); got !=
		"expenses.entry.approved,expenses.entry.unapproved,expenses.entry.approved,expenses.entry.reimbursed,"+
			"expenses.entry.reimbursement_undone,expenses.claim.approved" {
		t.Errorf("events = %s", got)
	}
	if got := text(`SELECT payload->>'seq' || ' ' || (payload->>'entryId') || ' ' || (payload->>'grossAmount')
		FROM platform.events WHERE name = 'expenses.entry.reimbursed'`); got != fmt.Sprintf("4 %d 500.00", entry) {
		t.Errorf("payload = %s", got)
	}
	// Append-only.
	if _, err := pool.Exec(ctx, `UPDATE expenses.journal_facts SET business_date = DATE '2026-01-01'`); err == nil {
		t.Error("a fact was changed")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM expenses.journal_facts`); err == nil {
		t.Error("a fact was deleted")
	}
}

// Many approvals committing at once take seqs 1 to n with no gap and no
// repeat: the counter is taken at commit, for an instant.
func TestExpensesJournal_SeqsUnderConcurrentApprovals(t *testing.T) {
	t.Parallel()
	pool, ctx := expensesJournalPool(t)
	const n = 24
	ids := make([]int64, n)
	for i := range ids {
		id, err := expenseEntry(ctx, pool, "submitted", nil)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			_, err := pool.Exec(ctx, `UPDATE expenses.entries SET status = 'approved', decided_at = now() WHERE id = $1`, id)
			errs <- err
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var seqs string
	var distinct int
	if err := pool.QueryRow(ctx, `SELECT string_agg(seq::text, ',' ORDER BY seq), count(DISTINCT subject_id)
		FROM expenses.journal_facts`).Scan(&seqs, &distinct); err != nil {
		t.Fatal(err)
	}
	want := ""
	for i := 1; i <= n; i++ {
		if i > 1 {
			want += ","
		}
		want += fmt.Sprint(i)
	}
	if seqs != want || distinct != n {
		t.Fatalf("seqs = %s over %d entries, want 1 to %d over %d", seqs, distinct, n, n)
	}
}

// The backfill: a database with approved and reimbursed expenses and claims
// before 00184 gets every approval and reimbursement in the order they
// happened, an approval first at the same moment, numbered from 1 with the
// counter after the last; a draft makes none. The Down then refuses.
func TestExpensesJournal_TheBackfill(t *testing.T) {
	t.Parallel()
	url := testdb.URL(t)
	migrateTo(t, url, expensesJournalVersion-1)
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	at := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	plus := func(h int) *time.Time { v := at.Add(time.Duration(h) * time.Hour); return &v }
	e1, err := expenseEntry(ctx, pool, "approved", plus(2))
	if err != nil {
		t.Fatal(err)
	}
	e2, err := expenseEntry(ctx, pool, "approved", plus(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE expenses.entries SET reimbursed_at = $2, reimbursement_date = DATE '2026-03-31'
		WHERE id = $1`, e2, plus(3)); err != nil {
		t.Fatal(err)
	}
	if _, err := expenseEntry(ctx, pool, "draft", nil); err != nil {
		t.Fatal(err)
	}
	var claim int64
	if err := pool.QueryRow(ctx, `INSERT INTO expenses.claims (user_id, created_by_user_id, purpose, departure_at, return_at,
		status, decided_at, created_at, updated_at) VALUES (gen_random_uuid(), gen_random_uuid(), 'Bergen', '2026-04-01T06:00:00Z',
		'2026-04-02T20:00:00Z', 'approved', $1, now(), now()) RETURNING id`, plus(2)).Scan(&claim); err != nil {
		t.Fatal(err)
	}

	migrateTo(t, url, expensesJournalVersion)
	want := []string{
		fmt.Sprintf("entry.approved:%d", e2),    // +1h
		fmt.Sprintf("claim.approved:%d", claim), // +2h, claim before entry
		fmt.Sprintf("entry.approved:%d", e1),    // +2h
		fmt.Sprintf("entry.reimbursed:%d", e2),  // +3h
	}
	if got := expenseFacts(t, ctx, pool); !equalStrings(got, want) {
		t.Errorf("backfilled = %v, want %v", got, want)
	}
	var next int64
	if err := pool.QueryRow(ctx, `SELECT next_value FROM expenses.counters WHERE counter_name = 'journal'`).Scan(&next); err != nil || next != 5 {
		t.Errorf("counter = %d (%v), want 5", next, err)
	}
	if err := migrateDown(t, url, expensesJournalVersion-1); !refusedWith(err, "expenses: the expense journal holds facts") {
		t.Errorf("the Down with facts: %v, want it refused", err)
	}
	if got := expenseFacts(t, ctx, pool); len(got) != 4 {
		t.Errorf("after the refused Down: %v", got)
	}
}
