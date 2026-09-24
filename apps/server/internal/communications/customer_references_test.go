package communications_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// repointCommunicationsCustomer runs the module's own holder inside a
// transaction the test owns — the customers merge's position — and commits it
// or rolls it back as told.
func repointCommunicationsCustomer(t *testing.T, h *modtest.Harness, from, into int32, commit bool) []contracts.RepointedReferences {
	t.Helper()
	build := communications.Module().CustomerReferences
	if build == nil {
		t.Fatal("the module declares no customer reference holder")
	}
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	moved, err := build(h.Deps()).RepointCustomer(ctx, tx, from, into)
	if err != nil {
		t.Fatalf("RepointCustomer(%d, %d): %v", from, into, err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	return moved
}

// insertConversationFor writes a conversation row directly with the two
// customer columns the holder re-points; nil leaves one unset.
func insertConversationFor(t *testing.T, h *modtest.Harness, channelID string, customerID, suggestedID *int32) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.Exec(t, `INSERT INTO communications.conversations (id, channel_id, status, customer_id, suggested_customer_id, last_activity_at, created_at)
	           VALUES ($1, $2, 'open', $3, $4, $5, $5)`, id, uuid.MustParse(channelID), customerID, suggestedID, h.Now())
	return id
}

// TestCustomerReferences_RepointsConversationsSuggestionsAndCandidates is
// communications' half of a customer merge (customers merge design D1): both
// customer columns of a conversation, and the candidate list — where a
// conversation that already lists the survivor keeps it once instead of
// colliding with its primary key, and the absorbed customer's row is gone
// either way.
func TestCustomerReferences_RepointsConversationsSuggestionsAndCandidates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	channel := setupChannel(t, h)
	absorbed, survivor := int32(1001), int32(1002)
	associated := insertConversationFor(t, h, channel, &absorbed, nil)
	suggested := insertConversationFor(t, h, channel, &survivor, &absorbed)
	untouched := insertConversationFor(t, h, channel, &survivor, nil)
	both := insertConversationFor(t, h, channel, nil, nil)
	insertCandidate(t, h, both.String(), absorbed)
	insertCandidate(t, h, both.String(), survivor)
	onlyAbsorbed := insertConversationFor(t, h, channel, nil, nil)
	insertCandidate(t, h, onlyAbsorbed.String(), absorbed)

	moved := repointCommunicationsCustomer(t, h, absorbed, survivor, true)

	want := []contracts.RepointedReferences{
		{Kind: "communications.conversations", Count: 1},
		{Kind: "communications.conversationSuggestions", Count: 1},
		{Kind: "communications.conversationCandidates", Count: 2},
	}
	if !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer = %+v, want %+v", moved, want)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM communications.conversations WHERE id = $1`, associated); got != survivor {
		t.Errorf("the associated conversation's customer_id = %d, want %d", got, survivor)
	}
	if got := modtest.One[int32](t, h, `SELECT suggested_customer_id FROM communications.conversations WHERE id = $1`, suggested); got != survivor {
		t.Errorf("the suggestion = %d, want %d", got, survivor)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM communications.conversations WHERE id = $1`, untouched); got != survivor {
		t.Errorf("the survivor's own conversation moved to %d", got)
	}
	for _, conversation := range []uuid.UUID{both, onlyAbsorbed} {
		if n := h.Count(t, `SELECT count(*) FROM communications.conversation_customer_candidates WHERE conversation_id = $1 AND customer_id = $2`, conversation, survivor); n != 1 {
			t.Errorf("conversation %s lists the survivor %d times, want once", conversation, n)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_customer_candidates WHERE customer_id = $1`, absorbed); n != 0 {
		t.Errorf("%d candidate rows still name the absorbed customer", n)
	}
}

// Rolled back, nothing moved: every one of the three statements ran in the
// caller's transaction.
func TestCustomerReferences_WritesOnlyInsideTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	channel := setupChannel(t, h)
	absorbed := int32(1001)
	conversation := insertConversationFor(t, h, channel, &absorbed, &absorbed)
	insertCandidate(t, h, conversation.String(), absorbed)

	moved := repointCommunicationsCustomer(t, h, absorbed, 1002, false)
	for _, m := range moved {
		if m.Count != 1 {
			t.Errorf("%s = %d, want 1", m.Kind, m.Count)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1 AND customer_id = $2 AND suggested_customer_id = $2`, conversation, absorbed); n != 1 {
		t.Error("a rolled-back re-point moved the conversation")
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_customer_candidates WHERE customer_id = $1`, absorbed); n != 1 {
		t.Error("a rolled-back re-point moved the candidate")
	}
}
