package purchasing

// Register invoice (purchasing design P37, A1, A11; module-boundaries rule
// 26): Accounting's supplier-invoice draft made from an order through
// contracts.SupplierInvoices.CreateDraft, with one line per order line of
// what is billable and not invoiced, and the provisional matches with it.
// The draft is made on the pool, on Accounting's own transaction, before
// this module's opens; Purchasing then records the request and the matches.
// The draft is the person's to complete and post, or to delete.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// maxDraftLines is what one draft holds (Accounting's
// maxSupplierInvoiceLines, A1); an order with more is invoiced in parts.
const maxDraftLines = 200

// registrationHash is what a replay is judged by: the order and the body
// without its clientRequestId.
func registrationHash(orderID int64, b gen.PurchasingInvoiceRegisterInput) string {
	b.ClientRequestId = [16]byte{}
	body, _ := json.Marshal(struct {
		OrderID int64                              `json:"orderId"`
		Body    gen.PurchasingInvoiceRegisterInput `json:"body"`
	}{orderID, b})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// draftLine is one planned line of the draft: the order line, what of it,
// and at what net.
type draftLine struct {
	line     store.PurchasingOrderLine
	quantity *big.Rat
	net      *big.Rat
}

// draftText is a draft line's text: the order line's name and what of it.
func draftText(l store.PurchasingOrderLine, quantity *big.Rat) string {
	name := l.Name
	if name == "" {
		name = l.Description
	}
	text := fmt.Sprintf("%s — %s %s", name, trimmed(quantity, quantityPlaces), l.Unit)
	if utf8.RuneCountInString(text) > 500 {
		text = string([]rune(text)[:500])
	}
	return strings.TrimSpace(text)
}

// registrationResult answers a registration as it was recorded.
func (s *server) registrationResult(ctx context.Context, r store.PurchasingInvoiceRegistration) (gen.PurchasingInvoiceRegistration, error) {
	q := s.store()
	o, err := q.GetOrder(ctx, r.OrderID)
	if err != nil {
		return gen.PurchasingInvoiceRegistration{}, fmt.Errorf("purchasing: read order %d: %w", r.OrderID, err)
	}
	matches, err := q.InvoiceMatches(ctx, r.SupplierInvoiceID)
	if err != nil {
		return gen.PurchasingInvoiceRegistration{}, fmt.Errorf("purchasing: read invoice %d's matches: %w", r.SupplierInvoiceID, err)
	}
	total := new(big.Rat)
	for _, m := range matches {
		total.Add(total, ratOrZero(m.LineNet))
	}
	return gen.PurchasingInvoiceRegistration{SupplierInvoiceId: r.SupplierInvoiceID, Lines: r.Lines, LinesLeft: r.LinesLeft,
		Total: amountText(total), Currency: o.Currency}, nil
}

// registeredBefore answers the registration a clientRequestId made: the
// same order and body is that one; anything else is client_request_reused.
func (s *server) registeredBefore(ctx context.Context, id [16]byte, orderID int64, hash string) (*store.PurchasingInvoiceRegistration, error) {
	prior, err := s.store().RegistrationByRequest(ctx, id)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("purchasing: read the registration of a request: %w", err)
	}
	if prior.OrderID != orderID || prior.RequestHash != hash {
		return nil, clientRequestReused()
	}
	return &prior, nil
}

// draftRefused is Accounting's refusal of the draft as this module's:
// books_not_set_up, supplier_account_missing and duplicate_supplier_invoice
// are 409, a field rule 400 naming Accounting's fields, which are this
// request's names too.
func draftRefused(err error) error {
	var r *contracts.SupplierInvoiceRefusal
	if !errors.As(err, &r) {
		return err
	}
	switch r.Code {
	case contracts.SupplierInvoiceBooksNotSetUp:
		return conflict(codeBooksNotSetUp, "The books are not set up", r.Detail)
	case contracts.SupplierInvoiceAccountMissing:
		return conflict(codeSupplierAccountMissing, "The supplier has no default account",
			"Set the supplier's default account in Accounting, then register the invoice again.")
	case contracts.SupplierInvoiceDuplicate:
		return conflict(codeDuplicateSupplierInvoice, "The supplier's invoice is already entered", r.Detail)
	}
	var f fieldErrors
	for _, field := range r.Fields {
		f.add(field.Field, field.Message)
	}
	if !f.failed() {
		f.add("invoiceNumber", r.Detail)
	}
	f.code = codeSupplierInvoiceInvalid
	p := invalid(invalidInvoiceTitle, f)
	p.Detail = ptr(r.Detail)
	return refuse(p)
}

// RegisterPurchasingInvoice Register a supplier invoice from a purchase order
// (POST /api/v1/purchasing/orders/{orderId}/register-invoice)
func (s *server) RegisterPurchasingInvoice(ctx context.Context, req gen.RegisterPurchasingInvoiceRequestObject) (gen.RegisterPurchasingInvoiceResponseObject, error) {
	resp := answers[gen.RegisterPurchasingInvoiceResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.RegisterPurchasingInvoiceResponseObject {
			return gen.RegisterPurchasingInvoice400ApplicationProblemPlusJSONResponse(p)
		},
		forbidden: func(p gen.PurchasingProblem) gen.RegisterPurchasingInvoiceResponseObject {
			return gen.RegisterPurchasingInvoice403ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.RegisterPurchasingInvoiceResponseObject {
			return gen.RegisterPurchasingInvoice404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.RegisterPurchasingInvoiceResponseObject {
			return gen.RegisterPurchasingInvoice409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.RegisterPurchasingInvoiceResponseObject {
			return gen.RegisterPurchasingInvoice503ApplicationProblemPlusJSONResponse(p)
		},
	}
	// The draft is Accounting's: making one needs Accounting's own key
	// beside the router's purchasing:invoices-match (A1).
	if !s.has(ctx, permAccountingPost) {
		return answer(refuse(problem(http.StatusForbidden, codeAccountingPostRequired, "Entering a supplier invoice is not yours to do",
			"Register invoice makes a supplier invoice in Accounting, which needs accounting:post as well as purchasing:invoices-match.")), resp)
	}
	b := *req.Body
	var f fieldErrors
	number := strings.TrimSpace(b.InvoiceNumber)
	if number == "" || utf8.RuneCountInString(number) > 60 {
		f.add("invoiceNumber", "The supplier's invoice number is 1 to 60 characters")
	}
	if b.DueDate != nil && b.DueDate.Before(b.InvoiceDate.Time) {
		f.add("dueDate", "The due date is on or after the invoice date")
	}
	var rate, totalNOK, printed string
	if b.Total != nil && strings.TrimSpace(*b.Total) != "" {
		if v, ok := parseDecimal(strings.TrimSpace(*b.Total), amountPlaces, maxAmount); !ok || v.Sign() <= 0 {
			f.add("total", "A total is more than 0, with at most two decimals")
		} else {
			printed = amountText(v)
		}
	}
	if b.ExchangeRate != nil && strings.TrimSpace(*b.ExchangeRate) != "" {
		if v, ok := parseDecimal(strings.TrimSpace(*b.ExchangeRate), 6, maxPrice); !ok || v.Sign() <= 0 {
			f.add("exchangeRate", "A rate is more than 0, with at most six decimals")
		} else {
			rate = v.FloatString(6)
		}
	}
	if b.TotalNok != nil && strings.TrimSpace(*b.TotalNok) != "" {
		if v, ok := parseDecimal(strings.TrimSpace(*b.TotalNok), amountPlaces, maxAmount); !ok || v.Sign() <= 0 {
			f.add("totalNok", "A total is more than 0, with at most two decimals")
		} else {
			totalNOK = amountText(v)
		}
	}
	if f.failed() {
		return answer(refuseInvalid(invalidInvoiceTitle, f), resp)
	}
	hash := registrationHash(req.OrderId, b)
	if prior, err := s.registeredBefore(ctx, b.ClientRequestId, req.OrderId, hash); err != nil {
		return answer(err, resp)
	} else if prior != nil {
		out, err := s.registrationResult(ctx, *prior)
		if err != nil {
			return nil, err
		}
		return gen.RegisterPurchasingInvoice201JSONResponse(out), nil
	}
	o, lines, err := s.orderAndLines(ctx, req.OrderId)
	if err != nil {
		return answer(err, resp)
	}
	if o.Status == statusDraft || o.Status == statusCancelled {
		return answer(conflict(codeOrderNotSent, "The order is not sent", "An invoice is registered from a sent order."), resp)
	}
	// Provisional matches count as invoiced (A11): a second draft takes
	// only what the first left.
	var planned []draftLine
	left := int32(0)
	for _, l := range lines {
		q := toInvoice(l)
		if q.Sign() <= 0 {
			continue
		}
		if len(planned) == maxDraftLines {
			left++
			continue
		}
		planned = append(planned, draftLine{line: l, quantity: q, net: netOf(l, q)})
	}
	if len(planned) == 0 {
		return answer(conflict(codeNothingToInvoice, "Nothing to invoice",
			"Nothing on the order is received, or marked delivered, and not invoiced already."), resp)
	}
	total := new(big.Rat)
	draft := contracts.SupplierInvoiceDraft{SupplierID: o.SupplierID, Kind: invoiceKind, Number: number,
		InvoiceDate: b.InvoiceDate.Time, Currency: o.Currency, ExchangeRate: rate, TotalNOK: totalNOK,
		Reference: strconv.FormatInt(deref64(o.Number), 10), By: callerID(ctx)}
	if b.DueDate != nil {
		draft.DueDate = ptr(b.DueDate.Time)
	}
	draft.Description = fmt.Sprintf("Innkjøpsordre %d", deref64(o.Number))
	if o.Language == "en" {
		draft.Description = fmt.Sprintf("Purchase order %d", deref64(o.Number))
	}
	for _, p := range planned {
		total.Add(total, p.net)
		draft.Lines = append(draft.Lines, contracts.SupplierInvoiceDraftLine{Text: draftText(p.line, p.quantity), Amount: amountText(p.net)})
	}
	// The total is the supplier's, VAT included, when the person gave it;
	// the lines' net otherwise, for the person to correct before posting.
	draft.Total = amountText(total)
	if printed != "" {
		draft.Total = printed
	}
	invoiceID, err := s.createSupplierInvoiceDraft(ctx, draft)
	if err != nil {
		// Two replays of one request race to the same invoice number: the
		// loser answers the winner's draft once it is recorded.
		var r *contracts.SupplierInvoiceRefusal
		if errors.As(err, &r) && r.Code == contracts.SupplierInvoiceDuplicate {
			if prior, perr := s.registeredBefore(ctx, b.ClientRequestId, req.OrderId, hash); perr == nil && prior != nil {
				out, err := s.registrationResult(ctx, *prior)
				if err != nil {
					return nil, err
				}
				return gen.RegisterPurchasingInvoice201JSONResponse(out), nil
			}
		}
		return answer(draftRefused(err), resp)
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	inv := &contracts.SupplierInvoice{ID: invoiceID, SupplierID: o.SupplierID, Kind: invoiceKind, Number: number,
		InvoiceDate: b.InvoiceDate.Time, Currency: o.Currency}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		now := s.deps.Clock()
		st, err := lockInvoice(ctx, txq, invoiceID, now)
		if err != nil {
			return err
		}
		locked, err := txq.LockOrder(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: lock order %d: %w", o.ID, err)
		}
		if err := txq.InsertRegistration(ctx, store.InsertRegistrationParams{ClientRequestID: b.ClientRequestId, RequestHash: hash,
			OrderID: o.ID, SupplierInvoiceID: invoiceID, Lines: int32(len(planned)), LinesLeft: left, Now: now}); err != nil {
			return err
		}
		for i, p := range planned {
			if _, err := txq.InsertMatch(ctx, matchInsert(inv, int32(i+1), locked, p.line.ID, p.quantity, p.net, st, by, now)); err != nil {
				return fmt.Errorf("purchasing: match the draft's line %d: %w", i+1, err)
			}
			if err := txq.AddInvoiced(ctx, store.AddInvoicedParams{ID: p.line.ID, Delta: numeric(p.quantity, quantityPlaces)}); err != nil {
				return fmt.Errorf("purchasing: move line %d's invoiced: %w", p.line.ID, err)
			}
		}
		return nil
	})
	if db.IsUniqueViolation(err, "invoice_registrations_pkey") {
		if prior, perr := s.registeredBefore(ctx, b.ClientRequestId, req.OrderId, hash); perr != nil {
			return answer(perr, resp)
		} else if prior != nil {
			out, err := s.registrationResult(ctx, *prior)
			if err != nil {
				return nil, err
			}
			return gen.RegisterPurchasingInvoice201JSONResponse(out), nil
		}
	}
	if err != nil {
		return answer(err, resp)
	}
	return gen.RegisterPurchasingInvoice201JSONResponse(gen.PurchasingInvoiceRegistration{SupplierInvoiceId: invoiceID,
		Lines: int32(len(planned)), LinesLeft: left, Total: amountText(total), Currency: o.Currency}), nil
}

// dayEnd is the instant a calendar day ends in zone.
func dayEnd(day time.Time, zone *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone).AddDate(0, 0, 1)
}
