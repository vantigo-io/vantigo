package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices/ehf"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the send as EHF (EHF and KID design D8): an issued document's
// UBL, rendered from its own rows with the stored PDF embedded, pre-checked,
// its receiver re-checked on the Peppol network, stored once by its hash, and
// queued as one invoices.transmissions row under the document's lock. Nothing
// here calls the access point — the invoices-ehf worker submits what is
// queued (D9). Every read before the lock is plain; the lookup and the object
// store are called outside any transaction; the transaction takes the
// document FOR UPDATE, so two sends serialise on it, and the credentials FOR
// SHARE, so a DELETE of them never leaves a queued row without them.

// The codes and titles of the send as EHF and of its transmissions.
const (
	codeNoPeppolID                 = "no_peppol_id"
	codeBuyerReferenceMissing      = "buyer_reference_missing"
	codeEhfAlreadySent             = "ehf_already_sent"
	codePeppolNotReceivable        = "peppol_not_receivable"
	codePeppolLookupFailed         = "peppol_lookup_failed"
	codeEhfInvalid                 = "ehf_invalid"
	codeTransmissionNotCancellable = "transmission_not_cancellable"
	codeTransmissionNotResolvable  = "transmission_not_resolvable"

	cannotSendEhfTitle = "The document cannot be sent as EHF"

	// warningEhfPreferred replaces warningDeliveryPreferenceEHF on the
	// e-mail send when the caller can send as EHF (D10).
	warningEhfPreferred = "ehf_preferred"
	// warningEhfBuyerReferenceMissing is a draft headed for EHF with neither
	// reference (D8): Peppol needs one, and neither changes after the issue.
	warningEhfBuyerReferenceMissing = "ehf_buyer_reference_missing"

	// ehfNotSent is a document's EHF status before any transmission.
	ehfNotSent = "not_sent"

	// uxTransmissionsActive is the partial unique index that allows one live
	// transmission per document: the floor under the lock's judgment.
	uxTransmissionsActive = "ux_transmissions_active"

	// maxReason is last_error's and a reason's length on the wire, in runes.
	maxReason = 500
)

// activeTransmission is ux_transmissions_active's predicate: a transmission in
// one of these blocks a new send.
var activeTransmission = map[string]bool{"queued": true, "submitted": true, "delivered": true, "unconfirmed": true}

// beforeTransmissionInsert, when a test sets it (export_test.go), is called
// inside the send's transaction after the judgment under the lock and right
// before the insert. nil in production.
var beforeTransmissionInsert func(ctx context.Context, invoiceID int64)

// sendEhfWithoutLock, when a test sets it (export_test.go), reads the document
// in the send's transaction without FOR UPDATE, so the partial unique index is
// shown to refuse a second transmission on its own. false in production.
var sendEhfWithoutLock bool

// Anything in a provider's or a receiver's words that names a person or a
// party: an e-mail address, and a participant identifier "<4-digit ICD>:<id>".
var (
	reasonEmail       = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	reasonParticipant = regexp.MustCompile(`\b[0-9]{4}:[A-Za-z0-9][A-Za-z0-9._\-]*`)
)

// redactReason is a failure's reason as it may be stored in last_error and
// answered on the wire (D9): trimmed, every e-mail address replaced by
// <e-mail> and every participant identifier by <participant>, then cut to 500
// runes on a rune boundary — redacted before it is cut, so an address at the
// cut never leaves half of itself.
func redactReason(s string) string {
	s = reasonEmail.ReplaceAllLiteralString(strings.TrimSpace(s), "<e-mail>")
	s = reasonParticipant.ReplaceAllLiteralString(s, "<participant>")
	if utf8.RuneCountInString(s) <= maxReason {
		return s
	}
	runes := 0
	for i := range s {
		if runes == maxReason {
			return s[:i]
		}
		runes++
	}
	return s
}

// validParticipant is whether a Peppol id has the <scheme>:<value> shape the
// render places as an EndpointID: anything else is no_peppol_id, never a
// render error.
func validParticipant(id *string) bool {
	if id == nil {
		return false
	}
	scheme, value, ok := strings.Cut(*id, ":")
	return ok && scheme != "" && value != ""
}

// hasBuyerReference is whether a document carries one of the two references
// Peppol needs (PEPPOL-EN16931-R003).
func hasBuyerReference(inv store.InvoicesInvoice) bool {
	return strings.TrimSpace(inv.YourReference) != "" || strings.TrimSpace(inv.OrderReference) != ""
}

// hasCategoryK is whether a document has a line in VAT category K, which
// the EHF cannot carry (D4): the K check the document's blockedBy makes
// without a render.
func hasCategoryK(lines []store.InvoicesLine) bool {
	for _, l := range lines {
		if l.VatCategory != nil && *l.VatCategory == "K" {
			return true
		}
	}
	return false
}

// ehfUnavailable is the 503 of an installation that cannot send as EHF.
func ehfUnavailable() gen.InvoicesConflictProblem {
	c := conflict(codeEhfUnavailable, ehfUnavailableTitle,
		"This installation cannot send as EHF: it is switched off, the Peppol lookup is disabled, no access point credentials are stored, or the seller has no Peppol id.")
	c.Status = ptr(int32(http.StatusServiceUnavailable))
	return c
}

// ehfAlreadySent is the 409 of a document with a live transmission.
func ehfAlreadySent() gen.InvoicesConflictProblem {
	return conflict(codeEhfAlreadySent, cannotSendEhfTitle,
		"This document is already queued, submitted, delivered or awaiting confirmation as EHF. Cancel or resolve that transmission first.")
}

// customerAnonymisedEhf is the 409 of a document whose customer this module
// has anonymised.
func customerAnonymisedEhf() gen.InvoicesConflictProblem {
	return conflict(codeCustomerAnonymised, cannotSendEhfTitle,
		"This document's customer has been anonymised, and is not sent to again.")
}

// documentTypeOf is the Peppol document type a document of kind is sent as.
func documentTypeOf(kind string) string {
	if kind == kindCreditNote {
		return peppol.CreditNoteDocumentType
	}
	return peppol.InvoiceDocumentType
}

// canReceive is whether a lookup's answer accepts a document of kind.
func canReceive(res peppol.Result, kind string) bool {
	if kind == kindCreditNote {
		return res.CanReceiveCreditNote
	}
	return res.CanReceiveInvoice
}

// isAnonymisedRefusal is the transmission insert trigger's refusal of an
// anonymised customer, raised when an erasure's marker committed after the
// judgment under the lock.
func isAnonymisedRefusal(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "P0001" && pgErr.Message == "invoices: the customer is anonymised"
}

// ublFileName is a transmission's UBL download name: the PDF's, with the
// transmission and .xml.
func ublFileName(inv store.InvoicesInvoice, transmissionID int64) string {
	return fmt.Sprintf("%s-%d.xml", strings.TrimSuffix(fileName(inv), ".pdf"), transmissionID)
}

// ublPath is where a transmission's UBL is downloaded.
func ublPath(invoiceID, transmissionID int64) string {
	return fmt.Sprintf("/api/v1/invoices/%d/transmissions/%d/ubl", invoiceID, transmissionID)
}

// renderedEHF is the send's UBL: the bytes to queue, their key and hash, the
// PDF's hash they embed and the sender they name.
type renderedEHF struct {
	body                 []byte
	key, sha256, pdfHash string
	sender               string
	reused               bool
}

// renderEHF renders an issued document's UBL from its own rows, the seller's
// current Peppol id and the stored PDF's bytes (D4), pre-checks it and holds
// it to the invariants. rules is the pre-check's refusal; err is the server's
// — a render that fails or an invariant the module broke.
func (s *server) renderEHF(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, sellerPeppolID string, pdf storedPDF) (renderedEHF, []ehf.Rule, error) {
	lines, err := q.Lines(ctx, inv.ID)
	if err != nil {
		return renderedEHF{}, nil, fmt.Errorf("invoices: read document %d's lines: %w", inv.ID, err)
	}
	sums, err := q.VatSummaries(ctx, inv.ID)
	if err != nil {
		return renderedEHF{}, nil, fmt.Errorf("invoices: read document %d's VAT: %w", inv.ID, err)
	}
	var original *store.InvoicesInvoice
	if inv.CreditsInvoiceID != nil {
		o, err := q.GetInvoice(ctx, *inv.CreditsInvoiceID)
		if err != nil {
			return renderedEHF{}, nil, fmt.Errorf("invoices: read credit note %d's original: %w", inv.ID, err)
		}
		original = &o
	}
	doc, err := ehfDocumentOf(inv, lines, sums, original, sellerPeppolID, pdf.body)
	if err != nil {
		return renderedEHF{}, nil, err
	}
	body, err := ehf.Render(doc)
	if err != nil {
		return renderedEHF{}, nil, fmt.Errorf("invoices: render document %d's EHF: %w", inv.ID, err)
	}
	rules, err := ehf.Precheck(body)
	if err != nil {
		return renderedEHF{}, nil, fmt.Errorf("invoices: pre-check document %d's EHF: %w", inv.ID, err)
	}
	if len(rules) > 0 {
		return renderedEHF{}, rules, nil
	}
	broken, err := ehf.Invariants(body, doc)
	if err != nil {
		return renderedEHF{}, nil, fmt.Errorf("invoices: check document %d's EHF: %w", inv.ID, err)
	}
	if len(broken) > 0 {
		ids := make([]string, 0, len(broken))
		for _, r := range broken {
			ids = append(ids, r.ID+": "+r.Message)
		}
		s.deps.Logger.ErrorContext(ctx, "invoices: a rendered EHF breaks the module's own invariants", "invoice_id", inv.ID,
			"rules", strings.Join(ids, "; "))
		return renderedEHF{}, nil, fmt.Errorf("invoices: document %d's EHF breaks %d invariants", inv.ID, len(broken))
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	return renderedEHF{
		body: body, sha256: hash, pdfHash: pdf.sha256, sender: sellerPeppolID,
		key: fmt.Sprintf("documents/%d/%d-%s.xml", inv.ID, *inv.Number, hash),
	}, nil, nil
}

// errUBLBroken is a stored UBL gone or altered: a 500 the operator is told of
// in the log, never papered over by rendering again.
var errUBLBroken = errors.New("invoices: a stored UBL is missing or does not match its hash")

// loadStoredUBL is a transmission's UBL as the object store holds it, read
// whole and verified against the hash the row carries. errUBLBroken is a
// missing or altered object; errObjectStore a store that could not be read.
func (s *server) loadStoredUBL(ctx context.Context, t store.InvoicesTransmission) ([]byte, error) {
	rc, err := s.objectGet(ctx, t.UblObjectKey)
	switch {
	case errors.Is(err, storage.ErrNotExist):
		s.deps.Logger.ErrorContext(ctx, "invoices: a transmission's stored UBL is gone", "invoice_id", t.InvoiceID,
			"transmission_id", t.ID, "key", t.UblObjectKey)
		return nil, errUBLBroken
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "invoices: the document store could not be read", "transmission_id", t.ID, "error", err.Error())
		return nil, fmt.Errorf("%w: %w", errObjectStore, err)
	}
	body, err := io.ReadAll(io.LimitReader(rc, 2*maxStoredPDF+1))
	_ = rc.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errObjectStore, err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != t.UblSha256 {
		s.deps.Logger.ErrorContext(ctx, "invoices: a transmission's stored UBL does not match its hash", "invoice_id", t.InvoiceID,
			"transmission_id", t.ID, "key", t.UblObjectKey)
		return nil, errUBLBroken
	}
	return body, nil
}

// reusedEHF is the UBL the reuse rule holds a send to (D4, reading 13): the
// newest transmission a person resolved as failed, among all of the
// document's — it was unconfirmed, so its bytes may have reached the receiver
// and every later send carries the same ones, a reused send that was then
// cancelled included. ok is false when the document has none.
func (s *server) reusedEHF(ctx context.Context, q *store.Queries, invoiceID int64) (renderedEHF, bool, error) {
	latest, err := q.LatestPersonResolvedFailedTransmission(ctx, invoiceID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return renderedEHF{}, false, nil
	case err != nil:
		return renderedEHF{}, false, fmt.Errorf("invoices: read document %d's person-resolved transmissions: %w", invoiceID, err)
	}
	body, err := s.loadStoredUBL(ctx, latest)
	if err != nil {
		return renderedEHF{}, false, err
	}
	return renderedEHF{
		body: body, key: latest.UblObjectKey, sha256: latest.UblSha256, pdfHash: latest.PdfSha256,
		sender: latest.SenderParticipant, reused: true,
	}, true, nil
}

// storeUBL puts a rendered UBL under its content-addressed key unless it is
// there already, D7's store-once shape: the same bytes are stored once.
func (s *server) storeUBL(ctx context.Context, r renderedEHF) error {
	exists, err := s.objectExists(ctx, r.key)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.objectPut(ctx, r.key, bytes.NewReader(r.body), "application/xml")
}

// PostInvoicesByIdSendEhf Send an issued document as EHF
// (POST /api/v1/invoices/{id}/send-ehf)
func (s *server) PostInvoicesByIdSendEhf(ctx context.Context, req gen.PostInvoicesByIdSendEhfRequestObject) (gen.PostInvoicesByIdSendEhfResponseObject, error) {
	refused := func(c gen.InvoicesConflictProblem) (gen.PostInvoicesByIdSendEhfResponseObject, error) {
		return gen.PostInvoicesByIdSendEhf409ApplicationProblemPlusJSONResponse(c), nil
	}

	// 1. An installation that cannot send as EHF reads no document.
	q := store.New(s.deps.Pool)
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	available, _, err := s.ehfAvailable(ctx, q, settings)
	if err != nil {
		return nil, err
	}
	if !available {
		return gen.PostInvoicesByIdSendEhf503ApplicationProblemPlusJSONResponse(ehfUnavailable()), nil
	}

	// 2. The document, issued, its customer not anonymised.
	inv, err := q.GetInvoice(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostInvoicesByIdSendEhf404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d: %w", req.Id, err)
	}
	if inv.Status != statusIssued {
		return refused(conflict(codeInvoiceDraft, cannotSendEhfTitle, "A draft is not a sales document: issue it before sending it."))
	}
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	if erased {
		return refused(customerAnonymisedEhf())
	}

	// 3–5. The receiver's address, a reference, and no live transmission.
	if !validParticipant(inv.BuyerPeppolID) {
		return refused(conflict(codeNoPeppolID, cannotSendEhfTitle,
			"The document was issued to a buyer without a Peppol id, so it has no address on the Peppol network. Credit it and issue it again once the customer has one, or send it by e-mail."))
	}
	if !hasBuyerReference(inv) {
		return refused(conflict(codeBuyerReferenceMissing, cannotSendEhfTitle,
			"Peppol needs the buyer's reference or an order reference, and the document has neither. They cannot change after the issue: credit it and issue it again with one."))
	}
	active, err := q.ActiveTransmissionExists(ctx, inv.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's transmissions: %w", inv.ID, err)
	}
	if active {
		return refused(ehfAlreadySent())
	}

	// 6. The stored PDF, the render and the pre-check: cheap and local,
	// before the network.
	pdf, problem := s.loadStoredPDF(ctx, q, inv)
	switch {
	case problem == nil:
	case problem.unavailable != nil:
		return gen.PostInvoicesByIdSendEhf503ApplicationProblemPlusJSONResponse(*problem.unavailable), nil
	case problem.broken:
		return gen.PostInvoicesByIdSendEhf500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
	default:
		return nil, problem.err
	}
	rendered, rules, err := s.renderEHF(ctx, q, inv, *settings.PeppolID, pdf)
	if err != nil {
		return nil, err
	}
	if len(rules) > 0 {
		c := conflict(codeEhfInvalid, cannotSendEhfTitle, "The document's EHF does not pass the Peppol rules named in rules.")
		wire := make([]gen.InvoicesEhfRule, 0, len(rules))
		for _, r := range rules {
			wire = append(wire, gen.InvoicesEhfRule{Id: r.ID, Message: r.Message})
		}
		c.Rules = &wire
		return refused(c)
	}

	// 7. The receiver, re-checked on the network outside any lock.
	participant := *inv.BuyerPeppolID
	lookup, err := s.lookupReceiver(ctx, participant)
	if err != nil {
		c := conflict(codePeppolLookupFailed, "The Peppol network could not be asked",
			"The Peppol network could not say whether the receiver accepts this document. Nothing was queued; try again.")
		c.Status = ptr(int32(http.StatusBadGateway))
		return gen.PostInvoicesByIdSendEhf502ApplicationProblemPlusJSONResponse(c), nil
	}
	receivable := canReceive(lookup, inv.Kind)
	if !lookup.Registered || !receivable {
		c := conflict(codePeppolNotReceivable, cannotSendEhfTitle,
			"The receiver is not registered on the Peppol network, or does not accept this document type there. Send it by e-mail instead.")
		c.PeppolRegistered, c.PeppolCanReceive = ptr(lookup.Registered), ptr(receivable)
		return refused(c)
	}

	// 8. The bytes to queue: the reused ones, or the render stored once.
	reused, ok, err := s.reusedEHF(ctx, q, inv.ID)
	switch {
	case errors.Is(err, errUBLBroken):
		return gen.PostInvoicesByIdSendEhf500ApplicationProblemPlusJSONResponse(storedDocumentBroken()), nil
	case errors.Is(err, errObjectStore):
		return gen.PostInvoicesByIdSendEhf503ApplicationProblemPlusJSONResponse(storageUnavailable("The document store could not be read. Try again.")), nil
	case err != nil:
		return nil, err
	case ok:
		rendered = reused
	default:
		if err := s.storeUBL(ctx, rendered); err != nil {
			s.deps.Logger.WarnContext(ctx, "invoices: an EHF could not be stored", "invoice_id", inv.ID, "error", err.Error())
			return gen.PostInvoicesByIdSendEhf503ApplicationProblemPlusJSONResponse(storageUnavailable("The document store could not be reached. Try again.")), nil
		}
	}

	// 9. The transmission, queued under the document's lock.
	var refusal *gen.InvoicesConflictProblem
	var noCredentials bool
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		read := txq.LockInvoice
		if sendEhfWithoutLock {
			read = txq.GetInvoice
		}
		locked, err := read(ctx, inv.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errDocumentGone
		}
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", inv.ID, err)
		}
		// An erasure that committed after the read above has committed
		// by now: it locks the customer's documents.
		erased, err := txq.CustomerErased(ctx, locked.CustomerID)
		if err != nil {
			return fmt.Errorf("invoices: read whether customer %d was erased: %w", locked.CustomerID, err)
		}
		if erased {
			refusal = ptr(customerAnonymisedEhf())
			return errRefused
		}
		if _, err := txq.LockAccessPointCredentialsForShare(ctx); errors.Is(err, pgx.ErrNoRows) {
			noCredentials = true
			return errRefused
		} else if err != nil {
			return fmt.Errorf("invoices: read the access point credentials: %w", err)
		}
		active, err := txq.ActiveTransmissionExists(ctx, locked.ID)
		if err != nil {
			return fmt.Errorf("invoices: read document %d's transmissions: %w", locked.ID, err)
		}
		if active {
			refusal = ptr(ehfAlreadySent())
			return errRefused
		}
		if beforeTransmissionInsert != nil {
			beforeTransmissionInsert(ctx, locked.ID)
		}
		now := s.deps.Clock()
		_, err = txq.InsertTransmission(ctx, store.InsertTransmissionParams{
			InvoiceID: locked.ID, Provider: providerStorecove, IdempotencyKey: uuid.New(),
			SenderParticipant: rendered.sender, ReceiverParticipant: participant,
			DocumentType: documentTypeOf(locked.Kind), ProcessID: peppol.BillingProcessID,
			UblObjectKey: rendered.key, UblSha256: rendered.sha256, PdfSha256: rendered.pdfHash,
			Now: now, LookupRegistered: lookup.Registered, LookupCanReceive: receivable, LookupAt: now,
			CreatedByUserID: callerID(ctx),
		})
		switch {
		case db.IsUniqueViolation(err, uxTransmissionsActive):
			refusal = ptr(ehfAlreadySent())
			return errRefused
		case isAnonymisedRefusal(err):
			refusal = ptr(customerAnonymisedEhf())
			return errRefused
		case err != nil:
			return fmt.Errorf("invoices: queue document %d's transmission: %w", locked.ID, err)
		}
		return nil
	})
	switch {
	case refusal != nil:
		return refused(*refusal)
	case noCredentials:
		return gen.PostInvoicesByIdSendEhf503ApplicationProblemPlusJSONResponse(ehfUnavailable()), nil
	case errors.Is(err, errDocumentGone):
		return gen.PostInvoicesByIdSendEhf404Response{}, nil
	case err != nil:
		return nil, err
	}
	s.deps.Logger.InfoContext(ctx, "invoices: a document is queued as EHF", "invoice_id", inv.ID, "reused_ubl", rendered.reused)

	// 10. The document with its EHF state, read again when step 6 stored its
	// PDF, so it says so.
	if inv.PdfSha256 == nil {
		if inv, err = q.GetInvoice(ctx, inv.ID); err != nil {
			return nil, fmt.Errorf("invoices: re-read document %d: %w", req.Id, err)
		}
	}
	canIssue := true // the operation's own rule
	resp, err := s.renderInvoice(ctx, q, inv, nil, nil, &canIssue)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdSendEhf200JSONResponse(resp), nil
}
