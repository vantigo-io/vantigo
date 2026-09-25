package expenses_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Supplier invoices (docs/superpowers/specs/2026-09-26-supplier-invoices-design.md):
// the invoice a supplier sends for work or goods on a project, recorded as a
// fourth kind of expense. It borrows the outlay's money whole, is paid by the
// company and so owes nobody, is booked by whoever holds the project's
// financial rights rather than by its team, needs its document to be
// submitted, and is visible to the project's financial side.

// subcontractorCategory is the second category 00012 seeds, after Materials —
// the one a supplier invoice is most often booked under.
const subcontractorCategory = 1002

// projectAbandoned is a cancelled project, the one status that takes no
// supplier invoice. The shared fixtures have none, so the tests that need one
// add it.
const projectAbandoned = 1006

func addAbandonedProject(h *harness) {
	customer := int32(1001)
	h.projects.addProject(contracts.ProjectEntry{
		ID: projectAbandoned, Code: "AVLYST01", Name: "Avlyst prosjekt",
		CustomerID: &customer, Status: "cancelled", OpenForWork: false,
		BillingType: "time-and-materials", Currency: ptr("NOK"),
	})
}

// supplierInvoiceBody is a valid supplier invoice on 1001, which tests
// override one field of at a time; a nil override removes the field. It names
// no payer — the request may leave it out, and the invoice is the company's —
// and it is billable, because what a supplier invoiced is usually billed on.
func supplierInvoiceBody(overrides map[string]any) map[string]any {
	return bodyWith(map[string]any{
		"kind":          "supplier_invoice",
		"entryDate":     "2026-03-10",
		"description":   "Rørleggerarbeid, uke 10",
		"categoryId":    subcontractorCategory,
		"supplier":      "Rør & Varme AS",
		"invoiceNumber": "F-20260310",
		"dueDate":       "2026-04-09",
		"currency":      "NOK",
		"grossAmount":   12500.00,
		"vatAmount":     2500.00,
		"projectId":     projectKraftVerket,
		"billable":      true,
	}, overrides)
}

// financeReader is the caller D2 and D4 are written for: they see every
// project and its money, and hold no role on any of them — so they are on no
// team and projects' own CanLogTime would let them book nothing.
func financeReader(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return signIn(t, h, "projects:view-all", "projects:view-financials")
}

// attachInvoice uploads the supplier's invoice to one expense.
func attachInvoice(t *testing.T, c *modtest.Client, entryID int64) {
	t.Helper()
	uploadReceipt(t, c, entryID, "faktura.pdf", "application/pdf", testPDF(64))
}

func TestSupplierInvoices_ARecordedInvoiceIsTheCompanysDraftOnItsProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, financeID := financeReader(t, h)

	e := createEntry(t, finance, supplierInvoiceBody(nil))
	if e.Kind != "supplier_invoice" || e.Status != "draft" || e.Owner.UserId != financeID {
		t.Fatalf("recorded = kind %q status %q owner %v, want the recorder's supplier_invoice draft", e.Kind, e.Status, e.Owner.UserId)
	}
	if e.Supplier == nil || *e.Supplier != "Rør & Varme AS" || e.InvoiceNumber == nil || *e.InvoiceNumber != "F-20260310" ||
		e.DueDate == nil || *e.DueDate != "2026-04-09" {
		t.Errorf("supplier/number/due = %v/%v/%v, want Rør & Varme AS, F-20260310, 2026-04-09", e.Supplier, e.InvoiceNumber, e.DueDate)
	}
	// The company pays a supplier invoice: stored as company-paid whatever the
	// request left out, and owed to nobody.
	if e.PaidBy == nil || *e.PaidBy != "company" || e.OwedToEmployee != 0 || e.Capabilities.CanMarkReimbursed {
		t.Errorf("paidBy %v owed %v canMarkReimbursed %v, want company, 0, false", e.PaidBy, e.OwedToEmployee, e.Capabilities.CanMarkReimbursed)
	}
	if e.NetAmount != 10000 || e.Category == nil || e.Category.Id != subcontractorCategory || e.Project == nil || e.Project.Id != projectKraftVerket {
		t.Errorf("net %v category %v project %v, want 10000 under Subcontractor on %d", e.NetAmount, e.Category, e.Project, projectKraftVerket)
	}
	// Priced as an outlay (D3): the settings' default markup, 0 %, on the net.
	if !e.Billable || e.Billing == nil || e.Billing.BillAmount != 10000 || e.Billing.MarkupPercent == nil || *e.Billing.MarkupPercent != 0 {
		t.Errorf("billable %v billing %+v, want billable at the default markup, 10000 billed", e.Billable, e.Billing)
	}
	// The supplier's number is never the outgoing invoice stamp.
	if e.Billing != nil && e.Billing.Invoice != nil {
		t.Errorf("billing.invoice = %+v on a line nobody has invoiced", e.Billing.Invoice)
	}
	// The list's kind filter takes the new kind.
	if ids := entryIDs(listEntries(t, finance, "?kind=supplier_invoice")); !slices.Equal(ids, []int64{e.Id}) {
		t.Errorf("?kind=supplier_invoice = %v, want [%d]", ids, e.Id)
	}
}

func TestSupplierInvoices_TheBodyIsRefusedFieldByField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, _ := financeReader(t, h)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	trip := createClaim(t, manager, map[string]any{"projectId": projectKraftVerket})

	cases := []struct {
		name   string
		caller *modtest.Client
		body   map[string]any
		field  string
		want   string
	}{
		{"no supplier", finance, supplierInvoiceBody(map[string]any{"supplier": nil}), "supplier", "A supplier invoice names its supplier"},
		{"a blank supplier", finance, supplierInvoiceBody(map[string]any{"supplier": "   "}), "supplier", "A supplier invoice names its supplier"},
		{"no invoice number", finance, supplierInvoiceBody(map[string]any{"invoiceNumber": nil}), "invoiceNumber", "A supplier invoice carries the supplier's invoice number"},
		{"an invoice number too long", finance, supplierInvoiceBody(map[string]any{"invoiceNumber": strings.Repeat("9", 101)}), "invoiceNumber", "An invoice number can be at most 100 characters"},
		{"no category", finance, supplierInvoiceBody(map[string]any{"categoryId": nil}), "categoryId", "A supplier invoice needs a category"},
		{"no amount", finance, supplierInvoiceBody(map[string]any{"grossAmount": nil, "vatAmount": nil}), "grossAmount", "A supplier invoice needs an amount"},
		{"VAT above the amount", finance, supplierInvoiceBody(map[string]any{"vatAmount": 20000.00}), "vatAmount", "VAT cannot be more than the amount it is part of"},
		{"paid by the employee", finance, supplierInvoiceBody(map[string]any{"paidBy": "employee"}), "paidBy", "A supplier invoice is paid by the company"},
		{"paid by no one the module knows", finance, supplierInvoiceBody(map[string]any{"paidBy": "supplier"}), "paidBy", "'supplier' is not a payer; a supplier invoice is paid by the company"},
		{"no project", finance, supplierInvoiceBody(map[string]any{"projectId": nil, "billable": nil}), "projectId", "A supplier invoice is booked on a project"},
		{"a due date before the invoice date", finance, supplierInvoiceBody(map[string]any{"dueDate": "2026-03-09"}), "dueDate", "The due date cannot be before the invoice date"},
		{"a distance", finance, supplierInvoiceBody(map[string]any{"distanceKm": 12.0}), "distanceKm", "A supplier invoice line carries no distanceKm"},
		{"passengers", finance, supplierInvoiceBody(map[string]any{"passengers": 1}), "passengers", "A supplier invoice line carries no passengers"},
		{"a per diem type", finance, supplierInvoiceBody(map[string]any{"perDiemType": "day_6_12"}), "perDiemType", "A supplier invoice line carries no perDiemType"},
		{"a covered meal", finance, supplierInvoiceBody(map[string]any{"lunchCovered": true}), "lunchCovered", "A supplier invoice line carries no lunchCovered"},
		{"in a travel claim", manager, supplierInvoiceBody(map[string]any{"claimId": trip.Id}), "claimId", "A supplier invoice is not a travel claim line"},
	}
	for _, c := range cases {
		errs := refusedEntry(t, c.caller, http.MethodPost, entriesPath, c.body)
		if !mentions(errs[c.field], c.want) {
			t.Errorf("%s: %s = %v, want %q", c.name, c.field, errs[c.field], c.want)
		}
	}
	// Every other kind carries neither of the invoice's fields.
	errs := refusedEntry(t, finance, http.MethodPost, entriesPath,
		outlayBody(map[string]any{"invoiceNumber": "F-1", "dueDate": "2026-04-09"}))
	for _, field := range []string{"invoiceNumber", "dueDate"} {
		if !mentions(errs[field], "An outlay line carries no "+field) {
			t.Errorf("an outlay with %s: %v, want it refused on the field", field, errs[field])
		}
	}
}

func TestSupplierInvoices_WithoutProjectsTheKindIsRefusedOutright(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	c, _ := signIn(t, h)
	errs := refusedEntry(t, c, http.MethodPost, entriesPath,
		supplierInvoiceBody(map[string]any{"projectId": nil, "billable": nil}))
	if !mentions(errs["kind"], "A supplier invoice is booked on a project") {
		t.Errorf("kind = %v, want the kind refused: a supplier invoice exists only on a project", errs["kind"])
	}
}

// An installation that loses the projects module keeps what was booked
// (decision X2): a supplier invoice already recorded stays editable, its
// project columns carried from the row and what it bills recomputed from the
// kept markup — while a new one is still refused outright.
func TestSupplierInvoices_WithoutProjectsAnExistingOneStaysEditable(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	c, owner := signIn(t, h)
	id := modtest.One[int64](t, h.Harness, `INSERT INTO expenses.entries
	    (user_id, created_by_user_id, kind, entry_date, description, category_id, supplier,
	     supplier_invoice_number, paid_by, currency, gross_amount, project_id, billable,
	     markup_percent, bill_amount, created_at, updated_at)
	    VALUES ($1, $1, 'supplier_invoice', DATE '2026-03-10', 'Rørleggerarbeid', $2, 'Rør & Varme AS',
	            'F-1', 'company', 'NOK', 1250.00, $3, true, 10.00, 1375.00, now(), now())
	    RETURNING id`, owner, int32(subcontractorCategory), int32(projectKraftVerket))

	changed := updateEntry(t, c, id, supplierInvoiceBody(map[string]any{
		"projectId": nil, "billable": nil, "vatAmount": nil, "grossAmount": 2500.00,
		"invoiceNumber": "F-2", "revision": 1}))
	if changed.Kind != "supplier_invoice" || changed.InvoiceNumber == nil || *changed.InvoiceNumber != "F-2" {
		t.Errorf("after the edit: kind %q number %v, want the supplier invoice renumbered F-2", changed.Kind, changed.InvoiceNumber)
	}
	if project := modtest.One[int32](t, h.Harness, `SELECT project_id FROM expenses.entries WHERE id = $1`, id); project != projectKraftVerket {
		t.Errorf("project_id after the edit = %d, want %d carried through", project, projectKraftVerket)
	}
	if billed := modtest.One[string](t, h.Harness, `SELECT bill_amount::text FROM expenses.entries WHERE id = $1`, id); billed != "2750.00" {
		t.Errorf("bill_amount after the edit = %s, want 2750.00: the kept 10 %% on the new net", billed)
	}
	errs := refusedEntry(t, c, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"projectId": nil, "billable": nil}))
	if !mentions(errs["kind"], "A supplier invoice is booked on a project") {
		t.Errorf("a new one without projects: kind = %v, want it refused", errs["kind"])
	}
}

func TestSupplierInvoices_TheRecordersFinancialRightsDecide(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	addAbandonedProject(h)

	finance, _ := financeReader(t, h)
	createEntry(t, finance, supplierInvoiceBody(nil))
	// An invoice often arrives after the work: a completed project takes one.
	completed := createEntry(t, finance, supplierInvoiceBody(map[string]any{"projectId": projectCompleted}))
	if completed.Project == nil || completed.Project.Id != projectCompleted {
		t.Errorf("project = %+v, want the completed project %d", completed.Project, projectCompleted)
	}
	// The manager role is financial rights; so is projects:manage-all.
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	createEntry(t, manager, supplierInvoiceBody(nil))
	everywhere, _ := signIn(t, h, "projects:manage-all")
	createEntry(t, everywhere, supplierInvoiceBody(nil))

	// A cancelled project refuses, and says so to someone who may know.
	errs := refusedEntry(t, finance, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"projectId": projectAbandoned}))
	if !mentions(errs["projectId"], "This project is cancelled, so it takes no supplier invoices") {
		t.Errorf("a cancelled project: projectId = %v, want the cancelled refusal", errs["projectId"])
	}

	// A member of the team may book an outlay here — CanLogTime — and may not
	// record a supplier invoice, and hears what an unknown project gets.
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	createEntry(t, member, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	blind, _ := signIn(t, h, "projects:view-financials") // sees no project at all
	for name, refusal := range map[string]map[string][]string{
		"a member without financial rights":     refusedEntry(t, member, http.MethodPost, entriesPath, supplierInvoiceBody(nil)),
		"an unknown project":                    refusedEntry(t, member, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"projectId": projectUnknown})),
		"view-financials on a project not seen": refusedEntry(t, blind, http.MethodPost, entriesPath, supplierInvoiceBody(nil)),
		"a cancelled project, to a member":      refusedEntry(t, member, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"projectId": projectAbandoned})),
	} {
		if !mentions(refusal["projectId"], "This project is not one you can record a supplier invoice on") {
			t.Errorf("%s: projectId = %v, want the one refusal that tells nothing apart", name, refusal["projectId"])
		}
	}
}

func TestSupplierInvoices_TheOwnerAndManageChangeAndDeleteThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, _ := financeReader(t, h)
	_, colleague := signIn(t, h)

	// Recording one for a colleague still needs expenses:manage, and the
	// financial rights are the recorder's own: the colleague becomes the owner.
	clerk, _ := signIn(t, h, "expenses:manage", "projects:view-all", "projects:view-financials")
	forThem := createEntry(t, clerk, supplierInvoiceBody(map[string]any{"userId": colleague}))
	if forThem.Owner.UserId != colleague {
		t.Errorf("owner = %v, want the colleague %v", forThem.Owner.UserId, colleague)
	}
	errs := refusedEntry(t, finance, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"userId": colleague}))
	if !mentions(errs["userId"], "needs the Manage expenses permission") {
		t.Errorf("recording for a colleague without expenses:manage: userId = %v", errs["userId"])
	}
	manageOnly, _ := signIn(t, h, "expenses:manage")
	errs = refusedEntry(t, manageOnly, http.MethodPost, entriesPath, supplierInvoiceBody(map[string]any{"userId": colleague}))
	if !mentions(errs["projectId"], "This project is not one you can record a supplier invoice on") {
		t.Errorf("expenses:manage without financial rights: projectId = %v", errs["projectId"])
	}

	mine := createEntry(t, finance, supplierInvoiceBody(nil))
	changed := updateEntry(t, finance, mine.Id, supplierInvoiceBody(map[string]any{
		"invoiceNumber": "F-20260311", "revision": mine.Revision}))
	if changed.InvoiceNumber == nil || *changed.InvoiceNumber != "F-20260311" {
		t.Errorf("invoiceNumber after the replace = %v, want F-20260311", changed.InvoiceNumber)
	}
	// expenses:manage changes anyone's draft; the project it keeps is not
	// judged again, and a full replace without dueDate clears it.
	byClerk := updateEntry(t, manageOnly, mine.Id, supplierInvoiceBody(map[string]any{
		"dueDate": nil, "revision": changed.Revision}))
	if byClerk.DueDate != nil || byClerk.Owner.UserId != mine.Owner.UserId {
		t.Errorf("after manage's replace: dueDate %v owner %v, want cleared and still the recorder", byClerk.DueDate, byClerk.Owner.UserId)
	}
	if r := finance.Do(http.MethodDelete, entryPath(mine.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("the owner's delete: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := manageOnly.Do(http.MethodDelete, entryPath(forThem.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("manage's delete: status %d body %s, want 204", r.Status, r.Body)
	}
}

func TestSupplierInvoices_ADraftChangesBetweenOutlayAndSupplierInvoice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	outlay := createEntry(t, manager, outlayBody(map[string]any{"projectId": projectKraftVerket, "paidBy": "company"}))
	attachInvoice(t, manager, outlay.Id)
	current := getEntry(t, manager, outlay.Id)

	// The missing-field refusals say what the new kind needs.
	errs := refusedEntry(t, manager, http.MethodPut, entryPath(outlay.Id), supplierInvoiceBody(map[string]any{
		"supplier": nil, "invoiceNumber": nil, "revision": current.Revision}))
	if !mentions(errs["supplier"], "A supplier invoice names its supplier") ||
		!mentions(errs["invoiceNumber"], "A supplier invoice carries the supplier's invoice number") {
		t.Errorf("an outlay made a supplier invoice without its fields: %v", errs)
	}
	// Both kinds carry documents, so the attachment comes along.
	invoice := updateEntry(t, manager, outlay.Id, supplierInvoiceBody(map[string]any{"revision": current.Revision}))
	if invoice.Kind != "supplier_invoice" || invoice.AttachmentCount != 1 || invoice.PaidBy == nil || *invoice.PaidBy != "company" {
		t.Errorf("after the change: kind %q attachments %d paidBy %v", invoice.Kind, invoice.AttachmentCount, invoice.PaidBy)
	}
	back := updateEntry(t, manager, invoice.Id, outlayBody(map[string]any{
		"projectId": projectKraftVerket, "paidBy": "employee", "revision": invoice.Revision}))
	if back.Kind != "outlay" || back.AttachmentCount != 1 || back.InvoiceNumber != nil || back.DueDate != nil {
		t.Errorf("back to an outlay: kind %q attachments %d number %v due %v", back.Kind, back.AttachmentCount, back.InvoiceNumber, back.DueDate)
	}
	// Mileage carries no documents, so that change is still refused.
	errs = refusedEntry(t, manager, http.MethodPut, entryPath(back.Id), mileageBody(map[string]any{"revision": back.Revision}))
	if !mentions(errs["kind"], "Remove this expense's receipts") {
		t.Errorf("an outlay with an attachment made mileage: kind = %v", errs["kind"])
	}
}

// The way back is a new booking: a supplier invoice's project was judged by
// its recorder's financial rights, so an outlay on the same project is judged
// on the owner's CanLogTime in full — and a finance reader on no team may not
// book one there, however long the invoice has sat on it.
func TestSupplierInvoices_TurnedIntoAnOutlayTheProjectIsJudgedAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, _ := financeReader(t, h)
	invoice := createEntry(t, finance, supplierInvoiceBody(nil))

	errs := refusedEntry(t, finance, http.MethodPut, entryPath(invoice.Id), outlayBody(map[string]any{
		"projectId": projectKraftVerket, "paidBy": "company", "revision": invoice.Revision}))
	if !mentions(errs["projectId"], "This project is not one the expense's owner can book on") {
		t.Errorf("a finance reader's supplier invoice made an outlay: projectId = %v, want the booking refusal", errs["projectId"])
	}
	if got := getEntry(t, finance, invoice.Id); got.Kind != "supplier_invoice" || got.Revision != invoice.Revision {
		t.Errorf("after the refusal: kind %q revision %d, want the supplier invoice untouched", got.Kind, got.Revision)
	}
}

func TestSupplierInvoices_SubmitNeedsTheSuppliersInvoiceAttached(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	finance, _ := financeReader(t, h)
	e := createEntry(t, finance, supplierInvoiceBody(nil))

	errs := refusedFlow(t, finance, submitPath, flowBody([]int64{e.Id}, nil))
	if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d cannot be submitted yet: Attach the supplier's invoice", e.Id)) {
		t.Errorf("entryIds = %v, want the document refusal", errs["entryIds"])
	}
	if got := getEntry(t, finance, e.Id); got.Status != "draft" {
		t.Errorf("status after the refusal = %q, want draft", got.Status)
	}
	attachInvoice(t, finance, e.Id)
	if moved := submitEntries(t, finance, e.Id); len(moved) != 1 || moved[0].Status != "submitted" {
		t.Errorf("submit with the invoice attached = %+v, want it submitted", moved)
	}
}

func TestSupplierInvoices_AreAttestedPricedAndInvoicedLikeAnyCost(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	approver, _ := signIn(t, h, "expenses:approve")
	clerk, _ := signIn(t, h, "expenses:manage")
	submitted := func(number string) entryJSON {
		e := createEntry(t, manager, supplierInvoiceBody(map[string]any{"invoiceNumber": number}))
		attachInvoice(t, manager, e.Id)
		submitEntries(t, manager, e.Id)
		return e
	}

	// The project's manager approves — their own recording included.
	own := submitted("F-1")
	if moved := approveEntries(t, manager, own.Id); moved[0].Status != "approved" {
		t.Errorf("the manager's approval = %q", moved[0].Status)
	}
	other := submitted("F-2")
	approveEntries(t, approver, other.Id)
	sent := submitted("F-3")
	if moved := rejectEntries(t, approver, "Feil prosjekt", sent.Id); moved[0].Status != "rejected" {
		t.Errorf("rejected = %q", moved[0].Status)
	}
	if moved := unapproveEntries(t, clerk, other.Id); moved[0].Status != "draft" {
		t.Errorf("unapproved = %q, want a fresh draft", moved[0].Status)
	}

	// The pricing door: a markup named from the project's side, on the net.
	priced := setBilling(t, manager, own.Id, setBillingBody(getEntry(t, manager, own.Id).Revision,
		map[string]any{"markupPercent": 10.0}))
	if priced.Billing == nil || priced.Billing.BillAmount != 11000 {
		t.Errorf("billing after a 10 %% markup = %+v, want 11000 on a 10000 net", priced.Billing)
	}
	ready := listEntries(t, manager, fmt.Sprintf("?projectId=%d&toInvoice=true", projectKraftVerket))
	if ids := entryIDs(ready); !slices.Equal(ids, []int64{own.Id}) {
		t.Errorf("ready to invoice = %v, want [%d]", ids, own.Id)
	}
	invoiced := markInvoiced(t, manager, own.Id, invoicedBody(priced.Revision, map[string]any{"reference": "KF-1001"}))
	if invoiced.Billing == nil || invoiced.Billing.Invoice == nil {
		t.Fatalf("billing after the stamp = %+v, want the invoice", invoiced.Billing)
	}
	errs := refusedFlow(t, manager, unapprovePath, flowBody([]int64{own.Id}, nil))
	if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d has been invoiced", own.Id)) {
		t.Errorf("unapproving an invoiced supplier invoice: %v", errs["entryIds"])
	}
}

// A supplier invoice owes nobody (D1): the company pays the supplier. So it is
// absent from every surface the owes-the-employee rule decides — the
// reimbursement list, the payroll CSV, a payroll run, the unreimbursed
// figures and the reimbursement attention item — while an outlay its owner
// paid, approved beside it, is on every one of them.
func TestSupplierInvoices_OweNobody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager, "expenses:manage", "expenses:approve")
	invoice := createEntry(t, manager, supplierInvoiceBody(nil))
	attachInvoice(t, manager, invoice.Id)
	approvedBy(t, manager, manager, invoice.Id)
	owed := createEntry(t, manager, outlayBody(nil))
	approvedBy(t, manager, manager, owed.Id)

	if got := getEntry(t, manager, invoice.Id); got.OwedToEmployee != 0 || got.Capabilities.CanMarkReimbursed {
		t.Errorf("approved invoice: owed %v canMarkReimbursed %v, want 0 and false", got.OwedToEmployee, got.Capabilities.CanMarkReimbursed)
	}
	var listed []int64
	for _, group := range getReimbursements(t, manager, "").Data {
		listed = append(listed, entryIDsOf(group.Entries)...)
	}
	if !slices.Contains(listed, owed.Id) || slices.Contains(listed, invoice.Id) {
		t.Errorf("reimbursement list = %v, want the outlay %d and never the invoice %d", listed, owed.Id, invoice.Id)
	}
	csv := string(exportCSV(t, manager, "").Body)
	if !strings.Contains(csv, "Kabel og kontakter") || strings.Contains(csv, "Rørleggerarbeid") {
		t.Errorf("payroll CSV:\n%s\nwant the outlay's row and no supplier invoice", csv)
	}
	errs := refusedReimbursement(t, manager, reimbursedPath, reimbursedBody([]int64{invoice.Id}, nil))
	if !mentions(errs["entryIds"], fmt.Sprintf("Expense %d owes the employee nothing", invoice.Id)) {
		t.Errorf("a payroll run over the invoice: %v", errs["entryIds"])
	}
	if stats := getStats(t, manager); len(stats.Unreimbursed) != 1 || stats.Unreimbursed[0].Amount != 1250 {
		t.Errorf("unreimbursed = %+v, want the outlay's 1250 alone", stats.Unreimbursed)
	}
	if summary := getStatsSummary(t, manager, ""); len(summary.MyUnreimbursed) != 1 || summary.MyUnreimbursed[0].Amount != 1250 {
		t.Errorf("myUnreimbursed = %+v, want the outlay's 1250 alone", summary.MyUnreimbursed)
	}
	waiting := attentionOfType(getAttention(t, manager), "reimbursementWaiting")
	if len(waiting) != 1 || waiting[0].Count == nil || *waiting[0].Count != 1 {
		t.Errorf("reimbursementWaiting = %+v, want one item counting the outlay alone", waiting)
	}
}

// D4: a supplier invoice carries no personal data, so its rows are the
// project's financial side's to see — the list, the project's own list, the
// detail and its document — while an employee's outlay on the same project
// keeps today's visibility.
func TestSupplierInvoices_TheProjectsFinancialSideSeesTheRows(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	invoice := createEntry(t, manager, supplierInvoiceBody(nil))
	attachInvoice(t, manager, invoice.Id)
	document := getEntry(t, manager, invoice.Id).Attachments[0].Id
	outlay := createEntry(t, member, outlayBody(map[string]any{"projectId": projectKraftVerket}))
	onProject := fmt.Sprintf("?projectId=%d", projectKraftVerket)

	finance, _ := financeReader(t, h)
	viewer, _ := signInAs(t, h, projectKraftVerket, roleViewer, "projects:view-financials")
	for name, c := range map[string]*modtest.Client{"view-financials with view-all": finance, "view-financials through a role": viewer} {
		if ids := entryIDs(listEntries(t, c, onProject)); !slices.Equal(ids, []int64{invoice.Id}) {
			t.Errorf("%s: the project's list = %v, want the invoice %d alone", name, ids, invoice.Id)
		}
		if got := getEntry(t, c, invoice.Id); got.InvoiceNumber == nil || got.AttachmentCount != 1 {
			t.Errorf("%s: the detail = %+v, want the invoice with its document", name, got)
		}
		if r := downloadReceipt(t, c, document); r.Status != http.StatusOK {
			t.Errorf("%s: the document: status %d", name, r.Status)
		}
		if r := c.Do(http.MethodGet, entryPath(outlay.Id), nil); r.Status != http.StatusNotFound {
			t.Errorf("%s: a colleague's outlay: status %d, want the bare 404", name, r.Status)
		}
	}
	// Seeing is not changing: the finance reader is not the invoice's writer.
	forbidden(t, finance, http.MethodDelete, entryPath(invoice.Id), nil)

	plainViewer, _ := signInAs(t, h, projectKraftVerket, roleViewer)
	if ids := entryIDs(listEntries(t, plainViewer, onProject)); len(ids) != 0 {
		t.Errorf("a viewer without financial rights lists %v, want nothing", ids)
	}
	if r := plainViewer.Do(http.MethodGet, entryPath(invoice.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("a viewer without financial rights reads the invoice: status %d, want 404", r.Status)
	}
}

func TestSupplierInvoices_ThePickerOffersTheProjectsTheCallerMayRecordOneOn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	addAbandonedProject(h)
	c, id := signIn(t, h, "projects:view-financials")
	for _, project := range []int32{projectKraftVerket, projectCompleted, projectAbandoned} {
		h.projects.addRole(project, id, roleViewer)
	}

	r := c.Do(http.MethodGet, projectOptionsPath+"?kind=supplier_invoice", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("the supplier invoice picker: status %d body %s", r.Status, r.Body)
	}
	var options []projectOptionJSON
	r.JSON(&options)
	var ids []int32
	for _, option := range options {
		ids = append(ids, option.Id)
	}
	if !slices.Equal(ids, []int32{projectKraftVerket, projectCompleted}) {
		t.Errorf("supplier invoice picker = %v, want %d and %d — never the cancelled %d", ids, projectKraftVerket, projectCompleted, projectAbandoned)
	}
	if len(options) > 0 && (len(options[0].BillingLines) != 1 || options[0].BillingLines[0].Id != lineFixed) {
		t.Errorf("1001's lines = %+v, want the one active line", options[0].BillingLines)
	}
	// The bookable-projects picker offers a viewer nothing: a viewer logs no time.
	if plain := listProjectOptions(t, c); len(plain) != 0 {
		t.Errorf("the ordinary picker = %+v, want nothing for a viewer", plain)
	}
	member, _ := signInAs(t, h, projectKraftVerket, roleMember)
	r = member.Do(http.MethodGet, projectOptionsPath+"?kind=supplier_invoice", nil)
	var none []projectOptionJSON
	r.JSON(&none)
	if r.Status != http.StatusOK || len(none) != 0 {
		t.Errorf("a member without financial rights: status %d options %+v, want none", r.Status, none)
	}
	for query, want := range map[string]string{
		"?kind=per_diem": "is not a kind this picker narrows by",
		"?kind=supplier_invoice&userId=" + uuid.NewString(): "the right to record one is the recorder's",
	} {
		errs := refused(t, c, http.MethodGet, projectOptionsPath+query, nil, invalidQueryTitle)
		if !mentions(errs["kind"], want) {
			t.Errorf("%s: kind = %v, want %q", query, errs["kind"], want)
		}
	}
}

// The approval queue counts a supplier invoice with no document the way it
// counts an outlay with no receipt. The submit refuses one now, so the row is
// seeded — one submitted before the rule, or written past it, must still be
// flagged to whoever checks it.
func TestSupplierInvoices_TheQueueCountsOneWithNoDocument(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	approver, _ := signIn(t, h, "expenses:approve")
	_, owner := signIn(t, h)
	h.Exec(t, `INSERT INTO expenses.entries
	    (user_id, created_by_user_id, kind, entry_date, description, category_id, supplier,
	     supplier_invoice_number, paid_by, currency, gross_amount, project_id, status,
	     submitted_at, created_at, updated_at)
	    VALUES ($1, $1, 'supplier_invoice', DATE '2026-03-10', 'Rørleggerarbeid', $2, 'Rør & Varme AS',
	            'F-1', 'company', 'NOK', 1000.00, $3, 'submitted', now(), now(), now())`,
		owner, int32(subcontractorCategory), int32(projectKraftVerket))
	page := getApprovals(t, approver, "")
	if len(page.Data) != 1 || page.Data[0].ReceiptsMissing != 1 {
		t.Errorf("approval queue = %+v, want one group missing one document", page.Data)
	}
}

func TestSupplierInvoices_TheSummarySaysWhoMayRecordOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	addAbandonedProject(h)
	finance, _ := financeReader(t, h)

	summary := getProjectSummary(t, finance, projectKraftVerket)
	if summary.Capabilities.CanRecord {
		t.Error("canRecord is true for a reader on no team")
	}
	if c := summary.Capabilities.CanRecordSupplierInvoice; c == nil || !*c {
		t.Errorf("canRecordSupplierInvoice = %v, want true: financial rights on an open project", c)
	}
	if p := summary.Project; p == nil || p.Id != projectKraftVerket || p.Code != projectKraftVerketCode ||
		len(p.BillingLines) != 1 || p.BillingLines[0].Id != lineFixed {
		t.Errorf("project = %+v, want 1001 with its one active line", summary.Project)
	}
	if c := getProjectSummary(t, finance, projectCompleted).Capabilities.CanRecordSupplierInvoice; c == nil || !*c {
		t.Errorf("a completed project: canRecordSupplierInvoice = %v, want true", c)
	}
	abandoned := getProjectSummary(t, finance, projectAbandoned)
	if c := abandoned.Capabilities.CanRecordSupplierInvoice; c == nil || *c || abandoned.Project != nil {
		t.Errorf("a cancelled project: canRecordSupplierInvoice %v project %+v, want false and no project", c, abandoned.Project)
	}
}
