package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// communicationsMailboxMarksVersion is 00094_communications_mailbox_marks.sql,
// the outbox-style queue for marking or moving a conversation's inbound
// messages in Outlook when the conversation closes
// (docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md,
// M11, M12, M17).
const communicationsMailboxMarksVersion = 94

// TestCommunicationsMailboxMarks_AppliesAndIsIdempotent proves migration 00094
// applies, rolls back and re-applies cleanly: communications.mailbox_marks with
// its columns, defaults, operation and status CHECKs, due partial index, and
// ON DELETE CASCADE from communications.conversations.
func TestCommunicationsMailboxMarks_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, communicationsMailboxMarksVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := staffCollect(t, ctx, pool, "communications.mailbox_marks columns", `
		SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
		    || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'communications' AND table_name = 'mailbox_marks'
		ORDER BY ordinal_position`)
	if want := []string{
		"conversation_id:uuid:NO:",
		"operation:character varying(20):NO:",
		"folder:character varying(120):YES:",
		"status:character varying(20):NO:'pending'::character varying",
		"attempts:integer:NO:0",
		"next_attempt_at:timestamp with time zone:NO:",
		"queued_at:timestamp with time zone:NO:",
		"completed_at:timestamp with time zone:YES:",
		"last_error:character varying(40):YES:",
		"lease_id:character varying(32):YES:",
		"lease_until:timestamp with time zone:YES:",
	}; !equalStrings(columns, want) {
		t.Errorf("communications.mailbox_marks columns =\n%s\nwant\n%s", strings.Join(columns, "\n"), strings.Join(want, "\n"))
	}
	if cols := primaryKeyColumns(t, ctx, pool, "communications", "mailbox_marks"); !equalStrings(cols, []string{"conversation_id"}) {
		t.Errorf("communications.mailbox_marks primary key = %v, want [conversation_id]", cols)
	}
	if got := staffCollect(t, ctx, pool, "mailbox_marks indexes", `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = 'communications' AND tablename = 'mailbox_marks' AND indexname NOT LIKE '%_pkey'
		ORDER BY indexname`); !equalStrings(got, []string{"ix_mailbox_marks_due"}) {
		t.Errorf("communications.mailbox_marks indexes = %v, want [ix_mailbox_marks_due]", got)
	}

	channel := insertTestChannel(t, ctx, pool)
	insertMark := func(convID uuid.UUID, op, status string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO communications.mailbox_marks (conversation_id, operation, status, next_attempt_at, queued_at)
			VALUES ($1, $2, $3, now(), now())`, convID, op, status)
		return err
	}
	conv := insertTestConversation(t, ctx, pool, channel)
	if err := insertMark(conv, "archive", "pending"); !checkViolationOf(err, "ck_mailbox_marks_operation") {
		t.Errorf("operation archive: %v, want ck_mailbox_marks_operation", err)
	}
	if err := insertMark(conv, "read", "queued"); !checkViolationOf(err, "ck_mailbox_marks_status") {
		t.Errorf("status queued: %v, want ck_mailbox_marks_status", err)
	}
	for _, op := range []string{"read", "move"} {
		for _, status := range []string{"pending", "processing", "retry", "completed", "failed"} {
			c := insertTestConversation(t, ctx, pool, channel)
			if err := insertMark(c, op, status); err != nil {
				t.Errorf("mark (%s, %s): %v", op, status, err)
			}
		}
	}

	// ON DELETE CASCADE from conversations.
	if err := insertMark(conv, "read", "pending"); err != nil {
		t.Fatalf("insert mark for cascade check: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM communications.conversations WHERE id = $1`, conv); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	if got := staffCollect(t, ctx, pool, "mark after conversation delete", `
		SELECT conversation_id::text FROM communications.mailbox_marks WHERE conversation_id = $1`, conv); len(got) != 0 {
		t.Errorf("mark after conversation delete = %v, want none", got)
	}

	migrateTo(t, url, communicationsMailboxMarksVersion-1)
	if got := staffCollect(t, ctx, pool, "table after down", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'communications' AND table_name = 'mailbox_marks'`); len(got) != 0 {
		t.Errorf("communications.mailbox_marks after down = %v, want none", got)
	}
}
