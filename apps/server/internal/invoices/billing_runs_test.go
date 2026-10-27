package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The billing runs' API (energy consumption billing design D10; billing runs
// plan readings 3, 8 and 9), through the API: the create's refusals in their
// order, the run and its items written in one transaction, the reads with
// their counts, filter, paging, customer names and the documents that still
// exist, and cancel, which leaves an item under a live lease alone. Energy's
// reads are fakeConsumption's; the clock stands on 12 September 2026.

const billingRunsPath = invoicesPath + "/billing-runs"

func billingRunPath(id int64) string { return fmt.Sprintf("%s/%d", billingRunsPath, id) }

// billingRunJSON, billingRunItemJSON and billingRunDetailJSON are the runs
// as a client reads them.
type billingRunJSON struct {
	ID              int64            `json:"id"`
	Through         string           `json:"through"`
	Issue           bool             `json:"issue"`
	Status          string           `json:"status"`
	Customers       int32            `json:"customers"`
	CreatedByUserID uuid.UUID        `json:"createdByUserId"`
	StartedAt       *string          `json:"startedAt"`
	FinishedAt      *string          `json:"finishedAt"`
	Counts          map[string]int32 `json:"counts"`
}

type billingRunItemJSON struct {
	ID           int64   `json:"id"`
	CustomerID   int32   `json:"customerId"`
	CustomerName *string `json:"customerName"`
	Status       string  `json:"status"`
	Invoices     []struct {
		InvoiceID int64  `json:"invoiceId"`
		Number    *int64 `json:"number"`
		Status    string `json:"status"`
	} `json:"invoices"`
	Code       *string `json:"code"`
	Detail     *string `json:"detail"`
	Attempts   int32   `json:"attempts"`
	FinishedAt *string `json:"finishedAt"`
}

type billingRunDetailJSON struct {
	Run        billingRunJSON       `json:"run"`
	Items      []billingRunItemJSON `json:"items"`
	Pagination struct {
		TotalCount int32 `json:"totalCount"`
		Page       int32 `json:"page"`
	} `json:"pagination"`
}

type billingRunRefusalJSON struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	RunID  *int64 `json:"runId"`
}

// runMaker is a caller who makes runs and issues.
func runMaker(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, "invoices:access", "invoices:create", "invoices:issue")
}

// startRun posts body as c and answers the run, failing unless it was made.
func startRun(t *testing.T, c *modtest.Client, body map[string]any) billingRunJSON {
	t.Helper()
	res := c.Do(http.MethodPost, billingRunsPath, body)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST /invoices/billing-runs %v = %d %s, want 201", body, res.Status, res.Body)
	}
	var run billingRunJSON
	res.JSON(&run)
	return run
}

// runRefused asserts res is a 409 with code and answers it.
func runRefused(t *testing.T, what string, res *modtest.Response, code string) billingRunRefusalJSON {
	t.Helper()
	var p billingRunRefusalJSON
	if res.Status != http.StatusConflict {
		t.Errorf("%s = %d %s, want 409 %s", what, res.Status, res.Body, code)
		return p
	}
	res.JSON(&p)
	if p.Code != code {
		t.Errorf("%s = %s (%s), want %s", what, p.Code, p.Detail, code)
	}
	return p
}

// runDetail reads a run as an invoices:access holder, with query.
func runDetail(t *testing.T, h *harness, id int64, query string) billingRunDetailJSON {
	t.Helper()
	path := billingRunPath(id)
	if query != "" {
		path += "?" + query
	}
	res := reader(t, h).Do(http.MethodGet, path, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, res.Status, res.Body)
	}
	var d billingRunDetailJSON
	res.JSON(&d)
	return d
}

func itemStatusesOf(items []billingRunItemJSON) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, fmt.Sprintf("%d:%s", i.CustomerID, i.Status))
	}
	return out
}

// The create's refusals, each before the next (plan reading 3): the body's
// 400s; issue without invoices:issue a 403; Energy off; a run queued or
// running; then the customers — one the directory does not know a 400 on its
// index, more than 5 000 supplies due too_many_customers, none due
// no_consumption_due. A run made has one queued item per distinct customer.
func TestBillingRuns_CreateRefusesInOrder(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	h := newHarness(t, f.options()...)
	maker, makerID := runMaker(t, h)
	creatorOnly := creator(t, h)

	many := make([]int32, 5001)
	for i := range many {
		many[i] = int32(i + 1)
	}
	badOn(t, "no customers named", maker.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": false, "customerIds": []int32{}}), "customerIds")
	badOn(t, "5 001 customers", maker.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": false, "customerIds": many}), "customerIds")
	res := maker.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": false, "customerIds": []int32{0, customerAcme, customerAcme}})
	badOn(t, "a non-positive id", res, "customerIds[0]")
	if p := problemOf(t, res); len(p.Errors["customerIds[2]"]) == 0 || len(p.Errors["customerIds[1]"]) != 0 {
		t.Errorf("errors = %v, want customerIds[2] named twice and customerIds[1] fine", p.Errors)
	}
	// The body before the permission.
	badOn(t, "a bad body that issues, without invoices:issue", creatorOnly.Do(http.MethodPost, billingRunsPath,
		map[string]any{"issue": true, "customerIds": []int32{-1}}), "customerIds[0]")
	if res := creatorOnly.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": true}); res.Status != http.StatusForbidden || res.Code() != "forbidden" {
		t.Errorf("issue without invoices:issue = %d %s, want 403 forbidden", res.Status, res.Body)
	}

	// Every customer DueSupplies names: more than it answers, none, then two
	// supplies of Acme's and one of Kari's.
	f.answerMore(true)
	runRefused(t, "more supplies than one page", maker.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": false}), "too_many_customers")
	f.answerMore(false)
	runRefused(t, "nothing due", maker.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": false}), "no_consumption_due")
	badOn(t, "an unknown customer", maker.Do(http.MethodPost, billingRunsPath,
		map[string]any{"issue": false, "customerIds": []int32{customerAcme, customerUnknown}}), "customerIds[1]")
	if n := h.Count(t, `SELECT count(*) FROM invoices.billing_runs`); n != 0 {
		t.Fatalf("%d runs after the refusals, want none", n)
	}

	f.putSupply(contracts.DueSupply{CustomerID: customerPerson, SupplyPeriodID: 3, GSRN: gsrnBodo, Cadence: contracts.CadenceMonthly, DuePeriods: 1})
	f.putSupply(contracts.DueSupply{CustomerID: customerAcme, SupplyPeriodID: 1, GSRN: gsrnTromso, Cadence: contracts.CadenceMonthly, DuePeriods: 1})
	f.putSupply(contracts.DueSupply{CustomerID: customerAcme, SupplyPeriodID: 2, GSRN: gsrnBodo, Cadence: contracts.CadenceMonthly, DuePeriods: 2})
	run := startRun(t, maker, map[string]any{"issue": true, "through": "2026-08-31"})
	if run.Status != "queued" || run.Customers != 2 || run.Counts["queued"] != 2 || !run.Issue || run.Through != "2026-08-31" ||
		run.CreatedByUserID != makerID || run.StartedAt != nil || run.FinishedAt != nil {
		t.Errorf("run = %+v, want queued for 2 customers through 2026-08-31, issuing, made by %s", run, makerID)
	}
	if got := modtest.One[[]int32](t, h.Harness, `SELECT array_agg(customer_id ORDER BY id) FROM invoices.billing_run_items WHERE run_id = $1`, run.ID); !slices.Equal(got, []int32{customerAcme, customerPerson}) {
		t.Errorf("items = %v, want Acme then Kari", got)
	}
	var due bool
	for _, c := range f.reads() {
		if c.method == "DueSupplies" && c.through.Equal(marketDay(2026, time.September, 1)) {
			due = true
		}
	}
	if !due {
		t.Errorf("DueSupplies was not asked through the start of 1 September: %+v", f.reads())
	}

	// One run at a time: the active one is named, before the customers are
	// read.
	p := runRefused(t, "a second run", maker.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": false, "customerIds": []int32{customerUnknown}}), "billing_run_active")
	if p.RunID == nil || *p.RunID != run.ID {
		t.Errorf("runId = %v, want %d", p.RunID, run.ID)
	}
	noLockedReads(t, f)
}

// Energy switched off refuses a run, after the permission.
func TestBillingRuns_CreateWithoutEnergy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	maker, _ := runMaker(t, h)
	if res := creator(t, h).Do(http.MethodPost, billingRunsPath, map[string]any{"issue": true}); res.Status != http.StatusForbidden {
		t.Errorf("issue without invoices:issue = %d %s, want 403 before consumption_unavailable", res.Status, res.Body)
	}
	runRefused(t, "Energy off", maker.Do(http.MethodPost, billingRunsPath, map[string]any{"issue": false, "customerIds": []int32{customerAcme}}), "consumption_unavailable")
}

// The reads (plan reading 8): the runs newest first with their counts; a
// run's items in order, each with its customer's name — absent for one the
// directory does not know — and the documents it made that still exist, a
// deleted draft left out; filtered by status, paged; a status not an item's
// a 400; an unknown run a bare 404.
func TestBillingRuns_Reads(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	h := newHarness(t, f.options()...)
	maker, _ := runMaker(t, h)
	first := startRun(t, maker, map[string]any{"issue": false, "customerIds": []int32{customerAcme}})
	if res := maker.Do(http.MethodPost, billingRunPath(first.ID)+"/cancel", nil); res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Body)
	}
	run := startRun(t, maker, map[string]any{"issue": false, "customerIds": []int32{customerAcme, customerPerson}})
	// A customer the directory no longer knows, and Acme's item drafted with
	// a draft that exists and one deleted since.
	h.Exec(t, `INSERT INTO invoices.billing_run_items (run_id, customer_id, next_attempt_at) VALUES ($1, $2, now())`, run.ID, customerUnknown)
	draft := createDraft(t, h, draftBody(customerAcme, line("Strøm", 1, 100, vat25)))
	h.Exec(t, `UPDATE invoices.billing_run_items SET status = 'drafted', finished_at = now(), invoice_ids = ARRAY[$3::bigint, $4::bigint]
		WHERE run_id = $1 AND customer_id = $2`, run.ID, customerAcme, draft.ID+1000, draft.ID)

	res := reader(t, h).Do(http.MethodGet, billingRunsPath, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/billing-runs = %d %s", res.Status, res.Body)
	}
	var list struct {
		Data       []billingRunJSON `json:"data"`
		Pagination struct {
			TotalCount int32 `json:"totalCount"`
		} `json:"pagination"`
	}
	res.JSON(&list)
	if len(list.Data) != 2 || list.Data[0].ID != run.ID || list.Data[1].ID != first.ID || list.Pagination.TotalCount != 2 {
		t.Fatalf("runs = %+v, want the second run then the first", list)
	}
	if c := list.Data[0].Counts; c["queued"] != 2 || c["drafted"] != 1 || c["cancelled"] != 0 {
		t.Errorf("the second run's counts = %v, want 2 queued and 1 drafted", c)
	}
	if c := list.Data[1].Counts; c["cancelled"] != 1 || c["queued"] != 0 || list.Data[1].Status != "cancelled" {
		t.Errorf("the first run = %s %v, want cancelled with 1 item cancelled", list.Data[1].Status, c)
	}
	if res := reader(t, h).Do(http.MethodGet, billingRunsPath+"?pageSize=1&page=2", nil); res.Status != http.StatusOK {
		t.Errorf("page 2 = %d", res.Status)
	} else if res.JSON(&list); len(list.Data) != 1 || list.Data[0].ID != first.ID {
		t.Errorf("page 2 of 1 = %+v, want the first run", list.Data)
	}
	if res := reader(t, h).Do(http.MethodGet, billingRunsPath+"?pageSize=101", nil); res.Status != http.StatusBadRequest {
		t.Errorf("pageSize 101 = %d, want 400", res.Status)
	}

	d := runDetail(t, h, run.ID, "")
	if got := itemStatusesOf(d.Items); !slices.Equal(got, []string{"2001:drafted", "2002:queued", "9999:queued"}) || d.Pagination.TotalCount != 3 {
		t.Fatalf("items = %v (%d), want Acme drafted, Kari and the unknown queued", got, d.Pagination.TotalCount)
	}
	acme, kari, unknown := d.Items[0], d.Items[1], d.Items[2]
	if acme.CustomerName == nil || *acme.CustomerName != "Acme AS" || kari.CustomerName == nil || *kari.CustomerName != "Kari Nordmann" || unknown.CustomerName != nil {
		t.Errorf("names = %v %v %v, want Acme AS, Kari Nordmann and none", acme.CustomerName, kari.CustomerName, unknown.CustomerName)
	}
	if len(acme.Invoices) != 1 || acme.Invoices[0].InvoiceID != draft.ID || acme.Invoices[0].Status != "draft" || acme.Invoices[0].Number != nil {
		t.Errorf("Acme's documents = %+v, want draft %d alone", acme.Invoices, draft.ID)
	}
	if kari.Invoices == nil || len(kari.Invoices) != 0 || kari.FinishedAt != nil || acme.FinishedAt == nil {
		t.Errorf("Kari = %+v, Acme finished %v: want no documents and unfinished, Acme finished", kari, acme.FinishedAt)
	}
	if d.Run.ID != run.ID || d.Run.Counts["queued"] != 2 {
		t.Errorf("run = %+v", d.Run)
	}
	if got := itemStatusesOf(runDetail(t, h, run.ID, "status=queued&pageSize=1&page=2").Items); !slices.Equal(got, []string{"9999:queued"}) {
		t.Errorf("queued, page 2 of 1 = %v, want the unknown customer's", got)
	}
	if got := runDetail(t, h, run.ID, "status=issued"); len(got.Items) != 0 || got.Pagination.TotalCount != 0 {
		t.Errorf("issued = %v, want none", itemStatusesOf(got.Items))
	}
	res = reader(t, h).Do(http.MethodGet, billingRunPath(run.ID)+"?status=done", nil)
	if res.Status != http.StatusBadRequest || !strings.Contains(string(res.Body), "status") {
		t.Errorf("status=done = %d %s, want 400 naming status", res.Status, res.Body)
	}
	bareNotFound(t, "an unknown run", reader(t, h).Do(http.MethodGet, billingRunPath(1), nil))
}

// Cancel (plan reading 9): the run cancelled with its queued items, but for
// one a live lease holds — being carried, it records its own outcome; a run
// finished already billing_run_finished; an unknown run a bare 404. After
// it, a new run may be made.
func TestBillingRuns_Cancel(t *testing.T) {
	t.Parallel()
	f := newFakeConsumption()
	h := newHarness(t, f.options()...)
	maker, _ := runMaker(t, h)
	run := startRun(t, maker, map[string]any{"issue": false, "customerIds": []int32{customerAcme, customerPerson, customerForeign}})
	h.Exec(t, `UPDATE invoices.billing_run_items SET lease_id = 'live', lease_until = $3 WHERE run_id = $1 AND customer_id = $2`,
		run.ID, customerPerson, h.Now().Add(5*time.Minute))
	h.Exec(t, `UPDATE invoices.billing_run_items SET lease_id = 'stale', lease_until = $3 WHERE run_id = $1 AND customer_id = $2`,
		run.ID, customerForeign, h.Now().Add(-time.Minute))

	res := creator(t, h).Do(http.MethodPost, billingRunPath(run.ID)+"/cancel", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Body)
	}
	var cancelled billingRunJSON
	res.JSON(&cancelled)
	if cancelled.Status != "cancelled" || cancelled.FinishedAt == nil || cancelled.Counts["cancelled"] != 2 || cancelled.Counts["queued"] != 1 {
		t.Errorf("cancelled = %+v, want cancelled, finished, 2 items cancelled and 1 queued", cancelled)
	}
	if got := itemStatusesOf(runDetail(t, h, run.ID, "").Items); !slices.Equal(got, []string{"2001:cancelled", "2002:queued", "2003:cancelled"}) {
		t.Errorf("items = %v, want Kari's, under a live lease, left queued", got)
	}
	runRefused(t, "a second cancel", maker.Do(http.MethodPost, billingRunPath(run.ID)+"/cancel", nil), "billing_run_finished")
	bareNotFound(t, "an unknown run", maker.Do(http.MethodPost, billingRunPath(1)+"/cancel", nil))
	startRun(t, maker, map[string]any{"issue": false, "customerIds": []int32{customerAcme}})
}
