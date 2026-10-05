package integration_test

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Work becomes invoices (invoices work design D1–D9), end to end against the
// real modules: the hours Time approved, the expense Expenses approved and the
// milestone Projects readied are read by Invoices through the source modules'
// own billable reads, held by a draft, stamped by the issue through their own
// holders inside its transaction, taken back by a credit note and pulled
// again. Each module's suite proves its half against fakes of the other; this
// proves the halves agree.

// workSource is one source of a wizard's body, as the view showed it.
type workSource struct {
	Kind     string `json:"kind"`
	ID       int64  `json:"id"`
	Revision int32  `json:"revision"`
}

// viewRow is one row of GET /invoices/work.
type viewRow struct {
	ID         int64  `json:"id"`
	Revision   int32  `json:"revision"`
	Selectable bool   `json:"selectable"`
	Reason     string `json:"reason"`
	HeldBy     *struct {
		InvoiceID int64 `json:"invoiceId"`
	} `json:"heldBy"`
}

// workView is GET /invoices/work as the tests read it.
type workView struct {
	Projects []struct {
		ID         int32     `json:"id"`
		Hours      []viewRow `json:"hours"`
		Expenses   []viewRow `json:"expenses"`
		Milestones []viewRow `json:"milestones"`
	} `json:"projects"`
}

// readView is the customer's uninvoiced work.
func readView(t *testing.T, c *modtest.Client, customer int32) workView {
	t.Helper()
	var v workView
	okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/work?customerId=%d", invoicesBase, customer), nil, &v)
	return v
}

// selectable is every selectable row of the view as a wizard's sources, in
// the view's order, of the kinds named.
func (v workView) selectable(kinds ...string) []workSource {
	var out []workSource
	for _, p := range v.Projects {
		for _, k := range []struct {
			kind string
			rows []viewRow
		}{{"time.entry", p.Hours}, {"expenses.entry", p.Expenses}, {"projects.milestone", p.Milestones}} {
			if !slices.Contains(kinds, k.kind) {
				continue
			}
			for _, r := range k.rows {
				if r.Selectable {
					out = append(out, workSource{Kind: k.kind, ID: r.ID, Revision: r.Revision})
				}
			}
		}
	}
	return out
}

// row is the view's row of kind and id, or nil.
func (v workView) row(kind string, id int64) *viewRow {
	for _, p := range v.Projects {
		rows := map[string][]viewRow{"time.entry": p.Hours, "expenses.entry": p.Expenses, "projects.milestone": p.Milestones}[kind]
		for i := range rows {
			if rows[i].ID == id {
				return &rows[i]
			}
		}
	}
	return nil
}

// workDoc is one invoices document as the work tests read it.
type workDoc struct {
	ID               int64   `json:"id"`
	Kind             string  `json:"kind"`
	Status           string  `json:"status"`
	Number           *int64  `json:"number"`
	Revision         int32   `json:"revision"`
	Note             string  `json:"note"`
	CustomerID       int32   `json:"customerId"`
	DeliveryFrom     *string `json:"deliveryFrom"`
	DeliveryTo       *string `json:"deliveryTo"`
	PaymentTermsDays *int32  `json:"paymentTermsDays"`
	ProjectID        *int32  `json:"projectId"`
	ProjectReference *string `json:"projectReference"`
	NetTotal         float64 `json:"netTotal"`
	GrossTotal       float64 `json:"grossTotal"`
	PdfStored        *bool   `json:"pdfStored"`
	Timesheet        bool    `json:"timesheet"`
	TimesheetRows    []struct {
		PersonLabel string  `json:"personLabel"`
		Date        string  `json:"date"`
		Hours       float64 `json:"hours"`
		Description string  `json:"description"`
	} `json:"timesheetRows"`
	Lines []struct {
		Description      string  `json:"description"`
		Quantity         float64 `json:"quantity"`
		Unit             string  `json:"unit"`
		UnitPrice        float64 `json:"unitPrice"`
		VatCodeID        int32   `json:"vatCodeId"`
		DeductsInvoiceID *int64  `json:"deductsInvoiceId"`
		Sources          []struct {
			Kind  string `json:"kind"`
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"sources"`
	} `json:"lines"`
	Sources *struct {
		Count        int `json:"count"`
		Held         int `json:"held"`
		Invoiced     int `json:"invoiced"`
		Released     int `json:"released"`
		WouldRelease []struct {
			Kind string `json:"kind"`
			ID   int64  `json:"id"`
		} `json:"wouldRelease"`
	} `json:"sources"`
}

// sourceStates is every line source of d as "kind id state", sorted.
func (d workDoc) sourceStates() []string {
	var out []string
	for _, l := range d.Lines {
		for _, s := range l.Sources {
			out = append(out, fmt.Sprintf("%s %d %s", s.Kind, s.ID, s.State))
		}
	}
	slices.Sort(out)
	return out
}

// statesOf is each source as "kind id state", sorted.
func statesOf(state string, sources ...workSource) []string {
	var out []string
	for _, s := range sources {
		out = append(out, fmt.Sprintf("%s %d %s", s.Kind, s.ID, state))
	}
	slices.Sort(out)
	return out
}

// fromWork makes a draft of sources for customer through the wizard, the
// timesheet on, grouped by project.
func fromWork(t *testing.T, c *modtest.Client, customer int32, sources ...workSource) workDoc {
	t.Helper()
	var d workDoc
	okJSON(t, c, http.MethodPost, invoicesBase+"/from-work", map[string]any{
		"customerId": customer, "sources": sources, "grouping": "project", "timesheet": true,
	}, &d)
	return d
}

// issueWork issues document id today.
func issueWork(t *testing.T, c *modtest.Client, id int64) workDoc {
	t.Helper()
	var d workDoc
	okJSON(t, c, http.MethodPost, invoiceAt(id)+"/issue", map[string]any{}, &d)
	if d.Status != "issued" || d.Number == nil {
		t.Fatalf("issue %d = %s number %v, want issued and numbered", id, d.Status, d.Number)
	}
	return d
}

// readWorkDoc is document id as it now stands.
func readWorkDoc(t *testing.T, c *modtest.Client, id int64) workDoc {
	t.Helper()
	var d workDoc
	okJSON(t, c, http.MethodGet, invoiceAt(id), nil, &d)
	return d
}

// keptLines is d's lines as a save sends them back: each with the sources it
// holds, as read.
func keptLines(d workDoc) []map[string]any {
	lines := []map[string]any{}
	for _, l := range d.Lines {
		refs := []map[string]any{}
		for _, s := range l.Sources {
			refs = append(refs, map[string]any{"kind": s.Kind, "id": s.ID})
		}
		lines = append(lines, map[string]any{"description": l.Description, "quantity": l.Quantity, "unit": l.Unit,
			"unitPrice": l.UnitPrice, "vatCodeId": l.VatCodeID, "sources": refs})
	}
	return lines
}

// pdfText is a text-showing operator of a PDF content stream: the string
// gofpdf writes, UTF-16BE with its parentheses and backslashes escaped.
var pdfText = regexp.MustCompile(`(?s)\(((?:\\.|[^\\)])*)\) ?Tj`)

// pdfStream is one stream object's bytes.
var pdfStream = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)

// pdfPageTexts is the text each page of the PDF res answered shows, page by
// page: every content stream that shows text, inflated when it is
// compressed, its strings decoded — what a reader of the stored document
// sees, read without the invoices package's own test hooks (SetPDFModelBuilt
// lives in its export_test.go), which no other package can reach.
func pdfPageTexts(t *testing.T, res *modtest.Response) [][]string {
	t.Helper()
	if res.Status != http.StatusOK || !bytes.HasPrefix(res.Body, []byte("%PDF")) {
		t.Fatalf("the PDF = %d %.40q, want a PDF", res.Status, res.Body)
	}
	var pages [][]string
	for _, m := range pdfStream.FindAllSubmatch(res.Body, -1) {
		content := m[1]
		if r, err := zlib.NewReader(bytes.NewReader(m[1])); err == nil {
			if inflated, err := io.ReadAll(r); err == nil {
				content = inflated
			}
		}
		var texts []string
		for _, shown := range pdfText.FindAllSubmatch(content, -1) {
			raw := regexp.MustCompile(`\\(.)`).ReplaceAll(shown[1], []byte("$1"))
			units := make([]uint16, 0, len(raw)/2)
			for i := 0; i+1 < len(raw); i += 2 {
				units = append(units, uint16(raw[i])<<8|uint16(raw[i+1]))
			}
			texts = append(texts, string(utf16.Decode(units)))
		}
		if len(texts) > 0 {
			pages = append(pages, texts)
		}
	}
	return pages
}

// sameCommit asserts each source module's row carries the xmin of the
// document's line sources — the transaction that last wrote it — so its stamp
// was written by the issue's own commit.
func sameCommit(t *testing.T, h *modtest.Harness, docID int64, rows map[string]int64) {
	t.Helper()
	issue := modtest.One[[]string](t, h, `SELECT array_agg(DISTINCT xmin::text) FROM invoices.line_sources WHERE invoice_id = $1`, docID)
	if len(issue) != 1 {
		t.Fatalf("document %d's line sources were last written by %v, want the one issue", docID, issue)
	}
	for table, id := range rows {
		if got := modtest.One[string](t, h, `SELECT xmin::text FROM `+table+` WHERE id = $1`, id); got != issue[0] {
			t.Errorf("%s %d was last written by transaction %s, the issue's is %s: not stamped by the issue's own commit", table, id, got, issue[0])
		}
	}
}

// unbilledHours is the customer overview's approved hours not yet invoiced,
// in hundredths — Time's ActualsTotals.Invoiced, read through the real
// contract.
func unbilledHours(t *testing.T, c *modtest.Client, customer int32) int64 {
	t.Helper()
	var o struct {
		Work *struct {
			UnbilledHoursHundredths int64 `json:"unbilledHoursHundredths"`
		} `json:"work"`
	}
	okJSON(t, c, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/overview", customer), nil, &o)
	if o.Work == nil {
		t.Fatalf("customer %d's overview has no work block", customer)
	}
	return o.Work.UnbilledHoursHundredths
}

// TestWork_FromApprovedWorkToReleasedWorkEndToEnd: Kari's and Ola's approved
// hours, a re-billable expense and a ready milestone on one project are
// listed by the view, made a draft by the wizard (grouped by project, the
// timesheet on) and issued; every source is then stamped with the invoice's
// id and number on its own module's wire, by the issue's own commit, and the
// figures read through the contracts move. An a-konto of a second milestone
// is issued and deducted by a settlement of a third; the first invoice is
// credited in full, its work released in all three modules, and pulled into a
// new draft that suggests the note naming both documents.
func TestWork_FromApprovedWorkToReleasedWorkEndToEnd(t *testing.T) {
	t.Parallel()
	h, kari, _ := workInstallation(t)
	customer := invoiceNewBusiness(t, kari, "Fjord Nord", "Fjord Nord AS", "923609016").Id
	project := newWorkProject(t, kari, customer, "FN1000", nil)
	ola, olaID := h.SignInUser(t, "time:access")
	h.Exec(t, `UPDATE identity.users SET display_name = 'Ola Hansen' WHERE id = $1`, olaID)
	addRole(t, h, project.Id, olaID, roleMember)

	kariHour := approvedHours(t, kari, kari, project.Id, "2026-09-01", 4)
	olaHour := approvedHours(t, ola, kari, project.Id, "2026-09-02", 3.5)
	expense := rebillableExpense(t, kari, project.Id, "2026-09-03", 1250)
	fase1 := newMilestone(t, kari, project.Id, "Fase 1", map[string]any{"amount": 10000})
	if got := unbilledHours(t, kari, customer); got != 750 {
		t.Fatalf("unbilled hours before the issue = %d hundredths, want 750", got)
	}

	// The view lists the work, all selectable; the wizard holds it.
	view := readView(t, kari, customer)
	work := view.selectable("time.entry", "expenses.entry", "projects.milestone")
	want := []workSource{
		{"time.entry", kariHour.Id, kariHour.Revision}, {"time.entry", olaHour.Id, olaHour.Revision},
		{"expenses.entry", expense.Id, expense.Revision}, {"projects.milestone", fase1.Id, fase1.Revision},
	}
	if !slices.Equal(work, want) {
		t.Fatalf("the view's selectable work = %+v, want %+v", work, want)
	}
	draft := fromWork(t, kari, customer, work...)
	if got := draft.sourceStates(); !slices.Equal(got, statesOf("held", want...)) {
		t.Errorf("the draft's sources = %v, want every one held", got)
	}
	if draft.ProjectID == nil || *draft.ProjectID != project.Id || draft.ProjectReference == nil || *draft.ProjectReference != project.Code {
		t.Errorf("the draft's project = %v %v, want %d %s", draft.ProjectID, draft.ProjectReference, project.Id, project.Code)
	}
	var sheet []string
	for _, r := range draft.TimesheetRows {
		sheet = append(sheet, fmt.Sprintf("%s %s %g %s", r.PersonLabel, r.Date, r.Hours, r.Description))
	}
	if wantSheet := []string{"KN 2026-09-01 4 " + project.Name, "OH 2026-09-02 3.5 " + project.Name}; !draft.Timesheet || !slices.Equal(sheet, wantSheet) {
		t.Errorf("the timesheet = %v %v, want on and %v", draft.Timesheet, sheet, wantSheet)
	}
	if r := readView(t, kari, customer).row("time.entry", kariHour.Id); r == nil || r.Selectable || r.HeldBy == nil || r.HeldBy.InvoiceID != draft.ID {
		t.Errorf("Kari's hour in the view after the wizard = %+v, want listed, held by draft %d", r, draft.ID)
	}

	// The issue stamps every source in its own transaction.
	first := issueWork(t, kari, draft.ID)
	if got := first.sourceStates(); !slices.Equal(got, statesOf("invoiced", want...)) {
		t.Errorf("the invoice's sources = %v, want every one invoiced", got)
	}
	if first.PdfStored == nil || !*first.PdfStored || len(first.TimesheetRows) != 2 {
		t.Errorf("the issued invoice: pdf stored %v, %d timesheet rows; want stored, with both", first.PdfStored, len(first.TimesheetRows))
	}
	for _, e := range []workHour{readHour(t, kari, kariHour.Id), readHour(t, kari, olaHour.Id)} {
		if e.Status != "invoiced" || e.InvoicedBy == nil || e.InvoicedBy.InvoiceId != first.ID || e.InvoicedBy.Number != *first.Number {
			t.Errorf("time entry %d = %s by %+v, want invoiced by invoice %d number %d", e.Id, e.Status, e.InvoicedBy, first.ID, *first.Number)
		}
	}
	if e := readExpense(t, kari, expense.Id); e.Billing == nil || e.Billing.Invoice == nil || e.Billing.Invoice.InvoicedBy == nil ||
		e.Billing.Invoice.InvoicedBy.InvoiceId != first.ID || e.Billing.Invoice.InvoicedBy.Number != *first.Number || e.Billing.Invoice.Reference != nil {
		t.Errorf("the expense's billing = %+v, want invoiced by invoice %d number %d, no reference", e.Billing, first.ID, *first.Number)
	}
	if m := readMilestone(t, kari, project.Id, fase1.Id); m.Status != "invoiced" || m.InvoicedByInvoice == nil ||
		m.InvoicedByInvoice.InvoiceId != first.ID || m.InvoicedByInvoice.Number != *first.Number {
		t.Errorf("the milestone = %s by %+v, want invoiced by invoice %d number %d", m.Status, m.InvoicedByInvoice, first.ID, *first.Number)
	}
	// The stored PDF carries the timesheet on a page of its own, after the
	// invoice's: one row per held hour, a total per person and overall.
	pages := pdfPageTexts(t, kari.Do(http.MethodGet, invoiceAt(first.ID)+"/pdf", nil))
	if len(pages) != 2 || slices.Contains(pages[0], "Timeliste") {
		t.Fatalf("the stored PDF's pages = %q, want the invoice and then the timesheet", pages)
	}
	for _, text := range []string{"Timeliste", "01.09.2026", "KN", "02.09.2026", "OH", project.Name, "4,00", "3,50",
		"Sum KN", "Sum OH", "Sum timer", "7,50"} {
		if !slices.Contains(pages[1], text) {
			t.Errorf("the timesheet page %q has no %q", pages[1], text)
		}
	}
	sameCommit(t, h, first.ID, map[string]int64{
		"time.entries": kariHour.Id, "expenses.entries": expense.Id, "projects.billing_milestones": fase1.Id,
	})
	if got := unbilledHours(t, kari, customer); got != 0 {
		t.Errorf("unbilled hours after the issue = %d hundredths, want 0: Time's invoiced bucket holds all 7.5", got)
	}
	economy := readEconomy(t, kari, project.Id)
	if economy.Expenses == nil || economy.Expenses.InvoicedCount != 1 || !same(economy.Expenses.InvoicedAmount, 1100) || economy.Expenses.ReadyCount != 0 {
		t.Errorf("the economy's expenses = %+v, want the one line invoiced at 1 100 and none ready", economy.Expenses)
	}
	if v := readView(t, kari, customer); len(v.selectable("time.entry", "expenses.entry", "projects.milestone")) != 0 {
		t.Errorf("the view after the issue still offers %+v", v.selectable("time.entry", "expenses.entry", "projects.milestone"))
	}

	// An a-konto of a second milestone, then a settlement of a third
	// deducting it.
	fase2 := newMilestone(t, kari, project.Id, "Fase 2 a konto", map[string]any{"amount": 20000})
	aKonto := issueWork(t, kari, fromWork(t, kari, customer, workSource{"projects.milestone", fase2.Id, fase2.Revision}).ID)
	fase3 := newMilestone(t, kari, project.Id, "Fase 3", map[string]any{"amount": 30000})
	settlement := fromWork(t, kari, customer, workSource{"projects.milestone", fase3.Id, fase3.Revision})
	var deductible []struct {
		InvoiceID int64   `json:"invoiceId"`
		VatCodeID int32   `json:"vatCodeId"`
		Left      float64 `json:"left"`
	}
	okJSON(t, kari, http.MethodGet, invoiceAt(settlement.ID)+"/deductible", nil, &deductible)
	i := slices.IndexFunc(deductible, func(d struct {
		InvoiceID int64   `json:"invoiceId"`
		VatCodeID int32   `json:"vatCodeId"`
		Left      float64 `json:"left"`
	}) bool {
		return d.InvoiceID == aKonto.ID
	})
	if i < 0 || deductible[i].Left != 20000 {
		t.Fatalf("deductible = %+v, want the a-konto %d with 20 000 left", deductible, aKonto.ID)
	}
	lines := append(keptLines(settlement), map[string]any{
		"description": fmt.Sprintf("Tidligere fakturert a konto, faktura %d", *aKonto.Number), "quantity": -1,
		"unitPrice": deductible[i].Left, "vatCodeId": deductible[i].VatCodeID, "deductsInvoiceId": aKonto.ID,
		"sources": []any{},
	})
	var saved workDoc
	okJSON(t, kari, http.MethodPut, invoiceAt(settlement.ID), map[string]any{
		"customerId": customer, "revision": settlement.Revision, "paymentTermsDays": *settlement.PaymentTermsDays,
		"deliveryFrom": settlement.DeliveryFrom, "deliveryTo": settlement.DeliveryTo, "lines": lines,
	}, &saved)
	settled := issueWork(t, kari, saved.ID)
	if len(settled.Lines) != 2 || settled.Lines[1].DeductsInvoiceID == nil || *settled.Lines[1].DeductsInvoiceID != aKonto.ID ||
		settled.Lines[1].Quantity != -1 || len(settled.Lines[1].Sources) != 0 || !same(settled.GrossTotal, 12500) {
		t.Errorf("the settlement = %+v gross %v, want the milestone and a deduction of the a-konto, 12 500 to pay", settled.Lines, settled.GrossTotal)
	}
	if m := readMilestone(t, kari, project.Id, fase3.Id); m.InvoicedByInvoice == nil || m.InvoicedByInvoice.InvoiceId != settled.ID {
		t.Errorf("the settlement's milestone = %+v, want invoiced by the settlement", m.InvoicedByInvoice)
	}

	// A full credit of the first invoice releases its work in all three
	// modules.
	var creditDraft workDoc
	okJSON(t, kari, http.MethodPost, invoiceAt(first.ID)+"/credit", nil, &creditDraft)
	if creditDraft.Sources == nil || len(creditDraft.Sources.WouldRelease) != 4 {
		t.Errorf("the credit-note draft's sources = %+v, want the four it would release", creditDraft.Sources)
	}
	credit := issueWork(t, kari, creditDraft.ID)
	if got := readWorkDoc(t, kari, first.ID).sourceStates(); !slices.Equal(got, statesOf("released", want...)) {
		t.Errorf("the credited invoice's sources = %v, want every one released", got)
	}
	for _, e := range []workHour{readHour(t, kari, kariHour.Id), readHour(t, kari, olaHour.Id)} {
		if e.Status != "approved" || e.InvoicedBy != nil || e.InvoicedAt != nil {
			t.Errorf("time entry %d after the credit = %s by %+v at %v, want approved again and unstamped", e.Id, e.Status, e.InvoicedBy, e.InvoicedAt)
		}
	}
	if e := readExpense(t, kari, expense.Id); e.Billing == nil || e.Billing.Invoice != nil {
		t.Errorf("the expense's billing after the credit = %+v, want no invoice", e.Billing)
	}
	if m := readMilestone(t, kari, project.Id, fase1.Id); m.Status != "ready" || m.InvoicedByInvoice != nil {
		t.Errorf("the milestone after the credit = %s by %+v, want ready again", m.Status, m.InvoicedByInvoice)
	}
	if got := unbilledHours(t, kari, customer); got != 750 {
		t.Errorf("unbilled hours after the credit = %d hundredths, want 750 again", got)
	}

	// The released work is selectable again and pulled into a new draft,
	// which suggests the note naming the invoice and its credit note.
	again := readView(t, kari, customer).selectable("time.entry", "expenses.entry", "projects.milestone")
	if len(again) != 4 {
		t.Fatalf("the view after the credit offers %+v, want the four released sources", again)
	}
	pulled := fromWork(t, kari, customer, again...)
	if want := fmt.Sprintf("Erstatter faktura %d, kreditert med kreditnota %d", *first.Number, *credit.Number); pulled.Note != want {
		t.Errorf("the new draft's note = %q, want %q", pulled.Note, want)
	}
	if got := pulled.sourceStates(); len(got) != 4 || !strings.HasSuffix(got[0], " held") || len(pulled.TimesheetRows) != 2 {
		t.Errorf("the new draft holds %v with %d timesheet rows, want the four held and both rows", got, len(pulled.TimesheetRows))
	}
}
