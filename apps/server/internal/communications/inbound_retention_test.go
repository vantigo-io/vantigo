package communications_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is what leaves with an inbound message (M16; delivery D, reading
// 23): its original and attachments through retention's and the erase's
// cleanup ledger, its receipt's link to it, and the receipts themselves after
// the window — each channel's newest kept, the restart point of an expired
// delta. And the channel_sync row the channel operations write.

func ownedOrPending(t *testing.T, h *modtest.Harness, key string) string {
	t.Helper()
	recs := readCleanupRecords(t, h, key)
	if len(recs) != 1 {
		t.Fatalf("cleanup records of %s = %+v, want one", key, recs)
	}
	return recs[0].status
}

// TestRetention_ReachesInboundMessages: past the window, retention deletes
// inbound messages with their originals and attachments queued for deletion
// and their receipts unlinked; the next batch sweeps the receipts but the
// channel's newest.
func TestRetention_ReachesInboundMessages(t *testing.T) {
	t.Parallel()
	in := newInbox(t, nil, nil)
	withFile := in.plainMail("with a file")
	withFile.Attachments = []testAttachment{{FileName: "a.pdf", ContentType: "application/pdf", Content: []byte("%PDF")}}
	older := in.importOne(t, withFile, "")
	newer := in.importOne(t, in.plainMail("later"), "")
	attachmentKey := modtest.One[string](t, in.h, `SELECT storage_key FROM communications.message_attachments WHERE message_id = $1`, older.id)

	in.h.Advance(366 * 24 * time.Hour)
	w := communications.NewRetentionWorker(in.h.Deps())
	if deleted, err := w.CleanupBatch(context.Background()); err != nil || deleted != 2 {
		t.Fatalf("CleanupBatch = %d, %v; want the 2 inbound messages", deleted, err)
	}
	for _, key := range []string{*older.rawKey, *newer.rawKey, attachmentKey} {
		if got := ownedOrPending(t, in.h, key); got != "pending" {
			t.Errorf("%s = %s, want queued for deletion", key, got)
		}
	}
	receipts := readReceipts(t, in.h, in.channelID)
	if len(receipts) != 2 {
		t.Fatalf("receipts = %d, want both kept by this batch", len(receipts))
	}
	for id, r := range receipts {
		if r.messageID != nil {
			t.Errorf("receipt %s still names its message", id)
		}
	}

	if _, err := w.CleanupBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	receipts = readReceipts(t, in.h, in.channelID)
	if len(receipts) != 1 {
		t.Fatalf("receipts after the sweep = %d, want the newest only", len(receipts))
	}
	for _, r := range receipts {
		if r.rfc == nil || *r.rfc != *newer.rfc {
			t.Errorf("the receipt kept = %+v, want the newest's", r)
		}
	}
}

// TestErase_ReachesInboundMessages: a customer's erase deletes the
// conversations the linker gave them with their originals queued for deletion
// and their receipts unlinked.
func TestErase_ReachesInboundMessages(t *testing.T) {
	t.Parallel()
	directory := &linkDirectory{matches: map[string][]contracts.ContactMatch{}}
	in := newInbox(t, nil, nil, modtest.WithDirectory(directory))
	mail := in.plainMail("Hei, strømmen er borte igjen.")
	directory.matches[senderOf(mail)] = []contracts.ContactMatch{{ContactID: 2001, CandidateCustomerIDs: []int32{1001}}}
	m := in.importOne(t, mail, "")

	eraseCommunicationsCustomer(t, in.h, 1001, true)
	if n := in.h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE id = $1`, m.id); n != 0 {
		t.Error("the message survived the erase")
	}
	if got := ownedOrPending(t, in.h, *m.rawKey); got != "pending" {
		t.Errorf("the original = %s, want queued for deletion", got)
	}
	for _, r := range readReceipts(t, in.h, in.channelID) {
		if r.messageID != nil {
			t.Error("the receipt still names the erased message")
		}
	}
}

// channelSyncJSON is ChannelResponse.sync.
type channelSyncJSON struct {
	Sync *struct {
		State        string     `json:"state"`
		Error        *string    `json:"error"`
		LastSyncedAt *time.Time `json:"lastSyncedAt"`
	} `json:"sync"`
}

// TestChannels_SyncRow: a Microsoft 365 channel is created and switched to
// with a pending row receiving from that moment, answered as sync; an SMTP
// channel has none and answers null; the way back to SMTP deletes the row.
func TestChannels_SyncRow(t *testing.T) {
	t.Parallel()
	fake := modtest.NewFakeChannelMailbox()
	h := newHarness(t, modtest.WithChannelMailbox(fake))
	c := h.SignIn(t, "communications:channels-manage")
	rows := func(id string) int {
		return h.Count(t, `SELECT count(*) FROM communications.channel_sync WHERE channel_id = $1`, id)
	}

	address := channelAddress(t)
	sharedMailbox(fake, address, "")
	r := c.Do(http.MethodPost, "/api/v1/communications/channels", entraChannelBody(address))
	var created struct {
		channelJSON
		channelSyncJSON
	}
	r.JSON(&created)
	if r.Status != http.StatusCreated || created.Sync == nil || created.Sync.State != "pending" ||
		created.Sync.Error != nil || created.Sync.LastSyncedAt != nil {
		t.Fatalf("create: status %d, sync %+v; want pending", r.Status, created.Sync)
	}
	s := readSync(t, h, uuid.MustParse(created.Id))
	if !s.importFrom.Equal(h.Now()) || !s.nextSyncAt.Equal(h.Now()) || s.state != "pending" {
		t.Errorf("channel_sync = %+v, want pending from now", s)
	}

	smtp := createChannel(t, c, newChannelBody(channelAddress(t)))
	if rows(smtp.Id) != 0 {
		t.Error("an SMTP channel has a sync row")
	}
	var list []struct {
		channelJSON
		channelSyncJSON
	}
	c.Do(http.MethodGet, "/api/v1/communications/channels", nil).JSON(&list)
	for _, ch := range list {
		if (ch.Provider == "entra") != (ch.Sync != nil) {
			t.Errorf("list: %s %s sync %+v, want sync for entra only", ch.Id, ch.Provider, ch.Sync)
		}
	}

	switched := channelAddress(t)
	sharedMailbox(fake, switched, "")
	smtp2 := createChannel(t, c, newChannelBody(switched))
	h.Advance(time.Hour)
	r = switchProvider(c, smtp2.Id)
	var after struct {
		channelJSON
		channelSyncJSON
	}
	r.JSON(&after)
	if r.Status != http.StatusOK || after.Sync == nil || after.Sync.State != "pending" {
		t.Fatalf("switch: status %d sync %+v", r.Status, after.Sync)
	}
	if s := readSync(t, h, uuid.MustParse(smtp2.Id)); !s.importFrom.Equal(h.Now()) {
		t.Errorf("the switched channel's import_from = %v, want the switch's %v", s.importFrom, h.Now())
	}

	r = c.Do(http.MethodPut, "/api/v1/communications/channels/"+smtp2.Id, map[string]any{
		"provider": "smtp", "smtp": map[string]any{"host": "smtp.example.test", "port": 587, "password": "pw"},
	})
	var back struct {
		channelJSON
		channelSyncJSON
	}
	r.JSON(&back)
	if r.Status != http.StatusOK || back.Sync != nil || rows(smtp2.Id) != 0 {
		t.Errorf("back to SMTP: status %d sync %+v rows %d; want no sync", r.Status, back.Sync, rows(smtp2.Id))
	}
	var got struct {
		channelJSON
		channelSyncJSON
	}
	c.Do(http.MethodGet, "/api/v1/communications/channels/"+created.Id, nil).JSON(&got)
	if got.Sync == nil || got.Sync.State != "pending" {
		t.Errorf("get: sync %+v, want pending", got.Sync)
	}
}
