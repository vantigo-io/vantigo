package invoices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is a customer's reminder policy (invoices payments and reminders
// design D7): whether a customer is reminded and charged — a credit-control
// decision, so an invoices table keyed by the opaque customer id, set under
// invoices:payments, not the customers module's billing profile. No row is
// normal. The merge, the export and the erase of it are customer_slots.go's.

// maxPolicyNote is the note's column width, in characters.
const maxPolicyNote = 500

// policyAfterLock is called by a policy PUT inside its transaction right
// after it has locked the customer's documents and the policy row, before
// it writes; an error rolls the PUT back. Nil in production; a race test
// parks a PUT there (SetPolicyAfterLock).
var policyAfterLock func(ctx context.Context, customerID int32) error

// policyStrictness orders the modes (D7, reading 7): none is stricter than
// no_charges, which is stricter than normal.
var policyStrictness = map[string]int{
	string(reminderrules.ModeNormal): 0, string(reminderrules.ModeNoCharges): 1, string(reminderrules.ModeNone): 2,
}

// stricter is the stricter of two modes — the merge's rule when both
// customers have a policy; a on a tie.
func stricter(a, b string) string {
	if policyStrictness[b] > policyStrictness[a] {
		return b
	}
	return a
}

// joinedNotes is the merge's note (D7): the survivor's first, then the
// absorbed customer's, " / " between them, an empty one left out, cut to the
// column's 500 characters.
func joinedNotes(into, from string) string {
	var notes []string
	for _, n := range []string{into, from} {
		if n != "" {
			notes = append(notes, n)
		}
	}
	joined := strings.Join(notes, " / ")
	if utf8.RuneCountInString(joined) > maxPolicyNote {
		joined = string([]rune(joined)[:maxPolicyNote])
	}
	return joined
}

// policyWire is a customer's policy on the wire; nil row is no policy —
// normal, with no note and nobody's.
func policyWire(customerID int32, row *store.InvoicesCustomerReminderPolicy) gen.InvoicesReminderPolicy {
	if row == nil {
		return gen.InvoicesReminderPolicy{CustomerId: customerID, Mode: gen.InvoicesReminderPolicyModeNormal, Note: ""}
	}
	return gen.InvoicesReminderPolicy{
		CustomerId: customerID, Mode: gen.InvoicesReminderPolicyMode(row.Mode), Note: row.Note,
		UpdatedAt: ptr(row.UpdatedAt), UpdatedBy: ptr(row.UpdatedByUserID),
	}
}

// GetInvoicesCustomersByCustomerIdReminderPolicy Get a customer's reminder policy
// (GET /api/v1/invoices/customers/{customerId}/reminder-policy)
func (s *server) GetInvoicesCustomersByCustomerIdReminderPolicy(ctx context.Context, req gen.GetInvoicesCustomersByCustomerIdReminderPolicyRequestObject) (gen.GetInvoicesCustomersByCustomerIdReminderPolicyResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetPolicy(ctx, req.CustomerId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesCustomersByCustomerIdReminderPolicy200JSONResponse(policyWire(req.CustomerId, nil)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read customer %d's reminder policy: %w", req.CustomerId, err)
	}
	return gen.GetInvoicesCustomersByCustomerIdReminderPolicy200JSONResponse(policyWire(req.CustomerId, &row)), nil
}

// PutInvoicesCustomersByCustomerIdReminderPolicy Set a customer's reminder policy
// (PUT /api/v1/invoices/customers/{customerId}/reminder-policy)
//
// The lock order (D18, plan reading 11): the customer's documents FOR SHARE,
// newest first — the order of the merge's LockCustomerDocuments and of every
// invoice-only path — then, under that lock, the customer must have a
// document here and must not be anonymised (404), and only then is the
// policy row locked and written. A PUT racing a merge therefore either lands
// first, and the merge — waiting on the documents — moves its row; or waits
// on the merge's documents and, once the merge commits, finds the customer
// has none left (404). Without the share lock a PUT that saw the documents
// on the pool would insert a row for the absorbed customer after the merge's
// lockPolicies found none: an orphan. normal with an empty note deletes the
// row — no row is normal.
func (s *server) PutInvoicesCustomersByCustomerIdReminderPolicy(ctx context.Context, req gen.PutInvoicesCustomersByCustomerIdReminderPolicyRequestObject) (gen.PutInvoicesCustomersByCustomerIdReminderPolicyResponseObject, error) {
	mode, note := string(req.Body.Mode), strings.TrimSpace(req.Body.Note)
	var errs map[string][]string
	if _, ok := policyStrictness[mode]; !ok {
		errs = withFieldError(errs, "mode", "A reminder policy is normal, no_charges or none")
	}
	if msg := maxLength("A note", note, maxPolicyNote); msg != "" {
		errs = withFieldError(errs, "note", msg)
	}
	if errs != nil {
		return gen.PutInvoicesCustomersByCustomerIdReminderPolicy400ApplicationProblemPlusJSONResponse(
			invalid("Invalid reminder policy", errs)), nil
	}
	now, by, id := s.deps.Clock(), callerID(ctx), req.CustomerId

	notFound := false
	var saved *store.InvoicesCustomerReminderPolicy
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := shareCustomerDocuments(ctx, txq, id); err != nil {
			return err
		}
		held, err := txq.CustomerHasDocuments(ctx, id)
		if err != nil {
			return fmt.Errorf("invoices: read whether customer %d has documents: %w", id, err)
		}
		if !held.HasDocuments || held.Erased {
			notFound = true
			return errRefused
		}
		if err := lockPolicies(ctx, txq, []int32{id}); err != nil {
			return err
		}
		if hook := policyAfterLock; hook != nil {
			if err := hook(ctx, id); err != nil {
				return err
			}
		}
		if mode == string(reminderrules.ModeNormal) && note == "" {
			if _, err := txq.DeletePolicy(ctx, id); err != nil {
				return fmt.Errorf("invoices: delete customer %d's reminder policy: %w", id, err)
			}
			return nil
		}
		row, err := txq.UpsertPolicy(ctx, store.UpsertPolicyParams{
			CustomerID: id, Mode: mode, Note: note, UpdatedByUserID: by, UpdatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("invoices: set customer %d's reminder policy: %w", id, err)
		}
		saved = &row
		return nil
	})
	switch {
	case notFound:
		return gen.PutInvoicesCustomersByCustomerIdReminderPolicy404Response{}, nil
	case err != nil:
		return nil, err
	}
	return gen.PutInvoicesCustomersByCustomerIdReminderPolicy200JSONResponse(policyWire(id, saved)), nil
}

// repointPolicy is the merge's part for the policies (D7), inside the
// merge's transaction and after its documents: both rows locked by customer
// id ascending; the absorbed customer's row moved when the survivor has
// none; when both have one, the survivor's row takes the stricter mode and
// the notes joined, the absorbed row is deleted, and the merged row is
// recorded as the stricter row's author's at the merge's time. It answers
// how many absorbed rows it re-pointed — 0 or 1.
func repointPolicy(ctx context.Context, q *store.Queries, from, into int32, now time.Time) (int64, error) {
	if err := lockPolicies(ctx, q, []int32{from, into}); err != nil {
		return 0, err
	}
	rows, err := q.PoliciesOf(ctx, []int32{from, into})
	if err != nil {
		return 0, fmt.Errorf("invoices: read the reminder policies of %d and %d: %w", from, into, err)
	}
	var absorbed, survivor *store.InvoicesCustomerReminderPolicy
	for i := range rows {
		switch rows[i].CustomerID {
		case from:
			absorbed = &rows[i]
		case into:
			survivor = &rows[i]
		}
	}
	switch {
	case absorbed == nil:
		return 0, nil
	case survivor == nil:
		n, err := q.MovePolicy(ctx, store.MovePolicyParams{FromCustomerID: from, IntoCustomerID: into})
		if err != nil {
			return 0, fmt.Errorf("invoices: move customer %d's reminder policy to %d: %w", from, into, err)
		}
		return n, nil
	}
	mode, author := survivor.Mode, survivor.UpdatedByUserID
	if stricter(survivor.Mode, absorbed.Mode) != survivor.Mode {
		mode, author = absorbed.Mode, absorbed.UpdatedByUserID
	}
	if _, err := q.UpsertPolicy(ctx, store.UpsertPolicyParams{
		CustomerID: into, Mode: mode, Note: joinedNotes(survivor.Note, absorbed.Note), UpdatedByUserID: author, UpdatedAt: now,
	}); err != nil {
		return 0, fmt.Errorf("invoices: merge customer %d's reminder policy into %d's: %w", from, into, err)
	}
	n, err := q.DeletePolicy(ctx, from)
	if err != nil {
		return 0, fmt.Errorf("invoices: delete customer %d's merged reminder policy: %w", from, err)
	}
	return n, nil
}
