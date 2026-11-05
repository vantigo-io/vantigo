package orders

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file stores a document's PDFs (O24), as Invoices' pdfstore.go stores
// an invoice's: each sent quote revision and each sent or confirmed order
// version once, through the storage port, under a key carrying its hash. A
// stored PDF is immutable; a download reads it back and verifies its hash,
// and never renders it again. No call to the store is made inside a
// transaction: the object is put first, and the row that names it is written
// after, on the transaction that changes the document's state.

// maxStoredPDF is how much of a stored object a read takes.
const maxStoredPDF = 20 << 20

// storedPDF is a PDF put in the object store, not yet recorded.
type storedPDF struct {
	key, sha256 string
	size        int64
	body        []byte
}

// errDocumentBroken marks a stored PDF that is gone or whose bytes no longer
// match its hash: an operator problem, answered as a 500 and never papered
// over by rendering again.
var errDocumentBroken = errors.New("orders: a stored PDF is missing or damaged")

// storeDocument puts a rendered PDF in the object store under a key naming
// the document, its kind, its revision or version and the bytes' hash, outside
// any transaction. Racing stores of the same bytes write the same object; a
// put whose row is never written leaves an orphan nothing serves. The row is
// recordDocument's, on the caller's transaction.
func (s *server) storeDocument(ctx context.Context, ownerKind string, ownerID, number int64, kind string, version int32, body []byte) (storedPDF, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	key := fmt.Sprintf("%s/%d/%s-%d-v%d-%s.pdf", ownerKind, ownerID, kind, number, version, hash)
	exists, err := s.objectExists(ctx, key)
	if err != nil {
		return storedPDF{}, fmt.Errorf("orders: the object store could not be read: %w", err)
	}
	if !exists {
		if err := s.objectPut(ctx, key, bytes.NewReader(body), "application/pdf"); err != nil {
			return storedPDF{}, fmt.Errorf("orders: the object store could not store %s: %w", key, err)
		}
	}
	return storedPDF{key: key, sha256: hash, size: int64(len(body)), body: body}, nil
}

// recordDocument writes the row of a stored PDF on the caller's transaction:
// once per document, kind and revision or version.
func recordDocument(ctx context.Context, txq *store.Queries, ownerKind string, ownerID int64, kind string, version int32, d storedPDF, at time.Time) (store.OrdersDocument, error) {
	row, err := txq.InsertDocument(ctx, store.InsertDocumentParams{
		OwnerKind: ownerKind, OwnerID: ownerID, Kind: kind, Version: version, StorageKey: d.key, Sha256: d.sha256,
		SizeBytes: d.size, CreatedAt: at,
	})
	if err != nil {
		return row, fmt.Errorf("orders: record %s %d's %s PDF of version %d: %w", ownerKind, ownerID, kind, version, err)
	}
	return row, nil
}

// loadDocument reads a stored PDF back and verifies it against its hash,
// outside any transaction.
func (s *server) loadDocument(ctx context.Context, d store.OrdersDocument) ([]byte, error) {
	rc, err := s.objectGet(ctx, d.StorageKey)
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "orders: a stored PDF could not be read", "document_id", d.ID, "error", err.Error())
		return nil, fmt.Errorf("%w: %w", errDocumentBroken, err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, maxStoredPDF+1))
	if err != nil {
		return nil, fmt.Errorf("orders: read stored PDF %d: %w", d.ID, err)
	}
	sum := sha256.Sum256(body)
	if len(body) > maxStoredPDF || hex.EncodeToString(sum[:]) != d.Sha256 {
		s.deps.Logger.ErrorContext(ctx, "orders: a stored PDF does not match its hash", "document_id", d.ID, "key", d.StorageKey)
		return nil, errDocumentBroken
	}
	return body, nil
}

// pdfHeaders are the headers a PDF answers with: never cached, never sniffed,
// and named.
func pdfHeaders(w http.ResponseWriter, disposition string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", disposition)
}

// quotePDF and quoteProforma set those headers before the generated
// response writes the type and the status.
type quotePDF struct {
	body        gen.GetQuotePdf200ApplicationpdfResponse
	disposition string
}

func (r quotePDF) VisitGetQuotePdfResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetQuotePdfResponse(w)
}

type quoteProforma struct {
	body        gen.GetQuoteProforma200ApplicationpdfResponse
	disposition string
}

func (r quoteProforma) VisitGetQuoteProformaResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetQuoteProformaResponse(w)
}

// quoteFileName is a quote's download name in its language.
func quoteFileName(q store.OrdersQuote, revision int32, proforma bool) string {
	switch {
	case proforma && q.Language == "en":
		return fmt.Sprintf("proforma-quote-%d.pdf", q.Number)
	case proforma:
		return fmt.Sprintf("proforma-tilbud-%d.pdf", q.Number)
	case q.Language == "en":
		return fmt.Sprintf("quote-%d-rev%d.pdf", q.Number, revision)
	}
	return fmt.Sprintf("tilbud-%d-rev%d.pdf", q.Number, revision)
}

// orderFileName is an order confirmation's download name in its language.
func orderFileName(o store.OrdersOrder, proforma bool) string {
	switch {
	case proforma && o.Language == "en":
		return fmt.Sprintf("proforma-order-%d.pdf", o.Number)
	case proforma:
		return fmt.Sprintf("proforma-ordre-%d.pdf", o.Number)
	case o.Language == "en":
		return fmt.Sprintf("order-confirmation-%d-v%d.pdf", o.Number, o.Version)
	}
	return fmt.Sprintf("ordrebekreftelse-%d-v%d.pdf", o.Number, o.Version)
}

// quoteDate is the date a quote's PDF prints: the day it was sent, or today
// for a draft.
func (s *server) quoteDate(ctx context.Context, q store.OrdersQuote) (time.Time, error) {
	zone, err := s.businessZone(ctx)
	if err != nil {
		return time.Time{}, err
	}
	at := s.deps.Clock()
	if q.SentAt != nil && q.Status != quoteDraft {
		at = *q.SentAt
	}
	return dayOf(at, zone), nil
}

// GetQuotePdf Download a quote's PDF
// (GET /api/v1/orders/quotes/{id}/pdf)
//
// A sent revision's PDF is the stored one; a draft's is printed now, with
// the draft mark, and never stored. ?revision= names a sent revision.
func (s *server) GetQuotePdf(ctx context.Context, req gen.GetQuotePdfRequestObject) (gen.GetQuotePdfResponseObject, error) {
	q := store.New(s.deps.Pool)
	quote, err := q.GetQuote(ctx, req.Id)
	if noRows(err) {
		return gen.GetQuotePdf404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read quote %d: %w", req.Id, err)
	}
	revision := quote.Revision
	if req.Params.Revision != nil {
		revision = *req.Params.Revision
	}
	var body []byte
	if quote.Status == quoteDraft && revision == quote.Revision {
		lines, err := linesOf(ctx, q, ownerQuote, quote.ID)
		if err != nil {
			return nil, err
		}
		seller, err := s.seller(ctx)
		if err != nil {
			return nil, err
		}
		date, err := s.quoteDate(ctx, quote)
		if err != nil {
			return nil, err
		}
		if body, err = renderQuote(quote, lines, seller, date, false); err != nil {
			return nil, err
		}
	} else {
		doc, err := q.DocumentOf(ctx, store.DocumentOfParams{OwnerKind: ownerQuote, OwnerID: quote.ID, Kind: docQuote, Version: revision})
		if noRows(err) {
			return gen.GetQuotePdf404Response{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("orders: read quote %d's stored PDF: %w", quote.ID, err)
		}
		if body, err = s.loadDocument(ctx, doc); err != nil {
			return nil, err
		}
	}
	return quotePDF{
		body:        gen.GetQuotePdf200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", quoteFileName(quote, revision, false)),
	}, nil
}

// GetQuoteProforma Download a pro forma of a quote
// (GET /api/v1/orders/quotes/{id}/proforma)
//
// Printed now from the quote as it is, titled as not an invoice; never stored
// and never sent (O24).
func (s *server) GetQuoteProforma(ctx context.Context, req gen.GetQuoteProformaRequestObject) (gen.GetQuoteProformaResponseObject, error) {
	q := store.New(s.deps.Pool)
	quote, err := q.GetQuote(ctx, req.Id)
	if noRows(err) {
		return gen.GetQuoteProforma404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read quote %d: %w", req.Id, err)
	}
	lines, err := linesOf(ctx, q, ownerQuote, quote.ID)
	if err != nil {
		return nil, err
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return nil, err
	}
	date, err := s.quoteDate(ctx, quote)
	if err != nil {
		return nil, err
	}
	body, err := renderQuote(quote, lines, seller, date, true)
	if err != nil {
		return nil, err
	}
	return quoteProforma{
		body:        gen.GetQuoteProforma200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("inline; filename=%q", quoteFileName(quote, quote.Revision, true)),
	}, nil
}

// storedDocumentsOut are a document's stored PDFs as the API lists them.
func storedDocumentsOut(rows []store.OrdersDocument) gen.OrdersStoredDocumentList {
	out := gen.OrdersStoredDocumentList{Items: []gen.OrdersStoredDocument{}}
	for _, d := range rows {
		out.Items = append(out.Items, gen.OrdersStoredDocument{
			Id: d.ID, Kind: gen.OrdersStoredDocumentKind(d.Kind), Version: d.Version, SizeBytes: d.SizeBytes, CreatedAt: d.CreatedAt,
		})
	}
	return out
}

// ListQuoteDocuments List a quote's stored PDFs
// (GET /api/v1/orders/quotes/{id}/documents)
func (s *server) ListQuoteDocuments(ctx context.Context, req gen.ListQuoteDocumentsRequestObject) (gen.ListQuoteDocumentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetQuote(ctx, req.Id); noRows(err) {
		return gen.ListQuoteDocuments404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("orders: read quote %d: %w", req.Id, err)
	}
	rows, err := q.DocumentsOf(ctx, store.DocumentsOfParams{OwnerKind: ownerQuote, OwnerID: req.Id})
	if err != nil {
		return nil, fmt.Errorf("orders: list quote %d's stored PDFs: %w", req.Id, err)
	}
	return gen.ListQuoteDocuments200JSONResponse(storedDocumentsOut(rows)), nil
}
