package invoices

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/payments/qrsvg"
)

// This file is pay links (invoices payments and reminders design D15): an
// issued invoice's stable address on the installation's pay page,
// PUBLIC_BASE_URL/pay?token=<token>, where the customer pays it with Vipps.
// The token is 32 random bytes, base64url — [A-Za-z0-9_-] only, so the public
// host never meets an escape — and only its SHA-256 is stored, for the
// lookup; the whole URL is sealed under its own purpose, so the PDF, the
// cover e-mail and a reminder letter can print it again. The token never goes
// into a log or a URL path.
//
// A link is made in the issue's own transaction when pay links are available
// — for the quick invoice, handed over on site, only when on-site payment is
// available too (I5) — on demand (POST /invoices/{id}/pay-link), and by the
// e-mail send when the invoice has none, so a quick invoice e-mailed later
// carries one. Every path judges availability with paymentsAvailable through
// its own transaction's queries, never on a settings switch alone. Sealing
// and opening are the in-process secrets box: no call leaves the process.
//
// On screen (I21): a link shown to a customer on site is kontantsalg (D17),
// so the response answers payLink's url and its QR only to an invoices:issue
// holder while on-site payment is available — the server's rule, not the
// frontend's.

// payLinkPurpose is the secrets box purpose a link's URL is sealed under.
const payLinkPurpose = "invoices/pay-link"

const cannotPayLinkTitle = "The pay link cannot be made"

// payLinkToken, when a test sets it (export_test.go), answers every new
// link's token in place of 32 random bytes. nil in production.
var payLinkToken func() string

// newPayLinkToken is a fresh token: 32 random bytes, base64url without
// padding.
func newPayLinkToken() (string, error) {
	if payLinkToken != nil {
		return payLinkToken(), nil
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("invoices: draw a pay link token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// tokenSHA256 is a token's hex SHA-256, the column a link is found by.
func tokenSHA256(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// insertPayLink makes invoiceID's live link on txq, whose transaction holds
// the invoice: a fresh token, its hash stored, the URL sealed. by is nil
// when the issue made it.
func (s *server) insertPayLink(ctx context.Context, txq *store.Queries, invoiceID int64, by *uuid.UUID, now time.Time) (store.InvoicesPayLink, error) {
	token, err := newPayLinkToken()
	if err != nil {
		return store.InvoicesPayLink{}, err
	}
	sealed, err := s.deps.Secrets.SealString(payLinkPurpose, s.deps.Config.PublicBaseURL+"/pay?token="+token)
	if err != nil {
		return store.InvoicesPayLink{}, fmt.Errorf("invoices: seal invoice %d's pay link: %w", invoiceID, err)
	}
	link, err := txq.InsertPayLink(ctx, store.InsertPayLinkParams{
		InvoiceID: invoiceID, TokenSha256: tokenSHA256(token), UrlCiphertext: sealed, CreatedAt: now, CreatedByUserID: by,
	})
	if err != nil {
		return store.InvoicesPayLink{}, fmt.Errorf("invoices: make invoice %d's pay link: %w", invoiceID, err)
	}
	return link, nil
}

// payLinkAtIssueBeforeInsert, when a test sets it (export_test.go), is called by
// the issue right before it inserts its pay link, inside its transaction,
// with the document's id: a test commits an erase's marker there. nil in
// production.
var payLinkAtIssueBeforeInsert func(ctx context.Context, invoiceID int64)

// payLinkAtIssue is the issue's link (D15): inserted on the issue's own
// transaction tx, after the document is written, when pay links are
// available now — for a quick invoice, handed over on site, only when
// on-site payment is available too (I5). The insert runs under a savepoint:
// when its trigger refuses an anonymised customer — an erase whose marker
// committed while the issue ran (the quick invoice's draft is invisible to
// the erase) — the savepoint is rolled back and the invoice is issued
// without a link; any other failure fails the issue.
func (s *server) payLinkAtIssue(ctx context.Context, tx pgx.Tx, txq *store.Queries, issued store.InvoicesInvoice, handedOver bool) error {
	if issued.Kind != kindInvoice {
		return nil
	}
	avail, err := s.paymentsAvailable(ctx, txq)
	if err != nil {
		return err
	}
	if !avail.payLinks || (handedOver && !avail.onSite) {
		return nil
	}
	if payLinkAtIssueBeforeInsert != nil {
		payLinkAtIssueBeforeInsert(ctx, issued.ID)
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("invoices: open a savepoint for invoice %d's pay link: %w", issued.ID, err)
	}
	if _, err := s.insertPayLink(ctx, store.New(sp), issued.ID, nil, *issued.IssuedAt); err != nil {
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		if isAnonymisedRefusal(err) {
			return nil
		}
		return err
	}
	return sp.Commit(ctx)
}

// openPayLink is a link's URL, opened from its ciphertext.
func (s *server) openPayLink(link store.InvoicesPayLink) (string, error) {
	url, err := s.deps.Secrets.OpenString(payLinkPurpose, link.UrlCiphertext)
	if err != nil {
		return "", fmt.Errorf("invoices: open pay link %d: %w", link.ID, err)
	}
	return url, nil
}

// printablePayLink is the URL the PDF, the cover e-mail and a reminder
// letter print for inv: its live link while pay links are available, ""
// otherwise — a link that cannot be opened (APP_SECRET changed) is logged and
// left out, never a failure of what prints it. q reads; nothing is locked.
func (s *server) printablePayLink(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (string, error) {
	link, err := s.liveLinkToPrint(ctx, q, inv)
	if err != nil || link == nil {
		return "", err
	}
	url, err := s.openPayLink(*link)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: a pay link cannot be opened and is not printed", "invoice_id", inv.ID, "pay_link_id", link.ID)
		return "", nil
	}
	return url, nil
}

// liveLinkToPrint is inv's live link when it may be printed: an issued
// invoice's, while pay links are available and its customer is not
// anonymised (an erase revokes the link; Task 12), nil otherwise. q reads;
// under a caller's lock it judges availability there.
func (s *server) liveLinkToPrint(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) (*store.InvoicesPayLink, error) {
	if inv.Kind != kindInvoice || inv.Status != statusIssued {
		return nil, nil
	}
	avail, err := s.paymentsAvailable(ctx, q)
	if err != nil || !avail.payLinks {
		return nil, err
	}
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	if erased {
		return nil, nil
	}
	link, err := q.LivePayLink(ctx, inv.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read invoice %d's pay link: %w", inv.ID, err)
	}
	return &link, nil
}

// letterPayLinkID is the pay link a letter's facts name (D10, D15): the
// invoice's live link when it may be printed, judged once, under the
// invoice's lock, when the facts are written; nil for none.
func (s *server) letterPayLinkID(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice) (*int64, error) {
	link, err := s.liveLinkToPrint(ctx, txq, inv)
	if err != nil || link == nil {
		return nil, err
	}
	return &link.ID, nil
}

// letterPayLink is the URL letter r prints: the link its facts named,
// opened from its ciphertext whether or not it was revoked since — never
// the live link — so a letter renders the same bytes every time. "" for
// none; a link that cannot be opened is logged and left out.
func (s *server) letterPayLink(ctx context.Context, q *store.Queries, r store.InvoicesReminder) (string, error) {
	if r.PayLinkID == nil {
		return "", nil
	}
	link, err := q.GetPayLink(ctx, *r.PayLinkID)
	if err != nil {
		return "", fmt.Errorf("invoices: read letter %d's pay link: %w", r.ID, err)
	}
	url, err := s.openPayLink(link)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: a letter's pay link cannot be opened and is not printed", "reminder_id", r.ID, "pay_link_id", link.ID)
		return "", nil
	}
	return url, nil
}

// withPayLink answers an issued invoice's live link: its id and when it was
// made to every reader, its URL and QR only to an invoices:issue holder while
// on-site payment is available (I5, I21). canIssue is the handler's answer
// when it asked already, nil to ask here — only when there is a link.
func (s *server) withPayLink(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, canIssue *bool, resp *gen.InvoicesInvoiceResponse) error {
	link, err := q.LivePayLink(ctx, inv.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("invoices: read invoice %d's pay link: %w", inv.ID, err)
	}
	out := gen.InvoicesPayLink{Id: link.ID, CreatedAt: link.CreatedAt}
	resp.PayLink = &out
	if canIssue == nil {
		canIssue = ptr(s.has(ctx, "invoices:issue"))
	}
	if !*canIssue {
		return nil
	}
	avail, err := s.paymentsAvailable(ctx, q)
	if err != nil || !avail.onSite {
		return err
	}
	url, err := s.openPayLink(link)
	if err != nil {
		s.deps.Logger.WarnContext(ctx, "invoices: a pay link cannot be opened and is not answered", "invoice_id", inv.ID, "pay_link_id", link.ID)
		return nil
	}
	svg, err := qrsvg.Render(url)
	if err != nil {
		return fmt.Errorf("invoices: pay link %d's QR: %w", link.ID, err)
	}
	out.Url, out.QrSvg = &url, &svg
	return nil
}

// payLinkOnDemand is the e-mail send's link (D15, I5): when inv is an
// invoice, pay links are available and it has no live link, one is made in a
// transaction of its own — the invoice locked, judged again under the lock,
// the link inserted — before the cover text is rendered, so an invoice issued
// without one (a quick invoice, or one issued before pay links) is e-mailed
// with one. The pool's reads first spare the lock when there is nothing to do.
func (s *server) payLinkOnDemand(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice) error {
	if inv.Kind != kindInvoice || inv.Status != statusIssued {
		return nil
	}
	avail, err := s.paymentsAvailable(ctx, q)
	if err != nil || !avail.payLinks {
		return err
	}
	if _, err := q.LivePayLink(ctx, inv.ID); !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	by := callerID(ctx)
	return s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := lockInvoice(ctx, txq, inv.ID)
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", inv.ID, err)
		}
		refusal, err := s.payLinkRefusal(ctx, txq, locked)
		if err != nil || refusal != nil {
			return err // a refusal now — another link, links gone — is no failure of the send
		}
		_, err = s.insertPayLink(ctx, txq, inv.ID, &by, s.deps.Clock())
		return err
	})
}

// payLinkRefusal judges a link's make on a locked invoice: the refusal, in
// the operation's order, or nil when a link may be made.
func (s *server) payLinkRefusal(ctx context.Context, txq *store.Queries, locked store.InvoicesInvoice) (*gen.InvoicesConflictProblem, error) {
	if locked.Kind != kindInvoice {
		return ptr(conflict(codeCreditNoteNoPayments, cannotPayLinkTitle, "A credit note is never paid, so it has no pay link.")), nil
	}
	if locked.Status != statusIssued {
		return ptr(conflict(codeInvoiceDraft, cannotPayLinkTitle, "A draft is not a sales document: issue it first.")), nil
	}
	erased, err := txq.CustomerErased(ctx, locked.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read whether customer %d was erased: %w", locked.CustomerID, err)
	}
	if erased {
		return ptr(conflict(codeCustomerAnonymised, cannotPayLinkTitle, "This invoice's customer has been anonymised, and gets no pay link.")), nil
	}
	avail, err := s.paymentsAvailable(ctx, txq)
	if err != nil {
		return nil, err
	}
	if !avail.payLinks {
		return ptr(conflict(codePayLinksUnavailable, cannotPayLinkTitle,
			"Pay links are not available: Vipps, its credentials, PUBLIC_BASE_URL or the payment settings' switch is missing.")), nil
	}
	if _, err := txq.LivePayLink(ctx, locked.ID); err == nil {
		return ptr(conflict(codePayLinkExists, cannotPayLinkTitle, "The invoice has a live pay link already. Revoke it first to make a new one.")), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("invoices: read invoice %d's pay link: %w", locked.ID, err)
	}
	return nil, nil
}

// PostInvoicesByIdPayLink Make a pay link
// (POST /api/v1/invoices/{id}/pay-link)
func (s *server) PostInvoicesByIdPayLink(ctx context.Context, req gen.PostInvoicesByIdPayLinkRequestObject) (gen.PostInvoicesByIdPayLinkResponseObject, error) {
	q := store.New(s.deps.Pool)
	now, by := s.deps.Clock(), callerID(ctx)
	var refusal *gen.InvoicesConflictProblem
	var inv store.InvoicesInvoice
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		if inv, err = lockInvoice(ctx, txq, req.Id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errDocumentGone
			}
			return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
		}
		if refusal, err = s.payLinkRefusal(ctx, txq, inv); err != nil {
			return err
		}
		if refusal != nil {
			return errRefused
		}
		_, err = s.insertPayLink(ctx, txq, inv.ID, &by, now)
		return err
	})
	if refusal != nil {
		return gen.PostInvoicesByIdPayLink409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	if errors.Is(err, errDocumentGone) {
		return gen.PostInvoicesByIdPayLink404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	canIssue := true
	resp, err := s.renderInvoice(ctx, q, inv, nil, nil, &canIssue)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdPayLink201JSONResponse(resp), nil
}

// PostInvoicesByIdPayLinkRevoke Revoke a pay link
// (POST /api/v1/invoices/{id}/pay-link/revoke)
func (s *server) PostInvoicesByIdPayLinkRevoke(ctx context.Context, req gen.PostInvoicesByIdPayLinkRevokeRequestObject) (gen.PostInvoicesByIdPayLinkRevokeResponseObject, error) {
	now, by := s.deps.Clock(), callerID(ctx)
	found := false
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := lockInvoice(ctx, txq, req.Id); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", req.Id, err)
		}
		live, err := txq.LivePayLink(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invoices: read invoice %d's pay link: %w", req.Id, err)
		}
		if _, err := lockPayLink(ctx, txq, live.ID); err != nil {
			return fmt.Errorf("invoices: lock pay link %d: %w", live.ID, err)
		}
		n, err := txq.RevokePayLink(ctx, store.RevokePayLinkParams{Now: now, By: &by, ID: live.ID})
		if err != nil {
			return fmt.Errorf("invoices: revoke pay link %d: %w", live.ID, err)
		}
		found = n == 1
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.PostInvoicesByIdPayLinkRevoke404Response{}, nil
	}
	return gen.PostInvoicesByIdPayLinkRevoke204Response{}, nil
}
