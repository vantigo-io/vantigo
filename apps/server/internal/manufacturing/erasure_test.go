package manufacturing_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestErasureBlanksAnErasedOperatorsNotes: the sweep blanks the notes of a
// user identity no longer knows and keeps the log, its operator's id and
// display name; a living user's and a disabled user's notes stay.
func TestErasureBlanksAnErasedOperatorsNotes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, living := h.SignInUser(t, planner...)
	_ = h.SignInDisabled(t)
	disabled := modtest.One[uuid.UUID](t, h.Harness, `SELECT id FROM identity.users WHERE is_disabled LIMIT 1`)
	gone := uuid.New() // a user identity no longer has
	wc := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	now := h.Now()
	for _, u := range []uuid.UUID{living, disabled, gone, gone} {
		h.Exec(t, `INSERT INTO manufacturing.time_logs (work_center_id, user_id, user_display, kind, reason, note, started_at, ended_at)
			VALUES ($1, $2, 'Operator', 'downtime', 'breakdown', 'my own words', $3, $3)`, wc.Id, u, now)
	}

	blanked, err := manufacturing.ErasureCycle(context.Background(), h.Deps())
	if err != nil || blanked != 2 {
		t.Fatalf("ErasureCycle = %d, %v, want the erased operator's two notes", blanked, err)
	}
	if n := modtest.One[int](t, h.Harness, `SELECT count(*) FROM manufacturing.time_logs WHERE user_id = $1 AND note = '' AND user_display = 'Operator'`, gone); n != 2 {
		t.Errorf("%d of the erased operator's logs kept with a blank note, want 2", n)
	}
	if n := modtest.One[int](t, h.Harness, `SELECT count(*) FROM manufacturing.time_logs WHERE note = 'my own words'`); n != 2 {
		t.Errorf("%d notes kept, want the living and the disabled user's", n)
	}
	if blanked, err := manufacturing.ErasureCycle(context.Background(), h.Deps()); err != nil || blanked != 0 {
		t.Errorf("a second cycle = %d, %v, want nothing", blanked, err)
	}
}
