package communications_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the tags half of task 14's unique-constraint audit. Two
// constraints live here and neither had ever been examined concurrently:
// ux_tags_name (tag create) and conversation_tags' composite primary key
// (tag add). The second is the one worth the most: adding a tag to a
// conversation is a natural double-submit — a double-clicked chip, a retried
// PUT — so a primary-key collision there is not a theoretical race.
//
// Both came back defended, and both tests are kept anyway: the audit's value
// is that the defence is now pinned rather than incidental, and a
// well-meaning "simplify" of either (dropping ON CONFLICT DO NOTHING for a
// read-then-insert, or the 409 mapping for a bare insert) fails a test
// instead of shipping.
//
// The gate is this package's established technique (channels_concurrency_test.go's
// race/awaitLockWaiters, reused rather than redeclared): EXCLUSIVE on the
// target table is compatible with each request's own ACCESS SHARE reads and
// conflicts with the ROW EXCLUSIVE its write needs, so every request reaches
// its write with its decision already made and parks there until
// awaitLockWaiters confirms every backend is genuinely blocked — never a
// sleep, which passes vacuously under exactly the load that makes a race
// bite.

// TestCreateTag_ConcurrentSameNameAnswersModuleShape drives four creates of
// one name at ux_tags_name simultaneously. tags.go already maps the
// violation to 409 tag_exists, so this is a confirmation, not a fix — but it
// confirms the whole shape, including that the loser's body is this module's
// {"error":{"code","message"}} on application/json and not the host-wide
// 23505 fallback's bare RFC 7807 problem, which is the exact way the other
// four constraint defects in this module presented.
func TestCreateTag_ConcurrentSameNameAnswersModuleShape(t *testing.T) {
	h := newHarness(t)
	name := "race-" + uuid.NewString()

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.tags IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock tags: %v", err)
	}

	const n = 4
	fns := make([]func() *modtest.Response, n)
	for i := 0; i < n; i++ {
		c := h.SignIn(t, "communications:conversations-manage")
		fns[i] = func() *modtest.Response {
			return c.Do(http.MethodPost, "/api/v1/communications/tags", map[string]any{"name": name})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(fns...)
		close(finished)
	}()
	awaitLockWaiters(t, h, n, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	var created, conflicted int
	for _, r := range responses {
		switch r.Status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicted++
			if ct := r.Header("Content-Type"); ct != "application/json" {
				t.Errorf("loser Content-Type = %q, want application/json (this module's own vocabulary)", ct)
			}
			var body commErrorJSON
			r.JSON(&body)
			if body.Error.Code != "tag_exists" {
				t.Errorf("loser code = %q, want tag_exists", body.Error.Code)
			}
		default:
			t.Errorf("status %d body %s, want 201 or 409", r.Status, r.Body)
		}
	}
	if created != 1 || conflicted != n-1 {
		t.Errorf("created = %d, conflicted = %d, want exactly 1 and %d", created, conflicted, n-1)
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.tags WHERE name = $1`, name); got != 1 {
		t.Errorf("tags with the raced name = %d, want exactly 1", got)
	}
}

// TestAddTagLink_ConcurrentDoubleSubmitIsAll204 is the composite primary key
// the brief singled out: tag add/remove is a natural double-submit and
// nothing had ever tested it concurrently. PUT is documented as idempotent
// (inventory §1.4: 204 whether or not the tag was already linked), so a
// second simultaneous PUT must be a 204 too — not a 409, and certainly not
// the bare problem+json a raw conversation_tags_pkey violation would
// produce. queries/tags.sql's ON CONFLICT DO NOTHING is what makes that
// true; this test is what keeps it true.
func TestAddTagLink_ConcurrentDoubleSubmitIsAll204(t *testing.T) {
	h := newHarness(t)
	convID, tagID := tagLinkFixture(t, h)
	path := "/api/v1/communications/conversations/" + convID + "/tags/" + tagID

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.conversation_tags IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock conversation_tags: %v", err)
	}

	const n = 4
	fns := make([]func() *modtest.Response, n)
	for i := 0; i < n; i++ {
		c := h.SignIn(t, "communications:conversations-manage")
		fns[i] = func() *modtest.Response { return c.Do(http.MethodPut, path, nil) }
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(fns...)
		close(finished)
	}()
	awaitLockWaiters(t, h, n, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	for i, r := range responses {
		if r.Status != http.StatusNoContent {
			t.Errorf("request %d: status %d body %s, want 204: PUT is idempotent, concurrently too",
				i, r.Status, r.Body)
		}
	}
	if got := h.Count(t, `SELECT count(*) FROM communications.conversation_tags WHERE conversation_id = $1 AND tag_id = $2`,
		uuid.MustParse(convID), uuid.MustParse(tagID)); got != 1 {
		t.Errorf("links = %d, want exactly 1", got)
	}
}
