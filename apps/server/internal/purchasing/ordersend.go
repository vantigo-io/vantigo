package purchasing

// Sending an order (P18, P20, P21, A11, A19): Send, Mark as sent and Send
// again. Send and Mark as sent freeze a draft in one transaction — the
// variants locked, the supplier held FOR SHARE (A8), then the order: its
// number from the gap-free series, version 1, the supplier's and the
// delivery address's snapshots, how it was sent, the change log and
// purchasing.order.sent.v1. After the commit the PDF is rendered from the
// committed rows, stored and recorded, and a Send mails it synchronously
// through contracts.OutboundMail from the settings' channel, as Orders does.
// A mail the provider refuses leaves the order sent with its mailing queued
// for the purchasing.mail worker, and answers 503 mail_failed. Both take a
// clientRequestId: a replay with the same body answers the first result,
// another body under the same id is 409 client_request_reused.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	netmail "net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/store"
)

// sendTimeout bounds one send; mailingRowTimeout the row that logs it.
const (
	sendTimeout       = 30 * time.Second
	mailingRowTimeout = 5 * time.Second
)

// messageIDDomain is the right-hand side of a Message-ID this module makes.
const messageIDDomain = "vantigo.invalid"

// How an order was sent (P18).
const (
	sendMail   = "mail"
	sendManual = "manual"
)

// Mailing states (P21).
const (
	mailingQueued = "queued"
	mailingSent   = "sent"
	mailingFailed = "failed"
)

// mailRequest is a send's dialog fields, judged: the recipient, the copies, a
// subject over the mail text's, and the message put in the body.
type mailRequest struct {
	recipient string
	cc        []string
	subject   string
	message   string
}

// validRecipient is s as a bare e-mail address, or false.
func validRecipient(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 {
		return s, false
	}
	a, err := netmail.ParseAddress(s)
	return s, err == nil && a.Address == s
}

// readMailRequest judges a send's dialog fields into f.
func readMailRequest(f *fieldErrors, recipient *openapi_types.Email, cc *[]openapi_types.Email, subject, message *string) mailRequest {
	var out mailRequest
	if recipient != nil && strings.TrimSpace(string(*recipient)) != "" {
		addr, ok := validRecipient(string(*recipient))
		if !ok {
			f.add("recipient", "This is not an e-mail address")
		}
		out.recipient = addr
	}
	if cc != nil {
		if len(*cc) > 10 {
			f.add("cc", "At most 10 copies")
		}
		for i, c := range *cc {
			addr, ok := validRecipient(string(c))
			if !ok {
				f.add(fmt.Sprintf("cc[%d]", i), "This is not an e-mail address")
				continue
			}
			out.cc = append(out.cc, addr)
		}
	}
	if subject != nil {
		out.subject = strings.TrimSpace(*subject)
		if utf8.RuneCountInString(out.subject) > 300 {
			f.add("subject", "A subject is at most 300 characters")
		}
	}
	if message != nil {
		out.message = *message
		if utf8.RuneCountInString(out.message) > 4000 {
			f.add("message", "A message is at most 4000 characters")
		}
	}
	return out
}

// requestHash is the hash a send's replay is judged by (A11): the operation,
// the version it was read at and the dialog's fields.
func requestHash(op string, expected time.Time, m mailRequest) string {
	body, _ := json.Marshal(struct {
		Op        string   `json:"op"`
		Expected  string   `json:"expected"`
		Recipient string   `json:"recipient"`
		Cc        []string `json:"cc"`
		Subject   string   `json:"subject"`
		Message   string   `json:"message"`
	}{op, expected.UTC().Format(time.RFC3339Nano), m.recipient, m.cc, m.subject, m.message})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// newMessageID is a send's Message-ID, bare as mail.Outbound takes it.
func newMessageID() string { return "purchasing-" + uuid.NewString() + "@" + messageIDDomain }

// truncate is s cut to n characters.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// withoutAddress is msg with the mailing's addresses — the recipient's and
// the copies' — taken out: a provider's refusal often quotes them.
func withoutAddress(msg string, addresses ...string) string {
	for _, a := range addresses {
		if a = strings.TrimSpace(a); a != "" {
			msg = strings.ReplaceAll(msg, a, "<recipient>")
		}
	}
	return msg
}

// envelope is one mailing's mail: the cover mail in the order's language,
// the dialog's subject over it when given, the PDF attached, our company's
// name beside the channel's address and replies to its e-mail.
func envelope(o store.PurchasingOrder, kind string, version int32, m mailRequest, seller contracts.SellerProfile, pdf []byte, messageID string) mail.Outbound {
	number := int64(0)
	if o.Number != nil {
		number = *o.Number
	}
	text := coverMail(mailFacts{kind: kind, language: o.Language, number: number, version: version, seller: seller.LegalName, message: m.message})
	if m.subject != "" {
		text.subject = m.subject
	}
	printed := o
	printed.Version = version
	return mail.Outbound{
		DisplayName: seller.LegalName, To: []string{m.recipient}, Cc: m.cc, Subject: text.subject, ReplyTo: seller.Email,
		TextBody: text.body, MessageID: messageID,
		Attachments: []mail.Attachment{{FileName: fileName(printed, kind), ContentType: "application/pdf", Content: pdf}},
	}
}

// queueMailing writes the mailing a Send, a Send again or a Cancel asks for
// inside that request's transaction (A19): the outbox row, queued, its
// stored PDF not yet named, held by the request until now + mailLease — the
// purchasing.mail worker takes it only after that, so a request that dies
// between its commit and its mail leaves the worker to send it. o is the
// order as the transaction wrote it.
func queueMailing(ctx context.Context, txq *store.Queries, o store.PurchasingOrder, kind string, version int32, m mailRequest,
	channel uuid.UUID, seller contracts.SellerProfile, by actor, now time.Time,
) (store.PurchasingMailing, error) {
	subject := envelope(o, kind, version, m, seller, nil, "").Subject
	row, err := txq.InsertMailing(ctx, store.InsertMailingParams{
		OrderID: o.ID, Kind: kind, Version: version, Recipient: m.recipient, Cc: strings.Join(m.cc, ", "),
		Subject: truncate(subject, 300), Message: truncate(m.message, 4000), ChannelID: &channel, Status: mailingQueued,
		NextAttemptAt: ptr(now.Add(mailLease)), SentByUserID: by.id, SentByDisplay: by.display, CreatedAt: now,
	})
	if err != nil {
		return row, fmt.Errorf("purchasing: queue the mailing of order %d: %w", o.ID, err)
	}
	return row, nil
}

// mailQueued is the request's own attempt, after its commit, at the mailing
// its transaction queued: claimed under the hold the insert wrote, then
// delivered as the worker delivers. It answers the mailing as it now stands
// and whether it went out; a mailing that did not stays queued for the
// worker (or failed, when it can never be sent).
func (s *server) mailQueued(ctx context.Context, queued store.PurchasingMailing) (store.PurchasingMailing, bool, error) {
	uncancelled := context.WithoutCancel(ctx)
	now := s.deps.Clock()
	stepCtx, cancel := context.WithTimeout(uncancelled, mailingRowTimeout)
	row, err := store.New(s.deps.Pool).ClaimHeldMailing(stepCtx, store.ClaimHeldMailingParams{
		ID: queued.ID, Held: *queued.NextAttemptAt, LeaseUntil: now.Add(mailLease), MessageID: newMessageID(),
	})
	cancel()
	if noRows(err) {
		return queued, false, nil
	}
	if err != nil {
		return queued, false, fmt.Errorf("purchasing: claim mailing %d: %w", queued.ID, err)
	}
	sent := s.deliver(uncancelled, row)
	stepCtx, cancel = context.WithTimeout(uncancelled, mailingRowTimeout)
	defer cancel()
	current, err := store.New(s.deps.Pool).GetMailing(stepCtx, row.ID)
	if err != nil {
		return row, sent, fmt.Errorf("purchasing: read mailing %d: %w", row.ID, err)
	}
	return current, sent, nil
}

// sendSpec is a Send or a Mark as sent.
type sendSpec struct {
	method          string
	clientRequestID uuid.UUID
	hash            string
	expected        time.Time
	mail            mailRequest
}

// errReplay marks a send whose clientRequestId this order was sent under
// with the same body: the first result is answered.
type errReplay struct{}

func (errReplay) Error() string { return "purchasing: a replay of an earlier send" }

// send sends or marks as sent, and answers the result.
func (s *server) send(ctx context.Context, orderID int64, spec sendSpec) (gen.PurchasingSendResult, error) {
	q := s.store()
	r, err := readOrder(ctx, q, orderID)
	if err != nil {
		return gen.PurchasingSendResult{}, err
	}
	o := r.order
	if prior, err := q.OrderBySendRequest(ctx, &spec.clientRequestID); err == nil {
		if prior.ID != o.ID || deref(prior.SendRequestHash) != spec.hash {
			return gen.PurchasingSendResult{}, clientRequestReused()
		}
		return s.sendResult(ctx, o.ID, spec.method == sendMail)
	} else if !noRows(err) {
		return gen.PurchasingSendResult{}, fmt.Errorf("purchasing: read the send of %s: %w", spec.clientRequestID, err)
	}
	if o.Status != statusDraft {
		return gen.PurchasingSendResult{}, conflict(codeOrderNotDraft, "The order is not a draft", "The order is sent already; send it again instead.")
	}
	if err := judgeFresh(o.UpdatedAt, spec.expected); err != nil {
		return gen.PurchasingSendResult{}, err
	}
	if len(r.lines) == 0 {
		return gen.PurchasingSendResult{}, refuse(problem(http.StatusBadRequest, codeOrderEmpty, invalidSendTitle, "An order is sent with at least one line."))
	}
	e, err := s.supplier(ctx, o.SupplierID)
	if err != nil {
		return gen.PurchasingSendResult{}, err
	}
	if err := judgeSupplier(e, o.SupplierID); err != nil {
		return gen.PurchasingSendResult{}, err
	}
	var channel uuid.UUID
	if spec.method == sendMail {
		var ok bool
		if channel, ok, err = s.sendingChannel(ctx); err != nil {
			return gen.PurchasingSendResult{}, err
		}
		if !ok {
			return gen.PurchasingSendResult{}, sendingDisabled()
		}
		if spec.mail.recipient == "" {
			spec.mail.recipient = strings.TrimSpace(e.Email)
		}
		if spec.mail.recipient == "" {
			return gen.PurchasingSendResult{}, refuseField(invalidSendTitle, "recipient", codeRecipientMissing,
				"The supplier has no e-mail in the register; name a recipient")
		}
	}
	sites, _, err := s.sites(ctx)
	if err != nil {
		return gen.PurchasingSendResult{}, err
	}
	settings, err := readSettings(ctx, q)
	if err != nil {
		return gen.PurchasingSendResult{}, err
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return gen.PurchasingSendResult{}, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return gen.PurchasingSendResult{}, err
	}
	var queued store.PurchasingMailing
	var variants []int32
	for _, l := range r.lines {
		if l.VariantID != nil {
			variants = append(variants, *l.VariantID)
		}
	}
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := s.lockVariants(ctx, tx, variants); err != nil {
			return err
		}
		held, err := s.lockSuppliers(ctx, tx, o.SupplierID)
		if err != nil {
			return err
		}
		entry, ok := held[o.SupplierID]
		if !ok {
			return judgeSupplier(nil, o.SupplierID)
		}
		if err := judgeSupplier(&entry, o.SupplierID); err != nil {
			return err
		}
		locked, err := txq.LockOrder(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: lock order %d: %w", o.ID, err)
		}
		if locked.SendClientRequestID != nil && *locked.SendClientRequestID == spec.clientRequestID {
			if deref(locked.SendRequestHash) != spec.hash {
				return clientRequestReused()
			}
			return errReplay{}
		}
		if locked.Status != statusDraft {
			return conflict(codeOrderNotDraft, "The order is not a draft", "The order is sent already; send it again instead.")
		}
		if err := judgeFresh(locked.UpdatedAt, spec.expected); err != nil {
			return err
		}
		number, err := txq.AllocateNumber(ctx, store.AllocateNumberParams{CounterName: seriesOrder, SeriesStart: settings.orderSeriesStart})
		if err != nil {
			return fmt.Errorf("purchasing: allocate an order number: %w", err)
		}
		siteName := ""
		if site, ok := sites[locked.SiteID]; ok {
			siteName = site.Name
		}
		dl := deliveryAddress(settings, seller, locked.SiteID, siteName)
		snap := snapshotOf(entry)
		now := s.deps.Clock()
		sent, err := txq.SendOrder(ctx, store.SendOrderParams{
			ID: o.ID, Number: &number, SupplierNumber: snap.number, SupplierName: snap.name,
			SupplierOrganisationNumber: snap.organisationNumber, SupplierAddressLine: snap.addressLine,
			SupplierPostalCode: snap.postalCode, SupplierCity: snap.city, SupplierCountry: snap.country, SupplierEmail: snap.email,
			DeliveryName: dl.name, DeliveryAddressLine1: dl.line1, DeliveryAddressLine2: dl.line2, DeliveryPostalCode: dl.postalCode,
			DeliveryCity: dl.city, DeliveryCountry: dl.country, Now: &now, SentByUserID: by.id, SentByDisplay: by.display,
			SendMethod: &spec.method, ClientRequestID: &spec.clientRequestID, RequestHash: &spec.hash,
		})
		if err != nil {
			return fmt.Errorf("purchasing: send order %d: %w", o.ID, err)
		}
		action := "sent"
		if spec.method == sendManual {
			action = "marked_sent"
		}
		if err := logChange(ctx, txq, o.ID, by, now, change{Action: action}); err != nil {
			return err
		}
		ev := contracts.PurchaseOrderSent{
			OrderID: sent.ID, Number: number, SupplierID: sent.SupplierID, SiteID: sent.SiteID, Currency: sent.Currency,
			NetTotal: amountText(ratOrZero(sent.NetTotal)), OrderDate: sent.OrderDate.Time.Format(time.DateOnly),
			ExpectedOn: dayText(timeOfDate(sent.ExpectedOn)), SendMethod: spec.method, SentAt: now,
		}
		if err := events.Publish(ctx, tx, moduleName, ev, now); err != nil {
			return fmt.Errorf("purchasing: publish the send of order %d: %w", o.ID, err)
		}
		if spec.method == sendMail {
			queued, err = queueMailing(ctx, txq, sent, docOrder, sent.Version, spec.mail, channel, seller, by, now)
			return err
		}
		return nil
	})
	if errors.As(err, new(errReplay)) {
		return s.sendResult(ctx, o.ID, spec.method == sendMail)
	}
	if db.IsUniqueViolation(err, "ux_orders_send_client_request") {
		return gen.PurchasingSendResult{}, clientRequestReused()
	}
	if err != nil {
		return gen.PurchasingSendResult{}, err
	}
	if spec.method != sendMail {
		if _, _, err := s.ensureDocument(ctx, o.ID, docOrder); err != nil {
			return gen.PurchasingSendResult{}, err
		}
		return s.sendResult(ctx, o.ID, false)
	}
	if _, sent, err := s.mailQueued(ctx, queued); err != nil {
		return gen.PurchasingSendResult{}, err
	} else if !sent {
		return gen.PurchasingSendResult{}, mailFailed()
	}
	return s.sendResult(ctx, o.ID, true)
}

// clientRequestReused is the 409 of a clientRequestId sent before with
// another body, or for another order (A11).
func clientRequestReused() error {
	return conflict(codeClientRequestReused, "The request id was used before",
		"This clientRequestId was sent before with another body or for another order; make a new one.")
}

// sendResult is the order after a send with its last mailing of the order's
// PDF, when it was mailed.
func (s *server) sendResult(ctx context.Context, orderID int64, mailed bool) (gen.PurchasingSendResult, error) {
	view, err := s.getOrderView(ctx, orderID)
	if err != nil {
		return gen.PurchasingSendResult{}, err
	}
	out := gen.PurchasingSendResult{Order: view}
	if mailed {
		m, err := s.store().LatestMailing(ctx, store.LatestMailingParams{OrderID: orderID, Kind: docOrder})
		switch {
		case err == nil:
			out.Mailing = ptr(mailingOut(m))
		case !noRows(err):
			return out, fmt.Errorf("purchasing: read order %d's mailing: %w", orderID, err)
		}
	}
	return out, nil
}

// SendPurchasingOrder Send a purchase order
// (POST /api/v1/purchasing/orders/{orderId}/send)
func (s *server) SendPurchasingOrder(ctx context.Context, req gen.SendPurchasingOrderRequestObject) (gen.SendPurchasingOrderResponseObject, error) {
	resp := answers[gen.SendPurchasingOrderResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.SendPurchasingOrderResponseObject {
			return gen.SendPurchasingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.SendPurchasingOrderResponseObject {
			return gen.SendPurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.SendPurchasingOrderResponseObject {
			return gen.SendPurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.SendPurchasingOrderResponseObject {
			return gen.SendPurchasingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	var f fieldErrors
	m := readMailRequest(&f, b.Recipient, b.Cc, b.Subject, b.Message)
	if f.failed() {
		return gen.SendPurchasingOrder400ApplicationProblemPlusJSONResponse(invalid(invalidSendTitle, f)), nil
	}
	out, err := s.send(ctx, req.OrderId, sendSpec{method: sendMail, clientRequestID: b.ClientRequestId,
		hash: requestHash(sendMail, b.ExpectedUpdatedAt, m), expected: b.ExpectedUpdatedAt, mail: m})
	if err != nil {
		return answer(err, resp)
	}
	return gen.SendPurchasingOrder200JSONResponse(out), nil
}

// MarkPurchasingOrderSent Mark a purchase order as sent
// (POST /api/v1/purchasing/orders/{orderId}/mark-sent)
func (s *server) MarkPurchasingOrderSent(ctx context.Context, req gen.MarkPurchasingOrderSentRequestObject) (gen.MarkPurchasingOrderSentResponseObject, error) {
	resp := answers[gen.MarkPurchasingOrderSentResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.MarkPurchasingOrderSentResponseObject {
			return gen.MarkPurchasingOrderSent400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.MarkPurchasingOrderSentResponseObject {
			return gen.MarkPurchasingOrderSent404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.MarkPurchasingOrderSentResponseObject {
			return gen.MarkPurchasingOrderSent409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.MarkPurchasingOrderSentResponseObject {
			return gen.MarkPurchasingOrderSent503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	out, err := s.send(ctx, req.OrderId, sendSpec{method: sendManual, clientRequestID: b.ClientRequestId,
		hash: requestHash(sendManual, b.ExpectedUpdatedAt, mailRequest{}), expected: b.ExpectedUpdatedAt})
	if err != nil {
		return answer(err, resp)
	}
	return gen.MarkPurchasingOrderSent200JSONResponse(out), nil
}

// recipientFor is a mailing's recipient: the request's, else the order's
// snapshot of the supplier's e-mail, else the register's now.
func (s *server) recipientFor(ctx context.Context, o store.PurchasingOrder, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	if e := strings.TrimSpace(deref(o.SupplierEmail)); e != "" {
		return e, nil
	}
	entry, err := s.supplier(ctx, o.SupplierID)
	if err != nil {
		return "", err
	}
	if entry != nil && strings.TrimSpace(entry.Email) != "" {
		return strings.TrimSpace(entry.Email), nil
	}
	return "", refuseField(invalidSendTitle, "recipient", codeRecipientMissing, "The supplier has no e-mail; name a recipient")
}

// ResendPurchasingOrder Send a purchase order again
// (POST /api/v1/purchasing/orders/{orderId}/send-again)
//
// The next version's PDF, marked Revised, is stored and — unless mail is
// false — mailed (P18).
func (s *server) ResendPurchasingOrder(ctx context.Context, req gen.ResendPurchasingOrderRequestObject) (gen.ResendPurchasingOrderResponseObject, error) {
	resp := answers[gen.ResendPurchasingOrderResponseObject]{
		badRequest: func(p gen.PurchasingProblem) gen.ResendPurchasingOrderResponseObject {
			return gen.ResendPurchasingOrder400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func(p gen.PurchasingProblem) gen.ResendPurchasingOrderResponseObject {
			return gen.ResendPurchasingOrder404ApplicationProblemPlusJSONResponse(p)
		},
		conflict: func(p gen.PurchasingProblem) gen.ResendPurchasingOrderResponseObject {
			return gen.ResendPurchasingOrder409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.PurchasingProblem) gen.ResendPurchasingOrderResponseObject {
			return gen.ResendPurchasingOrder503ApplicationProblemPlusJSONResponse(p)
		},
	}
	b := *req.Body
	var f fieldErrors
	m := readMailRequest(&f, b.Recipient, b.Cc, b.Subject, b.Message)
	if f.failed() {
		return gen.ResendPurchasingOrder400ApplicationProblemPlusJSONResponse(invalid(invalidSendTitle, f)), nil
	}
	mailIt := b.Mail == nil || *b.Mail
	o, err := s.store().GetOrder(ctx, req.OrderId)
	if noRows(err) {
		return answer(notFound("purchase order"), resp)
	}
	if err != nil {
		return nil, fmt.Errorf("purchasing: read order %d: %w", req.OrderId, err)
	}
	if err := judgeSent(o); err != nil {
		return answer(err, resp)
	}
	if err := judgeFresh(o.UpdatedAt, b.ExpectedUpdatedAt); err != nil {
		return answer(err, resp)
	}
	var channel uuid.UUID
	if mailIt {
		var ok bool
		if channel, ok, err = s.sendingChannel(ctx); err != nil {
			return nil, err
		}
		if !ok {
			return answer(sendingDisabled(), resp)
		}
		if m.recipient, err = s.recipientFor(ctx, o, m.recipient); err != nil {
			return answer(err, resp)
		}
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return nil, err
	}
	var queued store.PurchasingMailing
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := txq.LockOrder(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("purchasing: lock order %d: %w", o.ID, err)
		}
		if err := judgeSent(locked); err != nil {
			return err
		}
		if err := judgeFresh(locked.UpdatedAt, b.ExpectedUpdatedAt); err != nil {
			return err
		}
		now := s.deps.Clock()
		next := locked.Version + 1
		resent, err := txq.SetOrderVersion(ctx, store.SetOrderVersionParams{ID: o.ID, Version: next, Now: now})
		if err != nil {
			return fmt.Errorf("purchasing: count order %d's version: %w", o.ID, err)
		}
		if _, err := txq.SupersedeMailings(ctx, store.SupersedeMailingsParams{
			OrderID: o.ID, Version: next, LastError: fmt.Sprintf("Superseded: version %d was sent again before this one went out.", next),
		}); err != nil {
			return fmt.Errorf("purchasing: supersede order %d's queued mailings: %w", o.ID, err)
		}
		if err := logChange(ctx, txq, o.ID, by, now, change{Action: "sent_again", To: ptr(fmt.Sprint(next))}); err != nil {
			return err
		}
		if mailIt {
			queued, err = queueMailing(ctx, txq, resent, docOrder, next, m, channel, seller, by, now)
			return err
		}
		return nil
	})
	if err != nil {
		return answer(err, resp)
	}
	if mailIt {
		if _, sent, err := s.mailQueued(ctx, queued); err != nil {
			return nil, err
		} else if !sent {
			return answer(mailFailed(), resp)
		}
	} else if _, _, err := s.ensureDocument(ctx, o.ID, docOrder); err != nil {
		return nil, err
	}
	out, err := s.sendResult(ctx, o.ID, mailIt)
	if err != nil {
		return nil, err
	}
	return gen.ResendPurchasingOrder200JSONResponse(out), nil
}

// judgeSent refuses what needs a sent or partially received order
// (order_not_sent).
func judgeSent(o store.PurchasingOrder) error {
	if isOpen(o.Status) {
		return nil
	}
	return conflict(codeOrderNotSent, "The order is not sent",
		fmt.Sprintf("The order is %s; this needs a sent or partially received order.", o.Status))
}
