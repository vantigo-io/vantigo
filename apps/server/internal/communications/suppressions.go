package communications

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
)

// This file is the Suppressions area (EP/SuppressionEndpoints.cs,
// communications inventory §1.5, §2's CreateSuppression bullet, §19.2 item
// 3, §19.1 item 7, design doc D7): getCommunicationsSuppressions,
// postCommunicationsSuppressions, getCommunicationsSuppressionsById and
// deleteCommunicationsSuppressionsById.
//
// All four require communications:suppressions-manage ALONE
// (communications.yaml's x-vantigo-access on every /suppressions* operation)
// — unlike conversations-view/-manage's split between reads and writes
// elsewhere in this module, both suppression reads sit behind the same
// -manage permission as the writes. module.Router already enforces this
// from the contract; no handler here re-checks it, and suppressions_test.go
// pins that neither conversations-view nor conversations-manage alone
// substitutes for suppressions-manage on any of the four routes.
//
// The normalisation at the heart of this area is EmailSuppression.Normalize
// (SV/EmailSuppression.cs:5): Trim().ToUpperInvariant() — UPPERCASED, ported
// as normalizeEmail in conversations_validation.go and shared with reply's
// own suppression gate (conversations_reply.go) and the recipient dedupe
// check (addDuplicateRecipientError). The uppercased form is what gets
// stored (ux_suppressions_normalized_email_address, the migration's own
// comment), what SuppressionResponse.emailAddress echoes back below, and
// what reply's fields.recipients carries (conversations_reply_internal_test.go's
// TestQueueReply_RecipientSuppressed already pins that half). The contact
// linker's ConversationContactLinker.NormalizeEmail lowercases instead
// (inventory §19.1 item 7, §10 item 4, design doc D7) — a deliberate
// asymmetry this module never harmonises; there is no shared "normalize"
// helper spanning the two directions, by design.

// suppressionResponseOf is SuppressionResponse's direct constructor
// (`new SuppressionResponse(item.Id, item.NormalizedEmailAddress, item.Reason, item.CreatedAt)`,
// SuppressionEndpoints.cs:22-25).
func suppressionResponseOf(row store.CommunicationsSuppression) gen.SuppressionResponse {
	return gen.SuppressionResponse{
		Id:           row.ID,
		EmailAddress: row.NormalizedEmailAddress,
		Reason:       row.Reason,
		CreatedAt:    row.CreatedAt,
	}
}

// validSuppressionReason is CommunicationValidation.ValidOptional(value, 500)
// as CreateSuppression's own field rule uses it (`:68`): at most 500 UTF-16
// code units, already trimmed (untrimmed is invalid, never silently
// trimmed), no control characters. Duplicated rather than shared with
// channels_validation.go's validDisplayName (a 200-length instance of the
// exact same .NET rule) — this module's own convention, already followed by
// utf16Length's per-area duplication, is one small validator per area file
// rather than a cross-area helper for three lines of logic.
func validSuppressionReason(v string) bool {
	if utf16Length(v) > 500 {
		return false
	}
	if v != strings.TrimSpace(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// GetCommunicationsSuppressions List suppressed addresses
// (GET /api/v1/communications/suppressions)
//
// ListSuppressions (inventory §1.5, §19.2 item 19): a bare array, ordered by
// CreatedAt descending — newest first, no pagination envelope, and
// deliberately the opposite direction from tags' Name ascending order.
func (s *server) GetCommunicationsSuppressions(ctx context.Context, _ gen.GetCommunicationsSuppressionsRequestObject) (gen.GetCommunicationsSuppressionsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListSuppressions(ctx)
	if err != nil {
		return nil, fmt.Errorf("communications: list suppressions: %w", err)
	}
	data := make([]gen.SuppressionResponse, 0, len(rows))
	for _, row := range rows {
		data = append(data, suppressionResponseOf(row))
	}
	return gen.GetCommunicationsSuppressions200JSONResponse(data), nil
}

// PostCommunicationsSuppressions Suppress an address
// (POST /api/v1/communications/suppressions)
//
// CreateSuppression (inventory §1.5, §2, §19.2 item 20): (1) ValidateSuppression
// -> 400, field-keyed (emailAddress / reason); (2) normalize (uppercase,
// D7); (3) existing-row lookup by the normalised address -> 200 with the
// PRE-EXISTING row — its original reason and createdAt, never the request's
// new reason, which is silently discarded; (4) otherwise insert -> 201.
// Dedupe is 200, never 409: the unique index on normalized_email_address
// exists to enforce the invariant, not to report a conflict to the caller.
func (s *server) PostCommunicationsSuppressions(ctx context.Context, req gen.PostCommunicationsSuppressionsRequestObject) (gen.PostCommunicationsSuppressionsResponseObject, error) {
	var emailAddress string
	var reason *string
	if req.Body != nil {
		if req.Body.EmailAddress != nil {
			emailAddress = *req.Body.EmailAddress
		}
		reason = req.Body.Reason
	}

	fieldErrs := map[string][]string{}
	if !validChannelAddress(emailAddress) {
		fieldErrs["emailAddress"] = []string{"A valid email address is required."}
	}
	if reason != nil && !validSuppressionReason(*reason) {
		fieldErrs["reason"] = []string{"Reason is invalid."}
	}
	if len(fieldErrs) > 0 {
		return gen.PostCommunicationsSuppressions400JSONResponse(validationErrorBody(fieldErrs)), nil
	}

	normalized := normalizeEmail(emailAddress)
	q := store.New(s.deps.Pool)

	existing, err := q.GetSuppressionByNormalizedEmail(ctx, normalized)
	if err == nil {
		// The pre-existing row, unmodified: its own reason and createdAt,
		// not the request's (inventory §19.2 item 20).
		return gen.PostCommunicationsSuppressions200JSONResponse(suppressionResponseOf(existing)), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("communications: lookup suppression: %w", err)
	}

	row, err := q.InsertSuppression(ctx, store.InsertSuppressionParams{
		ID:                     uuid.New(),
		NormalizedEmailAddress: normalized,
		Reason:                 reason,
		CreatedAt:              s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("communications: insert suppression: %w", err)
	}
	return gen.PostCommunicationsSuppressions201JSONResponse(suppressionResponseOf(row)), nil
}

// GetCommunicationsSuppressionsById Get a suppressed address
// (GET /api/v1/communications/suppressions/{id})
//
// GetSuppression: id lookup -> 404 bare when absent.
func (s *server) GetCommunicationsSuppressionsById(ctx context.Context, req gen.GetCommunicationsSuppressionsByIdRequestObject) (gen.GetCommunicationsSuppressionsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetSuppressionByID(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCommunicationsSuppressionsById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get suppression: %w", err)
	}
	return gen.GetCommunicationsSuppressionsById200JSONResponse(suppressionResponseOf(row)), nil
}

// DeleteCommunicationsSuppressionsById Remove a suppressed address
// (DELETE /api/v1/communications/suppressions/{id})
//
// DeleteSuppression: id lookup -> 404 bare when absent, else delete -> 204.
func (s *server) DeleteCommunicationsSuppressionsById(ctx context.Context, req gen.DeleteCommunicationsSuppressionsByIdRequestObject) (gen.DeleteCommunicationsSuppressionsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.DeleteSuppression(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("communications: delete suppression: %w", err)
	}
	if rows == 0 {
		return gen.DeleteCommunicationsSuppressionsById404Response{}, nil
	}
	return gen.DeleteCommunicationsSuppressionsById204Response{}, nil
}
