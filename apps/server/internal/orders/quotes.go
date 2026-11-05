package orders

// The quote operations (orders design O13–O23): the CRUD of a draft, revise,
// decline, cancel and copy. Send, mark as sent, the PDFs and the lists of a
// quote's stored documents and sends are quotesend.go's; accept is
// accept.go's; applying a template is templates.go's.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// The quote states (O20).
const (
	quoteDraft     = "draft"
	quoteSent      = "sent"
	quoteAccepted  = "accepted"
	quoteDeclined  = "declined"
	quoteExpired   = "expired"
	quoteCancelled = "cancelled"
)

// The origin channels of this phase (O27); the set is open.
const (
	channelManual = "manual"
	channelQuote  = "quote"
	channelCopy   = "copy"
)

// moduleName is the module an origin's source names when it is Orders itself.
const moduleName = "orders"

// quoteSource is how an origin names a quote at a revision (O20, O27):
// "<id>@<revision>".
func quoteSource(id int64, revision int32) string {
	return strconv.FormatInt(id, 10) + "@" + strconv.FormatInt(int64(revision), 10)
}

// quoteView is a quote with its lines as the API answers it. acceptedNumber
// is the number of the document it was accepted into, when it was.
func quoteView(q store.OrdersQuote, lines []store.OrdersLine, acceptedNumber *int64) gen.OrdersQuote {
	h := headerOfQuote(q)
	out := gen.OrdersQuote{
		Id: q.ID, Number: q.Number, Status: gen.OrdersQuoteStatus(q.Status), Revision: q.Revision,
		Customer: snapshotOut(h), DefaultRecipient: defaultRecipient(h), YourReference: q.YourReference, Currency: q.Currency, PricesIncludeVat: q.PricesIncludeVat,
		PaymentTermsDays: q.PaymentTermsDays, DiscountVisible: q.DiscountVisible, Language: gen.OrdersQuoteLanguage(q.Language),
		IntroText: q.IntroText, ClosingText: q.ClosingText, InternalNote: q.InternalNote,
		Origin: gen.OrdersOrigin{
			Channel: q.Channel, SourceModule: q.SourceModule, SourceKind: q.SourceKind, SourceId: q.SourceID,
			ExternalId: q.ExternalID, ExternalNumber: q.ExternalNumber, PaymentReference: q.PaymentReference,
			PrepaidAmount: wirePtr(ratOf(q.PrepaidAmount), amountPlaces),
		},
		Totals: totalsOut(totalsOfRows(lines)), Lines: linesOut(lines, q.PricesIncludeVat),
		CreatedAt: q.CreatedAt, UpdatedAt: q.UpdatedAt, ProjectId: q.ProjectID,
		Salesperson: personOut(q.SalespersonID, q.SalespersonDisplay), DeliveryDate: dateOut(q.DeliveryDate),
		ValidUntil: dateOut(q.ValidUntil), SentAt: q.SentAt, AcceptedAt: q.AcceptedAt, DeclinedAt: q.DeclinedAt,
		DeclineReason: q.DeclineReason, CancelledAt: q.CancelledAt,
	}
	if q.AcceptedIntoKind != nil && q.AcceptedIntoID != nil && acceptedNumber != nil {
		out.AcceptedInto = &gen.OrdersDocumentRef{Kind: gen.OrdersDocumentRefKind(*q.AcceptedIntoKind), Id: *q.AcceptedIntoID, Number: *acceptedNumber}
	}
	return out
}

// readQuoteView reads a quote's lines and what it was accepted into on q,
// and answers the quote as the API does.
func readQuoteView(ctx context.Context, q *store.Queries, row store.OrdersQuote) (gen.OrdersQuote, error) {
	lines, err := linesOf(ctx, q, ownerQuote, row.ID)
	if err != nil {
		return gen.OrdersQuote{}, err
	}
	var accepted *int64
	if row.AcceptedIntoKind != nil && *row.AcceptedIntoKind == ownerOrder && row.AcceptedIntoID != nil {
		o, err := q.OrderByID(ctx, *row.AcceptedIntoID)
		switch {
		case err == nil:
			accepted = &o.Number
		case !noRows(err):
			return gen.OrdersQuote{}, fmt.Errorf("orders: read quote %d's order: %w", row.ID, err)
		}
	}
	return quoteView(row, lines, accepted), nil
}

// getQuoteView reads a quote and answers it, notFound when there is none.
func (s *server) getQuoteView(ctx context.Context, id int64) (gen.OrdersQuote, error) {
	var out gen.OrdersQuote
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		row, err := q.GetQuote(ctx, id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("orders: read quote %d: %w", id, err)
		}
		out, err = readQuoteView(ctx, q, row)
		return err
	})
	return out, err
}

// quoteParams are a quote's header as InsertQuote and UpdateQuoteDraft take
// it, with its totals.
func insertQuoteParams(h header, t docTotals) store.InsertQuoteParams {
	return store.InsertQuoteParams{
		CustomerID: h.CustomerID, CustomerNumber: h.CustomerNumber, CustomerName: h.CustomerName, CustomerType: h.CustomerType,
		CustomerOrganisationNumber: h.CustomerOrganisationNumber, CustomerForeignID: h.CustomerForeignID,
		CustomerCountry: h.CustomerCountry, InvoiceAddressLine1: h.InvoiceAddressLine1, InvoiceAddressLine2: h.InvoiceAddressLine2,
		InvoicePostalCode: h.InvoicePostalCode, InvoiceCity: h.InvoiceCity, InvoiceCountry: h.InvoiceCountry,
		DeliveryAddressID: h.DeliveryAddressID, DeliveryName: h.DeliveryName, DeliveryAddressLine1: h.DeliveryAddressLine1,
		DeliveryAddressLine2: h.DeliveryAddressLine2, DeliveryPostalCode: h.DeliveryPostalCode, DeliveryCity: h.DeliveryCity,
		DeliveryCountry: h.DeliveryCountry, ContactID: h.ContactID, ContactName: h.ContactName, ContactEmail: h.ContactEmail,
		ContactPhone: h.ContactPhone, CustomerInvoiceEmail: h.CustomerInvoiceEmail, YourReference: h.YourReference, SalespersonID: h.SalespersonID,
		SalespersonDisplay: h.SalespersonDisplay, ProjectID: h.ProjectID, Currency: h.Currency,
		PricesIncludeVat: h.PricesIncludeVat, PaymentTermsDays: h.PaymentTermsDays, DiscountVisible: h.DiscountVisible,
		Language: h.Language, IntroText: h.IntroText, ClosingText: h.ClosingText, InternalNote: h.InternalNote,
		DeliveryDate: h.DeliveryDate, NetTotal: numeric(t.net, amountPlaces), VatTotal: numeric(t.vat, amountPlaces),
		GrossTotal: numeric(t.gross, amountPlaces),
	}
}

func updateQuoteParams(id int64, h header, t docTotals, validUntil pgtype.Date, templateID *int64, now time.Time) store.UpdateQuoteDraftParams {
	p := insertQuoteParams(h, t)
	return store.UpdateQuoteDraftParams{
		ID: id, ValidUntil: validUntil, TemplateID: templateID, Now: now,
		CustomerID: p.CustomerID, CustomerNumber: p.CustomerNumber, CustomerName: p.CustomerName, CustomerType: p.CustomerType,
		CustomerOrganisationNumber: p.CustomerOrganisationNumber, CustomerForeignID: p.CustomerForeignID,
		CustomerCountry: p.CustomerCountry, InvoiceAddressLine1: p.InvoiceAddressLine1, InvoiceAddressLine2: p.InvoiceAddressLine2,
		InvoicePostalCode: p.InvoicePostalCode, InvoiceCity: p.InvoiceCity, InvoiceCountry: p.InvoiceCountry,
		DeliveryAddressID: p.DeliveryAddressID, DeliveryName: p.DeliveryName, DeliveryAddressLine1: p.DeliveryAddressLine1,
		DeliveryAddressLine2: p.DeliveryAddressLine2, DeliveryPostalCode: p.DeliveryPostalCode, DeliveryCity: p.DeliveryCity,
		DeliveryCountry: p.DeliveryCountry, ContactID: p.ContactID, ContactName: p.ContactName, ContactEmail: p.ContactEmail,
		ContactPhone: p.ContactPhone, CustomerInvoiceEmail: p.CustomerInvoiceEmail, YourReference: p.YourReference, SalespersonID: p.SalespersonID,
		SalespersonDisplay: p.SalespersonDisplay, ProjectID: p.ProjectID, Currency: p.Currency,
		PricesIncludeVat: p.PricesIncludeVat, PaymentTermsDays: p.PaymentTermsDays, DiscountVisible: p.DiscountVisible,
		Language: p.Language, IntroText: p.IntroText, ClosingText: p.ClosingText, InternalNote: p.InternalNote,
		DeliveryDate: p.DeliveryDate, NetTotal: p.NetTotal, VatTotal: p.VatTotal, GrossTotal: p.GrossTotal,
	}
}

// actor is who did something, snapshotted on the row it did (O29).
type actor struct {
	id      *uuid.UUID
	display *string
}

// actorFor is the caller as an actor, read before any transaction.
func (s *server) actorFor(ctx context.Context) (actor, error) {
	id := callerID(ctx)
	if id == uuid.Nil {
		return actor{}, nil
	}
	u, err := s.user(ctx, id)
	if err != nil {
		return actor{}, err
	}
	a := actor{id: &id}
	if u != nil {
		a.display = textPtr(u.DisplayName)
	}
	return a, nil
}

// newQuote is a quote to create: its header, lines, origin and valid-until.
type newQuote struct {
	header          header
	lines           []pricedLine
	validUntil      pgtype.Date
	templateID      *int64
	channel         string
	sourceModule    *string
	sourceKind      *string
	sourceID        *string
	clientRequestID *uuid.UUID
	createdBy       actor
}

// insertQuote writes a new draft quote and its lines on tx: the variants'
// locks first (MB rule 21), then the quote series' next number (O12).
func (s *server) insertQuote(ctx context.Context, tx pgx.Tx, txq *store.Queries, n newQuote) (store.OrdersQuote, []store.OrdersLine, error) {
	if err := s.lockLines(ctx, tx, invalidQuoteTitle, n.lines); err != nil {
		return store.OrdersQuote{}, nil, err
	}
	settings, err := readSettings(ctx, txq)
	if err != nil {
		return store.OrdersQuote{}, nil, err
	}
	number, err := allocateNumber(ctx, txq, seriesQuote, settings.quoteSeriesStart)
	if err != nil {
		return store.OrdersQuote{}, nil, err
	}
	p := insertQuoteParams(n.header, totalsOfPriced(n.lines))
	p.Number, p.ValidUntil, p.TemplateID = number, n.validUntil, n.templateID
	p.Channel, p.SourceModule, p.SourceKind, p.SourceID = n.channel, n.sourceModule, n.sourceKind, n.sourceID
	p.ClientRequestID, p.CreatedByUserID, p.CreatedByDisplay, p.Now = n.clientRequestID, n.createdBy.id, n.createdBy.display, s.deps.Clock()
	row, err := txq.InsertQuote(ctx, p)
	if err != nil {
		return store.OrdersQuote{}, nil, err
	}
	lines, err := writeLines(ctx, txq, ownerQuote, row.ID, n.lines)
	if err != nil {
		return store.OrdersQuote{}, nil, err
	}
	return row, lines, nil
}

// dateIn is an API date as a column's.
func dateIn(d *openapi_types.Date) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: d.Time, Valid: true}
}

// ListQuotes List quotes
// (GET /api/v1/orders/quotes)
func (s *server) ListQuotes(ctx context.Context, req gen.ListQuotesRequestObject) (gen.ListQuotesResponseObject, error) {
	page, size := int32(1), int32(50)
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	if req.Params.PageSize != nil {
		size = *req.Params.PageSize
	}
	if page < 1 || size < 1 || size > 200 {
		var f fieldErrors
		f.add("page", "page is from 1, and pageSize 1 to 200")
		return gen.ListQuotes400ApplicationProblemPlusJSONResponse(invalid(invalidQueryTitle, f)), nil
	}
	statuses := []string{}
	if req.Params.Status != nil {
		for _, st := range *req.Params.Status {
			statuses = append(statuses, string(st))
		}
	}
	search := ""
	if req.Params.Q != nil {
		search = strings.TrimSpace(*req.Params.Q)
	}
	out := gen.OrdersQuotePage{Items: []gen.OrdersQuoteSummary{}}
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		rows, err := q.ListQuotes(ctx, store.ListQuotesParams{
			CustomerID: req.Params.CustomerId, Statuses: statuses, SalespersonID: req.Params.SalespersonId, Q: search,
			PageSize: size, PageOffset: (page - 1) * size,
		})
		if err != nil {
			return fmt.Errorf("orders: list quotes: %w", err)
		}
		total, err := q.CountQuotes(ctx, store.CountQuotesParams{
			CustomerID: req.Params.CustomerId, Statuses: statuses, SalespersonID: req.Params.SalespersonId, Q: search,
		})
		if err != nil {
			return fmt.Errorf("orders: count quotes: %w", err)
		}
		out.Total = total
		for _, r := range rows {
			out.Items = append(out.Items, gen.OrdersQuoteSummary{
				Id: r.ID, Number: r.Number, Status: gen.OrdersQuoteSummaryStatus(r.Status), Revision: r.Revision,
				CustomerId: r.CustomerID, CustomerName: r.CustomerName, Currency: r.Currency,
				Net: wire(ratOrZero(r.NetTotal), amountPlaces), Gross: wire(ratOrZero(r.GrossTotal), amountPlaces),
				Salesperson: personOut(r.SalespersonID, r.SalespersonDisplay), ValidUntil: dateOut(r.ValidUntil),
				UpdatedAt: r.UpdatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gen.ListQuotes200JSONResponse(out), nil
}

// GetQuote Get a quote
// (GET /api/v1/orders/quotes/{id})
func (s *server) GetQuote(ctx context.Context, req gen.GetQuoteRequestObject) (gen.GetQuoteResponseObject, error) {
	out, err := s.getQuoteView(ctx, req.Id)
	if err != nil {
		return answer(err, answers[gen.GetQuoteResponseObject]{notFound: func() gen.GetQuoteResponseObject { return gen.GetQuote404Response{} }})
	}
	return gen.GetQuote200JSONResponse(out), nil
}

// CreateQuote Create a quote
// (POST /api/v1/orders/quotes)
//
// Everything a quote is made from is read before its transaction: the
// customer's billing profile, the settings, the salesperson, the project, a
// template, the catalog, the list prices and the VAT codes. The transaction
// locks the variants first, then takes the number and writes the quote.
func (s *server) CreateQuote(ctx context.Context, req gen.CreateQuoteRequestObject) (gen.CreateQuoteResponseObject, error) {
	resp := answers[gen.CreateQuoteResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.CreateQuoteResponseObject {
			return gen.CreateQuote400ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.OrdersProblem) gen.CreateQuoteResponseObject {
			return gen.CreateQuote409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.CreateQuoteResponseObject {
			return gen.CreateQuote503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, quoteInputSchema)
	if f.failed() {
		return gen.CreateQuote400ApplicationProblemPlusJSONResponse(invalid(invalidQuoteTitle, f)), nil
	}
	body := *req.Body
	requestID := uuid.UUID(body.ClientRequestId)

	// A repeated create answers what the first one made (O27).
	if repeat, err := s.repeatedQuote(ctx, requestID, body.CustomerId); repeat != nil || err != nil {
		if err != nil {
			return answer(err, resp)
		}
		return gen.CreateQuote200JSONResponse(*repeat), nil
	}

	in := headerInputOfQuote(body)
	reads, err := s.readHeader(ctx, in, nil)
	if err != nil {
		return answer(err, resp)
	}
	h := resolveHeader(&f, in, nil, reads)
	customerUsable(&f, reads.profile)
	var inputs []lineInput
	if body.Lines != nil {
		inputs = lineInputsOf(*body.Lines)
	}
	var templateID *int64
	if body.TemplateId != nil {
		t, tlines, found, err := s.readTemplate(ctx, *body.TemplateId)
		if err != nil {
			return answer(err, resp)
		}
		if !found {
			f.add("templateId", "There is no such template")
		} else {
			templateID = &t.ID
			applyTemplateTexts(&h, t, body.IntroText == nil, body.ClosingText == nil, body.DiscountVisible == nil)
			inputs = append(inputs, templateLineInputs(tlines)...)
		}
	}
	if f.failed() {
		return gen.CreateQuote400ApplicationProblemPlusJSONResponse(invalid(invalidQuoteTitle, f)), nil
	}
	var skipped []gen.OrdersSkippedLine
	priced, err := s.priceInputs(ctx, &inputs, &skipped, nil, h, false)
	if err != nil {
		return answer(err, resp)
	}
	createdBy, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.OrdersQuote
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		row, lines, err := s.insertQuote(ctx, tx, txq, newQuote{
			header: h, lines: priced, validUntil: dateIn(body.ValidUntil), templateID: templateID,
			channel: channelManual, clientRequestID: &requestID, createdBy: createdBy,
		})
		if err != nil {
			return err
		}
		out = quoteView(row, lines, nil)
		return nil
	})
	if db.IsUniqueViolation(err, "ux_quotes_client_request") {
		if repeat, err := s.repeatedQuote(ctx, requestID, body.CustomerId); repeat != nil || err != nil {
			if err != nil {
				return answer(err, resp)
			}
			return gen.CreateQuote200JSONResponse(*repeat), nil
		}
	}
	if err != nil {
		return answer(err, resp)
	}
	return gen.CreateQuote201JSONResponse(out), nil
}

// repeatedQuote is the quote an earlier create with requestID made, nil when
// none did; a repeat for another customer is client_request_reused.
func (s *server) repeatedQuote(ctx context.Context, requestID uuid.UUID, customerID int32) (*gen.OrdersQuote, error) {
	row, err := store.New(s.deps.Pool).QuoteByClientRequest(ctx, &requestID)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read the quote of request %s: %w", requestID, err)
	}
	if row.CustomerID != customerID {
		return nil, conflict(codeClientRequestReused, "The request id was used before",
			"An earlier create with this clientRequestId made a quote for another customer.")
	}
	out, err := s.getQuoteView(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// priceInputs reads the catalog and prices *inputs for a document with header
// h over existing (a save's current lines, nil otherwise), before any
// transaction. A skippable line — a template's or a copy's — whose variant
// went away is left out of *inputs and named in *skipped rather than refused
// (O23).
func (s *server) priceInputs(ctx context.Context, inputs *[]lineInput, skipped *[]gen.OrdersSkippedLine, existing []store.OrdersLine, h header, reprice bool) ([]pricedLine, error) {
	return s.priceInputsAt(ctx, inputs, skipped, existing, h, reprice, s.deps.Clock())
}

func (s *server) priceInputsAt(ctx context.Context, inputs *[]lineInput, skipped *[]gen.OrdersSkippedLine, existing []store.OrdersLine, h header, reprice bool, at time.Time) ([]pricedLine, error) {
	reads, err := s.readPricing(ctx, *inputs, h.Currency, at)
	if err != nil {
		return nil, err
	}
	*inputs = dropGone(*inputs, reads, skipped)
	priced, failures := priceLines(*inputs, byID(existing), reads, priceOptions{
		pricesIncludeVat: h.PricesIncludeVat, allowOptional: true, repriceKept: reprice,
	})
	if len(failures) > 0 {
		return nil, refuseLines(invalidQuoteTitle, failures)
	}
	return priced, nil
}

// UpdateQuote Save a draft quote
// (PUT /api/v1/orders/quotes/{id})
func (s *server) UpdateQuote(ctx context.Context, req gen.UpdateQuoteRequestObject) (gen.UpdateQuoteResponseObject, error) {
	resp := answers[gen.UpdateQuoteResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.UpdateQuoteResponseObject {
			return gen.UpdateQuote400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.UpdateQuoteResponseObject { return gen.UpdateQuote404Response{} },
		conflict: func(p gen.OrdersProblem) gen.UpdateQuoteResponseObject {
			return gen.UpdateQuote409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.UpdateQuoteResponseObject {
			return gen.UpdateQuote503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, quoteUpdateSchema)
	if f.failed() {
		return gen.UpdateQuote400ApplicationProblemPlusJSONResponse(invalid(invalidQuoteTitle, f)), nil
	}
	body := *req.Body
	cur, curLines, err := s.draftQuote(ctx, req.Id, body.Revision)
	if err != nil {
		return answer(err, resp)
	}
	base := headerOfQuote(cur)
	in := headerInputOfQuoteUpdate(body)
	reads, err := s.readHeader(ctx, in, &base)
	if err != nil {
		return answer(err, resp)
	}
	h := resolveHeader(&f, in, &base, reads)
	if in.CustomerID != cur.CustomerID {
		customerUsable(&f, reads.profile)
	}
	if f.failed() {
		return gen.UpdateQuote400ApplicationProblemPlusJSONResponse(invalid(invalidQuoteTitle, f)), nil
	}
	reprice, err := s.repriceKept(ctx, cur.Currency, h.Currency, cur.UpdatedAt)
	if err != nil {
		return nil, err
	}
	inputs := lineInputsOf(body.Lines)
	priced, err := s.priceInputs(ctx, &inputs, nil, curLines, h, reprice)
	if err != nil {
		return answer(err, resp)
	}
	validUntil := cur.ValidUntil
	if body.ValidUntil != nil {
		validUntil = dateIn(body.ValidUntil)
	}
	var out gen.OrdersQuote
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		row, lines, err := s.saveDraftQuote(ctx, tx, txq, req.Id, body.Revision, h, priced, validUntil, cur.TemplateID)
		if err != nil {
			return err
		}
		out = quoteView(row, lines, nil)
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.UpdateQuote200JSONResponse(out), nil
}

// draftQuote reads a quote and its lines for a write that needs a draft at
// revision: notFound, quote_not_draft or version_conflict otherwise.
func (s *server) draftQuote(ctx context.Context, id int64, revision int32) (store.OrdersQuote, []store.OrdersLine, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetQuote(ctx, id)
	if noRows(err) {
		return row, nil, notFound()
	}
	if err != nil {
		return row, nil, fmt.Errorf("orders: read quote %d: %w", id, err)
	}
	if err := judgeDraft(row, revision); err != nil {
		return row, nil, err
	}
	lines, err := linesOf(ctx, q, ownerQuote, id)
	return row, lines, err
}

// judgeDraft refuses a write to a quote that is not a draft, or not at the
// revision the client read.
func judgeDraft(row store.OrdersQuote, revision int32) error {
	if row.Status != quoteDraft {
		return conflict(codeQuoteNotDraft, "The quote is not a draft",
			fmt.Sprintf("Quote %d is %s; only a draft can be changed. Revise it first.", row.Number, row.Status))
	}
	if row.Revision != revision {
		return versionConflict("quote's revision", row.Revision, revision)
	}
	return nil
}

// repriceKept reports whether a draft's kept catalog lines are priced again
// on a save (O16): the currency moved, or the day the draft was last saved
// is not today.
func (s *server) repriceKept(ctx context.Context, fromCurrency, toCurrency string, lastSaved time.Time) (bool, error) {
	if fromCurrency != toCurrency {
		return true, nil
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return false, err
	}
	return !dayOf(lastSaved, zone).Equal(dayOf(s.deps.Clock(), zone)), nil
}

// saveDraftQuote writes a draft's new header and lines on tx: the variants'
// locks first (MB rule 21), then the quote's, judged again under it.
func (s *server) saveDraftQuote(ctx context.Context, tx pgx.Tx, txq *store.Queries, id int64, revision int32, h header, priced []pricedLine, validUntil pgtype.Date, templateID *int64) (store.OrdersQuote, []store.OrdersLine, error) {
	if err := s.lockLines(ctx, tx, invalidQuoteTitle, priced); err != nil {
		return store.OrdersQuote{}, nil, err
	}
	locked, err := txq.LockQuote(ctx, id)
	if noRows(err) {
		return locked, nil, notFound()
	}
	if err != nil {
		return locked, nil, fmt.Errorf("orders: lock quote %d: %w", id, err)
	}
	if err := judgeDraft(locked, revision); err != nil {
		return locked, nil, err
	}
	row, err := txq.UpdateQuoteDraft(ctx, updateQuoteParams(id, h, totalsOfPriced(priced), validUntil, templateID, s.deps.Clock()))
	if err != nil {
		return row, nil, fmt.Errorf("orders: save quote %d: %w", id, err)
	}
	lines, err := writeLines(ctx, txq, ownerQuote, id, priced)
	return row, lines, err
}

// DeleteQuote Delete a draft quote
// (DELETE /api/v1/orders/quotes/{id})
//
// Only a quote never sent is deleted (O54); its number is left a gap (O12).
func (s *server) DeleteQuote(ctx context.Context, req gen.DeleteQuoteRequestObject) (gen.DeleteQuoteResponseObject, error) {
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockQuote(ctx, req.Id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("orders: lock quote %d: %w", req.Id, err)
		}
		if row.Status != quoteDraft || row.SentAt != nil || row.Revision > 1 {
			return conflict(codeQuoteNotDraft, "The quote cannot be deleted",
				fmt.Sprintf("Quote %d has been sent; a sent quote is kept. Cancel it instead.", row.Number))
		}
		if err := txq.DeleteLinesOf(ctx, store.DeleteLinesOfParams{OwnerKind: ownerQuote, OwnerID: row.ID}); err != nil {
			return fmt.Errorf("orders: delete quote %d's lines: %w", row.ID, err)
		}
		if _, err := txq.DeleteQuoteRow(ctx, row.ID); err != nil {
			return fmt.Errorf("orders: delete quote %d: %w", row.ID, err)
		}
		return nil
	})
	if err != nil {
		return answer(err, answers[gen.DeleteQuoteResponseObject]{
			notFound: func() gen.DeleteQuoteResponseObject { return gen.DeleteQuote404Response{} },
			conflict: func(p gen.OrdersProblem) gen.DeleteQuoteResponseObject {
				return gen.DeleteQuote409ApplicationProblemPlusJSONResponse(p)
			},
		})
	}
	return gen.DeleteQuote204Response{}, nil
}

// changeQuoteState locks a quote, judges it with judge and writes it with
// write, on one transaction, and answers it as the API does.
func (s *server) changeQuoteState(ctx context.Context, id int64, judge func(store.OrdersQuote) error,
	write func(ctx context.Context, txq *store.Queries, row store.OrdersQuote) (store.OrdersQuote, error)) (gen.OrdersQuote, error) {
	var out gen.OrdersQuote
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockQuote(ctx, id)
		if noRows(err) {
			return notFound()
		}
		if err != nil {
			return fmt.Errorf("orders: lock quote %d: %w", id, err)
		}
		if err := judge(row); err != nil {
			return err
		}
		if row, err = write(ctx, txq, row); err != nil {
			return err
		}
		out, err = readQuoteView(ctx, txq, row)
		return err
	})
	return out, err
}

// judgeAnswerable refuses a quote that is not out with the customer — sent,
// or expired (O20) — or not at revision.
func judgeAnswerable(row store.OrdersQuote, revision int32) error {
	switch row.Status {
	case quoteSent, quoteExpired:
	case quoteAccepted:
		return conflict(codeQuoteAlreadyAccepted, "The quote was accepted",
			fmt.Sprintf("Quote %d was accepted already; a quote is accepted once. Copy it to make a new one.", row.Number))
	default:
		return conflict(codeQuoteNotSent, "The quote is not sent",
			fmt.Sprintf("Quote %d is %s; only a sent or expired quote can be answered.", row.Number, row.Status))
	}
	if row.Revision != revision {
		return versionConflict("quote's revision", row.Revision, revision)
	}
	return nil
}

// ReviseQuote Revise a sent quote
// (POST /api/v1/orders/quotes/{id}/revise)
func (s *server) ReviseQuote(ctx context.Context, req gen.ReviseQuoteRequestObject) (gen.ReviseQuoteResponseObject, error) {
	resp := answers[gen.ReviseQuoteResponseObject]{
		notFound: func() gen.ReviseQuoteResponseObject { return gen.ReviseQuote404Response{} },
		conflict: func(p gen.OrdersProblem) gen.ReviseQuoteResponseObject {
			return gen.ReviseQuote409ApplicationProblemPlusJSONResponse(p)
		},
	}
	out, err := s.changeQuoteState(ctx, req.Id,
		func(row store.OrdersQuote) error { return judgeAnswerable(row, req.Body.Revision) },
		func(ctx context.Context, txq *store.Queries, row store.OrdersQuote) (store.OrdersQuote, error) {
			return txq.ReviseQuoteRow(ctx, store.ReviseQuoteRowParams{ID: row.ID, Now: s.deps.Clock()})
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.ReviseQuote200JSONResponse(out), nil
}

// DeclineQuote Decline a quote
// (POST /api/v1/orders/quotes/{id}/decline)
func (s *server) DeclineQuote(ctx context.Context, req gen.DeclineQuoteRequestObject) (gen.DeclineQuoteResponseObject, error) {
	resp := answers[gen.DeclineQuoteResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.DeclineQuoteResponseObject {
			return gen.DeclineQuote400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.DeclineQuoteResponseObject { return gen.DeclineQuote404Response{} },
		conflict: func(p gen.OrdersProblem) gen.DeclineQuoteResponseObject {
			return gen.DeclineQuote409ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, declineInputSchema)
	var reason *string
	if req.Body.Reason != nil {
		reason = textPtr(*req.Body.Reason)
		if reason != nil && utf8.RuneCountInString(*reason) > 500 {
			f.add("reason", "A reason is at most 500 characters")
		}
	}
	if f.failed() {
		return gen.DeclineQuote400ApplicationProblemPlusJSONResponse(invalid(invalidQuoteTitle, f)), nil
	}
	out, err := s.changeQuoteState(ctx, req.Id,
		func(row store.OrdersQuote) error { return judgeAnswerable(row, req.Body.Revision) },
		func(ctx context.Context, txq *store.Queries, row store.OrdersQuote) (store.OrdersQuote, error) {
			return txq.DeclineQuoteRow(ctx, store.DeclineQuoteRowParams{ID: row.ID, Now: ptr(s.deps.Clock()), Reason: reason})
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.DeclineQuote200JSONResponse(out), nil
}

// CancelQuote Cancel a quote
// (POST /api/v1/orders/quotes/{id}/cancel)
func (s *server) CancelQuote(ctx context.Context, req gen.CancelQuoteRequestObject) (gen.CancelQuoteResponseObject, error) {
	resp := answers[gen.CancelQuoteResponseObject]{
		notFound: func() gen.CancelQuoteResponseObject { return gen.CancelQuote404Response{} },
		conflict: func(p gen.OrdersProblem) gen.CancelQuoteResponseObject {
			return gen.CancelQuote409ApplicationProblemPlusJSONResponse(p)
		},
	}
	out, err := s.changeQuoteState(ctx, req.Id,
		func(row store.OrdersQuote) error {
			switch row.Status {
			case quoteDraft, quoteSent:
			case quoteAccepted:
				return conflict(codeQuoteAlreadyAccepted, "The quote was accepted",
					fmt.Sprintf("Quote %d was accepted; cancel the order made from it instead.", row.Number))
			default:
				return conflict(codeQuoteNotSent, "The quote was answered",
					fmt.Sprintf("Quote %d is %s; only a draft or a sent quote can be cancelled.", row.Number, row.Status))
			}
			if row.Revision != req.Body.Revision {
				return versionConflict("quote's revision", row.Revision, req.Body.Revision)
			}
			return nil
		},
		func(ctx context.Context, txq *store.Queries, row store.OrdersQuote) (store.OrdersQuote, error) {
			return txq.CancelQuoteRow(ctx, store.CancelQuoteRowParams{ID: row.ID, Now: ptr(s.deps.Clock())})
		})
	if err != nil {
		return answer(err, resp)
	}
	return gen.CancelQuote200JSONResponse(out), nil
}

// CopyQuote Copy a quote
// (POST /api/v1/orders/quotes/{id}/copy)
//
// A copy is a new draft of any quote (O20), its origin channel copy and its
// source the quote at its revision (O27). The customer's snapshot is taken
// afresh and the catalog lines priced again, as a new draft's are; a manual
// price is kept, and a variant gone from the catalog is left out.
func (s *server) CopyQuote(ctx context.Context, req gen.CopyQuoteRequestObject) (gen.CopyQuoteResponseObject, error) {
	resp := answers[gen.CopyQuoteResponseObject]{
		notFound: func() gen.CopyQuoteResponseObject { return gen.CopyQuote404Response{} },
		unavailable: func(p gen.OrdersProblem) gen.CopyQuoteResponseObject {
			return gen.CopyQuote503ApplicationProblemPlusJSONResponse(p)
		},
	}
	q := store.New(s.deps.Pool)
	src, err := q.GetQuote(ctx, req.Id)
	if noRows(err) {
		return gen.CopyQuote404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("orders: read quote %d: %w", req.Id, err)
	}
	srcLines, err := linesOf(ctx, q, ownerQuote, src.ID)
	if err != nil {
		return nil, err
	}
	base := headerOfQuote(src)
	in := headerInput{CustomerID: src.CustomerID}
	reads, err := s.readHeader(ctx, in, &base)
	if err != nil {
		return answer(err, resp)
	}
	var f fieldErrors
	h := resolveHeader(&f, in, &base, reads)
	if f.failed() {
		// The customer, its address or its contact went away: the copy takes
		// the snapshot the quote holds.
		h = base
	}
	if reads.salesperson == nil {
		h.SalespersonID, h.SalespersonDisplay = base.SalespersonID, base.SalespersonDisplay
	}
	inputs := copyInputs(srcLines)
	var skipped []gen.OrdersSkippedLine
	priced, err := s.priceInputs(ctx, &inputs, &skipped, nil, h, true)
	if err != nil {
		return answer(err, resp)
	}
	createdBy, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.OrdersQuote
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		row, lines, err := s.insertQuote(ctx, tx, txq, newQuote{
			header: h, lines: priced, templateID: src.TemplateID, channel: channelCopy,
			sourceModule: ptr(moduleName), sourceKind: ptr(ownerQuote), sourceID: ptr(quoteSource(src.ID, src.Revision)),
			createdBy: createdBy,
		})
		if err != nil {
			return err
		}
		out = quoteView(row, lines, nil)
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	return gen.CopyQuote201JSONResponse(out), nil
}

// copyInputs are a document's lines as a new draft takes them: a catalog
// line to be priced again, a manual price kept as the net it is.
func copyInputs(lines []store.OrdersLine) []lineInput {
	out := make([]lineInput, 0, len(lines))
	for _, l := range lines {
		in := lineInput{kind: l.Kind, variantID: l.VariantID, name: l.Name, description: ptr(l.Description), optional: l.Optional, skippable: true}
		if l.Kind == kindProduct {
			in.unit = ptr(l.Unit)
			in.quantity = wirePtr(ratOf(l.Quantity), quantityPlaces)
			in.discount = wirePtr(ratOrZero(l.DiscountPercent), discountPlaces)
			in.vatCodeID = l.VatCodeID
			if l.PackagingID != nil && l.PackagingQuantity.Valid {
				in.packagingID, in.packagingQuantity = l.PackagingID, wirePtr(ratOf(l.PackagingQuantity), quantityPlaces)
			}
			if l.UnitPrice.Valid && (l.PriceSource == nil || *l.PriceSource != priceCatalog) {
				in.unitPriceNet = ratOf(l.UnitPrice)
			}
			if l.VariantID != nil {
				in.vatCodeID = nil
			}
		}
		if l.Kind != kindProduct {
			in.name = l.Name
		}
		out = append(out, in)
	}
	return out
}
