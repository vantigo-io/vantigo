package invoices

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is an issued document's EHF transmissions as the wire sees them
// (EHF and KID design D9, D10): the ehf block every issued document answers,
// the list's status, and the three operations on one transmission — a cancel
// of one never attempted, a person's resolution of an unconfirmed one, and the
// stored UBL's download. None reads the directory or calls the access point;
// cancel and resolve are each one conditional UPDATE, refused by the rows it
// matches.

const invalidResolutionTitle = "Invalid resolution"

// errNoSuchTransmission is a transmission that is not the document's.
var errNoSuchTransmission = errors.New("invoices: no such transmission on the document")

// transmissionResponse is one transmission on the wire. providerRef, the
// reason — the provider's and the receiver's words, redacted — and the crash
// marker, operational detail (reading 19), are an issuer's only (D10).
func transmissionResponse(t store.InvoicesTransmission, issuerFields bool) gen.InvoicesTransmission {
	out := gen.InvoicesTransmission{
		Id: t.ID, Status: t.Status, Provider: t.Provider, IdempotencyKey: t.IdempotencyKey,
		ReceiverParticipant: t.ReceiverParticipant, UblSha256: t.UblSha256, QueuedAt: t.QueuedAt,
		SubmittedAt: t.SubmittedAt, DeliveredAt: t.DeliveredAt, FailedAt: t.FailedAt, CancelledAt: t.CancelledAt,
		ResolvedByUserId: t.ResolvedByUserID, ResolutionNote: t.ResolutionNote, UblUrl: ublPath(t.InvoiceID, t.ID),
	}
	if issuerFields {
		out.ProviderRef, out.SubmitAttemptedAt = t.ProviderRef, t.SubmitAttemptedAt
		if t.LastError != nil && strings.TrimSpace(*t.LastError) != "" {
			out.Reason = ptr(redactReason(*t.LastError))
		}
	}
	return out
}

// ehfBlockedBy is the first refusal a send as EHF of inv would meet, judged
// without the network and without a render (reading 9): D8's steps 1 to 5 —
// the installation, the customer, the receiver's address, a reference, no
// live transmission — then a line in category K, the one pre-check rule the
// lines alone decide. "" when nothing blocks it.
func (s *server) ehfBlockedBy(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, lines []store.InvoicesLine, rows []store.InvoicesTransmission) (string, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return "", fmt.Errorf("invoices: read the settings: %w", err)
	}
	available, _, err := s.ehfAvailable(ctx, q, settings)
	if err != nil {
		return "", err
	}
	if !available {
		return codeEhfUnavailable, nil
	}
	erased, err := q.CustomerErased(ctx, inv.CustomerID)
	if err != nil {
		return "", fmt.Errorf("invoices: read whether customer %d was erased: %w", inv.CustomerID, err)
	}
	switch {
	case erased:
		return codeCustomerAnonymised, nil
	case !validParticipant(inv.BuyerPeppolID):
		return codeNoPeppolID, nil
	case !hasBuyerReference(inv):
		return codeBuyerReferenceMissing, nil
	}
	for _, t := range rows {
		if activeTransmission[t.Status] {
			return codeEhfAlreadySent, nil
		}
	}
	if hasCategoryK(lines) {
		return codeEhfInvalid, nil
	}
	return "", nil
}

// ehfState is an issued document's ehf block (D10): the latest transmission's
// status and timestamps, or not_sent; every transmission, the newest first;
// and whether a send would go. canIssue is whether the caller holds
// invoices:issue when the handler has asked already, nil to ask here — and
// only when a transmission carries the provider's words to show.
func (s *server) ehfState(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, lines []store.InvoicesLine, canIssue *bool) (gen.InvoicesEhfState, error) {
	rows, err := q.TransmissionsOf(ctx, inv.ID)
	if err != nil {
		return gen.InvoicesEhfState{}, fmt.Errorf("invoices: read document %d's transmissions: %w", inv.ID, err)
	}
	issuerFields := false
	switch {
	case canIssue != nil:
		issuerFields = *canIssue
	default:
		for _, t := range rows {
			if t.ProviderRef != nil || t.LastError != nil || t.SubmitAttemptedAt != nil {
				issuerFields = s.has(ctx, "invoices:issue")
				break
			}
		}
	}
	out := gen.InvoicesEhfState{Status: ehfNotSent, Transmissions: make([]gen.InvoicesTransmission, 0, len(rows))}
	for _, t := range rows {
		out.Transmissions = append(out.Transmissions, transmissionResponse(t, issuerFields))
	}
	if len(rows) > 0 {
		latest := out.Transmissions[0]
		out.Status, out.QueuedAt, out.SubmittedAt = latest.Status, &latest.QueuedAt, latest.SubmittedAt
		out.DeliveredAt, out.FailedAt = latest.DeliveredAt, latest.FailedAt
		out.ProviderRef, out.Reason = latest.ProviderRef, latest.Reason
	}
	blocked, err := s.ehfBlockedBy(ctx, q, inv, lines, rows)
	if err != nil {
		return gen.InvoicesEhfState{}, err
	}
	out.CanSend = blocked == ""
	if blocked != "" {
		out.BlockedBy = &blocked
	}
	return out, nil
}

// ehfHeaded is whether a draft is headed for EHF (D8): its customer's
// billing profile prefers EHF or carries a Peppol id — or, for a credit-note
// draft, which reads no directory, the buyer snapshot it copied has one.
func ehfHeaded(inv store.InvoicesInvoice, profile *contracts.CustomerBillingProfile) bool {
	if profile != nil {
		return profile.InvoiceDelivery == "ehf" || profile.PeppolID != ""
	}
	return inv.Kind == kindCreditNote && inv.BuyerPeppolID != nil && *inv.BuyerPeppolID != ""
}

// ehfStatuses is each issued document's latest EHF status on a page of the
// list, in one query: its newest transmission's, or not_sent.
func ehfStatuses(ctx context.Context, q *store.Queries, issued []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(issued))
	if len(issued) == 0 {
		return out, nil
	}
	for _, id := range issued {
		out[id] = ehfNotSent
	}
	rows, err := q.TransmissionsOfDocuments(ctx, issued)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the page's transmissions: %w", err)
	}
	for _, t := range rows {
		out[t.InvoiceID] = t.Status
	}
	return out, nil
}

// transmissionOf is a document and one of its transmissions, errDocumentGone
// or errNoSuchTransmission when either is not there — a transmission of
// another document is not this one's.
func transmissionOf(ctx context.Context, q *store.Queries, invoiceID, transmissionID int64) (store.InvoicesInvoice, store.InvoicesTransmission, error) {
	inv, err := q.GetInvoice(ctx, invoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.InvoicesInvoice{}, store.InvoicesTransmission{}, errDocumentGone
	}
	if err != nil {
		return store.InvoicesInvoice{}, store.InvoicesTransmission{}, fmt.Errorf("invoices: read document %d: %w", invoiceID, err)
	}
	t, err := q.GetTransmission(ctx, transmissionID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && t.InvoiceID != inv.ID) {
		return store.InvoicesInvoice{}, store.InvoicesTransmission{}, errNoSuchTransmission
	}
	if err != nil {
		return store.InvoicesInvoice{}, store.InvoicesTransmission{}, fmt.Errorf("invoices: read transmission %d: %w", transmissionID, err)
	}
	return inv, t, nil
}

// missing is whether err is transmissionOf's "not there".
func missing(err error) bool {
	return errors.Is(err, errDocumentGone) || errors.Is(err, errNoSuchTransmission)
}

// PostInvoicesByIdTransmissionsByTransmissionIdCancel Cancel a queued EHF transmission
// (POST /api/v1/invoices/{id}/transmissions/{transmissionId}/cancel)
func (s *server) PostInvoicesByIdTransmissionsByTransmissionIdCancel(ctx context.Context, req gen.PostInvoicesByIdTransmissionsByTransmissionIdCancelRequestObject) (gen.PostInvoicesByIdTransmissionsByTransmissionIdCancelResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, t, err := transmissionOf(ctx, q, req.Id, req.TransmissionId)
	switch {
	case missing(err):
		return gen.PostInvoicesByIdTransmissionsByTransmissionIdCancel404Response{}, nil
	case err != nil:
		return nil, err
	}
	// Never attempted and not leased, or nothing: once the worker has
	// stamped the crash marker the provider may hold the document.
	n, err := q.CancelUnattemptedTransmission(ctx, store.CancelUnattemptedTransmissionParams{Now: s.deps.Clock(), ID: t.ID, InvoiceID: inv.ID})
	if err != nil {
		return nil, fmt.Errorf("invoices: cancel transmission %d: %w", t.ID, err)
	}
	if n == 0 {
		return gen.PostInvoicesByIdTransmissionsByTransmissionIdCancel409ApplicationProblemPlusJSONResponse(conflict(codeTransmissionNotCancellable,
			"The transmission cannot be cancelled",
			"Only a queued transmission that was never attempted is cancelled. This one may already have reached the access point; its outcome decides.")), nil
	}
	s.deps.Logger.InfoContext(ctx, "invoices: an EHF transmission is cancelled", "invoice_id", inv.ID, "transmission_id", t.ID)
	canIssue := true // the operation's own rule
	resp, err := s.renderInvoice(ctx, q, inv, nil, nil, &canIssue)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdTransmissionsByTransmissionIdCancel200JSONResponse(resp), nil
}

// PostInvoicesByIdTransmissionsByTransmissionIdResolve Resolve an unconfirmed EHF transmission
// (POST /api/v1/invoices/{id}/transmissions/{transmissionId}/resolve)
func (s *server) PostInvoicesByIdTransmissionsByTransmissionIdResolve(ctx context.Context, req gen.PostInvoicesByIdTransmissionsByTransmissionIdResolveRequestObject) (gen.PostInvoicesByIdTransmissionsByTransmissionIdResolveResponseObject, error) {
	var errs map[string][]string
	if !req.Body.Outcome.Valid() {
		errs = withFieldError(errs, "outcome", "The outcome is delivered or failed")
	}
	note := strings.TrimSpace(req.Body.Note)
	if note == "" {
		errs = withFieldError(errs, "note", "A resolution needs a note saying what the provider answered")
	} else if msg := maxLength("A note", note, 500); msg != "" {
		errs = withFieldError(errs, "note", msg)
	}
	if errs != nil {
		return gen.PostInvoicesByIdTransmissionsByTransmissionIdResolve400ApplicationProblemPlusJSONResponse(invalid(invalidResolutionTitle, errs)), nil
	}
	q := store.New(s.deps.Pool)
	inv, t, err := transmissionOf(ctx, q, req.Id, req.TransmissionId)
	switch {
	case missing(err):
		return gen.PostInvoicesByIdTransmissionsByTransmissionIdResolve404Response{}, nil
	case err != nil:
		return nil, err
	}
	n, err := q.ResolveTransmission(ctx, store.ResolveTransmissionParams{
		Outcome: string(req.Body.Outcome), Now: s.deps.Clock(), ResolvedByUserID: ptr(callerID(ctx)), ResolutionNote: &note,
		ID: t.ID, InvoiceID: inv.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: resolve transmission %d: %w", t.ID, err)
	}
	if n == 0 {
		return gen.PostInvoicesByIdTransmissionsByTransmissionIdResolve409ApplicationProblemPlusJSONResponse(conflict(codeTransmissionNotResolvable,
			"The transmission cannot be resolved",
			"Only a transmission awaiting confirmation is resolved by a person; this one is not.")), nil
	}
	s.deps.Logger.InfoContext(ctx, "invoices: an unconfirmed EHF transmission is resolved", "invoice_id", inv.ID,
		"transmission_id", t.ID, "outcome", string(req.Body.Outcome))
	canIssue := true // the operation's own rule
	resp, err := s.renderInvoice(ctx, q, inv, nil, nil, &canIssue)
	if err != nil {
		return nil, err
	}
	return gen.PostInvoicesByIdTransmissionsByTransmissionIdResolve200JSONResponse(resp), nil
}

// storedUBLBroken is the 500 a UBL download answers when the stored object is
// gone or its bytes no longer match the transmission's hash.
func storedUBLBroken() apicommon.ProblemDetails {
	return apicommon.ProblemStatus("The stored document is damaged",
		"The transmission's stored UBL is missing or does not match what was stored. It is not rendered again; the operator has been told.",
		http.StatusInternalServerError)
}

// ublDownload sets the headers a UBL answers with, as a PDF's do.
type ublDownload struct {
	body        gen.GetInvoicesByIdTransmissionsByTransmissionIdUbl200ApplicationxmlResponse
	disposition string
}

func (r ublDownload) VisitGetInvoicesByIdTransmissionsByTransmissionIdUblResponse(w http.ResponseWriter) error {
	pdfHeaders(w, r.disposition)
	return r.body.VisitGetInvoicesByIdTransmissionsByTransmissionIdUblResponse(w)
}

// GetInvoicesByIdTransmissionsByTransmissionIdUbl Download an EHF transmission's UBL
// (GET /api/v1/invoices/{id}/transmissions/{transmissionId}/ubl)
func (s *server) GetInvoicesByIdTransmissionsByTransmissionIdUbl(ctx context.Context, req gen.GetInvoicesByIdTransmissionsByTransmissionIdUblRequestObject) (gen.GetInvoicesByIdTransmissionsByTransmissionIdUblResponseObject, error) {
	q := store.New(s.deps.Pool)
	inv, t, err := transmissionOf(ctx, q, req.Id, req.TransmissionId)
	switch {
	case missing(err):
		return gen.GetInvoicesByIdTransmissionsByTransmissionIdUbl404Response{}, nil
	case err != nil:
		return nil, err
	}
	if !s.storageConfigured {
		return gen.GetInvoicesByIdTransmissionsByTransmissionIdUbl503ApplicationProblemPlusJSONResponse(storageUnavailable("This installation has no object store.")), nil
	}
	body, err := s.loadStoredUBL(ctx, t)
	switch {
	case errors.Is(err, errUBLBroken):
		return gen.GetInvoicesByIdTransmissionsByTransmissionIdUbl500ApplicationProblemPlusJSONResponse(storedUBLBroken()), nil
	case errors.Is(err, errObjectStore):
		return gen.GetInvoicesByIdTransmissionsByTransmissionIdUbl503ApplicationProblemPlusJSONResponse(storageUnavailable("The document store could not be read. Try again.")), nil
	case err != nil:
		return nil, err
	}
	return ublDownload{
		body:        gen.GetInvoicesByIdTransmissionsByTransmissionIdUbl200ApplicationxmlResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))},
		disposition: fmt.Sprintf("attachment; filename=%q", ublFileName(inv, t.ID)),
	}, nil
}
