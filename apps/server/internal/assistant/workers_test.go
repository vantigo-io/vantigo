package assistant_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// seedConversation writes a conversation of userID last active at `at`, with a
// message, a run and an action, as the module would have left them.
func seedConversation(t *testing.T, h *modtest.Harness, userID uuid.UUID, at time.Time, state string) uuid.UUID {
	t.Helper()
	id, run := uuid.New(), uuid.New()
	h.Exec(t, `INSERT INTO assistant.conversations (id, user_id, title, origin_app, origin_route, created_at, last_activity_at)
	           VALUES ($1, $2, 't', 'customers', '/customers', $3, $3)`, id, userID, at)
	h.Exec(t, `INSERT INTO assistant.runs (id, conversation_id, user_id, state, started_at) VALUES ($1, $2, $3, $4, $5)`, run, id, userID, state, at)
	h.Exec(t, `INSERT INTO assistant.messages (id, conversation_id, run_id, role, content, created_at) VALUES ($1, $2, $3, 'user', '[]', $4)`, uuid.New(), id, run, at)
	h.Exec(t, `INSERT INTO assistant.actions (id, run_id, operation_id, mode, duration_ms, created_at) VALUES ($1, $2, 'tool:search_docs', 'tool', 1, $3)`, uuid.New(), run, at)
	return id
}

func TestWorkers_StaleRunsAreFailedAsServerRestarted(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	_, user := person(t, h)
	old := seedConversation(t, h, user, h.Now().Add(-11*time.Minute), "running")
	fresh := seedConversation(t, h, user, h.Now().Add(-2*time.Minute), "running")

	n, err := assistant.StaleRunsCycle(context.Background(), h.Deps())
	if err != nil || n != 1 {
		t.Fatalf("StaleRunsCycle = %d, %v, want 1", n, err)
	}
	if got := modtest.One[string](t, h, `SELECT state || ':' || error_code FROM assistant.runs WHERE conversation_id = $1`, old); got != "failed:server_restarted" {
		t.Errorf("old run = %s", got)
	}
	if got := modtest.One[string](t, h, `SELECT state FROM assistant.runs WHERE conversation_id = $1`, fresh); got != "running" {
		t.Errorf("a fresh run was touched: %s", got)
	}
}

func TestWorkers_RetentionDeletesWholeConversationsByLastActivity(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	o := owner(t, h)
	_, user := person(t, h)
	now := h.Now()
	expired := seedConversation(t, h, user, now.AddDate(0, -13, 0), "done")
	kept := seedConversation(t, h, user, now.AddDate(0, -6, 0), "done")

	expiredN, _, err := assistant.RetentionCycle(context.Background(), h.Deps())
	if err != nil || expiredN != 1 {
		t.Fatalf("RetentionCycle = %d, %v, want 1 (the default is 12 months)", expiredN, err)
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.conversations WHERE id = $1`, expired); n != 0 {
		t.Error("the expired conversation was kept")
	}
	for _, q := range []string{"messages", "runs"} {
		if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.`+q+` WHERE conversation_id = $1`, expired); n != 0 {
			t.Errorf("%s of the expired conversation were kept", q)
		}
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.actions`); n != 1 {
		t.Errorf("%d actions left, want only the kept conversation's", n)
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.conversations WHERE id = $1`, kept); n != 1 {
		t.Error("the recent conversation was deleted")
	}

	// A shorter retention takes the next one.
	r := o.Do("PUT", "/api/v1/assistant/settings", map[string]any{"retentionMonths": 3}, ifMatch(1))
	if r.Status != 200 {
		t.Fatalf("set retention: %d %s", r.Status, r.Body)
	}
	if n, _, err := assistant.RetentionCycle(context.Background(), h.Deps()); err != nil || n != 1 {
		t.Errorf("after shortening the retention = %d, %v, want 1", n, err)
	}
}

func TestWorkers_ErasedPeopleLeaveNothingBehind(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel())
	_, living := person(t, h)
	disabledClient := h.SignInDisabled(t)
	_ = disabledClient
	gone := uuid.New() // a user identity no longer has
	now := h.Now()

	seedConversation(t, h, living, now, "done")
	keptDisabled := modtest.One[uuid.UUID](t, h, `SELECT id FROM identity.users WHERE is_disabled LIMIT 1`)
	seedConversation(t, h, keptDisabled, now, "done")
	seedConversation(t, h, gone, now, "done")
	seedConversation(t, h, gone, now, "done")
	h.Exec(t, `INSERT INTO assistant.person_settings (user_id, enabled, version, updated_at) VALUES ($1, true, 1, $2)`, gone, now)

	_, erased, err := assistant.RetentionCycle(context.Background(), h.Deps())
	if err != nil || erased != 1 {
		t.Fatalf("RetentionCycle erased = %d, %v, want the one erased person", erased, err)
	}
	for _, q := range []string{"conversations", "person_settings", "runs"} {
		if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.`+q+` WHERE user_id = $1`, gone); n != 0 {
			t.Errorf("%d rows of the erased person left in %s", n, q)
		}
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.messages m JOIN assistant.conversations c ON c.id = m.conversation_id WHERE c.user_id = $1`, gone); n != 0 {
		t.Errorf("%d messages of the erased person left", n)
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM assistant.conversations`); n != 2 {
		t.Errorf("%d conversations left, want the living's and the disabled user's", n)
	}
}

// The retention lease is held on a pooled connection, so a cycle that asked
// the pool for a second connection while holding it would wait for ever on a
// pool of one — and, with as many such workers as connections, deadlock the
// installation's pool at boot (the image smoke test's readiness check on a
// four-core runner). On a pool of one the cycle must finish.
func TestWorkers_RetentionNeedsOneConnection(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.NewFakeChatModel(), modtest.WithPoolMaxConns(1))
	_, user := person(t, h)
	seedConversation(t, h, user, h.Now().AddDate(0, -13, 0), "done")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	expired, _, err := assistant.RetentionCycle(ctx, h.Deps())
	if err != nil || expired != 1 {
		t.Fatalf("RetentionCycle on a pool of one = %d, %v, want 1 and no error", expired, err)
	}
}
