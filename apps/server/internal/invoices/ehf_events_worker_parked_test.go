package invoices_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
)

// An unconfirmed row parked for a person ('infinity': thirty days of probing
// passed, or it never had a reference) awaits no event: the provider retries
// its events for days, not forever, so a drain for it would read the queue
// every cycle for nothing. The same row still probed starts a drain.
func TestEhfEventsWorker_IgnoresAnUnconfirmedRowParkedForAPerson(t *testing.T) {
	t.Parallel()
	h, w := eventsReady(t)
	id := plantWithRef(t, h, "unconfirmed", "guid-u")
	h.Exec(t, `UPDATE invoices.transmissions SET next_attempt_at = 'infinity' WHERE id = $1`, id)
	h.storecove.Enqueue(storecovetest.Event{Event: "succeeded", SubmissionGUID: "guid-x"})
	runEvents(t, w)
	if h.storecove.Requests() != 0 {
		t.Errorf("requests = %d for a parked unconfirmed row, want none", h.storecove.Requests())
	}

	h.Exec(t, `UPDATE invoices.transmissions SET next_attempt_at = $2 WHERE id = $1`, id, h.Now())
	runEvents(t, w)
	if h.storecove.Queued() != 0 {
		t.Errorf("an unconfirmed row still probed did not start a drain")
	}
}
