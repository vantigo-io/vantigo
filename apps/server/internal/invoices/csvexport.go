package invoices

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is GET /invoices/export.csv (payments and delivery design D5):
// the accountant's file of a period's issued documents — the journal's
// selection — one row per document and VAT row, in csvfile.go's form. A
// credit note is stored positive and signed negative here, in every amount,
// the journal's rule. Every amount is the stored numeric as exact text; the
// one computed column, Base NOK, is big.Rat arithmetic rounded to øre.

// exportHeader is D5's header row, fixed and English, in its order.
var exportHeader = []string{
	"Number", "Kind", "Issue date", "Delivery", "Due", "Customer number", "Buyer", "Buyer org no",
	"Currency", "SAF-T code", "Rate", "Base", "VAT", "Base NOK", "VAT NOK", "Credits number",
}

// tooManyRowsToExport is the cap's 400 (D5): a bare problem asking for a
// narrower period — counted before a byte is written, never a file cut short.
func tooManyRowsToExport() apicommon.ProblemDetails {
	return apicommon.Problem("Too many rows to export", fmt.Sprintf(
		"This export would hold more than %d rows, which is more than one file should; narrow the period.",
		invoicesFileMaxRows))
}

// csvDownload writes the file: the generated response hard-codes a bare
// text/csv and names no file, and this answer has to say which encoding it is
// in, what to call it and that nobody may cache it — the customers module's
// csvDownload.
type csvDownload struct {
	body     []byte
	fileName string
}

func (d csvDownload) VisitGetInvoicesExportCsvResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	// A period's invoices name who the business sold to and for how much. They
	// belong in nobody's cache, and least of all in a shared one.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}

// GetInvoicesExportCsv Export the issued documents as CSV
// (GET /api/v1/invoices/export.csv)
func (s *server) GetInvoicesExportCsv(ctx context.Context, req gen.GetInvoicesExportCsvRequestObject) (gen.GetInvoicesExportCsvResponseObject, error) {
	p := req.Params
	if p.From.After(p.To.Time) {
		return gen.GetInvoicesExportCsv400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, "'from' must be on or before 'to'.")), nil
	}
	from, to := utcDay(p.From.Time), utcDay(p.To.Time)

	// One row more than the cap, so "the whole period" and "more than a file
	// may hold" are told apart without a count of their own.
	rows, err := store.New(s.deps.Pool).ExportRows(ctx, store.ExportRowsParams{
		IssuedFrom: pgDate(from), IssuedTo: pgDate(to), Limit: invoicesFileMaxRows + 1,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: read the export: %w", err)
	}
	if len(rows) > invoicesFileMaxRows {
		return gen.GetInvoicesExportCsv400ApplicationProblemPlusJSONResponse(tooManyRowsToExport()), nil
	}
	body, err := invoicesCSV(rows)
	if err != nil {
		return nil, err
	}
	return csvDownload{
		body:     body,
		fileName: fmt.Sprintf("invoices-%s-%s.csv", from.Format(time.DateOnly), to.Format(time.DateOnly)),
	}, nil
}

// invoicesCSV is the file: the byte order mark, the header, then one row per
// document and VAT row. It is built whole in memory — about a megabyte at the
// cap, and Content-Length needs the whole body anyway.
func invoicesCSV(rows []store.ExportRowsRow) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	header := make([]csvValue, len(exportHeader))
	for i, name := range exportHeader {
		header[i] = csvValue{text: name, guard: true}
	}
	writeCSVRow(&b, header)
	for _, r := range rows {
		cells, err := exportCells(r)
		if err != nil {
			return nil, err
		}
		writeCSVRow(&b, cells)
	}
	return b.Bytes(), nil
}

// exportCells is one row's cells in the header's order (D5). The text
// columns are guarded; the number, the dates, the rate and the amounts never
// are. A value there is none of is the empty cell.
func exportCells(r store.ExportRowsRow) ([]csvValue, error) {
	credit := r.Kind == kindCreditNote
	rate, err := csvDecimal(r.RatePercent, false)
	if err != nil {
		return nil, err
	}
	base, err := csvDecimal(r.TaxableAmount, credit)
	if err != nil {
		return nil, err
	}
	vat, err := csvDecimal(r.VatAmount, credit)
	if err != nil {
		return nil, err
	}
	vatNOK, err := csvDecimal(r.VatAmountNok, credit)
	if err != nil {
		return nil, err
	}
	// Base NOK is the one computed column: the base at the document's
	// exchange rate, at øre.
	taxable, err := ratFromNumeric(r.TaxableAmount)
	if err != nil {
		return nil, err
	}
	exchangeRate, err := ratFromNumeric(r.ExchangeRate)
	if err != nil {
		return nil, err
	}
	baseNOK := csvAmount(new(big.Rat).Mul(taxable, exchangeRate), credit)

	text := func(s string) csvValue { return csvValue{text: s, guard: true} }
	plain := func(s string) csvValue { return csvValue{text: s} }
	return []csvValue{
		plain(csvInt(r.Number)),
		text(r.Kind),
		plain(csvDate(r.IssueDate)),
		text(csvDelivery(r)),
		plain(csvDate(r.DueDate)),
		text(csvInt(r.BuyerCustomerNumber)),
		text(csvText(r.BuyerName)),
		text(csvText(r.BuyerOrganisationNumber)),
		text(r.Currency),
		text(r.SafTCode),
		plain(rate), plain(base), plain(vat), plain(baseNOK), plain(vatNOK),
		text(csvInt(r.CreditsNumber)),
	}, nil
}

// csvDelivery is a document's delivery as one cell: the day, or the period
// in ISO 8601's interval notation, or nothing.
func csvDelivery(r store.ExportRowsRow) string {
	if r.DeliveryDate.Valid {
		return csvDate(r.DeliveryDate)
	}
	if r.DeliveryFrom.Valid && r.DeliveryTo.Valid {
		return csvDate(r.DeliveryFrom) + "/" + csvDate(r.DeliveryTo)
	}
	return ""
}

// csvDate is a date column as YYYY-MM-DD, the empty cell for NULL.
func csvDate(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format(time.DateOnly)
}

// csvInt is a nullable number as its digits, the empty cell for NULL.
func csvInt(n *int64) string {
	if n == nil {
		return ""
	}
	return strconv.FormatInt(*n, 10)
}

// csvText is a nullable text column, the empty cell for NULL.
func csvText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
