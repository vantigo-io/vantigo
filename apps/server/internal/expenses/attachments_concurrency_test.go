package expenses_test

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
)

// race runs every fn at once — released together, so none has a head start —
// and waits for all of them.
func race(fns ...func()) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn()
		}()
	}
	close(start)
	wg.Wait()
}

// raceRounds is how many times each race is run: enough that a missing row
// lock would not pass by luck of scheduling.
const raceRounds = 6

// TestExpensesReceipts_TwoUploadsRacingForTheLastSlot: the count bound is
// decided under the expense's own row lock, so two uploads that both see nine
// receipts cannot both become the tenth. Exactly one is created, the other is
// refused, and the refused one's object is removed again — the store never
// keeps more than the rows do.
func TestExpensesReceipts_TwoUploadsRacingForTheLastSlot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	for round := range raceRounds {
		entry := createEntry(t, owner, outlayBody(nil))
		for i := range 9 {
			uploadReceipt(t, owner, entry.Id, fmt.Sprintf("kvittering-%d.pdf", i), "application/pdf", testPDF(8))
		}

		var first, second int
		race(
			func() { first = postReceipt(t, owner, entry.Id, "ti.pdf", "application/pdf", testPDF(8)).Status },
			func() { second = postReceipt(t, owner, entry.Id, "elleve.pdf", "application/pdf", testPDF(16)).Status },
		)
		// Either order will do; what matters is that exactly one of them won.
		answers := []int{first, second}
		slices.Sort(answers)
		if !slices.Equal(answers, []int{http.StatusCreated, http.StatusBadRequest}) {
			t.Errorf("round %d: %d and %d, want one 201 and one 400", round, first, second)
		}
		if got := getEntry(t, owner, entry.Id); got.AttachmentCount != 10 || len(got.Attachments) != 10 {
			t.Errorf("round %d: the expense carries %d receipts, want ten", round, got.AttachmentCount)
		}
		if n := h.objects.count(); n != 10*(round+1) {
			t.Errorf("round %d: the store holds %d objects, want %d — the refused upload's was not removed",
				round, n, 10*(round+1))
		}
	}
}

// TestExpensesReceipts_AnUploadRacingTheExpensesDeletion: both take the
// expense's row lock, so they cannot interleave — either the upload commits
// first and the delete carries its object away with the rest, or the delete
// commits first and the upload finds the expense gone and removes the object
// it had already written. What must never happen is an object left behind by
// an expense that no longer exists: with the keys read outside the delete's
// own transaction, an upload committing in that window is exactly that.
func TestExpensesReceipts_AnUploadRacingTheExpensesDeletion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	for round := range raceRounds {
		entry := createEntry(t, owner, outlayBody(nil))
		uploadReceipt(t, owner, entry.Id, "fra-for.pdf", "application/pdf", testPDF(8))

		var upload, remove int
		race(
			func() {
				upload = postReceipt(t, owner, entry.Id, "i-kapplop.pdf", "application/pdf", testPDF(16)).Status
			},
			func() { remove = owner.Do(http.MethodDelete, entryPath(entry.Id), nil).Status },
		)
		if remove != http.StatusNoContent {
			t.Fatalf("round %d: delete answered %d, want 204", round, remove)
		}
		if upload != http.StatusCreated && upload != http.StatusNotFound {
			t.Errorf("round %d: upload answered %d, want 201 (it won) or 404 (the expense had gone)", round, upload)
		}
		if n := h.objects.count(); n != 0 {
			t.Errorf("round %d: the store holds %v after the expense was deleted, want nothing",
				round, h.objects.keys())
		}
	}
}
