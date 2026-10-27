package energy_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The invoiced-work holder's tests (energy consumption billing design D6,
// energy settlements plan reading 11). The holder is built as a disabled
// module's Deps build it — a logger and nothing else — and stamps and
// releases real rows through a transaction on the test database that the
// test rolls back, as Time's and Projects' are.

// stlIssuedAt is the issue's own clock: neither the database's now() nor the
// harness's, so a stamp written with any other clock shows.
var stlIssuedAt = time.Date(2026, 9, 14, 10, 30, 0, 0, time.UTC)

var stlIssuer = uuid.MustParse("6f1c2b0e-8d1d-4b8e-9a51-1d2a3b4c5d6e")

func stlInvoiceRef() contracts.InvoiceRef {
	return contracts.InvoiceRef{
		ID: 7001, Number: 10001,
		IssueDate: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
		IssuedAt:  stlIssuedAt, IssuedBy: stlIssuer,
	}
}

// stlSourceOf is s as a draft invoice would hold it.
func stlSourceOf(s contracts.Settlement) contracts.WorkSource {
	return contracts.WorkSource{Kind: contracts.WorkSourceSettlement, ID: s.ID, Revision: s.Revision, Currency: s.Currency, Amount: s.Amount}
}

// stlLog is a buffer the holder's logger writes into while the test reads.
type stlLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *stlLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *stlLog) warnings() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.buf.String(), `"level":"WARN"`)
}

func newStlHolder(t *testing.T) (contracts.InvoicedWorkHolder, *stlLog) {
	t.Helper()
	build := energy.Module().InvoicedWork
	if build == nil {
		t.Fatal("the energy module declares no InvoicedWork holder")
	}
	log := &stlLog{}
	return build(module.Deps{Logger: slog.New(slog.NewJSONHandler(log, nil))}), log
}

// inTx runs fn in a transaction on the fixture's pool, always rolled back.
func (f stlFixture) inTx(t *testing.T, fn func(ctx context.Context, tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(ctx, tx)
}

// wantRefusal asserts err is a refusal with code.
func wantRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var refusal *contracts.WorkSourceRefusal
	if !errors.As(err, &refusal) || refusal.Code != code {
		t.Errorf("err = %v, want a %s refusal", err, code)
	}
}

func TestInvoicedWork_DeclaresTheSettlementKind(t *testing.T) {
	t.Parallel()
	holder, _ := newStlHolder(t)
	if kinds := holder.Kinds(); len(kinds) != 1 || kinds[0] != contracts.WorkSourceSettlement {
		t.Errorf("Kinds = %v, want [%s]", kinds, contracts.WorkSourceSettlement)
	}
}

// TestInvoicedWork_RefusesInOrder: a missing settlement and a released or
// blocked one are not invoiceable; an invoiced one is already invoiced — each
// judged before any recomputation.
func TestInvoicedWork_RefusesInOrder(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	holder, _ := newStlHolder(t)
	months := func(i int) (time.Time, time.Time) {
		return stlJulyStart.AddDate(0, -i, 0), stlJulyStart.AddDate(0, -i+1, 0)
	}
	f.inTx(t, func(ctx context.Context, tx pgx.Tx) {
		s1, e1 := months(1)
		s2, e2 := months(2)
		s3, e3 := months(3)
		invoiced := f.insertSettlement(t, tx, s1, e1, contracts.SettlementInvoiced, "")
		released := f.insertSettlement(t, tx, s2, e2, contracts.SettlementReleased, "")
		blocked := f.insertSettlement(t, tx, s3, e3, contracts.SettlementDraft, contracts.SettlementIncomplete)
		for _, tc := range []struct {
			name string
			id   int64
			want string
		}{
			{"missing", 999999, contracts.SourceNotInvoiceable},
			{"invoiced", invoiced, contracts.SourceAlreadyInvoiced},
			{"released", released, contracts.SourceNotInvoiceable},
			{"blocked", blocked, contracts.SourceNotInvoiceable},
		} {
			source := contracts.WorkSource{Kind: contracts.WorkSourceSettlement, ID: tc.id, Revision: 1, Currency: "NOK", Amount: "930.00"}
			err := holder.MarkInvoiced(ctx, tx, stlInvoiceRef(), []contracts.WorkSource{source})
			var refusal *contracts.WorkSourceRefusal
			if !errors.As(err, &refusal) || refusal.Code != tc.want {
				t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
			}
		}
		// The first refusal in id order answers, whatever order the sources came in.
		err := holder.MarkInvoiced(ctx, tx, stlInvoiceRef(), []contracts.WorkSource{
			{Kind: contracts.WorkSourceSettlement, ID: released, Revision: 1, Currency: "NOK", Amount: "930.00"},
			{Kind: contracts.WorkSourceSettlement, ID: invoiced, Revision: 1, Currency: "NOK", Amount: "930.00"},
		})
		wantRefusal(t, err, contracts.SourceAlreadyInvoiced)
	})
}

// TestInvoicedWork_StampsAtIssuedAt: a settled draft is stamped invoiced with
// the five stamp columns from the ref, invoiced_at the issue's clock, the
// revision bumped; an amount equal by value in another scale is no change.
func TestInvoicedWork_StampsAtIssuedAt(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	draft := only(t, f.settle(t, stlAugStart))
	holder, _ := newStlHolder(t)
	f.inTx(t, func(ctx context.Context, tx pgx.Tx) {
		source := stlSourceOf(draft)
		source.Amount = "930.000"
		if err := holder.MarkInvoiced(ctx, tx, stlInvoiceRef(), []contracts.WorkSource{source}); err != nil {
			t.Fatalf("MarkInvoiced: %v", err)
		}
		var (
			state             string
			invoiceID, number int64
			invoicedAt        time.Time
			by                *uuid.UUID
			issueDate         time.Time
			revision          int32
			releasedAt        *time.Time
		)
		if err := tx.QueryRow(ctx, `SELECT state, invoiced_invoice_id, invoiced_number, invoiced_at, invoiced_by_user_id,
			invoice_date, revision, released_at FROM energy.settlements WHERE id = $1`, draft.ID).Scan(
			&state, &invoiceID, &number, &invoicedAt, &by, &issueDate, &revision, &releasedAt); err != nil {
			t.Fatalf("read the stamp: %v", err)
		}
		if state != contracts.SettlementInvoiced || invoiceID != 7001 || number != 10001 || !invoicedAt.Equal(stlIssuedAt) ||
			by == nil || *by != stlIssuer || issueDate.Format(time.DateOnly) != "2026-09-14" || revision != draft.Revision+1 || releasedAt != nil {
			t.Errorf("stamp = %s %d %d %v %v %v rev %d released %v; want invoiced 7001 10001 at %v by %v on 2026-09-14 rev %d",
				state, invoiceID, number, invoicedAt, by, issueDate, revision, releasedAt, stlIssuedAt, stlIssuer, draft.Revision+1)
		}
	})

	// An issuer the directory did not know is stamped as nobody.
	f.inTx(t, func(ctx context.Context, tx pgx.Tx) {
		ref := stlInvoiceRef()
		ref.IssuedBy = uuid.Nil
		if err := holder.MarkInvoiced(ctx, tx, ref, []contracts.WorkSource{stlSourceOf(draft)}); err != nil {
			t.Fatalf("MarkInvoiced: %v", err)
		}
		var by *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT invoiced_by_user_id FROM energy.settlements WHERE id = $1`, draft.ID).Scan(&by); err != nil {
			t.Fatalf("read: %v", err)
		}
		if by != nil {
			t.Errorf("invoiced_by_user_id = %v, want NULL for uuid.Nil", *by)
		}
	})
}

// TestInvoicedWork_RefusesWhatChanged: the recomputation differing from the
// stored draft, the revision, the currency or the amount differing from the
// source's, each is source_changed — and nothing is written.
func TestInvoicedWork_RefusesWhatChanged(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	draft := only(t, f.settle(t, stlAugStart))
	holder, _ := newStlHolder(t)
	for _, tc := range []struct {
		name   string
		mutate func(s *contracts.WorkSource)
	}{
		{"revision", func(s *contracts.WorkSource) { s.Revision++ }},
		{"currency", func(s *contracts.WorkSource) { s.Currency = "SEK" }},
		{"amount", func(s *contracts.WorkSource) { s.Amount = "930.01" }},
	} {
		f.inTx(t, func(ctx context.Context, tx pgx.Tx) {
			source := stlSourceOf(draft)
			tc.mutate(&source)
			err := holder.MarkInvoiced(ctx, tx, stlInvoiceRef(), []contracts.WorkSource{source})
			var refusal *contracts.WorkSourceRefusal
			if !errors.As(err, &refusal) || refusal.Code != contracts.SourceChanged {
				t.Errorf("%s: err = %v, want source_changed", tc.name, err)
			}
		})
	}
	f.inTx(t, func(ctx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(ctx, `UPDATE energy.consumption_intervals SET quantity_kwh = 4 WHERE metering_point_id = $1 AND start = $2`,
			f.point, stlJulyStart); err != nil {
			t.Fatalf("change an hour: %v", err)
		}
		err := holder.MarkInvoiced(ctx, tx, stlInvoiceRef(), []contracts.WorkSource{stlSourceOf(draft)})
		wantRefusal(t, err, contracts.SourceChanged)
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM energy.settlements WHERE id = $1`, draft.ID).Scan(&state); err != nil {
			t.Fatalf("read: %v", err)
		}
		if state != contracts.SettlementDraft {
			t.Errorf("after a refusal the settlement is %s, want draft", state)
		}
	})
}

// TestInvoicedWork_ReleaseAndItsTolerance: a release moves the settlement
// carrying the ref's stamp to released at the issue's clock, the stamp kept;
// one not carrying it (a draft, another invoice's) is left and warned about.
func TestInvoicedWork_ReleaseAndItsTolerance(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	holder, log := newStlHolder(t)
	f.inTx(t, func(ctx context.Context, tx pgx.Tx) {
		start, end := stlJulyStart.AddDate(0, -1, 0), stlJulyStart
		ours := f.insertSettlement(t, tx, start, end, contracts.SettlementDraft, "")
		if _, err := tx.Exec(ctx, `UPDATE energy.settlements SET state = 'invoiced', invoiced_invoice_id = 7001, invoiced_number = 10001,
			invoiced_at = $2, invoice_date = '2026-09-14' WHERE id = $1`, ours, stlIssuedAt); err != nil {
			t.Fatalf("stamp by hand: %v", err)
		}
		theirs := f.insertSettlement(t, tx, start.AddDate(0, -1, 0), start, contracts.SettlementInvoiced, "") // invoice 6001
		draft := f.insertSettlement(t, tx, start.AddDate(0, -2, 0), start.AddDate(0, -1, 0), contracts.SettlementDraft, "")

		releasedAt := stlIssuedAt.Add(48 * time.Hour)
		ref := stlInvoiceRef()
		ref.IssuedAt = releasedAt
		sources := []contracts.WorkSource{
			{Kind: contracts.WorkSourceSettlement, ID: draft}, {Kind: contracts.WorkSourceSettlement, ID: ours},
			{Kind: contracts.WorkSourceSettlement, ID: theirs}, {Kind: contracts.WorkSourceSettlement, ID: 999999},
		}
		if err := holder.ReleaseInvoiced(ctx, tx, ref, sources); err != nil {
			t.Fatalf("ReleaseInvoiced: %v", err)
		}
		state := func(id int64) (string, *time.Time, *int64) {
			var s string
			var at *time.Time
			var invoice *int64
			if err := tx.QueryRow(ctx, `SELECT state, released_at, invoiced_invoice_id FROM energy.settlements WHERE id = $1`, id).Scan(&s, &at, &invoice); err != nil {
				t.Fatalf("read %d: %v", id, err)
			}
			return s, at, invoice
		}
		if s, at, invoice := state(ours); s != contracts.SettlementReleased || at == nil || !at.Equal(releasedAt) || invoice == nil || *invoice != 7001 {
			t.Errorf("ours = %s at %v invoice %v, want released at %v with the stamp kept", s, at, invoice, releasedAt)
		}
		if s, _, _ := state(theirs); s != contracts.SettlementInvoiced {
			t.Errorf("another invoice's settlement is %s, want invoiced still", s)
		}
		if s, _, _ := state(draft); s != contracts.SettlementDraft {
			t.Errorf("a draft is %s, want draft still", s)
		}
		if n := log.warnings(); n != 3 {
			t.Errorf("warnings = %d, want 3 (the draft, the other invoice's, the missing one)", n)
		}
	})
}

// TestInvoicedWork_HoldsItsLocksWhenItJudges: both directions lock every
// source settlement (FOR NO KEY UPDATE, id ascending) before they judge or
// write: at the seam, another connection cannot take any of them. Not
// parallel: the seam is the package's.
func TestInvoicedWork_HoldsItsLocksWhenItJudges(t *testing.T) {
	f := newStlFixture(t, true)
	holder, _ := newStlHolder(t)
	var ids []int64
	calls := 0
	defer energy.SetInvoicedWorkAfterLock(func(ctx context.Context) {
		calls++
		_, err := f.h.Pool().Exec(ctx, `SELECT id FROM energy.settlements WHERE id = ANY($1) FOR NO KEY UPDATE NOWAIT`, ids)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			t.Errorf("another connection locking the settlements at the seam: err %v, want 55P03 lock_not_available", err)
		}
	})()
	f.inTx(t, func(ctx context.Context, tx pgx.Tx) {
		// Committed, so the other connection sees the rows it must not lock.
		a := f.insertSettlement(t, f.h.Pool(), stlJulyStart.AddDate(0, -2, 0), stlJulyStart.AddDate(0, -1, 0), contracts.SettlementInvoiced, "")
		b := f.insertSettlement(t, f.h.Pool(), stlJulyStart.AddDate(0, -1, 0), stlJulyStart, contracts.SettlementInvoiced, "")
		ids = []int64{a, b}
		ref := stlInvoiceRef()
		ref.ID = 6001
		_ = holder.MarkInvoiced(ctx, tx, ref, []contracts.WorkSource{{Kind: contracts.WorkSourceSettlement, ID: b}, {Kind: contracts.WorkSourceSettlement, ID: a}})
		if err := holder.ReleaseInvoiced(ctx, tx, ref, []contracts.WorkSource{{Kind: contracts.WorkSourceSettlement, ID: b}, {Kind: contracts.WorkSourceSettlement, ID: a}}); err != nil {
			t.Fatalf("ReleaseInvoiced: %v", err)
		}
	})
	if calls != 2 {
		t.Errorf("the seam ran %d times, want once per direction", calls)
	}
}

// TestInvoicedWork_RefusesAForeignKind: a source of another kind is the
// caller's bug, an error and not a refusal.
func TestInvoicedWork_RefusesAForeignKind(t *testing.T) {
	t.Parallel()
	holder, _ := newStlHolder(t)
	err := holder.MarkInvoiced(context.Background(), nil, stlInvoiceRef(), []contracts.WorkSource{{Kind: contracts.WorkSourceHours, ID: 1}})
	var refusal *contracts.WorkSourceRefusal
	if err == nil || errors.As(err, &refusal) {
		t.Errorf("err = %v, want a plain error", err)
	}
}

// TestInvoicedWork_JudgesADraftByItsSupplyPeriod: the holder judges a draft
// over its supply period as it stands. The end moved to 15 July: July is
// another period now, source_changed. The supply period cancelled: July is no
// longer a period at all, source_not_invoiceable.
func TestInvoicedWork_JudgesADraftByItsSupplyPeriod(t *testing.T) {
	t.Parallel()
	f := newStlFixture(t, true)
	draft := only(t, f.settle(t, stlAugStart))
	holder, _ := newStlHolder(t)
	for _, tc := range []struct {
		name, sql, want string
		arg             any
	}{
		{"the end moved", `UPDATE energy.supply_periods SET "end" = $2 WHERE id = $1`, contracts.SourceChanged,
			time.Date(2026, time.July, 15, 0, 0, 0, 0, stlOslo)},
		{"cancelled", `UPDATE energy.supply_periods SET status = $2 WHERE id = $1`, contracts.SourceNotInvoiceable, "Cancelled"},
	} {
		f.inTx(t, func(ctx context.Context, tx pgx.Tx) {
			if _, err := tx.Exec(ctx, tc.sql, f.supply, tc.arg); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			err := holder.MarkInvoiced(ctx, tx, stlInvoiceRef(), []contracts.WorkSource{stlSourceOf(draft)})
			var refusal *contracts.WorkSourceRefusal
			if !errors.As(err, &refusal) || refusal.Code != tc.want {
				t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
			}
		})
	}
}
