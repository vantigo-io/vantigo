package invoices_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The overdue list (invoices payments and reminders design D12): the issued
// invoices overdue today — the fixed clock's Saturday 12 September 2026 —
// judged whole by the reminder engine before the action filter and the page
// (M7). The invoices are planted by SQL, as issued documents with a buyer
// snapshot, so each test sets exactly the facts it judges.

const (
	overduePath      = invoicesPath + "/overdue"
	reminderRunsPath = invoicesPath + "/reminder-runs"
)

// overdueSpec is an issued invoice planted for the engine: its number,
// customer, buyer snapshot, dates and gross.
type overdueSpec struct {
	number             int64
	customer           int32
	issue, due         string
	gross              string
	buyerType, orgNo   string // "" for NULL
	language           string // "" for NULL
	deliveredOnIssueOn bool
}

// plantOverdue plants spec as an issued invoice and answers its id; with
// deliveredOnIssueOn, a manual delivery on its issue date beside it.
func plantOverdue(t *testing.T, h *harness, spec overdueSpec) int64 {
	t.Helper()
	gross := spec.gross
	if gross == "" {
		gross = "1000"
	}
	id := modtest.One[int64](t, h.Harness, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, due_date, exchange_rate_date,
		    seller_legal_name, buyer_name, buyer_type, buyer_organisation_number, buyer_language, gross_total, issued_at,
		    created_by_user_id, created_at, updated_at)
		VALUES ('invoice', 'issued', $1, $2, $3::date, $4::date, $3::date, 'Selger AS', $6, NULLIF($7, ''), NULLIF($8, ''),
		    NULLIF($9, ''), $5::numeric, now(), gen_random_uuid(), now(), now())
		RETURNING id`, spec.number, spec.customer, spec.issue, spec.due, gross, fmt.Sprintf("Kunde %d", spec.number),
		spec.buyerType, spec.orgNo, spec.language)
	if spec.deliveredOnIssueOn {
		plantManualDelivery(t, h, id, spec.issue)
	}
	return id
}

// delivered is an overdue invoice of customer, 1000 gross, issued 1 July
// 2026 and handed over that day, due on due.
func deliveredOn(t *testing.T, h *harness, number int64, customer int32, due string) int64 {
	t.Helper()
	return plantOverdue(t, h, overdueSpec{number: number, customer: customer, issue: "2026-07-01", due: due, deliveredOnIssueOn: true})
}

// plantLetterSent plants a sent letter of invoice id: its level, sent on
// sentOn with the deadline 14 days on, a reminder fee when fee is not "",
// and its cumulative interest. It answers the letter's id.
func plantLetterSent(t *testing.T, h *harness, id int64, sequence int, level, sentOn, fee, interest string) int64 {
	t.Helper()
	feeKind := "none"
	if fee != "" {
		feeKind = "reminder_fee"
	}
	if interest == "" {
		interest = "0"
	}
	return plantID(t, h, `
		INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id,
		    status, sent_on, deadline, regime, principal_open, fee_kind, fee, charges_earlier, interest,
		    interest_waived, interest_paid, total, sent_at)
		VALUES ($1, $2, $3, $4, 'email', 'nb', now(), gen_random_uuid(), 'sent', $5::date, $5::date + 14, 'inkassolov_1988', 1000,
		    $6, NULLIF($7, '')::numeric, 0, $8::numeric, 0, 0, 1000, now())
		RETURNING id`, id, plantRun(t, h), sequence, level, sentOn, feeKind, fee, interest)
}

// remindersOn switches reminders on, with set — further assignments, or "".
func remindersOn(t *testing.T, h *harness, set string) {
	t.Helper()
	if set != "" {
		set = ", " + set
	}
	h.Exec(t, `UPDATE invoices.reminder_settings SET enabled = true`+set)
}

// moveClockTo moves the harness clock forward to at.
func moveClockTo(t *testing.T, h *harness, at time.Time) {
	t.Helper()
	if !at.After(h.Now()) {
		t.Fatalf("the clock is at %v; it moves forward only, not to %v", h.Now(), at)
	}
	h.Advance(at.Sub(h.Now()))
}

type segmentJSON struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	Rate float64 `json:"rate"`
	Base float64 `json:"base"`
}

type letterFactsJSON struct {
	Level               string        `json:"level"`
	AnnouncesCollection bool          `json:"announcesCollection"`
	Regime              string        `json:"regime"`
	FeeKind             string        `json:"feeKind"`
	PrincipalOpen       float64       `json:"principalOpen"`
	ChargesEarlier      float64       `json:"chargesEarlier"`
	Fee                 float64       `json:"fee"`
	Compensation        float64       `json:"compensation"`
	Interest            float64       `json:"interest"`
	InterestWaived      float64       `json:"interestWaived"`
	InterestPaid        float64       `json:"interestPaid"`
	InterestFrom        *string       `json:"interestFrom"`
	InterestSegments    []segmentJSON `json:"interestSegments"`
	Inkassosats         *float64      `json:"inkassosats"`
	Total               float64       `json:"total"`
	Deadline            string        `json:"deadline"`
}

type nextActionJSON struct {
	Action      string   `json:"action"`
	EarliestOn  *string  `json:"earliestOn"`
	Reasons     []string `json:"reasons"`
	ChargeNotes []string `json:"chargeNotes"`
	Outdated    *struct {
		Kind     string `json:"kind"`
		HalfYear string `json:"halfYear"`
	} `json:"outdated"`
	Letter *letterFactsJSON `json:"letter"`
}

type overdueChargesJSON struct {
	chargesJSON
	InterestToday *float64 `json:"interestToday"`
}

type overdueItemJSON struct {
	InvoiceID     int64              `json:"invoiceId"`
	Number        int64              `json:"number"`
	CustomerID    int32              `json:"customerId"`
	BuyerName     string             `json:"buyerName"`
	BuyerType     *string            `json:"buyerType"`
	IssueDate     string             `json:"issueDate"`
	DueDate       string             `json:"dueDate"`
	DaysOverdue   int32              `json:"daysOverdue"`
	PrincipalOpen float64            `json:"principalOpen"`
	Charges       overdueChargesJSON `json:"charges"`
	InterestToday *float64           `json:"interestToday"`
	Delivered     bool               `json:"delivered"`
	LastLetter    *struct {
		ID       int64   `json:"id"`
		Sequence int32   `json:"sequence"`
		Level    string  `json:"level"`
		Status   string  `json:"status"`
		SentOn   *string `json:"sentOn"`
		Deadline *string `json:"deadline"`
	} `json:"lastLetter"`
	NextAction nextActionJSON `json:"nextAction"`
	Hold       *struct {
		ID   int64  `json:"id"`
		Kind string `json:"kind"`
		Note string `json:"note"`
	} `json:"hold"`
	Handoff *struct {
		ID     int64  `json:"id"`
		Agency string `json:"agency"`
	} `json:"handoff"`
	PolicyMode string `json:"policyMode"`
}

type freshnessJSON struct {
	LastBookedOn    *string  `json:"lastBookedOn"`
	Stale           bool     `json:"stale"`
	StaleImportDays int      `json:"staleImportDays"`
	OcrAccounts     []string `json:"ocrAccounts"`
}

type overdueJSON struct {
	Items     []overdueItemJSON `json:"items"`
	Total     int               `json:"total"`
	Freshness freshnessJSON     `json:"freshness"`
	Warnings  []string          `json:"warnings"`
}

// overdueOf reads the overdue list with query as c, failing unless 200.
func overdueOf(t *testing.T, c *modtest.Client, query string) overdueJSON {
	t.Helper()
	res := c.Do(http.MethodGet, overduePath+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/overdue%s = %d %s, want 200", query, res.Status, res.Body)
	}
	var got overdueJSON
	res.JSON(&got)
	return got
}

// overdueIDs is the list's invoices, in its order.
func overdueIDs(l overdueJSON) []int64 {
	out := make([]int64, 0, len(l.Items))
	for _, i := range l.Items {
		out = append(out, i.InvoiceID)
	}
	return out
}

// reader is a caller holding invoices:access alone.
func reader(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "invoices:access")
}

// overdueSet plants the list tests' invoices on the fixed clock (Saturday 12
// September 2026, the defaults but reminders on): one of each action, a paid
// one and one not yet due. Each is Acme's, handed over on its issue date, but
// for the not-yet-reminded one, which is Kari's.
type overdueSet struct {
	handOff, notice, reminder, waiting, blocked, notYet int64
}

func plantOverdueSet(t *testing.T, h *harness) overdueSet {
	t.Helper()
	remindersOn(t, h, "")
	var s overdueSet
	s.reminder = deliveredOn(t, h, 1, customerAcme, "2026-08-03") // E + 14 = 17 August: a reminder today
	s.notYet = deliveredOn(t, h, 2, customerPerson, "2026-09-07") // a reminder from 21 September
	s.blocked = deliveredOn(t, h, 3, customerAcme, "2026-08-10")
	plantQueued(t, h, s.blocked, 1) // a letter on its way
	s.waiting = deliveredOn(t, h, 4, customerAcme, "2026-08-05")
	plantLetterSent(t, h, s.waiting, 1, "reminder", "2026-09-05", "", "") // deadline 19 September
	s.handOff = deliveredOn(t, h, 5, customerAcme, "2026-07-06")
	plantLetterSent(t, h, s.handOff, 1, "reminder", "2026-07-21", "", "")
	plantLetterSent(t, h, s.handOff, 2, "collection_notice", "2026-08-10", "", "")
	s.notice = deliveredOn(t, h, 6, customerAcme, "2026-07-13")
	plantLetterSent(t, h, s.notice, 1, "reminder", "2026-08-01", "", "")
	paid := deliveredOn(t, h, 7, customerAcme, "2026-08-03")
	plantPayment(t, h, paid, "1000", "2026-08-20")
	deliveredOn(t, h, 8, customerAcme, "2026-09-20") // not due yet
	return s
}

// The list is the overdue invoices only — a paid one and one not yet due are
// not — the oldest due date first; each filter narrows it (customerId,
// dueBefore — a due date before that day — and each action); and the page is
// applied after the action filter (M7): the one blocked invoice, fifth of
// six, is page 1 of 1 of the blocked ones. Paging out of range is a 400.
func TestOverdue_FiltersOrderAndPagingAfterTheFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := plantOverdueSet(t, h)
	c := reader(t, h)

	all := overdueOf(t, c, "")
	if want := []int64{s.handOff, s.notice, s.reminder, s.waiting, s.blocked, s.notYet}; !slices.Equal(overdueIDs(all), want) || all.Total != 6 {
		t.Fatalf("the list = %v of %d, want %v of 6, the oldest due date first", overdueIDs(all), all.Total, want)
	}
	for action, want := range map[string]int64{
		"collection_notice": s.notice, "hand_off": s.handOff, "blocked": s.blocked, "waiting": s.waiting,
	} {
		got := overdueOf(t, c, "?action="+action)
		if !slices.Equal(overdueIDs(got), []int64{want}) || got.Total != 1 || got.Items[0].NextAction.Action != action {
			t.Errorf("?action=%s = %v of %d, want [%d]", action, overdueIDs(got), got.Total, want)
		}
	}
	if got := overdueOf(t, c, "?action=reminder"); !slices.Equal(overdueIDs(got), []int64{s.reminder, s.notYet}) {
		t.Errorf("?action=reminder = %v, want the reminder due today and the one due later", overdueIDs(got))
	}
	if got := overdueOf(t, c, fmt.Sprintf("?customerId=%d", customerPerson)); !slices.Equal(overdueIDs(got), []int64{s.notYet}) {
		t.Errorf("?customerId=Kari = %v, want hers alone", overdueIDs(got))
	}
	if got := overdueOf(t, c, "?dueBefore=2026-08-04"); !slices.Equal(overdueIDs(got), []int64{s.handOff, s.notice, s.reminder}) {
		t.Errorf("?dueBefore=2026-08-04 = %v, want the three due before it", overdueIDs(got))
	}
	if got := overdueOf(t, c, "?action=blocked&pageSize=1&page=1"); !slices.Equal(overdueIDs(got), []int64{s.blocked}) || got.Total != 1 {
		t.Errorf("the blocked ones' page 1 of size 1 = %v of %d, want [%d] of 1: the page is applied after the filter",
			overdueIDs(got), got.Total, s.blocked)
	}
	if got := overdueOf(t, c, "?action=reminder&pageSize=1&page=2"); !slices.Equal(overdueIDs(got), []int64{s.notYet}) || got.Total != 2 {
		t.Errorf("the reminders' page 2 of size 1 = %v of %d, want [%d] of 2", overdueIDs(got), got.Total, s.notYet)
	}
	pages := [][]int64{{s.handOff, s.notice}, {s.reminder, s.waiting}, {s.blocked, s.notYet}}
	for i, want := range pages {
		if got := overdueOf(t, c, fmt.Sprintf("?pageSize=2&page=%d", i+1)); !slices.Equal(overdueIDs(got), want) || got.Total != 6 {
			t.Errorf("page %d of size 2 = %v of %d, want %v of 6", i+1, overdueIDs(got), got.Total, want)
		}
	}
	if res := c.Do(http.MethodGet, overduePath+"?pageSize=101", nil); res.Status != http.StatusBadRequest {
		t.Errorf("?pageSize=101 = %d %s, want 400", res.Status, res.Body)
	}
}

// At most 5 000 overdue invoices are judged at once (D12): 5 001 is 409
// too_many_overdue, asking for a narrower list; narrowed by dueBefore to
// exactly 5 000 the list answers them, and by customer the one.
func TestOverdue_TheCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Exec(t, `
		INSERT INTO invoices.invoices (kind, status, number, customer_id, issue_date, due_date, exchange_rate_date,
		    seller_legal_name, buyer_name, gross_total, issued_at, created_by_user_id, created_at, updated_at)
		SELECT 'invoice', 'issued', n, $1, DATE '2026-07-01', CASE WHEN n = 5000 THEN DATE '2026-08-20' ELSE DATE '2026-08-03' END,
		    DATE '2026-07-01', 'Selger AS', 'Kunde AS', 100, now(), gen_random_uuid(), now(), now()
		FROM generate_series(1, 5000) AS n`, customerAcme)
	plantOverdue(t, h, overdueSpec{number: 5001, customer: customerPerson, issue: "2026-07-01", due: "2026-08-03"})
	c := reader(t, h)

	res := c.Do(http.MethodGet, overduePath, nil)
	if res.Status != http.StatusConflict {
		t.Fatalf("5 001 overdue = %d %s, want 409", res.Status, res.Body)
	}
	if p := problemOf(t, res); p.Code != "too_many_overdue" {
		t.Errorf("5 001 overdue = %s (%s), want too_many_overdue", p.Code, p.Detail)
	}
	if got := overdueOf(t, c, "?dueBefore=2026-08-04&pageSize=1"); got.Total != 5000 {
		t.Errorf("narrowed to 5 000 = %d, want 5 000 judged", got.Total)
	}
	if got := overdueOf(t, c, fmt.Sprintf("?customerId=%d", customerPerson)); got.Total != 1 {
		t.Errorf("narrowed to Kari = %d, want her one", got.Total)
	}
}

// Every field of an item (D12): daysOverdue counted from E — a due date on
// Saturday 1 August moves to Monday 3 August, so 40 days on 12 September,
// not 42 — the principal open after a payment, the charges apart with the
// interest accrued today, delivered, the latest letter, the next action with
// its letter, a live hold, a live hand-off and the customer's policy.
func TestOverdue_EachField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	remindersOn(t, h, "late_interest = true")
	full := plantOverdue(t, h, overdueSpec{
		number: 1, customer: customerAcme, issue: "2026-07-01", due: "2026-08-01", buyerType: "business", orgNo: "923609016",
		deliveredOnIssueOn: true,
	})
	plantPayment(t, h, full, "200", "2026-08-20")
	sent := plantLetterSent(t, h, full, 1, "reminder", "2026-08-20", "38", "5")
	plantChargePayment(t, h, full, "2026-08-25", "10")
	held := deliveredOn(t, h, 2, customerPerson, "2026-08-03")
	h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
		VALUES ($1, 'disputed', 'Bestrider leveransen', now(), gen_random_uuid())`, held)
	plantPolicy(t, h, customerPerson, "no_charges", "")
	handed := deliveredOn(t, h, 3, customerForeign, "2026-08-03")
	h.Exec(t, `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, created_at, created_by_user_id)
		VALUES ($1, DATE '2026-09-10', 'Inkasso AS', now(), gen_random_uuid())`, handed)

	got := overdueOf(t, reader(t, h), "")
	if len(got.Items) != 3 {
		t.Fatalf("the list = %v, want the three", overdueIDs(got))
	}
	item := got.Items[0]
	switch {
	case item.InvoiceID != full || item.Number != 1 || item.CustomerID != customerAcme || item.BuyerName != "Kunde 1" ||
		item.BuyerType == nil || *item.BuyerType != "business" || item.IssueDate != "2026-07-01" || item.DueDate != "2026-08-01":
		t.Errorf("the invoice = %+v, want number 1, Acme, Kunde 1, business, issued 1 July, due 1 August", item)
	case item.DaysOverdue != 40:
		t.Errorf("daysOverdue = %d, want 40: counted from Monday 3 August, E", item.DaysOverdue)
	case item.PrincipalOpen != 800:
		t.Errorf("principalOpen = %v, want 800 after the payment of 200", item.PrincipalOpen)
	case item.Charges.Claimed != 43 || item.Charges.Paid != 10 || item.Charges.Outstanding != 33:
		t.Errorf("charges = %+v, want claimed 43 (the fee 38 and interest 5), paid 10, outstanding 33", item.Charges)
	// 4–20 August on 1 000 and 21 August–12 September on 800, at 12.25 %:
	// 5.70548 + 6.17534 = 11.88.
	case item.InterestToday == nil || *item.InterestToday != 11.88 || item.Charges.InterestToday == nil || *item.Charges.InterestToday != 11.88:
		t.Errorf("interestToday = %v and in the charges %v, want 11.88", item.InterestToday, item.Charges.InterestToday)
	case !item.Delivered:
		t.Error("delivered = false, want the manual delivery on the issue date")
	case item.LastLetter == nil || item.LastLetter.ID != sent || item.LastLetter.Status != "sent" ||
		item.LastLetter.SentOn == nil || *item.LastLetter.SentOn != "2026-08-20" || item.LastLetter.Deadline == nil || *item.LastLetter.Deadline != "2026-09-03":
		t.Errorf("lastLetter = %+v, want letter %d sent 20 August, deadline 3 September", item.LastLetter, sent)
	case item.NextAction.Action != "collection_notice" || item.NextAction.EarliestOn == nil || *item.NextAction.EarliestOn != "2026-09-07" ||
		item.NextAction.Letter == nil || item.NextAction.Letter.Fee != 38 || item.NextAction.Letter.Interest != 11.88 ||
		item.NextAction.Letter.Level != "collection_notice":
		t.Errorf("nextAction = %+v, want the collection notice from 7 September with a fee of 38 and interest 11.88", item.NextAction)
	case item.Hold != nil || item.Handoff != nil || item.PolicyMode != "normal":
		t.Errorf("hold %v, handoff %v, policy %q, want none, none and normal", item.Hold, item.Handoff, item.PolicyMode)
	}
	if h := got.Items[1]; h.Hold == nil || h.Hold.Note != "Bestrider leveransen" || h.Hold.Kind != "disputed" ||
		h.NextAction.Action != "blocked" || !slices.Equal(h.NextAction.Reasons, []string{"on_hold"}) || h.PolicyMode != "no_charges" ||
		h.InterestToday != nil {
		t.Errorf("the held one = %+v, want its hold, blocked on_hold, no_charges and no interest", h)
	}
	if h := got.Items[2]; h.Handoff == nil || h.Handoff.Agency != "Inkasso AS" || h.NextAction.Action != "none" ||
		!slices.Equal(h.NextAction.Reasons, []string{"handed_off"}) || h.LastLetter != nil {
		t.Errorf("the handed-off one = %+v, want its hand-off, none handed_off and no letter", h)
	}
}

// With charges=outstanding the list also has the paid invoices whose
// charges are outstanding (D12): a paid one with its fee unpaid joins, with
// nothing open of the principal and its next action none; a paid one whose
// fee is paid does not; without the filter neither does.
func TestOverdue_ChargesOutstanding(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	remindersOn(t, h, "")
	overdue := deliveredOn(t, h, 1, customerAcme, "2026-08-03")
	owing := deliveredOn(t, h, 2, customerAcme, "2026-07-06")
	plantLetterSent(t, h, owing, 1, "reminder", "2026-07-21", "38", "")
	plantPayment(t, h, owing, "1000", "2026-08-01")
	settled := deliveredOn(t, h, 3, customerAcme, "2026-07-06")
	plantLetterSent(t, h, settled, 1, "reminder", "2026-07-21", "38", "")
	plantPayment(t, h, settled, "1000", "2026-08-01")
	plantChargePayment(t, h, settled, "2026-08-01", "38")
	c := reader(t, h)

	if got := overdueOf(t, c, ""); !slices.Equal(overdueIDs(got), []int64{overdue}) {
		t.Errorf("the list = %v, want the overdue one alone", overdueIDs(got))
	}
	got := overdueOf(t, c, "?charges=outstanding")
	if !slices.Equal(overdueIDs(got), []int64{owing, overdue}) {
		t.Fatalf("?charges=outstanding = %v, want the paid one owing its fee, then the overdue one", overdueIDs(got))
	}
	if i := got.Items[0]; i.PrincipalOpen != 0 || i.Charges.Outstanding != 38 || i.NextAction.Action != "none" {
		t.Errorf("the paid one = open %v, outstanding %v, action %s; want 0, 38 and none", i.PrincipalOpen, i.Charges.Outstanding, i.NextAction.Action)
	}
}

// plantBankFile plants an imported bank file whose last booking is
// lastBookedOn.
func plantBankFile(t *testing.T, h *harness, lastBookedOn string) {
	t.Helper()
	h.Exec(t, `
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
		    ignored, ignored_kinds, first_booked_on, last_booked_on, uploaded_by_user_id, uploaded_at)
		VALUES ('camt054', md5(random()::text) || md5(random()::text), md5(random()::text), 'bank-files/f.xml', 400,
		    ARRAY['15032080119'], 0, 0, '{}', $1::date, $1::date, gen_random_uuid(), now())`, lastBookedOn)
}

// The bank data's freshness and the warnings (D10, D12; plan reading 28):
// stale with no file ever imported; stale while the last booking is more
// than stale_import_days before today — 8 September is, 9 September is not,
// on 12 September with 3 days; an OCR account named with its note; and the
// rate warnings — in 2027 with late interest on and no row for 2027-H1 the
// rates are outdated and the regime review has lapsed, a row for the
// half-year clears the first, and a release seeding another value over it
// warns that it differs.
func TestOverdue_FreshnessAndWarnings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	remindersOn(t, h, "late_interest = true")
	c := reader(t, h)
	has := func(l overdueJSON, w string) bool { return slices.Contains(l.Warnings, w) }

	got := overdueOf(t, c, "")
	if !got.Freshness.Stale || got.Freshness.LastBookedOn != nil || got.Freshness.StaleImportDays != 3 || !has(got, "bank_data_stale") {
		t.Errorf("no import = %+v %v, want stale with no booking and bank_data_stale", got.Freshness, got.Warnings)
	}
	if len(got.Warnings) != 1 {
		t.Errorf("the warnings in September 2026 = %v, want bank_data_stale alone", got.Warnings)
	}
	plantBankFile(t, h, "2026-09-08")
	if got := overdueOf(t, c, ""); !got.Freshness.Stale || got.Freshness.LastBookedOn == nil || *got.Freshness.LastBookedOn != "2026-09-08" {
		t.Errorf("booked to 8 September = %+v, want stale: more than 3 days before the 12th", got.Freshness)
	}
	plantBankFile(t, h, "2026-09-09")
	if got := overdueOf(t, c, ""); got.Freshness.Stale || has(got, "bank_data_stale") {
		t.Errorf("booked to 9 September = %+v %v, want fresh: exactly 3 days before", got.Freshness, got.Warnings)
	}
	h.Exec(t, `INSERT INTO invoices.bank_import_accounts (account, format, set_by_user_id, set_at)
		VALUES ('15032080119', 'ocr', gen_random_uuid(), now())`)
	if got := overdueOf(t, c, ""); !slices.Equal(got.Freshness.OcrAccounts, []string{"15032080119"}) || !has(got, "ocr_without_kid_payments") {
		t.Errorf("an OCR account = %+v %v, want it named and ocr_without_kid_payments", got.Freshness, got.Warnings)
	}

	moveClockTo(t, h, time.Date(2027, time.January, 5, 9, 0, 0, 0, time.UTC))
	c = reader(t, h) // a session of the new day
	got = overdueOf(t, c, "")
	if !has(got, "collection_rates_outdated") || !has(got, "collection_regime_unreviewed") || has(got, "collection_rate_differs_from_release") {
		t.Errorf("5 January 2027 = %v, want the rates outdated and the regime unreviewed", got.Warnings)
	}
	h.Exec(t, `INSERT INTO invoices.collection_rates (kind, valid_from, value, source_ref, created_by_user_id, created_at)
		VALUES ('late_interest_percent', DATE '2027-01-01', 12.00, 'FOR-2026-12-01-1', gen_random_uuid(), now())`)
	h.Exec(t, `INSERT INTO invoices.collection_rates (kind, valid_from, value, source_ref, created_by_user_id, created_at)
		VALUES ('b2b_compensation_nok', DATE '2027-01-01', 430, 'FOR-2026-12-01-1', gen_random_uuid(), now())`)
	if got := overdueOf(t, c, ""); has(got, "collection_rates_outdated") || !has(got, "collection_regime_unreviewed") {
		t.Errorf("with the 2027-H1 rows = %v, want the rates current and the regime still unreviewed", got.Warnings)
	}
	h.Exec(t, `SELECT invoices.seed_collection_rate('late_interest_percent', DATE '2027-01-01', 12.50, 'FOR-2026-12-20-2')`)
	if got := overdueOf(t, c, ""); !has(got, "collection_rate_differs_from_release") {
		t.Errorf("a release over the row = %v, want collection_rate_differs_from_release", got.Warnings)
	}
}

// The list and the preview judge alike (D12): the preview's letters are the
// list's invoices whose letter is due today, with the same action and the
// same figures, and its blocked or waiting ones the list's blocked and
// waiting ones with the same reasons.
func TestOverdue_AgreesWithThePreview(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := plantOverdueSet(t, h)
	h.Exec(t, `UPDATE invoices.reminder_settings SET late_interest = true`)
	list := overdueOf(t, reader(t, h), "")
	preview := previewOf(t, h)

	var wantLetters, wantHeld []int64
	facts := map[int64]string{}
	for _, i := range list.Items {
		switch {
		case i.NextAction.Letter != nil:
			wantLetters = append(wantLetters, i.InvoiceID)
			b, _ := json.Marshal(struct {
				A string
				L *letterFactsJSON
				N []string
			}{i.NextAction.Action, i.NextAction.Letter, i.NextAction.ChargeNotes})
			facts[i.InvoiceID] = string(b)
		case i.NextAction.Action == "blocked" || i.NextAction.Action == "waiting":
			wantHeld = append(wantHeld, i.InvoiceID)
			b, _ := json.Marshal(i.NextAction)
			facts[i.InvoiceID] = string(b)
		}
	}
	if !slices.Equal(wantLetters, []int64{s.notice, s.reminder}) || !slices.Equal(wantHeld, []int64{s.waiting, s.blocked}) {
		t.Fatalf("the list's letters due = %v and held %v, want the notice and the reminder, and the waiting and blocked ones",
			wantLetters, wantHeld)
	}
	var gotLetters, gotHeld []int64
	for _, l := range preview.Letters {
		gotLetters = append(gotLetters, l.InvoiceID)
		b, _ := json.Marshal(struct {
			A string
			L *letterFactsJSON
			N []string
		}{l.Action, &l.Letter, l.ChargeNotes})
		if facts[l.InvoiceID] != string(b) {
			t.Errorf("invoice %d: the preview's letter %s, the list's %s", l.InvoiceID, b, facts[l.InvoiceID])
		}
	}
	for _, b := range preview.BlockedOrWaiting {
		gotHeld = append(gotHeld, b.InvoiceID)
		got, _ := json.Marshal(b.NextAction)
		if facts[b.InvoiceID] != string(got) {
			t.Errorf("invoice %d: the preview says %s, the list %s", b.InvoiceID, got, facts[b.InvoiceID])
		}
	}
	if !slices.Equal(gotLetters, wantLetters) || !slices.Equal(gotHeld, wantHeld) {
		t.Errorf("the preview = letters %v and held %v, the list's = %v and %v", gotLetters, gotHeld, wantLetters, wantHeld)
	}
	if !slices.Equal(preview.Warnings, list.Warnings) || preview.Freshness.Stale != list.Freshness.Stale {
		t.Errorf("the preview's warnings %v and freshness %+v, the list's %v and %+v", preview.Warnings, preview.Freshness, list.Warnings, list.Freshness)
	}
}

// The list is invoices:access's (D1, D12): a reader holding nothing else
// reads it, and the runs — invoices:payments' — are a 403 to them.
func TestOverdue_ReadableWithAccessAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantOverdueSet(t, h)
	c := reader(t, h)
	if got := overdueOf(t, c, ""); got.Total != 6 {
		t.Errorf("invoices:access alone = %d items, want the six", got.Total)
	}
	if res := c.Do(http.MethodPost, reminderRunsPath, map[string]any{"dryRun": true}); res.Status != http.StatusForbidden {
		t.Errorf("a preview with invoices:access alone = %d, want 403", res.Status)
	}
	if res := c.Do(http.MethodGet, reminderRunsPath, nil); res.Status != http.StatusForbidden {
		t.Errorf("the runs with invoices:access alone = %d, want 403", res.Status)
	}
	if res := h.SignIn(t).Do(http.MethodGet, overduePath, nil); res.Status != http.StatusForbidden {
		t.Errorf("the list without invoices:access = %d, want 403", res.Status)
	}
}
