package orders

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	netmail "net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is a quote's send and mark-as-sent (O20, O25) and the mailing
// log. Both freeze the draft: its PDF of this revision is rendered and stored
// (O24), its valid-until set, its status moved to sent and
// orders.quote.sent.v1 published. A send mails the PDF through
// contracts.OutboundMail on the settings' channel first — synchronously and
// outside any transaction, as Invoices' send does — and logs it in
// orders.mailings; a mail that fails leaves the quote a draft. Mark as sent
// mails nothing, and works whether or not Communications is on.

// sendTimeout bounds one send; mailingRowTimeout the row that logs it.
const (
	sendTimeout       = 30 * time.Second
	mailingRowTimeout = 5 * time.Second
)

// messageIDDomain is the right-hand side of a Message-ID this module makes.
const messageIDDomain = "vantigo.invalid"

// Mailing states (O25).
const (
	mailingQueued = "queued"
	mailingSent   = "sent"
	mailingFailed = "failed"
)

// sendRequest is a send from the dialog, judged: the recipient, the copies,
// a subject that overrides the mail text's, and the message put in the body.
type sendRequest struct {
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

// readSendRequest judges a send's dialog fields into f.
func readSendRequest(f *fieldErrors, b gen.OrdersSendInput) sendRequest {
	var out sendRequest
	var ok bool
	if out.recipient, ok = validRecipient(string(b.Recipient)); !ok {
		f.add("recipient", "This is not an e-mail address")
	}
	if b.Cc != nil {
		if len(*b.Cc) > 10 {
			f.add("cc", "At most 10 copies")
		}
		for i, c := range *b.Cc {
			addr, ok := validRecipient(string(c))
			if !ok {
				f.add(fmt.Sprintf("cc[%d]", i), "This is not an e-mail address")
				continue
			}
			out.cc = append(out.cc, addr)
		}
	}
	if b.Subject != nil {
		out.subject = strings.TrimSpace(*b.Subject)
		if utf8.RuneCountInString(out.subject) > 300 {
			f.add("subject", "A subject is at most 300 characters")
		}
	}
	if b.Message != nil {
		out.message = *b.Message
		if utf8.RuneCountInString(out.message) > 4000 {
			f.add("message", "A message is at most 4000 characters")
		}
	}
	return out
}

// sendingChannel is the channel the settings choose to send from, when
// Communications is on and the channel can send now (O25, MB rule 17); ok is
// false otherwise, which a send answers as sending_disabled.
func (s *server) sendingChannel(ctx context.Context) (uuid.UUID, bool, error) {
	if s.deps.OutboundMail == nil {
		return uuid.Nil, false, nil
	}
	settings, err := readSettings(ctx, store.New(s.deps.Pool))
	if err != nil {
		return uuid.Nil, false, err
	}
	if settings.sendChannel == nil {
		return uuid.Nil, false, nil
	}
	ch, err := s.outboundChannel(ctx, *settings.sendChannel)
	if errors.Is(err, contracts.ErrChannelUnknown) || errors.Is(err, errDependencyUnavailable) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("orders: read the send channel: %w", err)
	}
	return ch.ID, ch.Available, nil
}

// envelope is one send's mail: the cover mail in the document's language,
// the dialog's subject over it when given, the PDF attached, the seller's
// name beside the channel's address, replies to the seller's e-mail, and a
// fresh Message-ID.
func envelope(req sendRequest, facts mailFacts, settings ordersSettings, seller contracts.SellerProfile, pdf []byte, fileName, messageID string) mail.Outbound {
	text := coverMail(facts, settings.mailText(facts.kind, facts.language))
	if req.subject != "" {
		text.subject = req.subject
	}
	return mail.Outbound{
		DisplayName: seller.LegalName, To: []string{req.recipient}, Cc: req.cc, Subject: text.subject,
		ReplyTo: seller.Email, TextBody: text.body, MessageID: messageID,
		Attachments: []mail.Attachment{{FileName: fileName, ContentType: "application/pdf", Content: pdf}},
	}
}

// newMessageID is a send's Message-ID, bare as mail.Outbound takes it.
func newMessageID() string { return "orders-" + uuid.NewString() + "@" + messageIDDomain }

// mailSent is a send's outcome, logged after it was made.
type mailSent struct {
	channel   uuid.UUID
	out       mail.Outbound
	failed    bool
	lastError string
	by        actor
	at        time.Time
}

// logMailing writes one send from the dialog to orders.mailings, on q — the
// send's own transaction, or the pool when nothing else is written.
func logMailing(ctx context.Context, q *store.Queries, ownerKind string, ownerID int64, version int32, documentID *int64, m mailSent) error {
	status, lastError, sentAt := mailingSent, (*string)(nil), &m.at
	if m.failed {
		status, lastError, sentAt = mailingFailed, &m.lastError, nil
	}
	channel := m.channel
	_, err := q.InsertMailing(ctx, store.InsertMailingParams{
		OwnerKind: ownerKind, OwnerID: ownerID, Version: version, DocumentID: documentID, Recipient: strings.Join(m.out.To, ", "),
		Cc: strings.Join(m.out.Cc, ", "), Subject: truncate(m.out.Subject, 300), ChannelID: &channel, Status: status,
		ProviderMessageID: &m.out.MessageID, LastError: lastError, SentAt: sentAt, SentByUserID: m.by.id,
		SentByDisplay: m.by.display, CreatedAt: m.at,
	})
	if err != nil {
		return fmt.Errorf("orders: log the send of %s %d: %w", ownerKind, ownerID, err)
	}
	return nil
}

// truncate is s cut to n characters.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// mailFailed is the answer of a send the mail provider did not take: the
// document is left as it was.
func mailFailed() error {
	return refuse(problem(http.StatusServiceUnavailable, codeMailFailed, "The e-mail could not be sent",
		"The mail provider did not take the e-mail. Nothing was changed, and the failed send is listed. Try again, or download the PDF and mark the document as sent."))
}

// SendQuote Send a quote by e-mail
// (POST /api/v1/orders/quotes/{id}/send)
func (s *server) SendQuote(ctx context.Context, req gen.SendQuoteRequestObject) (gen.SendQuoteResponseObject, error) {
	resp := answers[gen.SendQuoteResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.SendQuoteResponseObject {
			return gen.SendQuote400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.SendQuoteResponseObject { return gen.SendQuote404Response{} },
		conflict: func(p gen.OrdersProblem) gen.SendQuoteResponseObject {
			return gen.SendQuote409ApplicationProblemPlusJSONResponse(p)
		},
		unavailable: func(p gen.OrdersProblem) gen.SendQuoteResponseObject {
			return gen.SendQuote503ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, sendInputSchema)
	send := readSendRequest(&f, *req.Body)
	override := s.readOverride(ctx, &f, req.Body.OverrideWarnings, req.Body.OverrideReason)
	if req.Body.Revision == nil {
		f.add("revision", "A quote's send names the revision it was read at")
	}
	if f.failed() {
		return gen.SendQuote400ApplicationProblemPlusJSONResponse(invalid(invalidSendTitle, f)), nil
	}
	channel, ok, err := s.sendingChannel(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return answer(sendingDisabled(), resp)
	}
	out, err := s.freezeQuote(ctx, req.Id, *req.Body.Revision, req.Body.ExpectedUpdatedAt, req.Body.ValidUntil, &quoteMail{channel: channel, req: send}, override)
	if err != nil {
		return answer(err, resp)
	}
	return gen.SendQuote200JSONResponse(out), nil
}

// MarkQuoteSent Mark a quote as sent
// (POST /api/v1/orders/quotes/{id}/mark-sent)
func (s *server) MarkQuoteSent(ctx context.Context, req gen.MarkQuoteSentRequestObject) (gen.MarkQuoteSentResponseObject, error) {
	resp := answers[gen.MarkQuoteSentResponseObject]{
		badRequest: func(p gen.OrdersProblem) gen.MarkQuoteSentResponseObject {
			return gen.MarkQuoteSent400ApplicationProblemPlusJSONResponse(p)
		},
		notFound: func() gen.MarkQuoteSentResponseObject { return gen.MarkQuoteSent404Response{} },
		conflict: func(p gen.OrdersProblem) gen.MarkQuoteSentResponseObject {
			return gen.MarkQuoteSent409ApplicationProblemPlusJSONResponse(p)
		},
	}
	f := s.unknownProperties(ctx, markSentInputSchema)
	override := s.readOverride(ctx, &f, req.Body.OverrideWarnings, req.Body.OverrideReason)
	if f.failed() {
		return gen.MarkQuoteSent400ApplicationProblemPlusJSONResponse(invalid(invalidSendTitle, f)), nil
	}
	out, err := s.freezeQuote(ctx, req.Id, req.Body.Revision, req.Body.ExpectedUpdatedAt, req.Body.ValidUntil, nil, override)
	if err != nil {
		return answer(err, resp)
	}
	return gen.MarkQuoteSent200JSONResponse(out), nil
}

// quoteMail is a send's channel and dialog; nil for mark as sent.
type quoteMail struct {
	channel uuid.UUID
	req     sendRequest
}

// freezeQuote is send and mark as sent (O20): the draft at revision judged
// sendable — every product line priced and with a VAT code — its valid-until
// settled, its PDF of this revision rendered and stored, the mail sent when
// m is given, and then, on one transaction that locks the variants and then
// the quote, the quote moved to sent with its stored PDF recorded, the send
// logged and orders.quote.sent.v1 published. A quote that changed while it
// was being sent is refused (version_conflict); a mail that went out is
// still logged.
//
// A block warning on the quote's customer or its products refuses it
// (blocked_by_warning) before anything is printed or mailed, and again under
// the quote's lock, unless o overrides it; an override is appended to the
// quote's internal note (O33).
func (s *server) freezeQuote(ctx context.Context, id int64, revision int32, expected time.Time, validUntilIn *openapi_types.Date, m *quoteMail, o warningOverride) (gen.OrdersQuote, error) {
	q := store.New(s.deps.Pool)
	quote, err := q.GetQuote(ctx, id)
	if noRows(err) {
		return gen.OrdersQuote{}, notFound()
	}
	if err != nil {
		return gen.OrdersQuote{}, fmt.Errorf("orders: read quote %d: %w", id, err)
	}
	if err := judgeDraft(quote, revision, expected); err != nil {
		return gen.OrdersQuote{}, err
	}
	lines, err := linesOf(ctx, q, ownerQuote, id)
	if err != nil {
		return gen.OrdersQuote{}, err
	}
	if failures := sendable(lines); len(failures) > 0 {
		return gen.OrdersQuote{}, refuseLines(invalidSendTitle, failures)
	}
	if _, err := judgeWarnings(ctx, q, quote.CustomerID, lines, o); err != nil {
		return gen.OrdersQuote{}, err
	}
	zone, err := s.businessZone(ctx)
	if err != nil {
		return gen.OrdersQuote{}, err
	}
	now := s.deps.Clock()
	today := dayOf(now, zone)
	validUntil, err := s.validUntil(ctx, q, quote, validUntilIn, today)
	if err != nil {
		return gen.OrdersQuote{}, err
	}
	settings, err := readSettings(ctx, q)
	if err != nil {
		return gen.OrdersQuote{}, err
	}
	seller, err := s.seller(ctx)
	if err != nil {
		return gen.OrdersQuote{}, err
	}

	// The PDF of this revision, as the quote will be once sent.
	printed := quote
	printed.Status, printed.SentAt, printed.ValidUntil = quoteSent, &now, pgtype.Date{Time: validUntil, Valid: true}
	body, err := renderQuote(printed, lines, seller, today, false)
	if err != nil {
		return gen.OrdersQuote{}, err
	}
	stored, err := s.storeDocument(ctx, ownerQuote, quote.ID, quote.Number, docQuote, quote.Revision, body)
	if err != nil {
		return gen.OrdersQuote{}, err
	}
	by, err := s.actorFor(ctx)
	if err != nil {
		return gen.OrdersQuote{}, err
	}

	// The mail, outside any transaction, on a context the request's
	// cancellation does not reach: a browser that goes away must not abort a
	// transfer the provider may already have taken.
	var sent *mailSent
	uncancelled := context.WithoutCancel(ctx)
	if m != nil {
		facts := mailFacts{
			kind: docQuote, language: quote.Language, number: quote.Number, version: quote.Revision, seller: seller.LegalName,
			currency: quote.Currency, gross: ratOrZero(quote.GrossTotal), validUntil: &validUntil, message: m.req.message,
		}
		out := envelope(m.req, facts, settings, seller, body, quoteFileName(quote, quote.Revision, false), newMessageID())
		sendCtx, cancel := context.WithTimeout(uncancelled, sendTimeout)
		err := s.outboundSend(sendCtx, m.channel, out)
		cancel()
		sent = &mailSent{channel: m.channel, out: out, by: by, at: s.deps.Clock()}
		if err != nil {
			s.deps.Logger.WarnContext(ctx, "orders: a quote could not be sent", "quote_id", quote.ID, "error", err.Error())
			sent.failed, sent.lastError = true, truncate(err.Error(), 500)
			rowCtx, cancelRow := context.WithTimeout(uncancelled, mailingRowTimeout)
			defer cancelRow()
			if err := logMailing(rowCtx, store.New(s.deps.Pool), ownerQuote, quote.ID, quote.Revision, nil, *sent); err != nil {
				return gen.OrdersQuote{}, err
			}
			return gen.OrdersQuote{}, mailFailed()
		}
	}

	var out gen.OrdersQuote
	err = s.withLockedTx(uncancelled, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		priced := make([]pricedLine, 0, len(lines))
		for _, l := range lines {
			priced = append(priced, pricedLineOf(l))
		}
		if err := s.lockLines(ctx, tx, invalidSendTitle, priced); err != nil {
			return err
		}
		locked, err := txq.LockQuote(ctx, id)
		if err != nil {
			return fmt.Errorf("orders: lock quote %d: %w", id, err)
		}
		if err := judgeDraft(locked, revision, expected); err != nil {
			return err
		}
		if !locked.UpdatedAt.Equal(quote.UpdatedAt) {
			return conflict(codeVersionConflict, "The quote changed",
				"The quote was saved while it was being sent; nothing was frozen. Reload it and send it again.")
		}
		overridden, err := judgeWarnings(ctx, txq, locked.CustomerID, lines, o)
		if err != nil {
			return err
		}
		if len(overridden) > 0 {
			note := overrideNote(locked.InternalNote, overridden, o.reason, by, now, zone, locked.Language)
			if err := txq.SetQuoteInternalNote(ctx, store.SetQuoteInternalNoteParams{ID: id, InternalNote: note}); err != nil {
				return fmt.Errorf("orders: note quote %d's override: %w", id, err)
			}
		}
		row, err := txq.SetQuoteSent(ctx, store.SetQuoteSentParams{ID: id, Now: &now, ValidUntil: pgtype.Date{Time: validUntil, Valid: true}})
		if err != nil {
			return fmt.Errorf("orders: mark quote %d sent: %w", id, err)
		}
		doc, err := recordDocument(ctx, txq, ownerQuote, id, docQuote, row.Revision, stored, now)
		if err != nil {
			return err
		}
		if sent != nil {
			if err := logMailing(ctx, txq, ownerQuote, id, row.Revision, &doc.ID, *sent); err != nil {
				return err
			}
		}
		if err := events.Publish(ctx, tx, moduleName, contracts.QuoteSent{
			QuoteID: row.ID, Number: row.Number, Revision: row.Revision, CustomerID: row.CustomerID, Currency: row.Currency,
			NetTotal: ratOrZero(row.NetTotal).FloatString(amountPlaces), SentAt: now,
		}, now); err != nil {
			return err
		}
		warnings, err := documentWarnings(ctx, txq, row.CustomerID, lines)
		if err != nil {
			return err
		}
		out = quoteView(row, lines, nil, warnings)
		return nil
	})
	if err != nil && sent != nil {
		// The mail went out; its row is the evidence whatever the quote is.
		rowCtx, cancelRow := context.WithTimeout(uncancelled, mailingRowTimeout)
		defer cancelRow()
		if logErr := logMailing(rowCtx, store.New(s.deps.Pool), ownerQuote, id, revision, nil, *sent); logErr != nil {
			s.deps.Logger.ErrorContext(ctx, "orders: a quote was mailed but its send could not be logged", "quote_id", id, "error", logErr.Error())
		}
	}
	return out, err
}

// validUntil is the day a quote is valid until when it is sent (O20): the
// request's, else the draft's own, else today plus the template's validity or
// the settings'. A day before today is refused.
func (s *server) validUntil(ctx context.Context, q *store.Queries, quote store.OrdersQuote, in *openapi_types.Date, today time.Time) (time.Time, error) {
	switch {
	case in != nil:
		d := time.Date(in.Year(), in.Month(), in.Day(), 0, 0, 0, 0, time.UTC)
		if d.Before(today) {
			return d, refuseField(invalidSendTitle, "validUntil", "", "Valid until is today or later")
		}
		return d, nil
	case quote.ValidUntil.Valid && !quote.ValidUntil.Time.Before(today):
		return quote.ValidUntil.Time, nil
	}
	days := int32(0)
	if quote.TemplateID != nil {
		t, err := q.GetTemplate(ctx, *quote.TemplateID)
		switch {
		case err == nil && t.ValidityDays != nil:
			days = *t.ValidityDays
		case err != nil && !noRows(err):
			return today, fmt.Errorf("orders: read template %d: %w", *quote.TemplateID, err)
		}
	}
	if days == 0 {
		settings, err := readSettings(ctx, q)
		if err != nil {
			return today, err
		}
		days = settings.quoteValidityDays
	}
	return today.AddDate(0, 0, int(days)), nil
}

// mailingsOut are a document's sends as the API lists them.
func mailingsOut(rows []store.OrdersMailing) gen.OrdersMailingList {
	out := gen.OrdersMailingList{Items: []gen.OrdersMailing{}}
	for _, m := range rows {
		cc := []string{}
		for _, c := range strings.Split(m.Cc, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cc = append(cc, c)
			}
		}
		out.Items = append(out.Items, gen.OrdersMailing{
			Id: m.ID, Version: m.Version, Recipient: m.Recipient, Cc: cc, Subject: m.Subject,
			Status: gen.OrdersMailingStatus(m.Status), CreatedAt: m.CreatedAt, SentAt: m.SentAt, LastError: m.LastError,
			SentBy: personOut(m.SentByUserID, m.SentByDisplay),
		})
	}
	return out
}

// ListQuoteMailings List a quote's sends
// (GET /api/v1/orders/quotes/{id}/mailings)
func (s *server) ListQuoteMailings(ctx context.Context, req gen.ListQuoteMailingsRequestObject) (gen.ListQuoteMailingsResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetQuote(ctx, req.Id); noRows(err) {
		return gen.ListQuoteMailings404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("orders: read quote %d: %w", req.Id, err)
	}
	rows, err := q.MailingsOf(ctx, store.MailingsOfParams{OwnerKind: ownerQuote, OwnerID: req.Id})
	if err != nil {
		return nil, fmt.Errorf("orders: list quote %d's sends: %w", req.Id, err)
	}
	return gen.ListQuoteMailings200JSONResponse(mailingsOut(rows)), nil
}
