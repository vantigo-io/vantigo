package purchasing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// This file stores an order's PDFs (P21, A19), as Orders' pdfdocs.go stores a
// confirmation's: each version an order is sent at, and its cancellation,
// once, through the storage port, under a key carrying its hash. A stored
// PDF is immutable; a download reads it back and verifies its hash, and
// never renders it again. No call to the store is made inside a transaction:
// the PDF is rendered from the order as it was committed, put first, and the
// row that names it written after.

// maxStoredPDF is how much of a stored object a read takes.
const maxStoredPDF = 20 << 20

// errDocumentBroken marks a stored PDF that is gone or whose bytes no longer
// match its hash: an operator problem, answered as a 500 and never papered
// over by rendering again.
var errDocumentBroken = errors.New("purchasing: a stored PDF is missing or damaged")

// storedPDF is a PDF put in the object store, not yet recorded.
type storedPDF struct {
	key, sha256 string
	size        int64
}

// storeDocument puts a rendered PDF in the object store under a key naming
// the order, its kind, its version and the bytes' hash, outside any
// transaction. Racing stores of the same bytes write the same object.
func (s *server) storeDocument(ctx context.Context, o store.PurchasingOrder, kind string, body []byte) (storedPDF, error) {
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	number := int64(0)
	if o.Number != nil {
		number = *o.Number
	}
	key := fmt.Sprintf("order/%d/%s-%d-v%d-%s.pdf", o.ID, kind, number, o.Version, hash)
	exists, err := s.objectExists(ctx, key)
	if err != nil {
		return storedPDF{}, fmt.Errorf("purchasing: the object store could not be read: %w", err)
	}
	if !exists {
		if err := s.objectPut(ctx, key, bytes.NewReader(body), "application/pdf"); err != nil {
			return storedPDF{}, fmt.Errorf("purchasing: the object store could not store %s: %w", key, err)
		}
	}
	return storedPDF{key: key, sha256: hash, size: int64(len(body))}, nil
}

// loadDocument reads a stored PDF back and verifies it against its hash,
// outside any transaction.
func (s *server) loadDocument(ctx context.Context, d store.PurchasingDocument) ([]byte, error) {
	if d.ErasedAt != nil {
		return nil, notFound("stored PDF")
	}
	rc, err := s.objectGet(ctx, d.StorageKey)
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "purchasing: a stored PDF could not be read", "document_id", d.ID, "error", err.Error())
		return nil, fmt.Errorf("%w: %w", errDocumentBroken, err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, maxStoredPDF+1))
	if err != nil {
		return nil, fmt.Errorf("purchasing: read stored PDF %d: %w", d.ID, err)
	}
	sum := sha256.Sum256(body)
	if len(body) > maxStoredPDF || hex.EncodeToString(sum[:]) != d.Sha256 {
		s.deps.Logger.ErrorContext(ctx, "purchasing: a stored PDF does not match its hash", "document_id", d.ID, "key", d.StorageKey)
		return nil, errDocumentBroken
	}
	return body, nil
}

// printOrder renders o's PDF of kind from its rows: a sent order from its
// snapshots, a draft as it is now. The seller and the settings' text are
// read here, before any transaction.
func (s *server) printOrder(ctx context.Context, r orderRead, kind string) ([]byte, error) {
	seller, err := s.seller(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := readSettings(ctx, s.store())
	if err != nil {
		return nil, err
	}
	o := r.order
	p := printable{kind: kind, order: o, supplier: storedSnapshot(o), delivery: storedDelivery(o), lines: r.lines,
		seller: seller, footer: settings.orderFooter}
	if o.Status == statusDraft {
		p.draft = true
		env, err := s.envFor(ctx, o)
		if err != nil {
			return nil, err
		}
		if env.supplier != nil {
			p.supplier = snapshotOf(*env.supplier)
		}
		siteName := ""
		if site, ok := env.sites[o.SiteID]; ok {
			siteName = site.Name
		}
		p.delivery = deliveryAddress(settings, seller, o.SiteID, siteName)
	}
	return renderOrder(p)
}

// ensureDocument answers the stored PDF of kind at the order's version and
// its bytes, rendering, storing and recording it first when it is not
// stored yet — after a send's commit, or when the store failed then. Two
// racing it record one row, and the loser answers the winner's.
func (s *server) ensureDocument(ctx context.Context, orderID int64, kind string) (store.PurchasingDocument, []byte, error) {
	var r orderRead
	var doc store.PurchasingDocument
	found := false
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if r, err = readOrder(ctx, q, orderID); err != nil {
			return err
		}
		doc, err = q.DocumentOf(ctx, store.DocumentOfParams{OrderID: orderID, Kind: kind, Version: r.order.Version})
		switch {
		case err == nil:
			found = true
		case !noRows(err):
			return fmt.Errorf("purchasing: read order %d's PDF: %w", orderID, err)
		}
		return nil
	})
	if err != nil {
		return doc, nil, err
	}
	if found {
		body, err := s.loadDocument(ctx, doc)
		return doc, body, err
	}
	if r.order.Status == statusDraft {
		return doc, nil, conflict(codeOrderNotSent, "The order is not sent", "A draft's PDF is printed, never stored.")
	}
	body, err := s.printOrder(ctx, r, kind)
	if err != nil {
		return doc, nil, err
	}
	stored, err := s.storeDocument(ctx, r.order, kind, body)
	if err != nil {
		return doc, nil, err
	}
	q := s.store()
	doc, err = q.InsertDocument(ctx, store.InsertDocumentParams{
		OrderID: orderID, Kind: kind, Version: r.order.Version, StorageKey: stored.key, Sha256: stored.sha256,
		SizeBytes: stored.size, CreatedAt: s.deps.Clock(),
	})
	if noRows(err) {
		// The supplier was erased since the PDF was put: the object is
		// written to the deletion ledger, so none survives the erase (A8).
		if lerr := q.LedgerObject(ctx, store.LedgerObjectParams{StorageKey: stored.key, RequestedAt: s.deps.Clock()}); lerr != nil {
			return doc, nil, fmt.Errorf("purchasing: write %s to the deletion ledger: %w", stored.key, lerr)
		}
		return doc, nil, notFound("stored PDF")
	}
	if db.IsUniqueViolation(err, "ux_documents_order_version") {
		if doc, err = q.DocumentOf(ctx, store.DocumentOfParams{OrderID: orderID, Kind: kind, Version: r.order.Version}); err != nil {
			return doc, nil, fmt.Errorf("purchasing: read order %d's PDF: %w", orderID, err)
		}
		body, err := s.loadDocument(ctx, doc)
		return doc, body, err
	}
	if err != nil {
		return doc, nil, fmt.Errorf("purchasing: record order %d's PDF: %w", orderID, err)
	}
	return doc, body, nil
}

// pdfHeaders are the headers a PDF answers with: never cached, never sniffed,
// and named.
func pdfHeaders(w http.ResponseWriter, name string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
}

// orderPDF and documentPDF set the PDF headers before the generated response
// writes the type and the status.
type orderPDF struct {
	body gen.GetPurchasingOrderPdf200ApplicationpdfResponse
	name string
}

func (r orderPDF) VisitGetPurchasingOrderPdfResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.name)
	return r.body.VisitGetPurchasingOrderPdfResponse(w)
}

type documentPDF struct {
	body gen.GetPurchasingOrderDocument200ApplicationpdfResponse
	name string
}

func (r documentPDF) VisitGetPurchasingOrderDocumentResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.name)
	return r.body.VisitGetPurchasingOrderDocumentResponse(w)
}

// GetPurchasingOrderPdf Download a purchase order's PDF
// (GET /api/v1/purchasing/orders/{orderId}/pdf)
//
// A draft prints now, marked Draft, never stored; a cancelled order answers
// its cancellation when one is stored, else its last version; any other
// its current version's stored PDF, storing it first when it is not yet.
func (s *server) GetPurchasingOrderPdf(ctx context.Context, req gen.GetPurchasingOrderPdfRequestObject) (gen.GetPurchasingOrderPdfResponseObject, error) {
	resp := answers[gen.GetPurchasingOrderPdfResponseObject]{
		notFound: func(p gen.PurchasingProblem) gen.GetPurchasingOrderPdfResponseObject {
			return gen.GetPurchasingOrderPdf404ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.GetPurchasingOrderPdfResponseObject {
			return gen.GetPurchasingOrderPdf503ApplicationProblemPlusJSONResponse(p)
		},
	}
	r, err := readOrder(ctx, s.store(), req.OrderId)
	if err != nil {
		return answer(err, resp)
	}
	kind := docOrder
	var body []byte
	switch r.order.Status {
	case statusDraft:
		if body, err = s.printOrder(ctx, r, docOrder); err != nil {
			return answer(err, resp)
		}
	case statusCancelled:
		doc, derr := s.store().DocumentOf(ctx, store.DocumentOfParams{OrderID: r.order.ID, Kind: docCancellation, Version: r.order.Version})
		if derr == nil {
			kind = docCancellation
			body, err = s.loadDocument(ctx, doc)
		} else if noRows(derr) {
			_, body, err = s.ensureDocument(ctx, r.order.ID, docOrder)
		} else {
			err = derr
		}
		if err != nil {
			return answer(err, resp)
		}
	default:
		if _, body, err = s.ensureDocument(ctx, r.order.ID, docOrder); err != nil {
			return answer(err, resp)
		}
	}
	return orderPDF{
		body: gen.GetPurchasingOrderPdf200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		name: fileName(r.order, kind),
	}, nil
}

// GetPurchasingOrderDocument Download a stored PDF of a purchase order
// (GET /api/v1/purchasing/orders/{orderId}/documents/{documentId})
func (s *server) GetPurchasingOrderDocument(ctx context.Context, req gen.GetPurchasingOrderDocumentRequestObject) (gen.GetPurchasingOrderDocumentResponseObject, error) {
	resp := answers[gen.GetPurchasingOrderDocumentResponseObject]{
		notFound: func(p gen.PurchasingProblem) gen.GetPurchasingOrderDocumentResponseObject {
			return gen.GetPurchasingOrderDocument404ApplicationProblemPlusJSONResponse(p)
		},
	}
	q := s.store()
	o, err := q.GetOrder(ctx, req.OrderId)
	if noRows(err) {
		return answer(notFound("purchase order"), resp)
	}
	if err != nil {
		return nil, fmt.Errorf("purchasing: read order %d: %w", req.OrderId, err)
	}
	doc, err := q.GetDocument(ctx, store.GetDocumentParams{ID: req.DocumentId, OrderID: o.ID})
	if noRows(err) {
		return answer(notFound("stored PDF"), resp)
	}
	if err != nil {
		return nil, fmt.Errorf("purchasing: read stored PDF %d: %w", req.DocumentId, err)
	}
	body, err := s.loadDocument(ctx, doc)
	if err != nil {
		return answer(err, resp)
	}
	printed := o
	printed.Version = doc.Version
	return documentPDF{
		body: gen.GetPurchasingOrderDocument200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		name: fileName(printed, doc.Kind),
	}, nil
}

// mailingOut is a mailing as the API answers it.
func mailingOut(m store.PurchasingMailing) gen.PurchasingMailing {
	cc := []string{}
	for _, c := range strings.Split(m.Cc, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cc = append(cc, c)
		}
	}
	return gen.PurchasingMailing{
		Id: m.ID, Kind: gen.PurchasingMailingKind(m.Kind), Version: m.Version, Recipient: m.Recipient, Cc: cc,
		Subject: m.Subject, Status: gen.PurchasingMailingStatus(m.Status), Attempts: m.Attempts, LastError: m.LastError,
		CreatedAt: m.CreatedAt, SentAt: m.SentAt, SentBy: personOut(m.SentByUserID, m.SentByDisplay),
	}
}

// GetPurchasingOrderHistory Get a purchase order's history
// (GET /api/v1/purchasing/orders/{orderId}/history)
func (s *server) GetPurchasingOrderHistory(ctx context.Context, req gen.GetPurchasingOrderHistoryRequestObject) (gen.GetPurchasingOrderHistoryResponseObject, error) {
	out := gen.PurchasingOrderHistory{Changes: []gen.PurchasingChange{}, Documents: []gen.PurchasingDocument{}, Mailings: []gen.PurchasingMailing{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := q.GetOrder(ctx, req.OrderId); noRows(err) {
			return notFound("purchase order")
		} else if err != nil {
			return fmt.Errorf("purchasing: read order %d: %w", req.OrderId, err)
		}
		changes, err := q.ChangesOf(ctx, req.OrderId)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's changes: %w", req.OrderId, err)
		}
		for _, c := range changes {
			var actions []change
			if err := json.Unmarshal(c.Actions, &actions); err != nil {
				return fmt.Errorf("purchasing: read change %d: %w", c.ID, err)
			}
			item := gen.PurchasingChange{Id: c.ID, At: c.At, Actor: personOut(c.ActorUserID, c.ActorDisplay), Actions: []gen.PurchasingChangeAction{}}
			for _, a := range actions {
				item.Actions = append(item.Actions, gen.PurchasingChangeAction{Action: a.Action, Field: a.Field, From: a.From, To: a.To, LineId: a.LineID})
			}
			out.Changes = append(out.Changes, item)
		}
		docs, err := q.DocumentsOf(ctx, req.OrderId)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's PDFs: %w", req.OrderId, err)
		}
		for _, d := range docs {
			out.Documents = append(out.Documents, gen.PurchasingDocument{Id: d.ID, Kind: gen.PurchasingDocumentKind(d.Kind),
				Version: d.Version, SizeBytes: d.SizeBytes, CreatedAt: d.CreatedAt, Erased: d.ErasedAt != nil})
		}
		mailings, err := q.MailingsOf(ctx, req.OrderId)
		if err != nil {
			return fmt.Errorf("purchasing: read order %d's mailings: %w", req.OrderId, err)
		}
		for _, m := range mailings {
			out.Mailings = append(out.Mailings, mailingOut(m))
		}
		return nil
	})
	if err != nil {
		return answer(err, answers[gen.GetPurchasingOrderHistoryResponseObject]{
			notFound: func(p gen.PurchasingProblem) gen.GetPurchasingOrderHistoryResponseObject {
				return gen.GetPurchasingOrderHistory404ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.GetPurchasingOrderHistory200JSONResponse(out), nil
}
