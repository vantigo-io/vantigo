package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// communicationsContactHintsVersion is 00134_communications_contact_hints.sql:
// the contact a Messenger participant is suggested for and the contact whose
// link it used (Messenger design N20, N21).
const communicationsContactHintsVersion = 134

// TestCommunicationsContactHints_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, that a hint row holds something,
// and that it goes with its participant.
func TestCommunicationsContactHints_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, communicationsContactHintsVersion)
	migrateTo(t, url, communicationsContactHintsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	channel := insertTestChannel(t, ctx, pool)
	participant := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.participants (id, channel_id, address, created_at) VALUES ($1, $2, '4242', now())`, participant, channel); err != nil {
		t.Fatalf("insert participant: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.participant_contact_hints (participant_id, updated_at) VALUES ($1, now())`, participant); err == nil {
		t.Error("an empty hint row was admitted, want the CHECK to refuse it")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.participant_contact_hints (participant_id, suggested_contact_id, updated_at) VALUES ($1, 2001, now())`, participant); err != nil {
		t.Fatalf("a suggestion: %v, want admitted", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.participant_contact_hints (participant_id, referral_contact_id, updated_at) VALUES ($1, 2002, now())`, participant); err == nil {
		t.Error("a second row for one participant was admitted, want one row per participant")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM communications.participants WHERE id = $1`, participant); err != nil {
		t.Fatalf("delete participant: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM communications.participant_contact_hints`).Scan(&n); err != nil || n != 0 {
		t.Errorf("hint rows = %d (%v) after the participant went, want 0", n, err)
	}
}
