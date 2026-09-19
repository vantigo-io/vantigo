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
