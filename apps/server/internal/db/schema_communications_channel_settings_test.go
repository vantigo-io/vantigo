package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// communicationsChannelSettingsVersion is
// 00091_communications_channel_settings.sql, a channel's settings that are
// not a secret's and one channel per Microsoft 365 mailbox
// (docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md,
// M12).
const communicationsChannelSettingsVersion = 91

// TestCommunicationsChannelSettings_AppliesAndIsIdempotent proves the
// migration applies, rolls back and re-applies cleanly: channels.settings is
// a required jsonb, '{}' by default, and the partial unique index refuses a
// second entra channel on one mailbox while two SMTP channels carrying the
// same settings are admitted. The down drops both.
func TestCommunicationsChannelSettings_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, communicationsChannelSettingsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	settingsColumn := func() []string {
		return staffCollect(t, ctx, pool, "communications.channels' settings", `
			SELECT column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default, '')
			FROM information_schema.columns
			WHERE table_schema = 'communications' AND table_name = 'channels' AND column_name = 'settings'`)
	}
	if got, want := settingsColumn(), []string{"settings:jsonb:NO:'{}'::jsonb"}; !equalStrings(got, want) {
		t.Errorf("communications.channels' settings = %v, want %v", got, want)
	}
	if got := staffCollect(t, ctx, pool, "the channels' mailbox index", `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'communications' AND indexname = 'ux_channels_entra_mailbox'`); !equalStrings(got, []string{
		"CREATE UNIQUE INDEX ux_channels_entra_mailbox ON communications.channels USING btree (((settings ->> 'mailbox'::text))) WHERE ((provider)::text = 'entra'::text)",
	}) {
		t.Errorf("ux_channels_entra_mailbox = %v", got)
	}

	// A channel inserted without settings carries the default.
	smtp := insertTestChannel(t, ctx, pool)
	if got := staffCollect(t, ctx, pool, "the default settings", `
		SELECT settings::text FROM communications.channels WHERE id = $1`, smtp); !equalStrings(got, []string{"{}"}) {
		t.Errorf("an SMTP channel's settings = %v, want [{}]", got)
	}

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
		t.Fatalf("a Microsoft 365 channel: %v", err)
	}
	if err := channel("entra"); !uniqueViolationOf(err, "ux_channels_entra_mailbox") {
		t.Errorf("a second Microsoft 365 channel on one mailbox: %v, want ux_channels_entra_mailbox", err)
	}
	for range 2 {
		if err := channel("smtp"); err != nil {
			t.Errorf("an SMTP channel carrying the same settings: %v", err)
		}
	}

	migrateTo(t, url, communicationsChannelSettingsVersion-1)
	if got := settingsColumn(); len(got) != 0 {
		t.Errorf("communications.channels' settings after the down = %v, want none", got)
	}
	if got := staffCollect(t, ctx, pool, "the index after the down", `
		SELECT indexname FROM pg_indexes WHERE schemaname = 'communications' AND indexname = 'ux_channels_entra_mailbox'`); len(got) != 0 {
		t.Errorf("ux_channels_entra_mailbox after the down = %v, want none", got)
	}
}
