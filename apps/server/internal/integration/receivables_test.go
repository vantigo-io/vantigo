package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/bankfile/bankfiletest"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The receivables end to end (invoices payments and reminders design D3–D12,
// Task 14): the real customers and invoices modules, the SMTP seam recorded
// with MAIL_DRIVER=smtp, bank files built by bankfiletest — the parsers'
// own builders — and the reminder worker as worker mode builds it, so the
// bank import, matching, the queue, the run, the letter and attention are
// the module's own, against a customer the customers module made.

// receivablesAccount is the seller's bank account as the files name it:
// invoicesInstallation's 8601.11.17947.
const receivablesAccount = "86011117947"

// receivableDoc is an issued invoice as the story reads it.
type receivableDoc struct {
	ID         int64    `json:"id"`
	State      string   `json:"state"`
	Kid        *string  `json:"kid"`
	DueDate    *string  `json:"dueDate"`
	GrossTotal float64  `json:"grossTotal"`
	OpenAmount *float64 `json:"openAmount"`
	Charges    *struct {
		Claimed     float64 `json:"claimed"`
		Paid        float64 `json:"paid"`
		Outstanding float64 `json:"outstanding"`
	} `json:"charges"`
	Payments []struct {
		Amount float64 `json:"amount"`
		Source string  `json:"source"`
	} `json:"payments"`
	ChargePayments *[]struct {
		Amount float64 `json:"amount"`
		Source string  `json:"source"`
	} `json:"chargePayments"`
	Reminders *[]struct {
		ID        int64    `json:"id"`
		Status    string   `json:"status"`
		Recipient *string  `json:"recipient"`
		Fee       *float64 `json:"fee"`
		Total     *float64 `json:"total"`
	} `json:"reminders"`
}

// receivableLine is one bank line of the queue as the story reads it.
type receivableLine struct {
	ID     int64   `json:"id"`
	Status string  `json:"status"`
	Reason *string `json:"reason"`
	Amount float64 `json:"amount"`
	Kid    *string `json:"kid"`
}

// attentionItem is one dashboard attention item.
type attentionItem struct {
	Type     string `json:"type"`
	EntityID string `json:"entityId"`
}

// uploadBankFile imports data as a bank file and answers the file's id.
func uploadBankFile(t *testing.T, c *modtest.Client, data []byte) int64 {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "bank.dat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	res := c.Do(http.MethodPost, invoicesBase+"/bank-files", nil, modtest.RawBody(w.FormDataContentType(), buf.Bytes()))
	if res.Status != http.StatusCreated {
		t.Fatalf("the import = %d %s, want 201", res.Status, res.Body)
	}
	var r struct {
		File struct {
			ID int64 `json:"id"`
		} `json:"file"`
	}
	res.JSON(&r)
	return r.File.ID
}

// linesOf is a bank file's lines through the queue's list.
func linesOf(t *testing.T, c *modtest.Client, fileID int64) []receivableLine {
	t.Helper()
	var body struct {
		Data []receivableLine `json:"data"`
	}
	okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/bank-transactions?bankFileId=%d&pageSize=100", invoicesBase, fileID), nil, &body)
	return body.Data
}

// lineStates is lines as "status reason amount", in the list's order.
func lineStates(lines []receivableLine) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		reason := "-"
		if l.Reason != nil {
			reason = *l.Reason
		}
		out = append(out, fmt.Sprintf("%s %s %.2f", l.Status, reason, l.Amount))
	}
	slices.Sort(out)
	return out
}

// readReceivable reads invoice id.
func readReceivable(t *testing.T, c *modtest.Client, id int64) receivableDoc {
	t.Helper()
	var doc receivableDoc
	okJSON(t, c, http.MethodGet, invoiceAt(id), nil, &doc)
	return doc
}

// attentionTypes is the caller's attention items as "type entity".
func attentionTypes(t *testing.T, c *modtest.Client) []string {
	t.Helper()
	var items []attentionItem
	okJSON(t, c, http.MethodGet, invoicesBase+"/stats/attention", nil, &items)
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.Type+" "+i.EntityID)
	}
	return out
}

// minorOf is an amount in øre.
func minorOf(amount float64) int64 { return int64(amount*100 + 0.5) }

// TestReceivables_FromBankFileToPaidCharges: a business made through the
// customers API, its reminders to its own address; two invoices issued with
// KIDs and e-mailed. An OCR file pays one in full and one in part and names
// an unknown KID: two payments and one kid_unknown exception. Past E + 14,
// a fresh file in and a run on the partly paid one: the worker sends the
// letter with its fee to the reminder address. The account goes over to
// camt.054: a file repeating the OCR period's payment is held back as a
// possible duplicate, and a payment of the principal and the fee with the
// KID registers both — a payment and a charge payment — so the invoice is
// paid with nothing outstanding. The queue's two lines handled, attention
// is empty.
func TestReceivables_FromBankFileToPaidCharges(t *testing.T) {
	t.Parallel()
	h, admin, smtp := invoicesInstallation(t)
	perms := []string{"invoices:access", "invoices:create", "invoices:issue", "invoices:manage", "invoices:payments"}
	signIn := func() *modtest.Client {
		c, _ := h.SignInUser(t, perms...)
		return c
	}
	advanceTo := func(at time.Time) *modtest.Client {
		h.Advance(at.Sub(h.Now()))
		return signIn()
	}

	var seller struct {
		Revision int32 `json:"revision"`
	}
	okJSON(t, admin, http.MethodGet, invoicesBase+"/settings", nil, &seller)
	okJSON(t, admin, http.MethodPut, invoicesBase+"/settings", map[string]any{
		"legalName": "Kraft-Verket AS", "organisationNumber": "974 760 673",
		"vatRegistered": true, "inForetaksregisteret": true,
		"addressLine1": "Storgata 1", "addressLine2": "", "postalCode": "0155", "city": "Oslo", "country": "no",
		"bankAccount": "8601.11.17947", "iban": "NO93 8601 1117 947", "bic": "dnbanokkxxx",
		"email": sellerEmail, "defaultPaymentTermsDays": 14, "defaultCurrency": "NOK",
		"footerText": "Takk for handelen.", "seriesStart": 1,
		"peppolId": nil, "kidLength": 7, "kidAlgorithm": kid.Mod10, "revision": seller.Revision,
		"workVatCodes":     map[string]any{"hours": 1, "expenses": 1, "milestones": 1},
		"timesheetDefault": false, "timesheetPersonLabel": "initials",
	}, nil)
	var reminders map[string]any
	okJSON(t, admin, http.MethodGet, invoicesBase+"/settings/reminders", nil, &reminders)
	okJSON(t, admin, http.MethodPut, invoicesBase+"/settings/reminders", map[string]any{
		"enabled": true, "firstReminderDays": 14, "deadlineDays": 14, "graceDays": 3, "remindersBeforeNotice": 1,
		"collectionNotice": true, "personCharge": "fee", "businessCharge": "fee", "lateInterest": false,
		"staleImportDays": 3, "inkassolov2026From": nil, "regimeReviewedThrough": "2026-12-31", "revision": reminders["revision"],
	}, nil)

	customer := invoiceNewBusiness(t, admin, "Kunde AS", "Kunde AS", "923609016")
	invoiceBillingProfile(t, admin, customer.Id, map[string]any{
		"invoiceEmail": "faktura@kunde.example", "reminderEmail": "purring@kunde.example",
	})
	var full, part receivableDoc
	for _, doc := range []*receivableDoc{&full, &part} {
		issued := issueDoc(t, admin, newDraft(t, admin, customer.Id).ID)
		okJSON(t, admin, http.MethodPost, invoiceAt(issued.ID)+"/send", map[string]any{}, nil)
		*doc = readReceivable(t, admin, issued.ID)
		if doc.Kid == nil || doc.GrossTotal != 12500 {
			t.Fatalf("invoice %d = KID %v gross %v, want a KID and 12 500", doc.ID, doc.Kid, doc.GrossTotal)
		}
	}
	unknown, err := kid.Compute(999, 7, kid.Mod10)
	if err != nil {
		t.Fatal(err)
	}

	// The OCR file: one paid in full, one in part, an unknown KID.
	c := advanceTo(time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC))
	booked := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	giro := func(minor int64, k, ref string, on time.Time) bankfiletest.OCRPayment {
		return bankfiletest.OCRPayment{Type: 10, Account: receivablesAccount, Settled: on, Ordered: on, AmountMinor: minor, KID: k, ArchiveRef: ref}
	}
	ocr := uploadBankFile(t, c, bankfiletest.OCR("0000001",
		giro(1250000, *full.Kid, "A1", booked), giro(500000, *part.Kid, "A2", booked), giro(300000, unknown, "A3", booked)))
	if got := lineStates(linesOf(t, c, ocr)); !slices.Equal(got, []string{
		"exception kid_unknown 3000.00", "matched - 12500.00", "matched - 5000.00",
	}) {
		t.Errorf("the OCR file's lines = %v, want two matched and one kid_unknown", got)
	}
	if got := readReceivable(t, c, full.ID); got.State != "paid" || len(got.Payments) != 1 || got.Payments[0].Source != "ocr" {
		t.Errorf("the invoice paid in full = %s %+v, want paid by one OCR payment", got.State, got.Payments)
	}
	if got := readReceivable(t, c, part.ID); got.State != "partially_paid" || got.OpenAmount == nil || *got.OpenAmount != 7500 {
		t.Errorf("the invoice paid in part = %s open %v, want partially_paid with 7 500 open", got.State, got.OpenAmount)
	}

	// Past E + 14 — due Saturday 26 September, E Monday the 28th — a fresh
	// file pays a little more, and a run reminds the partly paid one.
	c = advanceTo(time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))
	fresh := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	uploadBankFile(t, c, bankfiletest.OCR("0000002", giro(100000, *part.Kid, "B1", fresh)))
	if got := attentionTypes(t, c); !slices.Contains(got, fmt.Sprintf("invoiceOverdue %d", part.ID)) {
		t.Errorf("attention = %v, want the overdue invoice", got)
	}
	var run struct {
		Created []struct {
			ID      int64  `json:"id"`
			Channel string `json:"channel"`
		} `json:"created"`
	}
	okJSON(t, c, http.MethodPost, invoicesBase+"/reminder-runs", map[string]any{
		"dryRun": false, "items": []map[string]any{{"invoiceId": part.ID, "action": "reminder"}},
	}, &run)
	if len(run.Created) != 1 || run.Created[0].Channel != "email" {
		t.Fatalf("the run = %+v, want one letter by e-mail", run)
	}
	before := len(smtp.mails())
	if processed, err := invoices.NewReminderWorker(h.Deps()).ProcessOne(context.Background()); err != nil || !processed {
		t.Fatalf("the worker = %v, %v; want the letter sent", processed, err)
	}
	mails := smtp.mails()
	if len(mails) != before+1 || !slices.Equal(mails[len(mails)-1].To, []string{"purring@kunde.example"}) {
		t.Fatalf("mails = %d, the last to %v; want one more, to the reminder address", len(mails), mails[len(mails)-1].To)
	}
	letter := readReceivable(t, c, part.ID)
	if letter.Reminders == nil || len(*letter.Reminders) != 1 {
		t.Fatalf("the invoice's letters = %+v, want one", letter.Reminders)
	}
	sent := (*letter.Reminders)[0]
	if sent.Status != "sent" || sent.Fee == nil || *sent.Fee <= 0 || letter.Charges == nil || letter.Charges.Outstanding != *sent.Fee {
		t.Fatalf("the letter = %+v, charges %+v; want sent with its fee outstanding", sent, letter.Charges)
	}
	fee := *sent.Fee
	open := *letter.OpenAmount

	// Over to camt.054: the cutover holds back the OCR period's payment
	// repeated; the principal and the fee paid with the KID register both.
	c = advanceTo(time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC))
	okJSON(t, c, http.MethodPut, invoicesBase+"/bank-accounts/"+receivablesAccount+"/format", map[string]any{"format": "camt054"}, nil)
	camt := uploadBankFile(t, c, bankfiletest.Camt054("camt.054.001.02", "CAMT-1", h.Now(), receivablesAccount,
		bankfiletest.CamtEntry{BookedOn: booked, Txs: []bankfiletest.CamtTx{{AmountMinor: 500000, KID: *part.Kid, AcctSvcrRef: "C1"}}},
		bankfiletest.CamtEntry{BookedOn: fresh.AddDate(0, 0, 1), Txs: []bankfiletest.CamtTx{{AmountMinor: minorOf(open + fee), KID: *part.Kid, AcctSvcrRef: "C2"}}},
	))
	lines := linesOf(t, c, camt)
	if got := lineStates(lines); !slices.Equal(got, []string{
		"exception possible_duplicate 5000.00", fmt.Sprintf("matched - %.2f", open+fee),
	}) {
		t.Errorf("the camt.054 file's lines = %v, want the repeat a possible duplicate and the payment matched", got)
	}
	paid := readReceivable(t, c, part.ID)
	if paid.State != "paid" || paid.Charges == nil || paid.Charges.Outstanding != 0 || paid.Charges.Paid != fee ||
		paid.ChargePayments == nil || len(*paid.ChargePayments) != 1 || (*paid.ChargePayments)[0].Source != "camt054" ||
		(*paid.ChargePayments)[0].Amount != fee {
		t.Errorf("the invoice = %s, charges %+v, charge payments %+v; want paid, the fee paid by a camt.054 charge payment",
			paid.State, paid.Charges, paid.ChargePayments)
	}

	// The queue's two lines handled, nothing is left to look at.
	for _, l := range append(linesOf(t, c, ocr), lines...) {
		switch {
		case l.Status != "exception":
		case l.Reason != nil && *l.Reason == "possible_duplicate":
			okJSON(t, c, http.MethodPost, fmt.Sprintf("%s/bank-transactions/%d/confirm-duplicate", invoicesBase, l.ID),
				map[string]any{"note": "Samme innbetaling som i OCR-filen"}, nil)
		default:
			okJSON(t, c, http.MethodPost, fmt.Sprintf("%s/bank-transactions/%d/dismiss", invoicesBase, l.ID),
				map[string]any{"note": "Ikke vår kunde"}, nil)
		}
	}
	if got := attentionTypes(t, c); len(got) != 0 {
		t.Errorf("attention = %v, want nothing left: %s", got, strings.Join(got, ", "))
	}
}
