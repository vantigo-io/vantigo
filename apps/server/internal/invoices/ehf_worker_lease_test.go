package invoices_test

import (
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/accesspoint/storecovetest"
)

// The evidence claim makes up to three bounded calls — the fetch, the
// receipt, the delivered copy — against one 60-second lease, so it judges
// the lease again before the delivered copy: a claim whose receipt read took
// it past half the lease stores nothing more and is due again at once with
// nothing counted, and the next claim finishes the job.
func TestEhfWorker_TheEvidenceClaimStopsWhenItsLeaseRunsShort(t *testing.T) {
	t.Parallel()
	h, _ := ehfWorking(t)
	id, tid := queuedEhf(t, h)
	w := invoices.NewEhfWorker(h.Deps())
	processOne(t, w)
	guid := h.storecove.Submissions()[0].GUID
	delivered := []byte("<Invoice>as delivered</Invoice>")
	h.storecove.Evidence(guid, storecovetest.Document{Body: delivered})
	h.Advance(5 * time.Minute)
	processOne(t, w)
	if row := txRow(t, h, tid); row.Status != "delivered" || row.EvidenceObjectKey != nil {
		t.Fatalf("the probe: %s evidence %v, want delivered without evidence", row.Status, row.EvidenceObjectKey)
	}

	// An earlier claim stored the receipt, so this one reads it back for its
	// hash — and that read leaves 29 seconds of the lease, less than the 30 a
	// call may take.
	base := "documents/" + itoa(id) + "/" + itoa(documentNumber(t, h, id)) + "-" + itoa(tid)
	receiptKey := base + "-receipt.json"
	earlier := []byte(`{"stored":"by an earlier claim"}`)
	h.objects.replace(receiptKey, earlier)
	h.objects.beforeGets(func(key string) {
		if key == receiptKey {
			h.Advance(31 * time.Second)
		}
	})
	before := txRow(t, h, tid)
	processOne(t, w)
	h.objects.beforeGets(nil)

	row := txRow(t, h, tid)
	if h.objects.object(base+"-delivered.xml") != nil {
		t.Fatalf("the claim stored the delivered copy with less of its lease left than the call may take")
	}
	if row.EvidenceObjectKey != nil {
		t.Errorf("a claim out of time recorded the evidence %v", *row.EvidenceObjectKey)
	}
	if row.PollAttempts != before.PollAttempts || row.SubmitAttempts != before.SubmitAttempts {
		t.Errorf("attempts = %d submit, %d poll; want %d, %d: running out of time is not an attempt",
			row.SubmitAttempts, row.PollAttempts, before.SubmitAttempts, before.PollAttempts)
	}
	if got := deref(row.LastError); got != "The claim ran out of time before the call; it is retried." {
		t.Errorf("last_error = %q, want the out-of-time reason", got)
	}
	wantNext(t, row, h.Now())

	processOne(t, w)
	row = txRow(t, h, tid)
	if row.EvidenceObjectKey == nil || *row.EvidenceObjectKey != receiptKey || deref(row.EvidenceSha256) != sha(earlier) || row.LastError != nil {
		t.Fatalf("the next claim: evidence %v %s error %v; want %s with the stored receipt's hash, no error",
			row.EvidenceObjectKey, deref(row.EvidenceSha256), row.LastError, receiptKey)
	}
	if string(h.objects.object(base+"-delivered.xml")) != string(delivered) {
		t.Errorf("the next claim did not store the delivered copy")
	}
}
