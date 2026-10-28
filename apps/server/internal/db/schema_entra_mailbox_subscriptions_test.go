package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// entraMailboxSubscriptionsVersion is 00085_entra_mailbox_subscriptions.sql,
// the change notifications' subscriptions and the connection's three columns
// (docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md, O11.4).
const entraMailboxSubscriptionsVersion = 85

// TestEntraMailboxSubscriptions_AppliesAndIsIdempotent proves the migration
// applies, rolls back and re-applies cleanly, and pins what the provider's
// queries rest on: entra.mailbox_subscriptions with every column's type,
// nullability and default, one per mailbox, Graph's id unique, the index on
// notified_at; the connection's three columns, null. The Down drops them.
func TestEntraMailboxSubscriptions_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, entraMailboxSubscriptionsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := func(table, like string) []string {
		return staffCollect(t, ctx, pool, "entra."+table+" columns", `
			SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
			    || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'entra' AND table_name = $1 AND column_name LIKE $2
			ORDER BY ordinal_position`, table, like)
	}
	if got, want := columns("mailbox_subscriptions", "%"), []string{
		"object_id:uuid:NO:",
		"subscription_id:character varying(64):YES:",
		"client_state_hash:bytea:NO:",
		"expires_at:timestamp with time zone:YES:",
		"state:character varying(20):NO:'active'::character varying",
		"notified_at:timestamp with time zone:YES:",
		"created_at:timestamp with time zone:NO:",
		"updated_at:timestamp with time zone:NO:",
	}; !equalStrings(got, want) {
		t.Errorf("entra.mailbox_subscriptions columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if cols := primaryKeyColumns(t, ctx, pool, "entra", "mailbox_subscriptions"); !equalStrings(cols, []string{"object_id"}) {
		t.Errorf("entra.mailbox_subscriptions primary key = %v, want [object_id]", cols)
	}
	if got := staffCollect(t, ctx, pool, "the subscriptions' indexes", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'entra' AND tablename = 'mailbox_subscriptions' AND indexname NOT LIKE '%_pkey'
		ORDER BY indexname`); !equalStrings(got, []string{
		"CREATE INDEX ix_entra_mailbox_subscriptions_notified ON entra.mailbox_subscriptions USING btree (notified_at)",
		"CREATE UNIQUE INDEX mailbox_subscriptions_subscription_id_key ON entra.mailbox_subscriptions USING btree (subscription_id)",
	}) {
		t.Errorf("entra.mailbox_subscriptions indexes = %v", got)
	}
	notifications := []string{
		"notifications_validating_until:timestamp with time zone:YES:",
		"notifications_unreachable_at:timestamp with time zone:YES:",
		"last_notification_at:timestamp with time zone:YES:",
	}
	if got := append(columns("connection", "notifications_%"), columns("connection", "last_notification_at")...); !equalStrings(got, notifications) {
		t.Errorf("entra.connection's notification columns = %v, want %v", got, notifications)
	}

	subscription := func(id string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO entra.mailbox_subscriptions (object_id, subscription_id, client_state_hash, created_at, updated_at)
			VALUES ($1, NULLIF($2, ''), '\x00', now(), now())`, uuid.New(), id)
		return err
	}
	for _, id := range []string{"", "", "7f1c1e8e-8d5b-4c2e-9a3f-0b1d2c3e4f50"} {
		if err := subscription(id); err != nil {
			t.Fatalf("a subscription %q: %v", id, err)
		}
	}
	if err := subscription("7f1c1e8e-8d5b-4c2e-9a3f-0b1d2c3e4f50"); !uniqueViolationOf(err, "mailbox_subscriptions_subscription_id_key") {
		t.Errorf("a second row of Graph's id: %v, want mailbox_subscriptions_subscription_id_key", err)
	}

	migrateTo(t, url, entraMailboxSubscriptionsVersion-1)
	if got := staffCollect(t, ctx, pool, "the table after the down", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'entra' AND table_name = 'mailbox_subscriptions'`); len(got) != 0 {
		t.Errorf("entra.mailbox_subscriptions after the down = %v, want none", got)
	}
	if got := append(columns("connection", "notifications_%"), columns("connection", "last_notification_at")...); len(got) != 0 {
		t.Errorf("entra.connection's notification columns after the down = %v, want none", got)
	}
}
