package entra_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
)

// EnsureFolder creates the top-level folder on first call, returns the existing
// folder id on repeat calls without creating a second, and recovers when a
// concurrent create answers 409 ErrorFolderExists.
func TestChannelMailbox_EnsureFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, g, _ := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")

	if _, err := p.EnsureFolder(ctx, "00000000-0000-0000-0000-000000000099", "Handled in Vantigo"); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("EnsureFolder unrecorded = %v, want ErrMailboxUnknown", err)
	}
	if _, err := p.EnsureFolder(ctx, info.Mailbox, "   "); err == nil {
		t.Error("EnsureFolder blank name succeeded, want error")
	}

	folderID, err := p.EnsureFolder(ctx, info.Mailbox, "Handled in Vantigo")
	if err != nil || folderID == "" {
		t.Fatalf("EnsureFolder create = %q, %v", folderID, err)
	}
	if folders := g.MailFolders(post.ID); len(folders) != 1 || folders[0].ID != folderID || folders[0].DisplayName != "Handled in Vantigo" {
		t.Errorf("MailFolders = %+v", folders)
	}

	// Second call finds the existing folder via GET without POSTing again.
	before := len(g.Calls())
	sameID, err := p.EnsureFolder(ctx, info.Mailbox, "Handled in Vantigo")
	if err != nil || sameID != folderID {
		t.Fatalf("EnsureFolder existing = %q, %v; want %q", sameID, err, folderID)
	}
	calls := graphCalls(g, before)
	if len(calls) != 1 || calls[0].Method != http.MethodGet {
		t.Errorf("EnsureFolder existing calls = %+v, want single GET", calls)
	}
}

// Mark marks a message read, sets categories, and moves it to a folder with
// Prefer: IdType="ImmutableId", remaining idempotent when called again after a
// move and answering contracts.ErrMailItemGone when the message is gone.
func TestChannelMailbox_Mark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, g, o := newChannelHarness(t)
	post := sharedMailbox(g)
	p := channelProvider(h)
	info := checked(t, p, "post@nordlys.no")
	msgID := g.AddInboxMessage(post.ID, graphtest.InboxMessage{ReceivedAt: h.Now(), MIME: inboxMIME("Sak")})

	if err := p.Mark(ctx, "00000000-0000-0000-0000-000000000099", msgID, contracts.MailMark{Kind: contracts.MailMarkRead}); !errors.Is(err, contracts.ErrMailboxUnknown) {
		t.Errorf("Mark unrecorded = %v, want ErrMailboxUnknown", err)
	}

	// Mark read.
	from := len(g.Calls())
	if err := p.Mark(ctx, info.Mailbox, msgID, contracts.MailMark{Kind: contracts.MailMarkRead}); err != nil {
		t.Fatalf("Mark read: %v", err)
	}
	if calls := graphCalls(g, from); len(calls) != 1 || calls[0].Method != http.MethodPatch || calls[0].Prefer != `IdType="ImmutableId"` {
		t.Errorf("Mark read calls = %+v", calls)
	}
	if st, ok := g.MessageState(post.ID, msgID); !ok || !st.IsRead {
		t.Errorf("MessageState after read = %+v, %v", st, ok)
	}

	// Mark category.
	if err := p.Mark(ctx, info.Mailbox, msgID, contracts.MailMark{Kind: contracts.MailMarkCategory, Categories: []string{"Handled in Vantigo"}}); err != nil {
		t.Fatalf("Mark category: %v", err)
	}
	if st, ok := g.MessageState(post.ID, msgID); !ok || !slices.Equal(st.Categories, []string{"Handled in Vantigo"}) {
		t.Errorf("MessageState after category = %+v, %v", st, ok)
	}

	// EnsureFolder + Mark move, and repeat move for idempotence.
	folderID, err := p.EnsureFolder(ctx, info.Mailbox, "Handled in Vantigo")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	from = len(g.Calls())
	if err := p.Mark(ctx, info.Mailbox, msgID, contracts.MailMark{Kind: contracts.MailMarkMove, FolderID: folderID}); err != nil {
		t.Fatalf("Mark move: %v", err)
	}
	if calls := graphCalls(g, from); len(calls) != 1 || calls[0].Method != http.MethodPost || calls[0].Prefer != `IdType="ImmutableId"` {
		t.Errorf("Mark move calls = %+v", calls)
	}
	if st, ok := g.MessageState(post.ID, msgID); !ok || st.FolderID != folderID || st.FolderName != "Handled in Vantigo" {
		t.Errorf("MessageState after move = %+v, %v", st, ok)
	}
	// Repeat move succeeds idempotently.
	if err := p.Mark(ctx, info.Mailbox, msgID, contracts.MailMark{Kind: contracts.MailMarkMove, FolderID: folderID}); err != nil {
		t.Errorf("repeat Mark move: %v", err)
	}

	// Unknown message ID -> ErrMailItemGone.
	if err := p.Mark(ctx, info.Mailbox, "AAkALgAAAAAA-gone", contracts.MailMark{Kind: contracts.MailMarkRead}); !errors.Is(err, contracts.ErrMailItemGone) {
		t.Errorf("Mark missing message = %v, want ErrMailItemGone", err)
	}

	// Switch off -> ErrMailboxUnavailable.
	setMailChannels(t, o, false)
	if err := p.Mark(ctx, info.Mailbox, msgID, contracts.MailMark{Kind: contracts.MailMarkRead}); !errors.Is(err, contracts.ErrMailboxUnavailable) {
		t.Errorf("Mark with switch off = %v, want ErrMailboxUnavailable", err)
	}
}
