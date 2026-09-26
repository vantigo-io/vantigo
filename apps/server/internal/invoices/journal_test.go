package invoices_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

type journalJSON struct {
	Data []struct {
		Number        int64   `json:"number"`
		Kind          string  `json:"kind"`
		IssueDate     string  `json:"issueDate"`
		BuyerName     *string `json:"buyerName"`
		NetTotal      float64 `json:"netTotal"`
		VatTotal      float64 `json:"vatTotal"`
		GrossTotal    float64 `json:"grossTotal"`
		CreditsNumber *int64  `json:"creditsNumber"`
		VatSummaries  []struct {
			SafTCode      string  `json:"safTCode"`
			Category      string  `json:"category"`
			RatePercent   float64 `json:"ratePercent"`
			TaxableAmount float64 `json:"taxableAmount"`
			VatAmount     float64 `json:"vatAmount"`
		} `json:"vatSummaries"`
	} `json:"data"`
	Pagination struct {
		TotalCount int32 `json:"totalCount"`
	} `json:"pagination"`
	Totals struct {
		ByCode []struct {
			SafTCode      string  `json:"safTCode"`
			Category      string  `json:"category"`
			RatePercent   float64 `json:"ratePercent"`
			TaxableAmount float64 `json:"taxableAmount"`
			VatAmount     float64 `json:"vatAmount"`
		} `json:"byCode"`
		NetTotal   float64 `json:"netTotal"`
		VatTotal   float64 `json:"vatTotal"`
		GrossTotal float64 `json:"grossTotal"`
	} `json:"totals"`
	Gaps          []int64 `json:"gaps"`
	GapsTruncated bool    `json:"gapsTruncated"`
	SeriesStart   int64   `json:"seriesStart"`
	CounterLast   *int64  `json:"counterLast"`
	HighestIssued *int64  `json:"highestIssued"`
	CheckedFrom   *int64  `json:"checkedFrom"`
	CheckedTo     *int64  `json:"checkedTo"`
}

// is reports whether p holds want.
func is(p *int64, want int64) bool { return p != nil && *p == want }

const journalPath = "/api/v1/invoices/journal"

func journal(t *testing.T, h *harness, query string) journalJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, journalPath+query, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /journal%s = %d %s", query, res.Status, res.Body)
	}
	var j journalJSON
	res.JSON(&j)
	return j
}

// The journal lists the range's documents in number order, credit notes
// signed negative in the rows and the totals, the totals over the whole range
// and not the page, and no gaps in the normal case (D11).
func TestJournal_TheRangeInNumberOrder(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	first := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 10, 100, vat25))).ID)
	issued(t, h, createDraft(t, h, draftBody(customerPerson, line("B", 1, 200, vatZero))).ID)
	c := creditDraft(t, h, first.ID)
	partial := creditLine(c.Lines[0])
	partial["quantity"] = 4
	issued(t, h, saveCredit(t, h, c, creditBody(c, partial)).ID)

	j := journal(t, h, "?from=2026-09-01&to=2026-09-30")
	var numbers []int64
	for _, d := range j.Data {
		numbers = append(numbers, d.Number)
	}
	if !slices.Equal(numbers, []int64{1, 2, 3}) || j.Pagination.TotalCount != 3 {
		t.Fatalf("numbers = %v of %d, want 1, 2, 3", numbers, j.Pagination.TotalCount)
	}
	credit := j.Data[2]
	if credit.Kind != "credit_note" || credit.NetTotal != -400 || credit.VatTotal != -100 || credit.GrossTotal != -500 ||
		credit.CreditsNumber == nil || *credit.CreditsNumber != 1 || len(credit.VatSummaries) != 1 || credit.VatSummaries[0].TaxableAmount != -400 {
		t.Errorf("the credit note's row = %+v, want it signed negative, naming invoice 1, with one VAT row of -400", credit)
	}
	if j.Totals.NetTotal != 800 || j.Totals.VatTotal != 150 || j.Totals.GrossTotal != 950 {
		t.Errorf("totals = %+v, want 1000 + 200 − 400 net, 250 − 100 VAT", j.Totals)
	}
	if len(j.Totals.ByCode) != 2 || j.Totals.ByCode[0].SafTCode != "3" || j.Totals.ByCode[0].TaxableAmount != 600 || j.Totals.ByCode[0].VatAmount != 150 ||
		j.Totals.ByCode[1].Category != "Z" || j.Totals.ByCode[1].TaxableAmount != 200 {
		t.Errorf("by code = %+v", j.Totals.ByCode)
	}
	if len(j.Gaps) != 0 || j.GapsTruncated || j.SeriesStart != 1 || !is(j.CounterLast, 3) || !is(j.HighestIssued, 3) {
		t.Errorf("gaps %v truncated %v start %d counter %v highest %v; want none, 1, 3 and 3",
			j.Gaps, j.GapsTruncated, j.SeriesStart, j.CounterLast, j.HighestIssued)
	}
	if !is(j.CheckedFrom, 1) || !is(j.CheckedTo, 3) {
		t.Errorf("checked %v to %v, want 1 to 3", j.CheckedFrom, j.CheckedTo)
	}

	paged := journal(t, h, "?from=2026-09-01&to=2026-09-30&pageSize=1&page=2")
	if len(paged.Data) != 1 || paged.Data[0].Number != 2 || paged.Totals.NetTotal != 800 {
		t.Errorf("page 2 of 1 = %+v, want document 2 and the whole range's totals", paged.Data)
	}
	for _, bad := range []string{"?from=2026-09-30&to=2026-09-01", "?to=2026-09-01", "?from=2026-09-01&to=2026-09-30&pageSize=101"} {
		if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, journalPath+bad, nil); res.Status != http.StatusBadRequest {
			t.Errorf("GET /journal%s = %d, want 400", bad, res.Status)
		}
	}
}

// A gap planted behind the handlers' backs is reported: every number missing
// between the issued document before the range's first and the range's last,
// so a hole straddling two ranges is listed in full by the later one; at most
// 1000 are listed (D11). That a number below the series start never is a gap
// is TestJournal_TheSeriesStartAndTheCounter's.
func TestJournal_TheGapCheck(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantIssuedDocument(t, h, 1, "2026-08-20")
	plantIssuedDocument(t, h, 2, "2026-08-25")
	plantIssuedDocument(t, h, 5, "2026-09-02")
	plantIssuedDocument(t, h, 6, "2026-09-03")
	plantIssuedDocument(t, h, 9, "2026-09-10")

	// September's first is 5, and the issued document before it is 2: 3 and 4
	// are missing before its first, 7 and 8 inside.
	if j := journal(t, h, "?from=2026-09-01&to=2026-09-30"); !slices.Equal(j.Gaps, []int64{3, 4, 7, 8}) ||
		!is(j.CheckedFrom, 3) || !is(j.CheckedTo, 9) {
		t.Errorf("September = gaps %v checked %v to %v, want 3 and 4 before its first, 7 and 8 inside, checked 3 to 9",
			j.Gaps, j.CheckedFrom, j.CheckedTo)
	}
	if j := journal(t, h, "?from=2026-08-01&to=2026-08-31"); len(j.Gaps) != 0 || !is(j.CheckedFrom, 1) || !is(j.CheckedTo, 2) {
		t.Errorf("August = gaps %v checked %v to %v, want none, checked 1 (the series start) to 2", j.Gaps, j.CheckedFrom, j.CheckedTo)
	}
	if j := journal(t, h, "?from=2026-10-01&to=2026-10-31"); len(j.Gaps) != 0 || len(j.Data) != 0 || j.CheckedFrom != nil || j.CheckedTo != nil {
		t.Errorf("an empty range = %+v, want no gaps, no rows and no checked range", j)
	}
	plantIssuedDocument(t, h, 1012, "2026-11-02")
	if j := journal(t, h, "?from=2026-09-01&to=2026-11-30"); len(j.Gaps) != 1000 || !j.GapsTruncated || j.Gaps[0] != 3 {
		first := "none"
		if len(j.Gaps) > 0 {
			first = fmt.Sprint(j.Gaps[0])
		}
		t.Errorf("a long gap = %d listed, truncated %v, first %s; want 1000, true, 3", len(j.Gaps), j.GapsTruncated, first)
	}
}

// A past range, a page at a time: the page is the range's, while the checked
// range and highestIssued are the range's and the series' — not the page's —
// so a counter ahead of the range but level with the series is no warning,
// on either page (D11).
func TestJournal_APastRangeOverTwoPages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for n, day := range map[int64]string{1: "2026-08-03", 2: "2026-08-10", 3: "2026-08-17", 4: "2026-08-24", 5: "2026-09-02", 6: "2026-09-03"} {
		plantIssuedDocument(t, h, n, day)
	}
	h.Exec(t, `INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 7)`)

	for page, want := range map[string][]int64{"1": {1, 2}, "2": {3, 4}} {
		j := journal(t, h, "?from=2026-08-01&to=2026-08-31&pageSize=2&page="+page)
		var numbers []int64
		for _, d := range j.Data {
			numbers = append(numbers, d.Number)
		}
		if !slices.Equal(numbers, want) || j.Pagination.TotalCount != 4 {
			t.Errorf("page %s = %v of %d, want %v of 4", page, numbers, j.Pagination.TotalCount, want)
		}
		if !is(j.CheckedFrom, 1) || !is(j.CheckedTo, 4) || len(j.Gaps) != 0 {
			t.Errorf("page %s = checked %v to %v gaps %v, want the range's 1 to 4 and none", page, j.CheckedFrom, j.CheckedTo, j.Gaps)
		}
		if !is(j.HighestIssued, 6) || !is(j.CounterLast, 6) {
			t.Errorf("page %s = highest %v counter %v, want both 6: the series', not the page's", page, j.HighestIssued, j.CounterLast)
		}
	}
}

// Numbers below the series start are never gaps, and counterLast is the
// counter's even when it is ahead of every document.
func TestJournal_TheSeriesStartAndTheCounter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if j := journal(t, h, "?from=2026-09-01&to=2026-09-30"); j.HighestIssued != nil || j.CounterLast != nil {
		t.Errorf("nothing issued = highest %v counter %v, want both absent", j.HighestIssued, j.CounterLast)
	}
	body := completeSeller(1)
	body["seriesStart"] = 100
	saveSeller(t, h, body)
	plantIssuedDocument(t, h, 100, "2026-09-02")
	plantIssuedDocument(t, h, 101, "2026-09-03")
	h.Exec(t, `INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 103)`)

	j := journal(t, h, "?from=2026-09-01&to=2026-09-30")
	if len(j.Gaps) != 0 || j.SeriesStart != 100 {
		t.Errorf("gaps = %v with the series at 100, want none", j.Gaps)
	}
	if !is(j.CounterLast, 102) || !is(j.HighestIssued, 101) {
		t.Errorf("counterLast = %v, highestIssued %v; want 102 — ahead of the highest document, 101", j.CounterLast, j.HighestIssued)
	}
	if !is(j.CheckedFrom, 100) {
		t.Errorf("checkedFrom = %v, want the series start, 100", j.CheckedFrom)
	}
	if j := journal(t, h, "?from=2026-10-01&to=2026-10-31"); !is(j.HighestIssued, 101) {
		t.Errorf("an empty range's highestIssued = %v, want the series' 101", j.HighestIssued)
	}
}

// The journal is read with invoices:access (D11); a caller without it is the
// access layer's 403.
func TestJournal_NeedsAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if res := h.SignIn(t).Do(http.MethodGet, journalPath+"?from=2026-09-01&to=2026-09-30", nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /journal without invoices:access = %d %s, want 403", res.Status, res.Body)
	}
}
