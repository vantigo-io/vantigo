package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// This file is the send (payments and delivery design D4): an issued
// document's stored PDF, e-mailed now to the customer's current invoice
// address or an override, with the seller as Reply-To, and logged once in
// invoices.deliveries. Nothing here runs under a lock: the reads are plain,
// the send goes through the contract-call seam, and the log row is one
// insert whose trigger — after its wait on the document — blanks the
// recipient of a customer anonymised meanwhile (D6).

// The codes, titles and warnings of the send.
const (
	codeCustomerAnonymised = "customer_anonymised"
	codeNoInvoiceEmail     = "no_invoice_email"
	codeMailUnavailable    = "mail_unavailable"
	codeMailFailed         = "mail_failed"

	cannotSendTitle  = "The document cannot be sent"
	invalidSendTitle = "Invalid send"

	warningDeliveryPreferenceEHF          = "delivery_preference_ehf"
	warningDeliveryPreferenceOther        = "delivery_preference_other"
	warningBuyerNorwegianBusiness         = "buyer_norwegian_business"
	warningBuyerNorwegianBusinessRequired = "buyer_norwegian_business_required"
)

// b2bDutyFrom is the Oslo business day the B2B e-invoicing duty starts (Lov
// 19. juni 2026 nr. 39): from it a PDF by e-mail to a Norwegian business is
// not a lawful e-invoice. A day as businessDay answers one.
var b2bDutyFrom = time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)

// sendTimeout bounds the SMTP send alone: communications' send timeout.
const sendTimeout = 30 * time.Second

// deliveryRowTimeout bounds the delivery row, on a context of its own: a send
// the server accepted at the 29th second must still be logged. It includes
// the insert trigger's FOR SHARE wait on the document behind an erase that
// holds it (D6). The response after the row is rendered on a timeout of the
// same length.
const deliveryRowTimeout = 5 * time.Second

// messageIDDomain is the right-hand side of every Message-ID this module
// mints: communications' domain.
const messageIDDomain = "vantigo.invalid"

// beforeDeliveryWrite, when a test sets it (export_test.go), is called right
// before the delivery row is written, on an uncancellable context, so a race
// test can hold a send between its directory read and its row. nil in
// production.
var beforeDeliveryWrite func(ctx context.Context, invoiceID int64)

// sendWarnings are what the person sending is told and never refused for
// (D4): the customer's delivery preference when it is not e-mail — EHF, which
// an e-mailed PDF does not satisfy, or efaktura and paper — and a buyer with
// a Norwegian organisation number, whom the B2B e-invoicing duty covers from
// 2027-01-01: buyer_norwegian_business before that day and
// buyer_norwegian_business_required from it. The server judges the date on
// today, the Oslo business day of its own clock — the browser has neither.
// profile is the customer's current billing profile, nil when the directory
// knows none.
func sendWarnings(inv store.InvoicesInvoice, profile *contracts.CustomerBillingProfile, today time.Time) []string {
	warnings := []string{}
	if profile != nil {
		switch profile.InvoiceDelivery {
		case "ehf":
			warnings = append(warnings, warningDeliveryPreferenceEHF)
		case "efaktura", "paper":
			warnings = append(warnings, warningDeliveryPreferenceOther)
		}
	}
	if inv.BuyerOrganisationNumber != nil && *inv.BuyerOrganisationNumber != "" {
		if today.Before(b2bDutyFrom) {
			warnings = append(warnings, warningBuyerNorwegianBusiness)
		} else {
			warnings = append(warnings, warningBuyerNorwegianBusinessRequired)
		}
	}
	return warnings
}

// validRecipient is an override held to the rule the settings hold the
// seller's e-mail to (settings.go): trimmed, a bare address that parses to
// itself, at most 254 characters — so "Name <a@b>" is refused — and, unlike
// the settings' optional field, never empty.
func validRecipient(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 {
		return s, false
	}
	a, err := netmail.ParseAddress(s)
	return s, err == nil && a.Address == s
}

// withSendDefaults sets resp.SendDefaults for a caller who may send (D4):
// canIssue — invoices:issue, which the handler asked once for the whole
// response — on an installation that can send, on an issued document only,
// and never for a customer this module has anonymised: a send to one is
// refused, so there is nothing to open the dialog with. profile is the
// billing profile when the caller has read it already, nil to read it here.
// The directory read is best effort: a directory that fails leaves
// sendDefaults out and says so at warn, never failing a read of bookkeeping
// material.
func (s *server) withSendDefaults(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, profile *contracts.CustomerBillingProfile, canIssue bool, resp *gen.InvoicesInvoiceResponse) error {
	if inv.Status != statusIssued || !s.mailAvailable() || !canIssue {
		return nil
	}
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	if erased {
		return nil
	}
	if profile == nil {
		var err error
		if profile, err = s.customerProfile(ctx, inv.CustomerID); err != nil {
			s.deps.Logger.WarnContext(ctx, "invoices: the billing profile could not be read; the document is answered without its send defaults",
				"invoice_id", inv.ID, "customer_id", inv.CustomerID, "error", err.Error())
			return nil
		}
	}
	today := businessDay(s.deps.Clock())
	defaults := gen.InvoicesSendDefaults{Warnings: sendWarnings(inv, profile, today)}
	if profile != nil && profile.InvoiceEmail != "" {
		defaults.Recipient = ptr(profile.InvoiceEmail)
	}
	if profile != nil && profile.InvoiceDelivery != "" {
		defaults.Preference = ptr(profile.InvoiceDelivery)
	}
	resp.SendDefaults = &defaults
	return nil
}

// mailFailed is the 502 a send answers when the mail server did not take the
// mail, and mailUnavailable the 503 of an installation that cannot send.
func mailFailed() gen.InvoicesConflictProblem {
	c := conflict(codeMailFailed, "The e-mail could not be sent",
		"The mail server did not confirm the e-mail. Nothing was recorded. It may still have been delivered if the server timed out; check before sending again.")
	c.Status = ptr(int32(http.StatusBadGateway))
	return c
}

func mailUnavailable() gen.InvoicesConflictProblem {
	c := conflict(codeMailUnavailable, "E-mail is unavailable",
		"This installation has no mail server configured, so a document cannot be sent by e-mail.")
	c.Status = ptr(int32(http.StatusServiceUnavailable))
	return c
}

// PostInvoicesByIdSend Send an issued document by e-mail
// (POST /api/v1/invoices/{id}/send)
func (s *server) PostInvoicesByIdSend(ctx context.Context, req gen.PostInvoicesByIdSendRequestObject) (gen.PostInvoicesByIdSendResponseObject, error) {
	// 1. An installation that cannot send reads nothing.
	if !s.mailAvailable() {
		return gen.PostInvoicesByIdSend503ApplicationProblemPlusJSONResponse(mailUnavailable()), nil
	}

	// 2. The document.
	q := store.New(s.deps.Pool)
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdSend404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if inv.Status != statusIssued {
		return gen.PostInvoicesByIdSend409ApplicationProblemPlusJSONResponse(conflict(codeInvoiceDraft, cannotSendTitle,
			"A draft is not a sales document: issue it before sending it.")), nil
	}

	// 3. A person this module has anonymised is not written to again.
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	if erased {
		return gen.PostInvoicesByIdSend409ApplicationProblemPlusJSONResponse(conflict(codeCustomerAnonymised, cannotSendTitle,
			"This document's customer has been anonymised, and is not written to again.")), nil
	}

	// 4. The recipient: the override, else today's invoice e-mail — a credit
	// note's too, for the person's address changes and the document does not.
	var recipient string
	var profile *contracts.CustomerBillingProfile
	if req.Body.Recipient != nil {
		var ok bool
		if recipient, ok = validRecipient(*req.Body.Recipient); !ok {
			return gen.PostInvoicesByIdSend400ApplicationProblemPlusJSONResponse(invalid(invalidSendTitle,
				fieldError("recipient", "This is not an e-mail address"))), nil
		}
	} else {
		if profile, err = s.customerProfile(ctx, inv.CustomerID); err != nil {
			return nil, fmt.Errorf("invoices: read customer %d's billing profile: %w", inv.CustomerID, err)
		}
		if profile != nil {
			recipient = profile.InvoiceEmail
		}
		if recipient == "" {
			return gen.PostInvoicesByIdSend409ApplicationProblemPlusJSONResponse(conflict(codeNoInvoiceEmail, cannotSendTitle,
				"The customer has no invoice e-mail. Add one to the customer, or send to another address.")), nil
		}
	}

	// 5. The stored PDF, stored first when it never was.
	pdf, problem := s.loadStoredPDF(ctx, q, inv)
	switch {
	case problem == nil:
	case problem.unavailable != nil:
		return gen.PostInvoicesByIdSend503ApplicationProblemPlusJSONResponse(*problem.unavailable), nil
	case problem.broken:
		return gen.PostInvoicesByIdSend500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
	default:
		return nil, problem.err
	}

	// 6. The envelope: the seller as the PDF names it, replies to today's
	// mailbox, the text in the buyer's language.
	out, err := s.envelope(ctx, q, inv, recipient, pdf.body)
	if err != nil {
		return nil, err
	}

	// 7. The send, on a context the request's cancellation does not reach: a
	// browser that goes away must not abort a transfer the mail server may
	// already have accepted. The 30 seconds are the send's alone.
	uncancelled := context.WithoutCancel(ctx)
	sendCtx, cancelSend := context.WithTimeout(uncancelled, sendTimeout)
	err = s.smtpSend(sendCtx, s.deps.Config.Mail, out)
	cancelSend()
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: a document could not be sent", "invoice_id", inv.ID, "error", err.Error())
		return gen.PostInvoicesByIdSend502ApplicationProblemPlusJSONResponse(mailFailed()), nil
	}

	// 8. The row that is the send's evidence, uncancellable too and on a
	// short timeout of its own, never the send's remaining budget.
	if beforeDeliveryWrite != nil {
		beforeDeliveryWrite(uncancelled, inv.ID)
	}
	rowCtx, cancelRow := context.WithTimeout(uncancelled, deliveryRowTimeout)
	defer cancelRow()
	if _, err := q.InsertDelivery(rowCtx, store.InsertDeliveryParams{
		InvoiceID: inv.ID, Recipient: recipient, Subject: out.Subject, MessageID: out.MessageID,
		PdfSha256: pdf.sha256, SentAt: s.deps.Clock(), SentByUserID: callerID(ctx),
	}); err != nil {
		// The document id only, never the address: it may be a person's
		// whom an erase is anonymising at this very moment.
		s.deps.Logger.ErrorContext(ctx, "invoices: a document was sent but its delivery could not be logged",
			"invoice_id", inv.ID, "error", err.Error())
		return nil, fmt.Errorf("invoices: log the send of document %d: %w", inv.ID, err)
	}

	// 9. The document, its deliveries now holding the row — read again when
	// step 5 stored its PDF, so it says so. Uncancellable as well, and on a
	// short timeout of its own, so the re-read, the render and the
	// best-effort directory read are bounded: a caller who went away is owed
	// no error for a send that succeeded.
	renderCtx, cancelRender := context.WithTimeout(uncancelled, deliveryRowTimeout)
	defer cancelRender()
	if pdf.body != nil && inv.PdfSha256 == nil {
		if inv, err = q.GetInvoice(renderCtx, inv.ID); err != nil {
			return nil, fmt.Errorf("invoices: re-read document %d: %w", req.Id, err)
		}
	}
	// has() asks Access with the request itself, whose context a disconnect
	// cancels: a caller who went away gets the response without sendDefaults
	// and the deliveries' recipients — it fails closed, and nobody reads it.
	canIssue := s.has(ctx, "invoices:issue")
	resp, err := s.renderInvoice(renderCtx, q, inv, nil, nil, &canIssue)
	if err != nil {
		return nil, err
	}
	if err := s.withSendDefaults(renderCtx, q, inv, profile, canIssue, &resp); err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdSend200JSONResponse(resp), nil
}

// envelope is one send's mail.Outbound: From the installation's address
// (Config.Mail.From, which the SMTP client sets) under the seller snapshot's
// legal name, Reply-To the current settings' e-mail when there is one, the
// cover mail in the document's language, a fresh bare Message-ID, and the
// PDF attached under the download's own name.
func (s *server) envelope(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, recipient string, pdf []byte) (mail.Outbound, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return mail.Outbound{}, fmt.Errorf("invoices: read the settings: %w", err)
	}
	var original *store.InvoicesInvoice
	var open *big.Rat
	if inv.CreditsInvoiceID != nil {
		o, err := q.GetInvoice(ctx, *inv.CreditsInvoiceID)
		if err != nil {
			return mail.Outbound{}, fmt.Errorf("invoices: read credit note %d's original: %w", inv.ID, err)
		}
		original = &o
	}
	if inv.Kind == kindInvoice {
		if open, err = openOf(ctx, q, inv); err != nil {
			return mail.Outbound{}, err
		}
	}
	text, err := coverMail(inv, original, open)
	if err != nil {
		return mail.Outbound{}, err
	}
	out := mail.Outbound{
		To: []string{recipient}, Subject: text.subject, ReplyTo: settings.Email, TextBody: text.body,
		MessageID:   uuid.NewString() + "@" + messageIDDomain,
		Attachments: []mail.Attachment{{FileName: fileName(inv), ContentType: "application/pdf", Content: pdf}},
	}
	if inv.SellerLegalName != nil {
		out.DisplayName = *inv.SellerLegalName
	}
	return out, nil
}
