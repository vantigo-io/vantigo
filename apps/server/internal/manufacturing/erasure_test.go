package manufacturing_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestErasureBlanksAnErasedOperatorsNotes: the sweep blanks the notes whose
// writer (note_by) identity no longer knows and keeps the log, its
// operator's id and display name; a living writer's and a disabled writer's
// notes stay, and so does a note an erased operator's log carries in a
// living supervisor's words.
func TestErasureBlanksAnErasedOperatorsNotes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, living := h.SignInUser(t, planner...)
	_ = h.SignInDisabled(t)
	disabled := modtest.One[uuid.UUID](t, h.Harness, `SELECT id FROM identity.users WHERE is_disabled LIMIT 1`)
	gone := uuid.New() // a user identity no longer has
	wc := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	now := h.Now()
	for _, writer := range []uuid.UUID{living, disabled, gone, gone} {
		h.Exec(t, `INSERT INTO manufacturing.time_logs (work_center_id, user_id, user_display, kind, reason, note, note_by, note_by_display, started_at, ended_at)
			VALUES ($1, $2, 'Operator', 'downtime', 'breakdown', 'my own words', $3, 'Writer', $4, $4)`, wc.Id, gone, writer, now)
	}

	blanked, err := manufacturing.ErasureCycle(context.Background(), h.Deps())
	if err != nil || blanked != 2 {
		t.Fatalf("ErasureCycle = %d, %v, want the erased writer's two notes", blanked, err)
	}
	if n := modtest.One[int](t, h.Harness, `SELECT count(*) FROM manufacturing.time_logs WHERE note_by = $1 AND note = '' AND user_display = 'Operator' AND note_by_display = 'Writer'`, gone); n != 2 {
		t.Errorf("%d of the erased writer's logs kept with a blank note, want 2", n)
	}
	if n := modtest.One[int](t, h.Harness, `SELECT count(*) FROM manufacturing.time_logs WHERE note = 'my own words'`); n != 2 {
		t.Errorf("%d notes kept, want the living and the disabled writer's", n)
	}
	if blanked, err := manufacturing.ErasureCycle(context.Background(), h.Deps()); err != nil || blanked != 0 {
		t.Errorf("a second cycle = %d, %v, want nothing", blanked, err)
	}
}

// TestErasureBlanksABlockersAndASupervisorsNotes is M2's writers: a block's
// note is the blocker's on every log it opens, the center's own one naming
// who blocked it, and a correction's is the supervisor's; neither is copied
// into the order's timeline. Once identity no longer knows them, the sweep
// blanks their notes and leaves the operator's logs as they are.
func TestErasureBlanksABlockersAndASupervisorsNotes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, supervisor := h.SignInUser(t, planner...)
	f := newFloor(t, h, c)
	o, wos := openOrder(t, c, "1")
	op, operatorID := h.SignInUser(t, operator...)
	lead, blocker := h.SignInUser(t, operator...)
	act(t, op, wos[0].Id, "start", nil)
	h.Advance(time.Hour)

	res := lead.Do(http.MethodPost, workCenterPath(f.saw.Id, "block"), gen.ManufacturingBlockInput{Reason: gen.Breakdown, Note: str("Belt snapped")})
	mustStatus(t, res, http.StatusOK, "block")
	var q gen.ManufacturingWorkCenterQueue
	res.JSON(&q)
	if q.Block == nil || q.Block.By == nil || q.Block.By.Id != blocker {
		t.Fatalf("the block does not name its blocker: %+v", q.Block)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.time_logs WHERE note = 'Belt snapped' AND note_by = $1`, blocker); n != 2 {
		t.Fatalf("%d of the block's logs carry the blocker's note, want the operator's and the center's", n)
	}
	mustStatus(t, lead.Do(http.MethodPost, workCenterPath(f.saw.Id, "unblock"), nil), http.StatusOK, "unblock")

	first := logsOfOrder(t, c, o.Id)[0]
	fix := gen.ManufacturingTimeLogCorrection{StartedAt: first.StartedAt, EndedAt: first.StartedAt.Add(30 * time.Minute), Note: "Left early"}
	mustStatus(t, c.Do(http.MethodPatch, base+fmt.Sprintf("/time-logs/%d", first.Id), fix), http.StatusOK, "correct")
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.time_logs WHERE note = 'Left early' AND note_by = $1 AND user_id = $2`, supervisor, operatorID); n != 1 {
		t.Fatalf("the correction's note is not the supervisor's")
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.order_events WHERE order_id = $1 AND payload ? 'note'`, o.Id); n != 0 {
		t.Errorf("%d timeline events copy a note", n)
	}

	// Identity forgets the blocker and the supervisor.
	h.Exec(t, `UPDATE manufacturing.time_logs SET note_by = $1 WHERE note_by IN ($2, $3)`, uuid.New(), blocker, supervisor)
	blanked, err := manufacturing.ErasureCycle(context.Background(), h.Deps())
	if err != nil || blanked != 3 {
		t.Fatalf("ErasureCycle = %d, %v, want the block's two notes and the correction's", blanked, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.time_logs WHERE note <> ''`); n != 0 {
		t.Errorf("%d notes left", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM manufacturing.time_logs WHERE user_id = $1`, operatorID); n != 2 {
		t.Errorf("the operator's logs = %d, want their productive and downtime logs kept", n)
	}
}

// TestErasureSkipsALockedLog is L2: the sweep skips a log another
// transaction holds rather than wait for it, and blanks it the next cycle.
func TestErasureSkipsALockedLog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, planner...)
	wc := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	gone := uuid.New()
	now := h.Now()
	for range 2 {
		h.Exec(t, `INSERT INTO manufacturing.time_logs (work_center_id, kind, reason, note, note_by, started_at)
			VALUES ($1, 'downtime', 'breakdown', 'my own words', $2, $3)`, wc.Id, gone, now)
	}
	ctx := context.Background()
	tx, err := h.Pool().BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM manufacturing.time_logs WHERE note_by = $1 ORDER BY id LIMIT 1 FOR UPDATE`, gone); err != nil {
		t.Fatal(err)
	}
	if blanked, err := manufacturing.ErasureCycle(ctx, h.Deps()); err != nil || blanked != 1 {
		t.Fatalf("ErasureCycle beside a held log = %d, %v, want the other one", blanked, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if blanked, err := manufacturing.ErasureCycle(ctx, h.Deps()); err != nil || blanked != 1 {
		t.Errorf("the next cycle = %d, %v, want the log skipped before", blanked, err)
	}
}
