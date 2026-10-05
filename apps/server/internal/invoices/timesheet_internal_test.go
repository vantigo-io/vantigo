package invoices

import (
	"bytes"
	"fmt"
	"math/big"
	"regexp"
	"testing"
	"time"
)

// pageObject is one page object of a PDF gofpdf wrote: "/Type /Page", not
// "/Type /Pages".
var pageObject = regexp.MustCompile(`/Type /Page[^s]`)

// A timesheet of a few hundred rows renders without error over several
// pages after the invoice's own: maroto's AddRows breaks it where a page
// ends, and every row is in the model.
func TestPDF_TheTimesheetPaginates(t *testing.T) {
	t.Parallel()
	plain, err := renderPDF(buildPDFModel(anInvoice()))
	if err != nil {
		t.Fatalf("render the invoice alone: %v", err)
	}
	d := anInvoice()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := range 300 {
		d.timesheet = append(d.timesheet, pdfTimesheetRow{
			date: day.AddDate(0, 0, i%30), person: fmt.Sprintf("P%d", i%7), workType: "Rådgivning",
			description: "Prosjekt 41", hours: big.NewRat(int64(25+i%400), 100),
		})
	}
	m := buildPDFModel(d)
	if m.timesheet == nil || len(m.timesheet.rows) != 300 || len(m.timesheet.totals) != 8 {
		t.Fatalf("the model's timesheet = %+v; want 300 rows and totals for seven people and the whole", m.timesheet)
	}
	body, err := renderPDF(m)
	if err != nil {
		t.Fatalf("render with the timesheet: %v", err)
	}
	alone, with := len(pageObject.FindAll(plain, -1)), len(pageObject.FindAll(body, -1))
	if alone != 1 || with < alone+4 || !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Errorf("pages = %d alone and %d with 300 rows, want 1 and at least five", alone, with)
	}
}
