package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// communicationsPersonalSendingVersion is 00095_communications_personal_sending.sql,
// adding send_as, from_address and from_display_name to
// communications.conversation_messages
// (docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md, M20).
const communicationsPersonalSendingVersion = 95

func TestCommunicationsPersonalSending_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, communicationsPersonalSendingVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	columns := staffCollect(t, ctx, pool, "communications.conversation_messages personal columns", `
		SELECT column_name || ':' || data_type || coalesce('(' || character_maximum_length || ')', '') || ':'
		    || is_nullable || ':' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'communications' AND table_name = 'conversation_messages'
		  AND column_name IN ('send_as', 'from_address', 'from_display_name')
		ORDER BY ordinal_position`)
	if want := []string{
		"send_as:character varying(20):NO:'channel'::character varying",
		"from_address:character varying(320):YES:",
		"from_display_name:character varying(200):YES:",
	}; !equalStrings(columns, want) {
		t.Errorf("communications.conversation_messages personal columns =\n%s\nwant\n%s", strings.Join(columns, "\n"), strings.Join(want, "\n"))
	}

	channel := insertTestChannel(t, ctx, pool)
	conv := insertTestConversation(t, ctx, pool, channel)
	insertMessage := func(sendAs string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO communications.conversation_messages
			 (id, conversation_id, direction, send_as, from_address, from_display_name, occurred_at, created_at)
			VALUES ($1, $2, 'outbound', $3, 'ingrid@example.com', 'Ingrid Hansen', now(), now())`,
			uuid.New(), conv, sendAs)
		return err
	}
	if err := insertMessage("alias"); !checkViolationOf(err, "ck_conversation_messages_send_as") {
		t.Errorf("send_as alias: %v, want ck_conversation_messages_send_as", err)
	}
	for _, sendAs := range []string{"channel", "personal"} {
		if err := insertMessage(sendAs); err != nil {
			t.Errorf("send_as %s: %v", sendAs, err)
		}
	}

	migrateTo(t, url, communicationsPersonalSendingVersion-1)
	if got := staffCollect(t, ctx, pool, "columns after down", `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'communications' AND table_name = 'conversation_messages'
		  AND column_name IN ('send_as', 'from_address', 'from_display_name')`); len(got) != 0 {
		t.Errorf("personal columns after down = %v, want none", got)
	}
}
