package communications_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// storedAddress is how a participant's address is actually persisted:
// normalizeEmail uppercases and trims (design §5's D7 — suppression
// normalisation uppercases, and the participant lookup key shares it), so a
// query for the address as the caller sent it finds nothing at all. Spelled
// out here because a test that compared the raw form would fail looking like
// "the row was never written" rather than "the row is stored differently".
func storedAddress(address string) string { return strings.ToUpper(strings.TrimSpace(address)) }

// This file is task 14's fix for ux_participants_channel_id_address, the one
// unique constraint the module's constraint audit inherited as KNOWN-OPEN.
//
// findOrCreateParticipantByAddress (conversations_create.go) looks up
// (channelId, address) and inserts when it finds nothing — .NET's own
// FirstOrDefaultAsync-then-Add, ported faithfully, including the gap between
// the two statements. Two concurrent POST /conversations naming the same
// not-yet-known recipient on the same channel both miss the lookup and both
// insert. The loser used to take a 23505 that aborted its ENTIRE
// transaction: conversation, message, participants, deliveries, queued
// events, outbox job. Nothing of that request survived, and the caller got
// httpx.WriteError's host-wide fallback — a bare RFC 7807 409, which is
// neither this module's error vocabulary (design §3: only the three stats
// endpoints use ProblemDetails) nor a status postCommunicationsConversations
// declares at all. This is the fifth instance of one defect class in this
// module, and the first four were each invisible to every sequential test.
//
// The fix is in the query, not the handler (queries/conversations.sql's
// InsertParticipant): ON CONFLICT (channel_id, address) DO UPDATE yields the
// live row whoever wins, so there is no violation left to map to a status
// the contract does not have.
//
// The gate is this package's established technique (channels_concurrency_test.go's
// race/awaitLockWaiters, reused rather than redeclared): a gate transaction
// takes LOCK TABLE ... IN EXCLUSIVE MODE before either request starts.
// EXCLUSIVE is compatible with the ACCESS SHARE of each request's
// FindParticipantByChannelAndAddress, so both really do look up the address
// and both really do find nothing — and it conflicts with the ROW EXCLUSIVE
// their INSERT needs, so both park there with the decision to insert already
// made. Only once awaitLockWaiters confirms both backends are genuinely
// blocked on a lock (pg_stat_activity, never a sleep) does the gate release.

// TestCreateConversation_ConcurrentNewRecipientCreatesOneParticipant is the
// teeth check: two conversations, one brand-new recipient address, two 201s
// and exactly one participants row. Before the fix one of the two answered a
// bare problem+json 409 and wrote nothing at all.
func TestCreateConversation_ConcurrentNewRecipientCreatesOneParticipant(t *testing.T) {
	h := newHarness(t)
	channelID := setupChannel(t, h)
	recipient := "shared-" + uuid.NewString() + "@example.test"

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `LOCK TABLE communications.participants IN EXCLUSIVE MODE`); err != nil {
		t.Fatalf("gate: lock participants: %v", err)
	}

	const n = 2
	fns := make([]func() *modtest.Response, n)
	for i := 0; i < n; i++ {
		// A client each, as two callers would be; each request carries its
		// own Idempotency-Key, so nothing here collides on
		// ux_idempotency_records_key instead (that race has its own test).
		c := h.SignIn(t, "communications:conversations-reply")
		fns[i] = func() *modtest.Response {
			return c.Do(http.MethodPost, "/api/v1/communications/conversations",
				newConversationBody(recipient), modtest.Header("Idempotency-Key", uuid.NewString()))
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(fns...)
		close(finished)
	}()
	awaitLockWaiters(t, h, n, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	for i, r := range responses {
		if r.Status != http.StatusCreated {
			t.Errorf("request %d: status %d body %s, want 201: neither request loses a participant race",
				i, r.Status, r.Body)
		}
		if ct := r.Header("Content-Type"); r.Status == http.StatusConflict && ct == "application/problem+json" {
			t.Errorf("request %d answered the host-wide 23505 fallback, the wrong vocabulary for this module", i)
		}
	}

	if n := h.Count(t, `SELECT count(*) FROM communications.participants WHERE channel_id = $1 AND address = $2`,
		uuid.MustParse(channelID), storedAddress(recipient)); n != 1 {
		t.Errorf("participants for the shared address = %d, want exactly 1", n)
	}
	// Both requests' whole transactions survived, not just their responses:
	// the loser's abort used to take its conversation and outbox job with it.
	if n := h.Count(t, `SELECT count(*) FROM communications.conversations`); n != 2 {
		t.Errorf("conversations = %d, want 2: a lost participant race must not roll back the whole create", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.outbox_jobs`); n != 2 {
		t.Errorf("outbox jobs = %d, want 2: both conversations must still be queued for delivery", n)
	}
}

// TestCreateConversation_ConcurrentNewRecipientKeepsTheWinnersContactId
// guards the one thing ON CONFLICT DO UPDATE could plausibly have broken.
// .NET never assigns ContactId to an ALREADY EXISTING participant — neither
// AddGenericDeliveriesAsync branch does — so the conflict path must not
// become the single code path in this module that overwrites it. The SET
// writes the conflict key back to itself for exactly this reason.
func TestCreateConversation_ConcurrentNewRecipientKeepsTheWinnersContactId(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	channelID := setupChannel(t, h)
	address := "contact-" + uuid.NewString() + "@example.test"
	c := h.SignIn(t, "communications:conversations-reply")

	// First create resolves the recipient by the recipients[] branch with a
	// contactId, so the participant row is written WITH one.
	createConversation(t, c, newConversationBodyWithRecipients([]map[string]any{
		{"address": address, "contactId": 2001, "type": "to"},
	}))
	// A second create names the same address with no contactId at all, which
	// is the sequential shape of the racing request: it must find the
	// existing participant and leave it alone.
	createConversation(t, c, newConversationBody(address))

	var contactID *int32
	if err := h.Pool().QueryRow(context.Background(),
		`SELECT contact_id FROM communications.participants WHERE channel_id = $1 AND address = $2`,
		uuid.MustParse(channelID), storedAddress(address)).Scan(&contactID); err != nil {
		t.Fatalf("read participant: %v", err)
	}
	if contactID == nil || *contactID != 2001 {
		t.Errorf("contact_id = %v, want 2001 kept: an existing participant's contact_id is never reassigned", contactID)
	}
}
