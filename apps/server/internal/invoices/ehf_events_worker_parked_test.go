package invoices_test

import (
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
)

// An unconfirmed row parked for a person ('infinity') with a provider
// reference awaits no event: it waits for a person, so a drain for it alone
// would read the queue every cycle. One parked without a reference — parked
// the moment it became unconfirmed — can be resolved by the machine only
// through an event matched by its idempotency key, so it is listened for
// until fourteen days after its queueing, and not after. The same row with a
// reference, still probed, starts a drain. An event for any row still in the
// queue is applied whenever a drain runs.
func TestEhfEventsWorker_IgnoresAnUnconfirmedRowParkedForAPerson(t *testing.T) {
	t.Parallel()
	h, w := eventsReady(t)
	park := func(id int64) {
		h.Exec(t, `UPDATE invoices.transmissions SET next_attempt_at = 'infinity' WHERE id = $1`, id)
	}
	drained := func(what string, want bool) {
		t.Helper()
		h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-x"})
		requests := h.storecove.Requests()
		runEvents(t, w)
		if got := h.storecove.Requests() > requests; got != want {
			t.Errorf("%s: a drain = %v, want %v", what, got, want)
		}
	}

	referenced := plantWithRef(t, h, "unconfirmed", "guid-u")
	park(referenced)
	drained("a parked unconfirmed row with a reference", false)
	h.Exec(t, `UPDATE invoices.transmissions SET next_attempt_at = $2 WHERE id = $1`, referenced, h.Now())
	drained("an unconfirmed row still probed", true)
	park(referenced)
	drained("the row parked again", false)

	referenceless := plantWithRef(t, h, "unconfirmed", "")
	park(referenceless)
	drained("a parked unconfirmed row without a reference, queued today", true)
	h.Advance(13 * 24 * time.Hour)
	drained("the same row thirteen days after its queueing", true)
	h.Advance(2 * 24 * time.Hour)
	drained("the same row fifteen days after its queueing", false)
}
