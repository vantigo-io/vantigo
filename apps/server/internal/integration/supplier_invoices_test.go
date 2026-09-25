package integration_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// subcontractorCategory is the second category 00012 seeds, after
// materialsCategory.
const subcontractorCategory = 1002

// TestSupplierInvoices_AFinanceReaderRecordsOneOnACompletedProject is the
// supplier invoices design end to end (D1–D4): the real expenses module takes
// a supplier invoice from somebody who holds the project's financial rights
// and no role on it — judged through Compose's real project directory, on a
// project already completed, which CanLogTime would refuse — and the real
// economy reads it back through the real provider as the expenses block's
// supplier-invoice sub-figure, the net as its cost and, at the default 0 %
// markup, as what it bills:
//
//	12 500 gross − 2 500 VAT = 10 000 cost, 10 000 billed
func TestSupplierInvoices_AFinanceReaderRecordsOneOnACompletedProject(t *testing.T) {
	t.Parallel()
	// newInstallation composes no object store, and the supplier's invoice
	// goes through the receipts door, so this installation is composed with
	// the real filesystem store, rooted in a directory it creates itself
	// (restrictively permissioned — an existing temp directory would not be).
	store, err := storage.NewFS(filepath.Join(t.TempDir(), "objects"), false, false)
	if err != nil {
		t.Fatalf("open a receipt store: %v", err)
	}
	h := modtest.New(t,
		modtest.WithRecorder(recorder),
		modtest.WithDirectory(fakeCustomers{}),
		modtest.WithModule(moduleNamed(t, modProjects)),
		modtest.WithModule(moduleNamed(t, modExpenses)),
		modtest.WithObjectStore(store),
	)
	admin, _ := signInAdmin(t, h)
	project := createProject(t, admin)
	// The work is done; the subcontractor's invoice arrives afterwards.
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("%s/%d/status", projectsPath, project.Id),
		map[string]any{"status": "completed"}, nil)

	finance, financeID := h.SignInUser(t, "projects:access", "projects:view-all", "projects:view-financials", "expenses:access")
	var invoice struct {
		Id             int64   `json:"id"`
		Kind           string  `json:"kind"`
		Status         string  `json:"status"`
		OwedToEmployee float64 `json:"owedToEmployee"`
		Owner          struct {
			UserId string `json:"userId"`
		} `json:"owner"`
	}
	okJSON(t, finance, http.MethodPost, expensesEntries, map[string]any{
		"kind": "supplier_invoice", "entryDate": "2026-09-22", "description": "Rørleggerarbeid, uke 38",
		"categoryId": subcontractorCategory, "supplier": "Rør & Varme AS", "invoiceNumber": "F-20260922",
		"dueDate": "2026-10-22", "currency": "NOK", "grossAmount": 12500, "vatAmount": 2500,
		"projectId": project.Id, "billable": true,
	}, &invoice)
	if invoice.Kind != "supplier_invoice" || invoice.Status != "draft" || invoice.OwedToEmployee != 0 ||
		invoice.Owner.UserId != financeID.String() {
		t.Fatalf("recorded = %+v, want the finance reader's draft supplier invoice, owed to nobody", invoice)
	}

	type splitBucket struct {
		Count  int32   `json:"count"`
		Cost   float64 `json:"cost"`
		Amount float64 `json:"amount"`
	}
	type economy struct {
		Expenses *struct {
			TotalCost        float64 `json:"totalCost"`
			SupplierInvoices *struct {
				Approved splitBucket `json:"approved"`
				Draft    splitBucket `json:"draft"`
				Total    splitBucket `json:"total"`
			} `json:"supplierInvoices"`
		} `json:"expenses"`
	}
	read := func() economy {
		var e economy
		okJSON(t, finance, http.MethodGet, fmt.Sprintf(projectEconomyPath, project.Id), nil, &e)
		if e.Expenses == nil || e.Expenses.SupplierInvoices == nil {
			t.Fatalf("economy expenses = %+v, want the supplier invoice sub-figure", e.Expenses)
		}
		return e
	}
	want := splitBucket{Count: 1, Cost: 10000, Amount: 10000}
	if got := read(); got.Expenses.SupplierInvoices.Draft != want || got.Expenses.SupplierInvoices.Total != want ||
		got.Expenses.TotalCost != 10000 {
		t.Errorf("recorded: supplierInvoices = %+v, totalCost %v, want the invoice in draft costing 10000",
			got.Expenses.SupplierInvoices, got.Expenses.TotalCost)
	}

	// The supplier's invoice goes through the receipts door, the submit
	// accepts it, and an approver's approval moves it in the economy too.
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="faktura.pdf"`},
		"Content-Type":        {"application/pdf"},
	})
	if err != nil {
		t.Fatalf("build the upload: %v", err)
	}
	if _, err := part.Write([]byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n")); err != nil {
		t.Fatalf("write the upload: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the upload: %v", err)
	}
	r := finance.Do(http.MethodPost, fmt.Sprintf("%s/%d/attachments", expensesEntries, invoice.Id), nil,
		modtest.RawBody(w.FormDataContentType(), form.Bytes()))
	if r.Status != http.StatusCreated {
		t.Fatalf("attach the invoice: status %d body %s, want 201", r.Status, r.Body)
	}
	okJSON(t, finance, http.MethodPost, expensesSubmit, map[string]any{"entryIds": []int64{invoice.Id}}, nil)
	okJSON(t, admin, http.MethodPost, expensesApprove, map[string]any{"entryIds": []int64{invoice.Id}}, nil)

	if got := read(); got.Expenses.SupplierInvoices.Approved != want || got.Expenses.SupplierInvoices.Draft != (splitBucket{}) {
		t.Errorf("approved: supplierInvoices = %+v, want the invoice in approved and none in draft", got.Expenses.SupplierInvoices)
	}
}
