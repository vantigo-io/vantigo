package invoices_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// A letter's own operations (invoices payments and reminders design D10,
// plan reading 12): the list, the PDF, a person's withdrawal and the retry.

// letterProblemJSON is a letter's 409 as a client reads it.
type letterProblemJSON struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// refusedLetter asserts res is a 409 with code.
func refusedLetter(t *testing.T, what string, res *modtest.Response, code string) {
	t.Helper()
	var p letterProblemJSON
	if res.Status != http.StatusConflict {
		t.Errorf("%s = %d %s, want 409 %s", what, res.Status, res.Body, code)
		return
	}
	res.JSON(&p)
	if p.Code != code {
		t.Errorf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
	}
}

// letterAnswer asserts res is a 200 and answers the letter.
func letterAnswer(t *testing.T, what string, res *modtest.Response) reminderJSON {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("%s = %d %s, want 200", what, res.Status, res.Body)
	}
	var r reminderJSON
	res.JSON(&r)
	return r
}

// The retry (D10): a failed letter back to queued, due now, its 48 hours and
// backoff begun again — and then sent by the next claim; anything else is
// reminder_not_failed; no letter is a 404; a reader without
// invoices:payments is refused.
func TestReminderLetters_Retry(t *testing.T) {
	t.Parallel()
	h, mails := workerHarness(t, "")
	c := payer(t, h)
	if res := c.Do(http.MethodPost, letterPath(999999, "retry"), nil); res.Status != http.StatusNotFound {
		t.Errorf("retry of no letter = %d, want 404", res.Status)
	}
	_, queued := dueLetter(t, h, 1)
	refusedLetter(t, "retry of a queued letter", c.Do(http.MethodPost, letterPath(queued, "retry"), nil), "reminder_not_failed")
	failed := plantFailed(t, h, deliveredOn(t, h, 2, customerAcme, "2026-08-03"))
	if res := reader(t, h).Do(http.MethodPost, letterPath(failed, "retry"), nil); res.Status != http.StatusForbidden {
		t.Errorf("retry by a reader = %d, want 403", res.Status)
	}
	got := letterAnswer(t, "the retry", c.Do(http.MethodPost, letterPath(failed, "retry"), nil))
	r := letterOf(t, h, failed)
	if got.Status != "queued" || got.Attempts != 0 || r.NextAttemptAt == nil || !r.NextAttemptAt.Equal(h.Now()) ||
		r.FirstAttemptAt != nil || r.FailedAt != nil {
		t.Errorf("after the retry: %s attempts %d next %v first %v failed %v; want queued, due now, begun again",
			got.Status, got.Attempts, r.NextAttemptAt, r.FirstAttemptAt, r.FailedAt)
	}
	refusedLetter(t, "a second retry", c.Do(http.MethodPost, letterPath(failed, "retry"), nil), "reminder_not_failed")
	w := invoices.NewReminderWorker(h.Deps())
	for dispatch(t, w) {
	}
	if r := letterOf(t, h, failed); r.Status != "sent" || len(mails.delivered()) != 2 {
		t.Errorf("the retried letter = %s after %d mails, want sent", r.Status, len(mails.delivered()))
	}
}

// A withdrawal during a claim (plan reading 45): one before the dispatch's
// first step is seen at its re-read — the letter withdrawn by the person,
// nothing mailed; one after it — the facts written, the lease live: the
// letter is being sent — is refused reminder_not_withdrawable and the letter
// becomes sent; after a failed attempt, which cleared the lease and the
// facts, it may be withdrawn again. A sent letter is not withdrawable; a
// reason is required.
func TestReminderLetters_WithdrawDuringAClaim(t *testing.T) {
	h, mails := workerHarness(t, "")
	c := payer(t, h)
	w := invoices.NewReminderWorker(h.Deps())
	withdraw := func(id int64) *modtest.Response {
		return c.Do(http.MethodPost, letterPath(id, "withdraw"), map[string]any{"reason": " Avtalt nedbetaling "})
	}

	// Before step 1: the dispatch holds the invoice; the letter is free.
	_, before := dueLetter(t, h, 1)
	if res := c.Do(http.MethodPost, letterPath(before, "withdraw"), map[string]any{"reason": "  "}); res.Status != http.StatusBadRequest {
		t.Errorf("a withdrawal without a reason = %d, want 400", res.Status)
	}
	var answered *modtest.Response
	restore := invoices.SetDispatchAfterLock(func(context.Context, int64) error {
		answered = withdraw(before)
		return nil
	})
	dispatch(t, w)
	restore()
	r := letterOf(t, h, before)
	if answered == nil || answered.Status != http.StatusOK || r.Status != "withdrawn" || deref(r.WithdrawalReason) != "Avtalt nedbetaling" ||
		r.WithdrawnByUserID == nil || len(mails.tried()) != 0 {
		t.Fatalf("a withdrawal before step 1 = %v; the letter %s %q by %v after %d sends; want it withdrawn by the person, nothing mailed",
			answered, r.Status, deref(r.WithdrawalReason), r.WithdrawnByUserID, len(mails.tried()))
	}

	// After step 1: being sent.
	_, during := dueLetter(t, h, 2)
	var refused *modtest.Response
	mails.holdWith(func(context.Context, mail.Outbound) { refused = withdraw(during) })
	dispatch(t, w)
	mails.holdWith(nil)
	if refused == nil {
		t.Fatal("the send was never reached")
	}
	refusedLetter(t, "a withdrawal while the letter is being sent", refused, "reminder_not_withdrawable")
	if r := letterOf(t, h, during); r.Status != "sent" {
		t.Errorf("the letter being sent became %s, want sent", r.Status)
	}
	refusedLetter(t, "a withdrawal of a sent letter", withdraw(during), "reminder_not_withdrawable")

	// After a failed attempt: withdrawable again.
	_, after := dueLetter(t, h, 3)
	mails.failWith(errors.New("421 try again later"))
	dispatch(t, w)
	if got := letterAnswer(t, "a withdrawal after a failed attempt", withdraw(after)); got.Status != "withdrawn" {
		t.Errorf("the letter = %s, want withdrawn", got.Status)
	}
	refusedLetter(t, "a second withdrawal", withdraw(after), "reminder_not_withdrawable")
	if res := c.Do(http.MethodPost, letterPath(999999, "withdraw"), map[string]any{"reason": "x"}); res.Status != http.StatusNotFound {
		t.Errorf("a withdrawal of no letter = %d, want 404", res.Status)
	}
}

// The download (D10): a printed or sent letter's stored PDF, to anyone with
// invoices:access, as an attachment in the letter's language, never cached;
// a letter not sent is reminder_not_sent; an object altered or gone is a
// 500, never rendered again; no letter is a 404.
func TestReminderLetters_Download(t *testing.T) {
	t.Parallel()
	h, mails := workerHarness(t, "")
	c := reader(t, h)
	inv := plantLetterInvoice(t, h, letterInvoice{number: 41, customer: customerAcme, due: "2026-08-03"})
	letter := queueLetter(t, h, inv, 1, "reminder", false, "nb")
	if res := c.Do(http.MethodGet, letterPath(999999, "pdf"), nil); res.Status != http.StatusNotFound {
		t.Errorf("the PDF of no letter = %d, want 404", res.Status)
	}
	refusedLetter(t, "the PDF of a queued letter", c.Do(http.MethodGet, letterPath(letter, "pdf"), nil), "reminder_not_sent")
	dispatch(t, invoices.NewReminderWorker(h.Deps()))
	res := c.Do(http.MethodGet, letterPath(letter, "pdf"), nil)
	mailed := mails.delivered()[0].Attachments[0].Content
	if res.Status != http.StatusOK || res.Header("Content-Type") != "application/pdf" ||
		res.Header("Content-Disposition") != `attachment; filename="purring-41-1.pdf"` ||
		res.Header("Cache-Control") != "private, no-store" || string(res.Body) != string(mailed) {
		t.Errorf("the PDF = %d %s %q %q (%d bytes), want the mailed PDF as purring-41-1.pdf, never cached",
			res.Status, res.Header("Content-Type"), res.Header("Content-Disposition"), res.Header("Cache-Control"), len(res.Body))
	}
	key := deref(letterOf(t, h, letter).PdfObjectKey)
	h.objects.replace(key, []byte("%PDF-1.4 something else"))
	if res := c.Do(http.MethodGet, letterPath(letter, "pdf"), nil); res.Status != http.StatusInternalServerError {
		t.Errorf("the PDF of an altered object = %d, want 500", res.Status)
	}
	h.objects.lose(key)
	if res := c.Do(http.MethodGet, letterPath(letter, "pdf"), nil); res.Status != http.StatusInternalServerError {
		t.Errorf("the PDF of a lost object = %d, want 500", res.Status)
	}
}

// The list (plan reading 12): every letter, the newest first, filtered by
// status, channel, invoice and run, paged after the filter; its recipient
// answered only to a caller with invoices:payments; a status that is none of
// the letters' is a 400.
func TestReminderLetters_List(t *testing.T) {
	t.Parallel()
	h, _ := workerHarness(t, "")
	a := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	b := deliveredOn(t, h, 2, customerAcme, "2026-08-03")
	sent := plantLetterSent(t, h, a, 1, "reminder", "2026-08-20", "", "")
	queued := queueLetter(t, h, a, 2, "collection_notice", false, "nb")
	paper := plantLetterIn(t, h, b, 1, "awaiting_print")
	runOf := modtest.One[int64](t, h.Harness, `SELECT run_id FROM invoices.reminders WHERE id = $1`, queued)

	type page struct {
		Data []struct {
			ID        int64   `json:"id"`
			Recipient *string `json:"recipient"`
		} `json:"data"`
		Pagination struct {
			TotalCount int `json:"totalCount"`
		} `json:"pagination"`
	}
	list := func(c *modtest.Client, query string) page {
		t.Helper()
		res := c.Do(http.MethodGet, remindersPath+query, nil)
		if res.Status != http.StatusOK {
			t.Fatalf("GET /invoices/reminders%s = %d %s, want 200", query, res.Status, res.Body)
		}
		var p page
		res.JSON(&p)
		return p
	}
	ids := func(p page) []int64 {
		out := []int64{}
		for _, d := range p.Data {
			out = append(out, d.ID)
		}
		return out
	}
	c := payer(t, h)
	for _, f := range []struct {
		query string
		want  []int64
	}{
		{"", []int64{paper, queued, sent}},
		{"?status=queued", []int64{queued}},
		{"?status=sent", []int64{sent}},
		{"?channel=paper", []int64{paper}},
		{"?invoiceId=" + itoa(a), []int64{queued, sent}},
		{"?runId=" + itoa(runOf), []int64{queued}},
		{"?invoiceId=" + itoa(a) + "&channel=email&pageSize=1&page=2", []int64{sent}},
	} {
		if got := ids(list(c, f.query)); !slices.Equal(got, f.want) {
			t.Errorf("GET /invoices/reminders%s = %v, want %v", f.query, got, f.want)
		}
	}
	if p := list(c, "?invoiceId="+itoa(a)+"&pageSize=1"); p.Pagination.TotalCount != 2 {
		t.Errorf("the total = %d, want 2: paged after the filter", p.Pagination.TotalCount)
	}
	if p := list(c, "?status=queued"); p.Data[0].Recipient == nil || *p.Data[0].Recipient != "purring@acme.example" {
		t.Errorf("a payer reads the recipient %v, want purring@acme.example", p.Data[0].Recipient)
	}
	if p := list(reader(t, h), "?status=queued"); p.Data[0].Recipient != nil {
		t.Errorf("a reader reads the recipient %q, want none", *p.Data[0].Recipient)
	}
	for _, q := range []string{"?status=posted", "?channel=fax", "?page=0"} {
		if res := c.Do(http.MethodGet, remindersPath+q, nil); res.Status != http.StatusBadRequest {
			t.Errorf("GET /invoices/reminders%s = %d, want 400", q, res.Status)
		}
	}
}
