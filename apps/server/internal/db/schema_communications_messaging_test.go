package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// communicationsMessagingVersion is 00130_communications_messaging.sql:
// message components, the attachment role, the messenger sync's state and
// the progress of a message sent as parts, and when a participant's profile was
// read (Messenger design N3, N4, N7).
const communicationsMessagingVersion = 130

// TestCommunicationsMessaging_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, adds what the design names, and
// that the down takes it all away.
func TestCommunicationsMessaging_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, communicationsMessagingVersion)
	migrateTo(t, url, communicationsMessagingVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := func() []string {
		return staffCollect(t, ctx, pool, "the new columns", `
			SELECT table_name || '.' || column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':' || is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'communications'
			  AND ((table_name = 'conversation_messages' AND column_name = 'components')
			    OR (table_name = 'message_attachments' AND column_name = 'role'))
			ORDER BY table_name, column_name`)
	}
	if got, want := columns(), []string{
		"conversation_messages.components:jsonb:YES",
		"message_attachments.role:character varying(10):NO",
	}; !equalStrings(got, want) {
		t.Errorf("columns = %v, want %v", got, want)
	}

	// The attachment role admits file and card only, and defaults to file.
	channel := insertTestChannel(t, ctx, pool)
	conv := insertTestConversation(t, ctx, pool, channel)
	message := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.conversation_messages (id, conversation_id, direction, components, occurred_at, created_at)
		VALUES ($1, $2, 'outbound', '{"shareEmail": true}'::jsonb, now(), now())`, message, conv); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	attach := func(role string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO communications.message_attachments
			 (id, message_id, file_name, content_type, size_bytes, content_hash, storage_key, is_inline, created_at, role)
			VALUES ($1, $2, 'a.png', 'image/png', 1, 'h', 'k', false, now(), $3)`, uuid.New(), message, role)
		return err
	}
	if err := attach("card"); err != nil {
		t.Errorf("a card attachment: %v, want admitted", err)
	}
	if err := attach("banner"); err == nil {
		t.Error("an attachment of role banner was admitted, want ck_message_attachments_role")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.message_attachments
		 (id, message_id, file_name, content_type, size_bytes, content_hash, storage_key, is_inline, created_at)
		VALUES ($1, $2, 'b.png', 'image/png', 1, 'h', 'k2', false, now())`, uuid.New(), message); err != nil {
		t.Fatalf("insert an attachment with no role: %v", err)
	}
	if got := staffCollect(t, ctx, pool, "the default role", `
		SELECT role FROM communications.message_attachments WHERE file_name = 'b.png'`); !equalStrings(got, []string{"file"}) {
		t.Errorf("default role = %v, want file", got)
	}

	// The messenger sync's state is active, failed or stopped.
	syncRow := func(state string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO communications.messenger_sync (channel_id, next_sync_at, state, updated_at)
			VALUES ($1, now(), $2, now())`, channel, state)
		return err
	}
	if err := syncRow("pending"); err == nil {
		t.Error("a messenger_sync row in state pending was admitted, want ck_messenger_sync_state")
	}
	if err := syncRow("active"); err != nil {
		t.Errorf("a messenger_sync row in state active: %v", err)
	}

	// A message's parts are keyed by (message, index), and go with the message.
	part := func(index int) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO communications.messenger_parts (message_id, part_index, attempted_at)
			VALUES ($1, $2, now())`, message, index)
		return err
	}
	if err := part(0); err != nil {
		t.Fatalf("a first part: %v", err)
	}
	if err := part(0); err == nil {
		t.Error("a second part 0 of one message was admitted, want the primary key")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM communications.conversation_messages WHERE id = $1`, message); err != nil {
		t.Fatalf("delete message: %v", err)
	}
	if got := staffCollect(t, ctx, pool, "the parts after the message's deletion", `
		SELECT part_index::text FROM communications.messenger_parts WHERE message_id = $1`, message); len(got) != 0 {
		t.Errorf("parts after the message's deletion = %v, want none: they cascade", got)
	}

	// A participant's profile read goes with the participant.
	participant := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.participants (id, channel_id, address, created_at)
		VALUES ($1, $2, '4242', now())`, participant, channel); err != nil {
		t.Fatalf("insert participant: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO communications.participant_profiles (participant_id, fetched_at) VALUES ($1, now())`, participant); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM communications.participants WHERE id = $1`, participant); err != nil {
		t.Fatalf("delete participant: %v", err)
	}
	if got := staffCollect(t, ctx, pool, "the profile after the participant's deletion", `
		SELECT participant_id::text FROM communications.participant_profiles WHERE participant_id = $1`, participant); len(got) != 0 {
		t.Errorf("profiles after the participant's deletion = %v, want none: they cascade", got)
	}

	migrateTo(t, url, communicationsMessagingVersion-1)
	if got := columns(); len(got) != 0 {
		t.Errorf("columns after the down = %v, want none", got)
	}
	if got := staffCollect(t, ctx, pool, "the tables after the down", `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'communications' AND table_name IN ('messenger_sync', 'messenger_parts', 'participant_profiles')`); len(got) != 0 {
		t.Errorf("tables after the down = %v, want none", got)
	}
}
