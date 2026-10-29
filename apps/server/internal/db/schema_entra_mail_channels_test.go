package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// entraMailChannelsVersion is 00090_entra_mail_channels.sql, the Mail
// channels switch and the mailboxes ChannelMailbox.Check resolved
// (docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md,
// M10 and M11).
const entraMailChannelsVersion = 90

// TestEntraMailChannels_AppliesAndIsIdempotent proves the migration applies,
// rolls back and re-applies cleanly, and pins what the provider's queries
// rest on: entra.connection's mail_channels off by default, and
// entra.channel_mailboxes keyed by the object id, every column required. The
// down drops both.
func TestEntraMailChannels_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, entraMailChannelsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	switchColumn := func() []string {
		return staffCollect(t, ctx, pool, "entra.connection's mail_channels", `
			SELECT column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'entra' AND table_name = 'connection' AND column_name = 'mail_channels'`)
	}
	if got, want := switchColumn(), []string{"mail_channels:boolean:NO:false"}; !equalStrings(got, want) {
		t.Errorf("entra.connection's mail_channels = %v, want %v", got, want)
	}
	if got := staffCollect(t, ctx, pool, "the connection's switch", `
		SELECT mail_channels::text FROM entra.connection`); !equalStrings(got, []string{"false"}) {
		t.Errorf("the connection's mail_channels = %v, want [false]", got)
	}

	if got, want := staffCollect(t, ctx, pool, "entra.channel_mailboxes columns", `
		SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
		    || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'entra' AND table_name = 'channel_mailboxes'
		ORDER BY ordinal_position`), []string{
		"object_id:uuid:NO:",
		"address:character varying(320):NO:",
		"checked_at:timestamp with time zone:NO:",
	}; !equalStrings(got, want) {
		t.Errorf("entra.channel_mailboxes columns =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if cols := primaryKeyColumns(t, ctx, pool, "entra", "channel_mailboxes"); !equalStrings(cols, []string{"object_id"}) {
		t.Errorf("entra.channel_mailboxes primary key = %v, want [object_id]", cols)
	}
	mailbox := uuid.New()
	insert := func() error {
		_, err := pool.Exec(ctx, `
			INSERT INTO entra.channel_mailboxes (object_id, address, checked_at) VALUES ($1, 'post@example.com', now())`, mailbox)
		return err
	}
	if err := insert(); err != nil {
		t.Fatalf("a channel mailbox: %v", err)
	}
	if err := insert(); !uniqueViolationOf(err, "channel_mailboxes_pkey") {
		t.Errorf("the same mailbox twice: %v, want channel_mailboxes_pkey", err)
	}

	migrateTo(t, url, entraMailChannelsVersion-1)
	if got := switchColumn(); len(got) != 0 {
		t.Errorf("entra.connection's mail_channels after the down = %v, want none", got)
	}
	if got := staffCollect(t, ctx, pool, "the table after the down", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'entra' AND table_name = 'channel_mailboxes'`); len(got) != 0 {
		t.Errorf("entra.channel_mailboxes after the down = %v, want none", got)
	}
}
