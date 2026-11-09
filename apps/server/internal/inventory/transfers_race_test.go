package inventory_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Transfers under a race (phase 2 design I38, I41, §9 cases 1–3, 7 and 8),
// each on a pool of four: the transfer row's lock orders two writers to one
// transfer, the sites' FOR SHARE orders an action and an archive, and the
// deadlock retry reruns the loser of two opposite send-and-receives.

// raceRounds is how many times each race is run.
const raceRounds = 4

// TestTransfer_SendRacesEdit is §9, case 1: a send and an edit of the same
// draft at its revision — one lands, the other is revision_conflict or
// transfer_not_draft, never both.
func TestTransfer_SendRacesEdit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	for range raceRounds {
		tr := createTransfer(t, c, s, line(varBolt, 1))
		var sent, edited *modtest.Response
		var wg sync.WaitGroup
		wg.Go(func() { sent = send(c, tr, gen.InventoryTransferSendInput{Lines: sendLines(tr, s.shelf1.Id)}) })
		wg.Go(func() {
			edited = c.Do(http.MethodPut, transferPath(tr.Id), draftInput(s.osl.Id, s.bgo.Id, line(varBolt, 2)), ifMatch(tr.Revision))
		})
		wg.Wait()
		switch {
		case sent.Status == http.StatusOK:
			if edited.Status != http.StatusConflict {
				t.Fatalf("edit after the send = %d %s, want 409", edited.Status, edited.Body)
			}
			if code := *problemOf(t, edited).Code; code != "transfer_not_draft" && code != "revision_conflict" {
				t.Fatalf("edit after the send = %s", code)
			}
		case edited.Status == http.StatusOK:
			expectRefusal(t, sent, http.StatusConflict, "revision_conflict", "the send after the edit")
		default:
			t.Fatalf("send %d %s, edit %d %s: one must land", sent.Status, sent.Body, edited.Status, edited.Body)
		}
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_ReceiveRacesCancel is §9, case 2: the row lock orders them; a
// cancel after a receipt is transfer_not_cancellable, a receive after a
// cancel transfer_not_receivable.
func TestTransfer_ReceiveRacesCancel(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	for range raceRounds {
		tr := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 2)), s.shelf1.Id)
		var received, cancelled *modtest.Response
		var wg sync.WaitGroup
		wg.Go(func() { received = receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 1, s.bgoShelf.Id)) })
		wg.Go(func() { cancelled = cancelTransfer(c, tr) })
		wg.Wait()
		switch {
		case received.Status == http.StatusOK:
			expectRefusal(t, cancelled, http.StatusConflict, "transfer_not_cancellable", "the cancel after the receive")
		case cancelled.Status == http.StatusOK:
			expectRefusal(t, received, http.StatusConflict, "transfer_not_receivable", "the receive after the cancel")
		default:
			t.Fatalf("receive %d %s, cancel %d %s: one must land", received.Status, received.Body, cancelled.Status, cancelled.Body)
		}
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_TwoReceivers is §9, case 3 and I41: two receives of one line
// at once, no If-Match — serial, each against the other's result, so of two
// 3s of 5 one lands and the other is transfer_over_received; of two 1s of 2
// both land.
func TestTransfer_TwoReceivers(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	for range raceRounds {
		tr := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 5)), s.shelf1.Id)
		results := make([]*modtest.Response, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Go(func() { results[i] = receiveTransfer(c, tr.Id, nil, arrived(tr.Lines[0].Id, 3, s.bgoShelf.Id)) })
		}
		wg.Wait()
		landed := 0
		for _, res := range results {
			switch res.Status {
			case http.StatusOK:
				landed++
			default:
				expectRefusal(t, res, http.StatusBadRequest, "transfer_over_received", "the second receiver")
			}
		}
		if landed != 1 {
			t.Fatalf("%d receives landed, want 1", landed)
		}
		both := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 2)), s.shelf1.Id)
		for i := range results {
			wg.Go(func() { results[i] = receiveTransfer(c, both.Id, nil, arrived(both.Lines[0].Id, 1, s.bgoShelf.Id)) })
		}
		wg.Wait()
		for _, res := range results {
			mustStatus(t, res, http.StatusOK, "a receive of 1 of 2")
		}
		if got := getTransfer(t, c, both.Id); got.Status != gen.InventoryTransferStatusReceived || got.InTransit != 0 {
			t.Fatalf("after both = %+v, want received", got)
		}
		// Return the bolts, so the next round has them.
		receive(t, c, s.osl.Id, s.shelf1.Id, receiptLine(varBolt, 7))
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_RetriedReceiveConcurrently is §9, case 4 under a race: the
// same receive twice at once — the second waits on the row, then replays
// before the outstanding is judged, so both answer 200 and one is posted.
func TestTransfer_RetriedReceiveConcurrently(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	tr := mustSend(t, c, createTransfer(t, c, s, line(varBolt, 3)), s.shelf1.Id)
	body := gen.InventoryTransferReceiveInput{ClientRequestId: uuid.New(), Lines: []gen.InventoryTransferReceiveLine{arrived(tr.Lines[0].Id, 3, s.bgoShelf.Id)}}
	results := make([]*modtest.Response, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = c.Do(http.MethodPost, transferPath(tr.Id)+"/receive", body) })
	}
	wg.Wait()
	for _, res := range results {
		mustStatus(t, res, http.StatusOK, "the receive and its retry")
	}
	if n := h.Count(t, `SELECT count(*) FROM inventory.movements WHERE kind = 'transfer_in'`); n != 1 {
		t.Errorf("%d transfer_in movements, want 1", n)
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_SendRacesSiteArchive is §9, case 7's second half: a send and
// an archive of its destination at once — the send's FOR SHARE on the sites
// makes the archive wait and then refuse site_has_open_transfers, or the
// archive lands first and the send is site_archived. Never both.
func TestTransfer_SendRacesSiteArchive(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	for i := range raceRounds {
		to := createSite(t, c, "D"+string(rune('A'+i)))
		tr := transferAnswer(t, c.Do(http.MethodPost, transfersPath, draftInput(s.osl.Id, to.Id, line(varBolt, 1))), http.StatusCreated, "a draft")
		var sent, archived *modtest.Response
		var wg sync.WaitGroup
		wg.Go(func() { sent = send(c, tr, gen.InventoryTransferSendInput{Lines: sendLines(tr, s.shelf1.Id)}) })
		wg.Go(func() {
			archived = c.Do(http.MethodPut, sitePath(to.Id), gen.InventorySiteUpdate{Code: to.Code, Name: to.Name, Kind: to.Kind, Status: gen.Archived}, ifMatch(to.Revision))
		})
		wg.Wait()
		switch {
		case sent.Status == http.StatusOK:
			expectRefusal(t, archived, http.StatusConflict, "site_has_open_transfers", "the archive after the send")
		case archived.Status == http.StatusOK:
			expectRefusal(t, sent, http.StatusBadRequest, "site_archived", "the send after the archive")
		default:
			t.Fatalf("send %d %s, archive %d %s: one must land", sent.Status, sent.Body, archived.Status, archived.Body)
		}
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_OppositeSendAndReceives is §9, case 8: OSL→BGO and BGO→OSL
// sent and received at once lock each other's balances in opposite orders;
// the deadlock retry reruns the loser, and both land.
func TestTransfer_OppositeSendAndReceives(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	receive(t, c, s.bgo.Id, s.bgoShelf.Id, receiptLine(varBolt, 20), receiptLine(varNut, 10))
	for range raceRounds {
		there := createTransfer(t, c, s, line(varBolt, 1), line(varNut, 1))
		back := transferAnswer(t, c.Do(http.MethodPost, transfersPath, draftInput(s.bgo.Id, s.osl.Id, line(varBolt, 1), line(varNut, 1))), http.StatusCreated, "back")
		both := func(tr gen.InventoryTransfer, from, to []int64) gen.InventoryTransferSendInput {
			return gen.InventoryTransferSendInput{Lines: sendLines(tr, from...), Receive: &gen.InventoryTransferSendReceive{Lines: []gen.InventoryTransferDestination{
				{LineId: tr.Lines[0].Id, ToLocationId: to[0]}, {LineId: tr.Lines[1].Id, ToLocationId: to[1]},
			}}}
		}
		var a, b *modtest.Response
		var wg sync.WaitGroup
		wg.Go(func() {
			a = send(c, there, both(there, []int64{s.shelf1.Id, s.shelf2.Id}, []int64{s.bgoShelf.Id, s.bgoShelf.Id}))
		})
		wg.Go(func() {
			b = send(c, back, both(back, []int64{s.bgoShelf.Id, s.bgoShelf.Id}, []int64{s.shelf1.Id, s.shelf2.Id}))
		})
		wg.Wait()
		mustStatus(t, a, http.StatusOK, "OSL to BGO")
		mustStatus(t, b, http.StatusOK, "BGO to OSL")
	}
	if got := onHand(t, h, varBolt, s.bgoShelf.Id); got != "20.000" {
		t.Errorf("bolts at BGO-B1 = %s, want 20: as many came as went", got)
	}
	assertLedgerConsistent(t, h)
}

// TestTransfer_SendRacesLocationArchive is I48 under a race: a send of a
// bin's whole stock and the bin's archive at once. The send's FOR SHARE on
// the location orders them: an archive first is refused location_has_stock
// and the send lands; a send first leaves the bin empty and the archive is
// refused location_in_transit, since a cancel would bring the stock back
// there. The archive never lands.
func TestTransfer_SendRacesLocationArchive(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPoolMaxConns(4))
	c, _ := signIn(t, h, allKeys...)
	s := newTransferSites(t, h, c)
	for i := range raceRounds {
		bin := createLocation(t, c, s.osl.RootLocationId, gen.InventoryLocationKindBin, "R"+string(rune('A'+i)))
		receive(t, c, s.osl.Id, bin.Id, receiptLine(varBolt, 1))
		tr := createTransfer(t, c, s, line(varBolt, 1))
		var sent, archived *modtest.Response
		var wg sync.WaitGroup
		wg.Go(func() { sent = send(c, tr, gen.InventoryTransferSendInput{Lines: sendLines(tr, bin.Id)}) })
		wg.Go(func() {
			archived = putLocation(c, bin, func(u *gen.InventoryLocationUpdate) { u.Status = gen.Archived })
		})
		wg.Wait()
		mustStatus(t, sent, http.StatusOK, "the send")
		if archived.Status != http.StatusConflict {
			t.Fatalf("archive = %d %s, want 409", archived.Status, archived.Body)
		}
		if code := *problemOf(t, archived).Code; code != "location_has_stock" && code != "location_in_transit" {
			t.Fatalf("archive = %s, want location_has_stock or location_in_transit", code)
		}
	}
	assertLedgerConsistent(t, h)
}
