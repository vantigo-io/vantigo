package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/kid"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is D7's store-once path and the two PDF operations. A PDF that was
// never stored was never downloaded, because every download goes through the
// stored object; once a document's hash is set it is never rendered again, and
// the module never deletes an object in its scope. No lock is held around a
// store call: the render reads the document's own committed rows, the object
// is put, and only then is the row told where it is.

// maxStoredPDF is how much of a stored object a download reads (D7).
const maxStoredPDF = 20 << 20

const codeInvoiceDraft = "invoice_draft"

// pdfDocumentOf is an issued document as its PDF prints it: its own rows and
// snapshots, never the settings, the directory or the VAT tables. Its KID is
// re-verified against the algorithm stored beside it — never the agreement
// in force (EHF and KID design D3) — and one that does not verify is an
// error, a 500, never a silent reprint.
func pdfDocumentOf(inv store.InvoicesInvoice, lines []store.InvoicesLine, sums []store.InvoicesVatSummary, original *store.InvoicesInvoice, deducted []deductedRef) (pdfDocument, error) {
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	d := pdfDocument{
		kind: inv.Kind, language: str(inv.BuyerLanguage), currency: inv.Currency, number: inv.Number, issueDate: inv.IssueDate.Time,
		paymentTermsDays: inv.PaymentTermsDays, yourReference: inv.YourReference, ourReference: inv.OurReference,
		orderRef: inv.OrderReference, projectRef: str(inv.ProjectReference), note: inv.Note, footer: str(inv.SellerFooterText),
		seller: sellerParty(inv), buyer: buyerParty(inv),
		bankAccount: str(inv.SellerBankAccount), iban: str(inv.SellerIban), bic: str(inv.SellerBic),
	}
	if inv.Kid != nil || inv.KidAlgorithm != nil {
		if inv.Kid == nil || inv.KidAlgorithm == nil || inv.Number == nil || !kid.Verify(*inv.Kid, *inv.KidAlgorithm, *inv.Number) {
			return pdfDocument{}, fmt.Errorf("invoices: document %d's stored KID does not verify against its stored algorithm", inv.ID)
		}
		d.kid = *inv.Kid
	}
	if inv.IssuedAt != nil {
		d.created = *inv.IssuedAt
	}
	if inv.DueDate.Valid {
		d.dueDate = &inv.DueDate.Time
	}
	setDelivery(&d, inv)
	for _, l := range lines {
		pl, err := pdfLineOf(l)
		if err != nil {
			return pdfDocument{}, err
		}
		if l.VatRatePercent.Valid {
			if pl.rate, err = ratFromNumeric(l.VatRatePercent); err != nil {
				return pdfDocument{}, err
			}
		}
		d.lines = append(d.lines, pl)
	}
	for _, s := range sums {
		row := vatSummary{category: s.VatCategory, safT: s.SafTCode, reason: s.ExemptionReason}
		var err error
		if row.rate, err = ratFromNumeric(s.RatePercent); err != nil {
			return pdfDocument{}, err
		}
		if row.taxable, err = ratFromNumeric(s.TaxableAmount); err != nil {
			return pdfDocument{}, err
		}
		if row.vat, err = ratFromNumeric(s.VatAmount); err != nil {
			return pdfDocument{}, err
		}
		d.summaries = append(d.summaries, row)
	}
	var err error
	if d.totals.net, err = ratFromNumeric(inv.NetTotal); err != nil {
		return pdfDocument{}, err
	}
	if d.totals.vat, err = ratFromNumeric(inv.VatTotal); err != nil {
		return pdfDocument{}, err
	}
	if d.totals.gross, err = ratFromNumeric(inv.GrossTotal); err != nil {
		return pdfDocument{}, err
	}
	if original != nil && original.Number != nil {
		d.credits = &struct {
			number    int64
			issueDate time.Time
		}{*original.Number, original.IssueDate.Time}
	}
	if inv.Kind == kindInvoice {
		d.deducted = deducted
	}
	return d, nil
}

// sellerParty and buyerParty are an issued document's snapshots as its PDF —
// and its reminder letters — print them.
func sellerParty(inv store.InvoicesInvoice) pdfParty {
	return pdfParty{
		name: deref(inv.SellerLegalName), line1: deref(inv.SellerAddressLine1), line2: deref(inv.SellerAddressLine2),
		postalCode: deref(inv.SellerPostalCode), city: deref(inv.SellerCity), country: deref(inv.SellerCountry),
		organisationNumber: deref(inv.SellerOrganisationNumber), email: deref(inv.SellerEmail),
		vatRegistered:      inv.SellerVatRegistered != nil && *inv.SellerVatRegistered,
		foretaksregisteret: inv.SellerInForetaksregisteret != nil && *inv.SellerInForetaksregisteret,
	}
}

func buyerParty(inv store.InvoicesInvoice) pdfParty {
	return pdfParty{
		name: deref(inv.BuyerName), line1: deref(inv.BuyerAddressLine1), line2: deref(inv.BuyerAddressLine2),
		postalCode: deref(inv.BuyerPostalCode), city: deref(inv.BuyerCity), country: deref(inv.BuyerCountry),
		organisationNumber: deref(inv.BuyerOrganisationNumber), foreignID: deref(inv.BuyerForeignID),
	}
}

// setDelivery copies a document's delivery and place of delivery.
func setDelivery(d *pdfDocument, inv store.InvoicesInvoice) {
	if inv.DeliveryDate.Valid {
		d.deliveryDate = &inv.DeliveryDate.Time
	}
	if inv.DeliveryFrom.Valid && inv.DeliveryTo.Valid {
		d.deliveryFrom, d.deliveryTo = &inv.DeliveryFrom.Time, &inv.DeliveryTo.Time
	}
	if inv.DeliveryAddressLine1 != nil {
		str := func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		}
		d.deliveryPlace = &pdfParty{
			line1: *inv.DeliveryAddressLine1, line2: str(inv.DeliveryAddressLine2), postalCode: str(inv.DeliveryPostalCode),
			city: str(inv.DeliveryCity), country: str(inv.DeliveryCountry),
		}
	}
}

// pdfLineOf is one stored line's printed amounts; the rate is the caller's.
func pdfLineOf(l store.InvoicesLine) (pdfLine, error) {
	pl := pdfLine{description: l.Description, unit: l.Unit, rate: new(big.Rat)}
	var err error
	if pl.quantity, err = ratFromNumeric(l.Quantity); err != nil {
		return pdfLine{}, err
	}
	if pl.unitPrice, err = ratFromNumeric(l.UnitPrice); err != nil {
		return pdfLine{}, err
	}
	if pl.discount, err = ratFromNumeric(l.DiscountPercent); err != nil {
		return pdfLine{}, err
	}
	if pl.net, err = ratFromNumeric(l.LineNet); err != nil {
		return pdfLine{}, err
	}
	return pl, nil
}

// renderIssued renders an issued document from its own rows.
func renderIssued(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) ([]byte, error) {
	lines, err := q.Lines(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's lines: %w", inv.ID, err)
	}
	sums, err := q.VatSummaries(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's VAT: %w", inv.ID, err)
	}
	var original *store.InvoicesInvoice
	if inv.CreditsInvoiceID != nil {
		o, err := q.GetInvoice(ctx, *inv.CreditsInvoiceID)
		if err != nil {
			return nil, fmt.Errorf("invoices: read credit note %d's original: %w", inv.ID, err)
		}
		original = &o
	}
	deducted, err := deductedRefsOf(ctx, q, inv, lines)
	if err != nil {
		return nil, err
	}
	doc, err := pdfDocumentOf(inv, lines, sums, original, deducted)
	if err != nil {
		return nil, fmt.Errorf("invoices: render document %d: %w", inv.ID, err)
	}
	if doc.timesheet, err = timesheetOf(ctx, q, inv); err != nil {
		return nil, err
	}
	m := buildPDFModel(doc)
	if pdfModelBuilt != nil {
		pdfModelBuilt(inv.ID, m)
	}
	body, err := renderPDF(m)
	if err != nil {
		return nil, fmt.Errorf("invoices: render document %d: %w", inv.ID, err)
	}
	return body, nil
}

// timesheetOf is a document's timesheet as its PDF prints it (invoices work
// design D5): its own stored rows, in order, when it carries one — part of
// the one PDF, so the store-once key and hash cover it as they cover every
// other word, and the EHF attaches it with the rest.
func timesheetOf(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) ([]pdfTimesheetRow, error) {
	if !inv.Timesheet {
		return nil, nil
	}
	rows, err := q.TimesheetRowsOf(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's timesheet: %w", inv.ID, err)
	}
	out := make([]pdfTimesheetRow, 0, len(rows))
	for _, r := range rows {
		hours, err := ratFromNumeric(r.Hours)
		if err != nil {
			return nil, err
		}
		work := ""
		if r.WorkType != nil {
			work = *r.WorkType
		}
		out = append(out, pdfTimesheetRow{date: r.EntryDate.Time, person: r.PersonLabel, workType: work, description: r.Description, hours: hours})
	}
	return out, nil
}

// pdfModelBuilt, when a test sets it (export_test.go), is told every model a
// PDF is laid out from — an issued document's and a preview's — so what a PDF
// says is asserted on the model, never on the renderer's content stream. Nil
// outside tests.
var pdfModelBuilt func(invoiceID int64, m pdfModel)

// storedPDF is where an issued document's PDF is, and, when this call stored
// it, the bytes it stored.
type storedPDF struct {
	key, sha256 string
	body        []byte
}

// errObjectStore marks a store-once failure that is the object store's: a
// download answers it with a 503 to retry. Anything else storeOnce returns —
// a render that fails, a database read or write — is a 500: retrying does not
// mend a document that cannot be rendered.
var errObjectStore = errors.New("invoices: the object store failed")

// afterPDFRender, when a test sets it (export_test.go), is called on the
// store-once path with the rendered bytes and answers the bytes to store. The
// render is reproducible, so racing downloads would store identical bytes and
// a loser streaming its own render would look like one streaming the winner's;
// the race test makes each render differ, and holds every racer here until all
// of them have rendered. Nil outside tests.
var afterPDFRender func(ctx context.Context, body []byte) []byte

// storeOnce is D7's store-once path: render, hash, key the object by the
// hash, put it unless it is there, and record it on the row only while the
// row has no hash. A loser of a race sees no row updated, re-reads the row and
// answers the winner's key without bytes; its own object is an orphan nothing
// ever serves.
func (s *server) storeOnce(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (storedPDF, error) {
	body, err := renderIssued(ctx, q, inv)
	if err != nil {
		return storedPDF{}, err
	}
	if afterPDFRender != nil {
		body = afterPDFRender(ctx, body)
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	key := fmt.Sprintf("documents/%d/%d-%s.pdf", inv.ID, *inv.Number, hash)
	exists, err := s.objectExists(ctx, key)
	if err != nil {
		return storedPDF{}, fmt.Errorf("%w: %w", errObjectStore, err)
	}
	if !exists {
		if err := s.objectPut(ctx, key, bytes.NewReader(body), "application/pdf"); err != nil {
			return storedPDF{}, fmt.Errorf("%w: %w", errObjectStore, err)
		}
	}
	n, err := q.SetDocumentPDF(ctx, store.SetDocumentPDFParams{ID: inv.ID, PdfObjectKey: &key, PdfSha256: &hash})
	if err != nil {
		return storedPDF{}, fmt.Errorf("invoices: record document %d's PDF: %w", inv.ID, err)
	}
	if n == 1 {
		return storedPDF{key: key, sha256: hash, body: body}, nil
	}
	winner, err := q.GetInvoice(ctx, inv.ID)
	if err != nil {
		return storedPDF{}, fmt.Errorf("invoices: re-read document %d: %w", inv.ID, err)
	}
	if winner.PdfObjectKey == nil || winner.PdfSha256 == nil {
		return storedPDF{}, fmt.Errorf("invoices: document %d lost its PDF race to nobody", inv.ID)
	}
	return storedPDF{key: *winner.PdfObjectKey, sha256: *winner.PdfSha256}, nil
}

// storeAfterIssue is the store-once path right after an issue commits. A
// failure is logged at warn and never fails the issue: the number is
// committed, and the next download stores the PDF (D6).
func (s *server) storeAfterIssue(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) store.InvoicesInvoice {
	if !s.storageConfigured {
		// Nothing to store into. The issue refuses before allocating a number
		// without a store (D6), so this is only a guard: no warning that "the
		// next download stores it", which a download answering 503 never does.
		s.deps.Logger.DebugContext(ctx, "invoices: no object store; the issued document's PDF is not stored", "invoice_id", inv.ID)
		return inv
	}
	if _, err := s.storeOnce(ctx, q, inv); err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: the PDF of an issued document could not be stored; the next download stores it",
			"invoice_id", inv.ID, "number", *inv.Number, "error", err.Error())
		return inv
	}
	stored, err := q.GetInvoice(ctx, inv.ID)
	if err != nil {
		return inv
	}
	return stored
}

// fileName is the download's name in the document's language (D7).
func fileName(inv store.InvoicesInvoice) string {
	english := inv.BuyerLanguage != nil && *inv.BuyerLanguage == "en"
	switch {
	case inv.Kind == kindCreditNote && english:
		return fmt.Sprintf("credit-note-%d.pdf", *inv.Number)
	case inv.Kind == kindCreditNote:
		return fmt.Sprintf("kreditnota-%d.pdf", *inv.Number)
	case english:
		return fmt.Sprintf("invoice-%d.pdf", *inv.Number)
	}
	return fmt.Sprintf("faktura-%d.pdf", *inv.Number)
}

// pdfProblem is why loadStoredPDF has no bytes to answer, in the terms both
// its callers answer with: unavailable is the object store's 503 to retry
// (storage_unavailable), broken a stored object gone or altered — a 500 the
// operator has been told of in the log — and err anything else, a render or
// a database read or write, which the server answers with a 500.
type pdfProblem struct {
	unavailable *gen.InvoicesConflictProblem
	broken      bool
	err         error
}

// storageUnavailable is the 503 a download or a send answers when the store
// is not configured or cannot be read.
func storageUnavailable(detail string) gen.InvoicesConflictProblem {
	c := conflict(codeStorageUnavailable, storageUnavailableTitle, detail)
	c.Status = ptr(int32(http.StatusServiceUnavailable))
	return c
}

// unavailable is a pdfProblem the object store caused.
func unavailable(detail string) *pdfProblem {
	return &pdfProblem{unavailable: ptr(storageUnavailable(detail))}
}

// storedDocumentBroken is the 500 a download or a send answers when the
// stored object is gone or its bytes no longer match its hash — an operator
// problem, logged at error and never papered over by rendering again.
func storedDocumentBroken() apicommon.ProblemDetails {
	return apicommon.ProblemStatus("The stored document is damaged",
		"The document's stored PDF is missing or does not match what was stored. It is not rendered again; the operator has been told.",
		http.StatusInternalServerError)
}

// storeOnceFailed is the problem of a store-once path that failed: the
// object store's failure is a 503 to retry, and anything else — a render, a
// database read or write — an error the server answers with a 500.
func storeOnceFailed(err error) *pdfProblem {
	if errors.Is(err, errObjectStore) {
		return unavailable("The document store could not be reached. Try again.")
	}
	return &pdfProblem{err: err}
}

// loadStoredPDF is an issued document's PDF as the object store holds it
// (D7), for the download and the send alike: stored once when the row has no
// hash yet, and otherwise read whole and verified against the hash, so
// neither ever hands over bytes the store does not hold. Its answer carries
// the bytes; its problem says why there are none.
func (s *server) loadStoredPDF(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (storedPDF, *pdfProblem) {
	if !s.storageConfigured {
		return storedPDF{}, unavailable("This installation has no object store.")
	}
	found := storedPDF{}
	// Stored means both columns (ck_invoices_pdf keeps them together); either
	// one missing is "not stored", never a dereference of the other.
	if inv.PdfSha256 == nil || inv.PdfObjectKey == nil {
		var err error
		if found, err = s.storeOnce(ctx, q, inv); err != nil {
			s.deps.Logger.ErrorContext(ctx, "invoices: a PDF could not be stored", "invoice_id", inv.ID, "error", err.Error())
			return storedPDF{}, storeOnceFailed(err)
		}
	} else {
		found = storedPDF{key: *inv.PdfObjectKey, sha256: *inv.PdfSha256}
	}
	if found.body != nil {
		return found, nil
	}
	rc, err := s.objectGet(ctx, found.key)
	switch {
	case errors.Is(err, storage.ErrNotExist):
		s.deps.Logger.ErrorContext(ctx, "invoices: an issued document's stored PDF is gone", "invoice_id", inv.ID, "key", found.key)
		return storedPDF{}, &pdfProblem{broken: true}
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "invoices: the document store could not be read", "invoice_id", inv.ID, "error", err.Error())
		return storedPDF{}, unavailable("The document store could not be read. Try again.")
	}
	body, err := io.ReadAll(io.LimitReader(rc, maxStoredPDF+1))
	_ = rc.Close()
	if err != nil {
		return storedPDF{}, unavailable("The document store could not be read. Try again.")
	}
	sum := sha256.Sum256(body)
	if len(body) > maxStoredPDF || hex.EncodeToString(sum[:]) != found.sha256 {
		s.deps.Logger.ErrorContext(ctx, "invoices: an issued document's stored PDF does not match its hash",
			"invoice_id", inv.ID, "key", found.key)
		return storedPDF{}, &pdfProblem{broken: true}
	}
	found.body = body
	return found, nil
}

// pdfDownload and pdfPreview set the headers a PDF answers with before the
// generated response writes the content type and the status: never cached,
// never sniffed, and named. A document is as private as the invoicing it
// belongs to.
type pdfDownload struct {
	body        gen.GetInvoicesByIdPdf200ApplicationpdfResponse
	disposition string
}

func (r pdfDownload) VisitGetInvoicesByIdPdfResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetInvoicesByIdPdfResponse(w)
}

type pdfPreview struct {
	body        gen.GetInvoicesByIdPreviewPdf200ApplicationpdfResponse
	disposition string
}

func (r pdfPreview) VisitGetInvoicesByIdPreviewPdfResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetInvoicesByIdPreviewPdfResponse(w)
}

func pdfHeaders(w http.ResponseWriter, disposition string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", disposition)
}

// GetInvoicesByIdPdf Download an issued document's PDF
// (GET /api/v1/invoices/{id}/pdf)
func (s *server) GetInvoicesByIdPdf(ctx context.Context, req gen.GetInvoicesByIdPdfRequestObject) (gen.GetInvoicesByIdPdfResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesByIdPdf404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if inv.Status != statusIssued {
		return gen.GetInvoicesByIdPdf409ApplicationProblemPlusJSONResponse(conflict(codeInvoiceDraft, "The document is a draft",
			"A draft has no document to download yet; preview it instead.")), nil
	}
	found, problem := s.loadStoredPDF(ctx, q, inv)
	switch {
	case problem == nil:
	case problem.unavailable != nil:
		return gen.GetInvoicesByIdPdf503ApplicationProblemPlusJSONResponse(*problem.unavailable), nil
	case problem.broken:
		return gen.GetInvoicesByIdPdf500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
	default:
		return nil, problem.err
	}
	body := found.body
	return pdfDownload{
		body:        gen.GetInvoicesByIdPdf200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", fileName(inv)),
	}, nil
}

// GetInvoicesByIdPreviewPdf Preview a draft as PDF
// (GET /api/v1/invoices/{id}/preview.pdf)
//
// A draft as it would be if issued today, rendered on demand and never
// stored: the current settings (even incomplete), the customer's billing
// profile as it is now for an invoice draft and the copied snapshot for a
// credit-note draft, today's rates for an invoice draft and the original
// lines' snapshot rates for a credit-note draft, the watermark and no number.
func (s *server) GetInvoicesByIdPreviewPdf(ctx context.Context, req gen.GetInvoicesByIdPreviewPdfRequestObject) (gen.GetInvoicesByIdPreviewPdfResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesByIdPreviewPdf404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if inv.Status != statusDraft {
		return gen.GetInvoicesByIdPreviewPdf409ApplicationProblemPlusJSONResponse(invoiceIssued()), nil
	}
	var profile *contracts.CustomerBillingProfile
	if inv.Kind == kindInvoice {
		if profile, err = s.customerProfile(ctx, inv.CustomerID); err != nil {
			return nil, err
		}
	}
	body, err := s.renderPreview(ctx, q, inv, profile)
	if err != nil {
		return nil, err
	}
	return pdfPreview{
		body:        gen.GetInvoicesByIdPreviewPdf200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("inline; filename=\"utkast-%d.pdf\"", inv.ID),
	}, nil
}

// renderPreview is a draft rendered as if issued today (D4's preview).
func (s *server) renderPreview(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, profile *contracts.CustomerBillingProfile) ([]byte, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	stored, err := q.Lines(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's lines: %w", inv.ID, err)
	}
	today := businessDay(s.deps.Clock())
	codes, err := vatCodesOn(ctx, q, pgDate(today))
	if err != nil {
		return nil, err
	}
	var params store.IssueDocumentParams
	if profile != nil {
		params = *buyerSnapshot(profile)
	} else {
		keepBuyer(&params, inv)
	}
	sellerSnapshot(&params, settings)
	draft := inv
	draft.Number, draft.IssueDate, draft.IssuedAt = nil, pgDate(today), ptr(s.deps.Clock())
	copyIssueParams(&draft, params)
	if inv.Kind == kindInvoice && inv.PaymentTermsDays != nil {
		draft.DueDate = pgDate(today.AddDate(0, 0, int(*inv.PaymentTermsDays)))
	}
	lines, err := storedDraftLines(stored)
	if err != nil {
		return nil, err
	}
	// A settlement's deductions at their a-kontos' snapshots, the a-kontos
	// under the references (invoices work design D7).
	if err := withDeductionSnapshots(ctx, q, lines); err != nil {
		return nil, err
	}
	deducted, err := deductedRefsOf(ctx, q, inv, stored)
	if err != nil {
		return nil, err
	}
	exchangeRate, err := ratFromNumeric(inv.ExchangeRate)
	if err != nil {
		return nil, err
	}
	taxed := taxedLines(lines, codes)
	summaries, totals, _ := summarize(taxed, exchangeRate)
	var squared []lineAmounts
	var original *store.InvoicesInvoice // a credit note's, for its "til faktura" line
	if inv.Kind == kindCreditNote {
		// As the draft's response does: a credit note reverses its
		// original's treatment, with the original lines' snapshot rates,
		// never today's, and what a line or the invoice has left squared (D8).
		cd, err := readCreditDraft(ctx, q, inv, stored, nil)
		if err != nil {
			return nil, err
		}
		taxed, summaries, totals, squared = cd.totals.taxed, cd.totals.rows, cd.totals.totals, cd.totals.amounts
		original = &cd.book.original
	}
	doc, err := pdfDocumentOf(draft, stored, nil, original, deducted)
	if err != nil {
		return nil, err
	}
	for i := range doc.lines {
		doc.lines[i].rate = taxed[i].rate
		if squared != nil {
			doc.lines[i].net = squared[i].net
		}
	}
	doc.summaries, doc.totals, doc.preview = summaries, totals, true
	if doc.timesheet, err = timesheetOf(ctx, q, inv); err != nil {
		return nil, err
	}
	if previewRendered != nil {
		previewRendered(inv.ID, doc.totals.vat)
	}
	m := buildPDFModel(doc)
	if pdfModelBuilt != nil {
		pdfModelBuilt(inv.ID, m)
	}
	return renderPDF(m)
}

// previewRendered, when a test sets it, is told each preview's VAT total: no
// PDF text extractor in this module's dependencies reads the words back
// (pdf_internal_test.go), so a preview's arithmetic is asserted here.
var previewRendered func(invoiceID int64, vatTotal *big.Rat)

// copyIssueParams puts a would-be issue's snapshots on a draft's row, for its
// preview.
func copyIssueParams(inv *store.InvoicesInvoice, p store.IssueDocumentParams) {
	inv.BuyerCustomerNumber, inv.BuyerType, inv.BuyerName = p.BuyerCustomerNumber, p.BuyerType, p.BuyerName
	inv.BuyerOrganisationNumber, inv.BuyerForeignID = p.BuyerOrganisationNumber, p.BuyerForeignID
	inv.BuyerAddressLine1, inv.BuyerAddressLine2 = p.BuyerAddressLine1, p.BuyerAddressLine2
	inv.BuyerPostalCode, inv.BuyerCity, inv.BuyerRegion, inv.BuyerCountry = p.BuyerPostalCode, p.BuyerCity, p.BuyerRegion, p.BuyerCountry
	inv.BuyerPeppolID, inv.BuyerGln, inv.BuyerLanguage = p.BuyerPeppolID, p.BuyerGln, p.BuyerLanguage
	inv.SellerLegalName, inv.SellerOrganisationNumber = p.SellerLegalName, p.SellerOrganisationNumber
	inv.SellerVatRegistered, inv.SellerInForetaksregisteret = p.SellerVatRegistered, p.SellerInForetaksregisteret
	inv.SellerAddressLine1, inv.SellerAddressLine2 = p.SellerAddressLine1, p.SellerAddressLine2
	inv.SellerPostalCode, inv.SellerCity, inv.SellerCountry = p.SellerPostalCode, p.SellerCity, p.SellerCountry
	inv.SellerBankAccount, inv.SellerIban, inv.SellerBic = p.SellerBankAccount, p.SellerIban, p.SellerBic
	inv.SellerEmail, inv.SellerFooterText = p.SellerEmail, p.SellerFooterText
}
