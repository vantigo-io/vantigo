package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// communicationsInboundVersion is 00092_communications_inbound.sql, each
// Microsoft 365 channel's sync state and a receipt for every message the
// communications-mailbox-sync worker saw
// (docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md,
// M14; docs/superpowers/plans/2026-10-29-communications-m365-inbound.md,
// reading 17).
const communicationsInboundVersion = 92

// TestCommunicationsInbound_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly: both tables and their CHECKs,
// the provider unique refusing a second receipt for one message, the rfc
// partial unique refusing two imported receipts with one Message-Id while
// admitting a duplicate beside its original, and ON DELETE SET NULL on
// message_id. The down drops both tables and the expression index.
func TestCommunicationsInbound_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, communicationsInboundVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	tables := func() []string {
		return staffCollect(t, ctx, pool, "the inbound tables", `
			SELECT table_name FROM information_schema.tables
			WHERE table_schema = 'communications' AND table_name IN ('channel_sync', 'inbound_receipts')
			ORDER BY 1`)
	}
	if got, want := tables(), []string{"channel_sync", "inbound_receipts"}; !equalStrings(got, want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	indexes := func() []string {
		return staffCollect(t, ctx, pool, "the inbound indexes", `
			SELECT indexname FROM pg_indexes
			WHERE schemaname = 'communications' AND indexname IN (
				'ix_channel_sync_due', 'ux_inbound_receipts_provider', 'ux_inbound_receipts_rfc',
				'ix_inbound_receipts_channel_received', 'ix_inbound_receipts_message_id',
				'ix_conversation_messages_graph_conversation')
			ORDER BY 1`)
	}
	if got, want := indexes(), []string{
		"ix_channel_sync_due",
		"ix_conversation_messages_graph_conversation",
		"ix_inbound_receipts_channel_received",
		"ix_inbound_receipts_message_id",
		"ux_inbound_receipts_provider",
		"ux_inbound_receipts_rfc",
	}; !equalStrings(got, want) {
		t.Errorf("indexes = %v, want %v", got, want)
	}

	channel := insertTestChannel(t, ctx, pool)

	// channel_sync: the state CHECK, the defaults.
	sync := func(channelID uuid.UUID, state string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO communications.channel_sync (channel_id, import_from, state, next_sync_at, updated_at)
			VALUES ($1, now(), $2, now(), now())`, channelID, state)
		return err
	}
	if err := sync(channel, "paused"); !checkViolationOf(err, "ck_channel_sync_state") {
		t.Errorf("a channel_sync row in state paused: %v, want ck_channel_sync_state", err)
	}
	for _, state := range []string{"pending", "active", "failed", "stopped"} {
		other := insertTestChannel(t, ctx, pool)
		if err := sync(other, state); err != nil {
			t.Errorf("a channel_sync row in state %s: %v", state, err)
		}
	}
	if err := sync(channel, "pending"); err != nil {
		t.Fatalf("a channel_sync row: %v", err)
	}
	if got := staffCollect(t, ctx, pool, "channel_sync's defaults", `
		SELECT folder || ':' || attempts || ':' || notified || ':' || (delta_link IS NULL) || ':' || (lease_id IS NULL)
		FROM communications.channel_sync WHERE channel_id = $1`, channel); !equalStrings(got, []string{"inbox:0:false:true:true"}) {
		t.Errorf("channel_sync's defaults = %v", got)
	}

	// inbound_receipts: the outcome CHECK, the two uniques.
	receipt := func(providerID string, rfcID *string, outcome string, messageID *uuid.UUID) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO communications.inbound_receipts
				(id, channel_id, provider_message_id, rfc_message_id, message_id, outcome, received_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, now(), now())`,
			uuid.New(), channel, providerID, rfcID, messageID, outcome)
		return err
	}
	rfc := "<a@example.com>"
	if err := receipt("p0", nil, "lost", nil); !checkViolationOf(err, "ck_inbound_receipts_outcome") {
		t.Errorf("a receipt with outcome lost: %v, want ck_inbound_receipts_outcome", err)
	}
	message := insertTestConversationMessage(t, ctx, pool, insertTestConversation(t, ctx, pool, channel))
	if err := receipt("p1", &rfc, "imported", &message); err != nil {
		t.Fatalf("an imported receipt: %v", err)
	}
	if err := receipt("p1", nil, "skipped", nil); !uniqueViolationOf(err, "ux_inbound_receipts_provider") {
		t.Errorf("a second receipt for one provider id: %v, want ux_inbound_receipts_provider", err)
	}
	if err := receipt("p2", &rfc, "imported", nil); !uniqueViolationOf(err, "ux_inbound_receipts_rfc") {
		t.Errorf("a second imported receipt with one Message-Id: %v, want ux_inbound_receipts_rfc", err)
	}
	if err := receipt("p3", &rfc, "duplicate", &message); err != nil {
		t.Errorf("a duplicate beside its original: %v", err)
	}
	for _, outcome := range []string{"skipped", "failed"} {
		if err := receipt("p-"+outcome, &rfc, outcome, nil); err != nil {
			t.Errorf("a %s receipt with the original's Message-Id: %v", outcome, err)
		}
	}
	// Another channel's imported receipt with the same Message-Id.
	other := insertTestChannel(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO communications.inbound_receipts
			(id, channel_id, provider_message_id, rfc_message_id, outcome, received_at, created_at)
		VALUES ($1, $2, 'p1', $3, 'imported', now(), now())`, uuid.New(), other, rfc); err != nil {
		t.Errorf("another channel's receipt with one provider id and Message-Id: %v", err)
	}

	// ON DELETE SET NULL: the receipts outlive their message.
	if _, err := pool.Exec(ctx, `DELETE FROM communications.conversation_messages WHERE id = $1`, message); err != nil {
		t.Fatalf("delete the message: %v", err)
	}
	if got := staffCollect(t, ctx, pool, "the receipts after the message", `
		SELECT provider_message_id || ':' || (message_id IS NULL)
		FROM communications.inbound_receipts WHERE channel_id = $1 AND provider_message_id IN ('p1', 'p3')
		ORDER BY 1`, channel); !equalStrings(got, []string{"p1:true", "p3:true"}) {
		t.Errorf("the receipts after their message is deleted = %v, want both kept, message_id null", got)
	}

	migrateTo(t, url, communicationsInboundVersion-1)
	if got := tables(); len(got) != 0 {
		t.Errorf("tables after the down = %v, want none", got)
	}
	if got := indexes(); len(got) != 0 {
		t.Errorf("indexes after the down = %v, want none", got)
	}
}

// TestCommunicationsInbound_BackfillsMicrosoft365Channels proves the up
// writes one pending channel_sync row, importing from the moment of the
// upgrade, for every entra channel that existed before it — an inactive one
// too — and none for an SMTP channel; a down and an up again write it anew.
func TestCommunicationsInbound_BackfillsMicrosoft365Channels(t *testing.T) {
	url := testdb.URL(t)
	migrateTo(t, url, communicationsInboundVersion-1)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	entraChannel := func(active bool) uuid.UUID {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO communications.channels (id, type, address, provider, is_active, settings, created_at)
			VALUES ($1, 'email', $2, 'entra', $3, jsonb_build_object('mailbox', $4::text), now() - interval '30 days')`,
			id, id.String()+"@example.com", active, uuid.NewString()); err != nil {
			t.Fatalf("insert an entra channel: %v", err)
		}
		return id
	}
	active, inactive := entraChannel(true), entraChannel(false)
	smtp := insertTestChannel(t, ctx, pool)

	for _, pass := range []string{"up", "up again"} {
		before := time.Now()
		migrateTo(t, url, communicationsInboundVersion)
		after := time.Now()

		assertBackfill(t, ctx, pool, pass, []uuid.UUID{active, inactive}, smtp, before, after)
		migrateTo(t, url, communicationsInboundVersion-1)
	}
}

// assertBackfill checks channel_sync holds exactly one pending row for each
// of want, importing from between before and after, and none for smtp.
func assertBackfill(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pass string, want []uuid.UUID, smtp uuid.UUID, before, after time.Time) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT channel_id, folder, state, import_from, next_sync_at, attempts, delta_link IS NULL
		FROM communications.channel_sync`)
	if err != nil {
		t.Fatalf("%s: query channel_sync: %v", pass, err)
	}
	defer rows.Close()
	seen := map[uuid.UUID]int{}
	// The database's clock and the test's may differ by a little.
	from, to := before.Add(-time.Minute), after.Add(time.Minute)
	for rows.Next() {
		var (
			channelID              uuid.UUID
			folder, state          string
			importFrom, nextSyncAt time.Time
			attempts               int
			noLink                 bool
		)
		if err := rows.Scan(&channelID, &folder, &state, &importFrom, &nextSyncAt, &attempts, &noLink); err != nil {
			t.Fatalf("%s: scan: %v", pass, err)
		}
		seen[channelID]++
		if channelID == smtp {
			t.Errorf("%s: the SMTP channel has a channel_sync row", pass)
			continue
		}
		if folder != "inbox" || state != "pending" || attempts != 0 || !noLink {
			t.Errorf("%s: channel %s's row = %s, %s, attempts %d, no link %v; want inbox, pending, 0, true", pass, channelID, folder, state, attempts, noLink)
		}
		if importFrom.Before(from) || importFrom.After(to) || nextSyncAt.Before(from) || nextSyncAt.After(to) {
			t.Errorf("%s: channel %s imports from %v, next sync %v; want both about now (%v)", pass, channelID, importFrom, nextSyncAt, before)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: rows: %v", pass, err)
	}
	if len(seen) != len(want) {
		t.Errorf("%s: channel_sync holds %d channels, want %d", pass, len(seen), len(want))
	}
	for _, id := range want {
		if seen[id] != 1 {
			t.Errorf("%s: channel %s has %d channel_sync rows, want 1", pass, id, seen[id])
		}
	}
}
