package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// communicationsProviderNeutralVersion is
// 00111_communications_provider_neutral.sql: one channel per mailbox per
// provider, the provider's thread under a neutral key in either direction,
// send_provider and sent_message_id (Google Workspace design G3, G4, G5).
const communicationsProviderNeutralVersion = 111

// TestCommunicationsProviderNeutral_AppliesAndIsIdempotent proves the
// migration applies, rolls back and re-applies cleanly: the mailbox index is
// per provider, the two columns are there, and the indexes it makes are
// there; the down puts 00091's and 00092's indexes back and drops the
// columns.
func TestCommunicationsProviderNeutral_AppliesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	url := testdb.MigratedURL(t)
	applyUpDownUp(t, url, communicationsProviderNeutralVersion)
	migrateTo(t, url, communicationsProviderNeutralVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	indexes := func() []string {
		return staffCollect(t, ctx, pool, "the communications indexes", `
			SELECT indexdef FROM pg_indexes
			WHERE schemaname = 'communications' AND indexname IN (
				'ux_channels_entra_mailbox', 'ux_channels_provider_mailbox',
				'ix_conversation_messages_graph_conversation', 'ix_conversation_messages_provider_thread',
				'ix_conversation_messages_sent_message_id')
			ORDER BY indexname`)
	}
	if got, want := indexes(), []string{
		"CREATE INDEX ix_conversation_messages_provider_thread ON communications.conversation_messages USING btree (((channel_metadata_json ->> 'providerThreadId'::text)))",
		"CREATE INDEX ix_conversation_messages_sent_message_id ON communications.conversation_messages USING btree (sent_message_id) WHERE (sent_message_id IS NOT NULL)",
		"CREATE UNIQUE INDEX ux_channels_provider_mailbox ON communications.channels USING btree (provider, ((settings ->> 'mailbox'::text))) WHERE (settings ? 'mailbox'::text)",
	}; !equalStrings(got, want) {
		t.Errorf("indexes =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	columns := func() []string {
		return staffCollect(t, ctx, pool, "the new columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':' || is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'communications' AND table_name = 'conversation_messages'
			  AND column_name IN ('send_provider', 'sent_message_id')
			ORDER BY column_name`)
	}
	if got, want := columns(), []string{
		"send_provider:character varying(20):YES",
		"sent_message_id:character varying(998):YES",
	}; !equalStrings(got, want) {
		t.Errorf("columns = %v, want %v", got, want)
	}

	// One mailbox per provider: the same id under two providers is two
	// mailboxes, twice under one is refused.
	mailbox := uuid.NewString()
	channel := func(provider string) error {
		id := uuid.New()
		_, err := pool.Exec(ctx, `
			INSERT INTO communications.channels (id, type, address, provider, settings, created_at)
			VALUES ($1, 'email', $2, $3, jsonb_build_object('mailbox', $4::text), now())`,
			id, id.String()+"@example.com", provider, mailbox)
		return err
	}
	if err := channel("entra"); err != nil {
		t.Fatalf("a channel on entra: %v", err)
	}
	if err := channel("google"); err != nil {
		t.Errorf("the same mailbox id on google: %v, want admitted", err)
	}
	if err := channel("google"); !uniqueViolationOf(err, "ux_channels_provider_mailbox") {
		t.Errorf("a second google channel on one mailbox: %v, want ux_channels_provider_mailbox", err)
	}

	migrateTo(t, url, communicationsProviderNeutralVersion-1)
	if got := columns(); len(got) != 0 {
		t.Errorf("columns after the down = %v, want none", got)
	}
}

// TestCommunicationsProviderNeutral_RewritesTheThreadKeyAndBackfills proves
// the up renames graphConversationId to providerThreadId in every message
// that has it, leaving the rest of the metadata and the messages without it
// alone, and backfills send_provider 'entra' for a personal message only;
// the down renames it back.
func TestCommunicationsProviderNeutral_RewritesTheThreadKeyAndBackfills(t *testing.T) {
	t.Parallel()
	url := testdb.URL(t)
	migrateTo(t, url, communicationsProviderNeutralVersion-1)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	conv := insertTestConversation(t, ctx, pool, insertTestChannel(t, ctx, pool))
	message := func(direction, sendAs, metadata string) uuid.UUID {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO communications.conversation_messages
			 (id, conversation_id, direction, send_as, channel_metadata_json, occurred_at, created_at)
			VALUES ($1, $2, $3, $4, $5::jsonb, now(), now())`, id, conv, direction, sendAs, metadata); err != nil {
			t.Fatalf("insert message: %v", err)
		}
		return id
	}
	inbound := message("inbound", "channel", `{"from": "a@example.com", "graphConversationId": "conv-1"}`)
	other := message("inbound", "channel", `{"from": "b@example.com"}`)
	personal := message("outbound", "personal", `{}`)
	channel := message("outbound", "channel", `{}`)

	read := func(id uuid.UUID) string {
		got := staffCollect(t, ctx, pool, "a message", `
			SELECT channel_metadata_json::text || '|' || coalesce(send_provider, '-')
			FROM communications.conversation_messages WHERE id = $1`, id)
		if len(got) != 1 {
			t.Fatalf("message %s = %v", id, got)
		}
		return got[0]
	}

	migrateTo(t, url, communicationsProviderNeutralVersion)
	for id, want := range map[uuid.UUID]string{
		inbound:  `{"from": "a@example.com", "providerThreadId": "conv-1"}|-`,
		other:    `{"from": "b@example.com"}|-`,
		personal: `{}|entra`,
		channel:  `{}|-`,
	} {
		if got := read(id); got != want {
			t.Errorf("after the up %s = %s, want %s", id, got, want)
		}
	}

	migrateTo(t, url, communicationsProviderNeutralVersion-1)
	if got := staffCollect(t, ctx, pool, "the inbound message after the down", `
		SELECT channel_metadata_json::text FROM communications.conversation_messages WHERE id = $1`, inbound); !equalStrings(got, []string{
		`{"from": "a@example.com", "graphConversationId": "conv-1"}`,
	}) {
		t.Errorf("after the down = %v", got)
	}
}
