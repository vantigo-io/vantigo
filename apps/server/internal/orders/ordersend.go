package orders

// An order's confirmations (orders design O24, O25, O26): the PDF of each
// confirmed version, stored once — printed inside the confirm's transaction
// from its rows and stored after its commit, and by the first download or
// send of a later version, printed from one snapshot — the pro forma, the send by
// e-mail, and the lists of the stored PDFs and the sends. A send changes no
// state of the order: it mails the stored confirmation of the order's current
// version, synchronously and outside any transaction, and logs it; a
// confirmation at version 2 or later prints and mails as revised.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// ensureConfirmation answers the stored confirmation of an order's current
// version and its bytes, rendering, storing and recording it first when it is
// not stored yet. The order, its lines and the stored row are read in one
// snapshot, so what is printed is the version it is recorded as; every other
// step is outside any transaction but the row's insert.
func (s *server) ensureConfirmation(ctx context.Context, orderID int64) (store.OrdersDocument, []byte, error) {
	var o store.OrdersOrder
	var lines []store.OrdersLine
	var doc store.OrdersDocument
	found := false
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if o, lines, err = readOrder(ctx, q, orderID); err != nil {
			return err
		}
		doc, err = q.DocumentOf(ctx, store.DocumentOfParams{OwnerKind: ownerOrder, OwnerID: o.ID, Kind: docConfirmation, Version: o.Version})
		switch {
		case err == nil:
			found = true
		case !noRows(err):
			return fmt.Errorf("orders: read order %d's confirmation: %w", o.ID, err)
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
	if o.Status == orderDraft {
		return doc, nil, conflict(codeOrderNotConfirmed, "The order is not confirmed",
			fmt.Sprintf("Order %d is a draft; confirm it first.", o.Number))
	}
	p, err := s.readPrinter(ctx)
	if err != nil {
		return doc, nil, err
	}
	body, err := p.confirmation(o, lines, s.deps.Clock())
	if err != nil {
		return doc, nil, err
	}
	return s.storeConfirmation(ctx, o, body)
}

// printer is what printing a confirmation needs from outside the module —
// the seller and the business time zone — read before any transaction, so a
// confirmation can be printed inside one (rendering is pure).
type printer struct {
	seller contracts.SellerProfile
	zone   *time.Location
}

func (s *server) readPrinter(ctx context.Context) (printer, error) {
	seller, err := s.seller(ctx)
	if err != nil {
		return printer{}, err
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return printer{}, err
	}
	return printer{seller: seller, zone: zone}, nil
}

// confirmation prints o with lines, as they are, dated at's day.
func (p printer) confirmation(o store.OrdersOrder, lines []store.OrdersLine, at time.Time) ([]byte, error) {
	return renderOrderConfirmation(o, lines, p.seller, dayOf(at, p.zone), false)
}

// storeConfirmation stores body as the confirmation of o at o's version and
// records it, outside any transaction but the row's insert; two racing it
// store the same bytes and record one row, and the loser answers the
// winner's.
func (s *server) storeConfirmation(ctx context.Context, o store.OrdersOrder, body []byte) (store.OrdersDocument, []byte, error) {
	q := store.New(s.deps.Pool)
	stored, err := s.storeDocument(ctx, ownerOrder, o.ID, o.Number, docConfirmation, o.Version, body)
	if err != nil {
		return store.OrdersDocument{}, nil, err
	}
	doc, err := recordDocument(ctx, q, ownerOrder, o.ID, docConfirmation, o.Version, stored, s.deps.Clock())
	if db.IsUniqueViolation(err, "ux_documents_owner_version") {
		key := store.DocumentOfParams{OwnerKind: ownerOrder, OwnerID: o.ID, Kind: docConfirmation, Version: o.Version}
		if doc, err = q.DocumentOf(ctx, key); err != nil {
			return doc, nil, fmt.Errorf("orders: read order %d's confirmation: %w", o.ID, err)
		}
		body, err := s.loadDocument(ctx, doc)
		return doc, body, err
	}
	if err != nil {
		return doc, nil, err
	}
	return doc, body, nil
}

// orderPDF and orderProforma set the PDF headers before the generated
// response writes the type and the status.
type orderPDF struct {
	body        gen.GetSalesOrderPdf200ApplicationpdfResponse
	disposition string
}

func (r orderPDF) VisitGetSalesOrderPdfResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetSalesOrderPdfResponse(w)
}

type orderProforma struct {
	body        gen.GetSalesOrderProforma200ApplicationpdfResponse
	disposition string
}

func (r orderProforma) VisitGetSalesOrderProformaResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetSalesOrderProformaResponse(w)
}

// renderOrderNow prints an order as it is, never stored: a draft's, a pro
// forma, or a cancelled order's that was never stored.
func (s *server) renderOrderNow(ctx context.Context, o store.OrdersOrder, lines []store.OrdersLine, proforma bool) ([]byte, error) {
	seller, err := s.seller(ctx)
	if err != nil {
		return nil, err
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return nil, err
	}
	return renderOrderConfirmation(o, lines, seller, dayOf(s.deps.Clock(), zone), proforma)
}

// GetSalesOrderPdf Download an order confirmation
// (GET /api/v1/orders/orders/{id}/pdf)
//
// ?version= names a stored version. Without it a draft prints now with the
// draft mark, a confirmed order answers its current version's stored
// confirmation — storing it first when it is not yet — and a cancelled one
// its last version's when stored, else prints now.
func (s *server) GetSalesOrderPdf(ctx context.Context, req gen.GetSalesOrderPdfRequestObject) (gen.GetSalesOrderPdfResponseObject, error) {
	// The order, its lines and the stored PDF asked for, in one snapshot.
	var o store.OrdersOrder
	var lines []store.OrdersLine
	var doc *store.OrdersDocument
	var version int32
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if o, lines, err = readOrder(ctx, q, req.Id); err != nil {
			return err
		}
		version = o.Version
		if req.Params.Version != nil {
			version = *req.Params.Version
		}
		d, err := q.DocumentOf(ctx, store.DocumentOfParams{OwnerKind: ownerOrder, OwnerID: o.ID, Kind: docConfirmation, Version: version})
		switch {
		case err == nil:
			doc = &d
		case !noRows(err):
			return fmt.Errorf("orders: read order %d's confirmation: %w", o.ID, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, answers[gen.GetSalesOrderPdfResponseObject]{notFound: func() gen.GetSalesOrderPdfResponseObject { return gen.GetSalesOrderPdf404Response{} }})
	}
	var body []byte
	switch {
	case doc != nil:
		if body, err = s.loadDocument(ctx, *doc); err != nil {
			return nil, err
		}
	case req.Params.Version != nil:
		return gen.GetSalesOrderPdf404Response{}, nil
	case o.Status == orderConfirmed:
		// The current version, not stored yet: printed from the snapshot.
		p, err := s.readPrinter(ctx)
		if err != nil {
			return nil, err
		}
		if body, err = p.confirmation(o, lines, s.deps.Clock()); err != nil {
			return nil, err
		}
		if _, body, err = s.storeConfirmation(ctx, o, body); err != nil {
			return nil, err
		}
	default:
		if body, err = s.renderOrderNow(ctx, o, lines, false); err != nil {
			return nil, err
		}
	}
	printed := o
	printed.Version = version
	return orderPDF{
		body:        gen.GetSalesOrderPdf200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", orderFileName(printed, false)),
	}, nil
}

// GetSalesOrderProforma Download a pro forma of an order
// (GET /api/v1/orders/orders/{id}/proforma)
func (s *server) GetSalesOrderProforma(ctx context.Context, req gen.GetSalesOrderProformaRequestObject) (gen.GetSalesOrderProformaResponseObject, error) {
	var o store.OrdersOrder
	var lines []store.OrdersLine
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		o, lines, err = readOrder(ctx, q, req.Id)
		return err
	})
	if err != nil {
		return answer(err, answers[gen.GetSalesOrderProformaResponseObject]{notFound: func() gen.GetSalesOrderProformaResponseObject { return gen.GetSalesOrderProforma404Response{} }})
	}
	body, err := s.renderOrderNow(ctx, o, lines, true)
	if err != nil {
		return nil, err
	}
	return orderProforma{
		body:        gen.GetSalesOrderProforma200ApplicationpdfResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("inline; filename=%q", orderFileName(o, true)),
	}, nil
}

// SendSalesOrder Send an order confirmation
// (POST /api/v1/orders/orders/{id}/send)
func (s *server) SendSalesOrder(ctx context.Context, req gen.SendSalesOrderRequestObject) (gen.SendSalesOrderResponseObject, error) {
	resp := answers[gen.SendSalesOrderResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.SendSalesOrderResponseObject {
			return gen.SendSalesOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.SendSalesOrderResponseObject { return gen.SendSalesOrder404Response{} },
		conflict: func(p gen.OrdersProblem) gen.SendSalesOrderResponseObject {
			return gen.SendSalesOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.SendSalesOrderResponseObject {
			return gen.SendSalesOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, sendInputSchema)
	send := readSendRequest(&f, *req.Body)
	if req.Body.Version == nil {
		f.add("version", "An order's send names the version it was read at")
	}
	if f.failed() {
		return gen.SendSalesOrder400ApplicationProblemPlusJSONResponse(invalid(invalidSendTitle, f)), nil
	}
	q := store.New(s.deps.Pool)
	o, _, err := readOrder(ctx, q, req.Id)
	if err != nil {
		return answer(err, resp)
	}
	if err := judgeConfirmed(o); err != nil {
		return answer(err, resp)
	}
	if err := judgeVersion(o, *req.Body.Version, req.Body.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	channel, ok, err := s.sendingChannel(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return answer(sendingDisabled(), resp)
	}
	doc, pdf, err := s.ensureConfirmation(ctx, o.ID)
	if err != nil {
		return answer(err, resp)
	}
	if doc.Version != o.Version {
		return answer(versionConflict("order's version", doc.Version, o.Version), resp)
	}
	settings, err := readSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return nil, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	facts := mailFacts{
		kind: docConfirmation, language: o.Language, number: o.Number, version: doc.Version, revised: doc.Version > 1,
		seller: seller.LegalName, currency: o.Currency, gross: ratOrZero(o.GrossTotal), message: send.message,
	}
	printed := o
	printed.Version = doc.Version
	out := envelope(send, facts, settings, seller, pdf, orderFileName(printed, false), newMessageID())
	uncancelled := context.WithoutCancel(ctx)
	sendCtx, cancel := context.WithTimeout(uncancelled, sendTimeout)
	sendErr := s.outboundSend(sendCtx, channel, out)
	cancel()
	sent := mailSent{channel: channel, out: out, by: by, at: s.deps.Clock()}
	if sendErr != nil {
		s.deps.Logger.WarnContext(ctx, "orders: an order confirmation could not be sent", "order_id", o.ID, "error", sendErr.Error())
		sent.failed, sent.lastError = true, truncate(withoutAddress(sendErr.Error(), send.recipient), 500)
	}
	rowCtx, cancelRow := context.WithTimeout(uncancelled, mailingRowTimeout)
	defer cancelRow()
	if err := logMailing(rowCtx, store.New(s.deps.Pool), ownerOrder, o.ID, doc.Version, &doc.ID, sent); err != nil {
		return nil, err
	}
	if sendErr != nil {
		return answer(mailFailed(), resp)
	}
	view, err := s.getOrderView(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	return gen.SendSalesOrder200JSONResponse(view), nil
}

// ListSalesOrderDocuments List an order's stored PDFs
// (GET /api/v1/orders/orders/{id}/documents)
func (s *server) ListSalesOrderDocuments(ctx context.Context, req gen.ListSalesOrderDocumentsRequestObject) (gen.ListSalesOrderDocumentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.OrderByID(ctx, req.Id); noRows(err) {
		return gen.ListSalesOrderDocuments404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("orders: read order %d: %w", req.Id, err)
	}
	rows, err := q.DocumentsOf(ctx, store.DocumentsOfParams{OwnerKind: ownerOrder, OwnerID: req.Id})
	if err != nil {
		return nil, fmt.Errorf("orders: list order %d's stored PDFs: %w", req.Id, err)
	}
	return gen.ListSalesOrderDocuments200JSONResponse(storedDocumentsOut(rows)), nil
}

// ListSalesOrderMailings List an order's sends
// (GET /api/v1/orders/orders/{id}/mailings)
func (s *server) ListSalesOrderMailings(ctx context.Context, req gen.ListSalesOrderMailingsRequestObject) (gen.ListSalesOrderMailingsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.OrderByID(ctx, req.Id); noRows(err) {
		return gen.ListSalesOrderMailings404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("orders: read order %d: %w", req.Id, err)
	}
	rows, err := q.MailingsOf(ctx, store.MailingsOfParams{OwnerKind: ownerOrder, OwnerID: req.Id})
	if err != nil {
		return nil, fmt.Errorf("orders: list order %d's sends: %w", req.Id, err)
	}
	return gen.ListSalesOrderMailings200JSONResponse(mailingsOut(rows)), nil
}
