package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is POST /customers/{id}/merge (customers merge design D1-D3): the
// customer in the path survives and absorbs sourceId. Everything happens in one
// transaction, because every module shares one database: both customer rows
// are locked, this module's own tables move, and then every
// contracts.CustomerReferenceHolder Compose collected re-points its own schema
// through the same transaction (docs/module-boundaries.md rule 8). An error
// anywhere — a holder's included — rolls every module's part back together.

// The four kinds of reference this module's own tables hold, reported first in
// a merge's answer, before every holder's.
const (
	mergeKindContacts        = "customers.contacts"
	mergeKindAddresses       = "customers.addresses"
	mergeKindTimelineEntries = "customers.timelineEntries"
	mergeKindTags            = "customers.tags"
)

// mergeWriteAttempts is how often a merge's transaction runs before a deadlock
// it keeps losing escapes as a 500 — contactRoleWriteAttempts' three, and for
// that constant's reason. Two writers take their locks in the other order. A
// merge takes both customer rows first and then, copying the absorbed
// customer's associations, a key-share on each contact row, while DELETE
// /customers/contacts/{id} takes the contact row first and the customers
// after. And the merge's tag union locks the absorbed customer's customer_tags
// rows and then key-shares each tag, while DELETE /customers/tags/{tagId}
// locks the tag and then, through its cascade, those same rows — tags.go's
// tagWriteAttempts shape. Either pair can cycle; PostgreSQL kills one side
// (40P01), and the loser runs again from a fresh snapshot in which the other's
// write has simply happened. An attach cannot cycle with a merge: it takes the
// customer first too. The holders' statements run inside this retry as well.
const mergeWriteAttempts = 3

var (
	// errMergeCustomerNotFound is either customer missing under its lock: 404.
	errMergeCustomerNotFound = errors.New("customers: merge customer not found")
	// errMergeRefused aborts the transaction on a refusal; the body travels
	// beside it, as errDuplicateIdentity's does (duplicates.go).
	errMergeRefused = errors.New("customers: merge refused")
)

// customerReadOnlyError is a write refused because its customer takes no more
// changes. Two reasons: merged away (customers merge design D2) — its records
// are the survivor's now, and a write here would quietly start a second
// history nobody sees — or anonymised (customers GDPR design D4) — what is left
// is kept for bookkeeping, and a write would put a person back into it. The 409
// carries code customer_merged and names the survivor, or customer_anonymised
// and names the day. It is an error, not a return value, so that it can abort
// whichever transaction found it and travel out through the helpers in between;
// isReadOnlyCustomer and readOnlyProblem read it back.
type customerReadOnlyError struct {
	problem gen.CustomerConflictProblem
	// into is the survivor as a person reads it, "#1002 Acme AS"; empty for an
	// anonymised customer.
	into string
	// anonymised tells the two apart for the one caller whose words differ,
	// the CSV import's row error.
	anonymised bool
}

func (e *customerReadOnlyError) Error() string {
	if e.anonymised {
		return "customers: customer was anonymised"
	}
	return "customers: customer was merged away"
}

// isReadOnlyCustomer reports whether err is either refusal — customer_merged
// or customer_anonymised — and readOnlyProblem is its 409 body: the pair a
// handler's switch uses, case and answer. Every handler that refused a
// merged-away customer refuses an anonymised one the same way, with no change
// of its own.
func isReadOnlyCustomer(err error) bool {
	var readOnly *customerReadOnlyError
	return errors.As(err, &readOnly)
}

func readOnlyProblem(err error) gen.CustomerConflictProblem {
	var readOnly *customerReadOnlyError
	if errors.As(err, &readOnly) {
		return readOnly.problem
	}
	return gen.CustomerConflictProblem{}
}

// isAnonymisedRefusal reports whether err is the customer_anonymised refusal.
func isAnonymisedRefusal(err error) bool {
	var readOnly *customerReadOnlyError
	return errors.As(err, &readOnly) && readOnly.anonymised
}

// mergedAwayInto is the survivor err's refusal names, "#1002 Acme AS" — empty
// for an anonymised customer's.
func mergedAwayInto(err error) string {
	var readOnly *customerReadOnlyError
	if errors.As(err, &readOnly) {
		return readOnly.into
	}
	return ""
}

// customerMerged is the customer_merged refusal, naming the customer this one
// was merged into.
func customerMerged(intoNumber int64, intoName string) *customerReadOnlyError {
	into := customerLabel(intoNumber, intoName)
	return &customerReadOnlyError{into: into, problem: *mergeConflict("Customer was merged", "customer_merged", fmt.Sprintf(
		"This customer was merged into %s. Its records are there now, and it takes no more changes.", into))}
}

// customerAnonymised is the customer_anonymised refusal (customers GDPR design
// D4), naming the day it was anonymised and nothing else — there is nothing
// else left to name.
func customerAnonymised(at time.Time) *customerReadOnlyError {
	return &customerReadOnlyError{anonymised: true, problem: *mergeConflict("Customer was anonymised", "customer_anonymised", fmt.Sprintf(
		"This customer's personal data was anonymised on %s. What is left is kept for bookkeeping and takes no more changes.",
		at.UTC().Format(time.DateOnly)))}
}

// lockWritableCustomer is every customer-scoped write's LockCustomer: the
// customer row's FOR NO KEY UPDATE lock, then the refusal when the row it
// locked was anonymised or merged away. A merge and an anonymisation both hold
// this same lock until they commit, so a write that queued behind one reads its
// outcome here and refuses rather than writing to a customer that just stopped
// taking writes. Anonymised is asked first: a customer both merged away and
// anonymised has nothing left the survivor's name would help with.
// pgx.ErrNoRows still means the customer does not exist.
func lockWritableCustomer(ctx context.Context, txq *store.Queries, id int32) (store.LockCustomerRow, error) {
	locked, err := txq.LockCustomer(ctx, id)
	if err != nil {
		return locked, err
	}
	if locked.AnonymisedAt != nil {
		return locked, customerAnonymised(*locked.AnonymisedAt)
	}
	if locked.MergedIntoCustomerID == nil {
		return locked, nil
	}
	// Read, not locked, as the merge ladder's own merge_already_merged reads
	// it: the refusal only names it, and the marker's foreign key guarantees
	// the row.
	into, err := txq.GetCustomer(ctx, *locked.MergedIntoCustomerID)
	if err != nil {
		return locked, fmt.Errorf("read the customer %d was merged into: %w", id, err)
	}
	return locked, customerMerged(into.CustomerNumber, into.Name)
}

// refuseReadOnlyCustomer is the same refusal read on the pool, before a write
// gets as far as its lock, for the writes that would otherwise answer something
// else first or do something costly before it: an entry, an association or a
// follow-up of a merged-away customer moved with the merge, so looking it up
// answers 404; and a Peppol lookup or a registry refresh would ask the network
// first. It answers the error lockWritableCustomer would, or nil — for a
// customer that takes writes and equally for one that does not exist, whose 404
// is the caller's to give. Only the lock decides a race; this is the answer
// when there is none.
func refuseReadOnlyCustomer(ctx context.Context, q *store.Queries, id int32) error {
	at, err := q.CustomerAnonymisedAt(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("customers: read the anonymisation of %d: %w", id, err)
	case at != nil:
		return customerAnonymised(*at)
	}
	rows, err := q.MergedIntoForCustomers(ctx, []int32{id})
	if err != nil {
		return fmt.Errorf("customers: read merge marker of %d: %w", id, err)
	}
	if len(rows) == 0 {
		return nil
	}
	return customerMerged(rows[0].CustomerNumber, rows[0].Name)
}

// isUniqueOrExclusionViolation is err carrying a PostgreSQL unique (23505) or
// exclusion (23P01) violation — the two httpx.WriteError answers with a 409.
func isUniqueOrExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23P01")
}

// mergeLockOrder is the rows a merge locks, in the order it locks them:
// ascending id, every multi-customer writer's order (the contact delete's),
// so two merges of one pair never deadlock with each other — and a customer
// named twice is locked once, so merge_self is answered rather than waited on.
func mergeLockOrder(a, b int32) []int32 {
	if a == b {
		return []int32{a}
	}
	return []int32{min(a, b), max(a, b)}
}

// mergeConflict is a merge refusal's 409 body: the module's
// CustomerConflictProblem with its code.
func mergeConflict(title, code, detail string) *gen.CustomerConflictProblem {
	status := int32(http.StatusConflict)
	return &gen.CustomerConflictProblem{Title: &title, Code: &code, Detail: &detail, Status: &status}
}

// customerTypePhrase is a customer type as a refusal says it.
func customerTypePhrase(customerType string) string {
	if customerType == "person" {
		return "a private person"
	}
	return "a business"
}

// mergeRefusal is design D2's ladder after the 404s, read from the two rows
// under their locks, in its order: the same customer twice, two types, a
// survivor that was anonymised, an archived survivor, an absorbed customer that
// was anonymised, an absorbed customer merged away before, then the survivor's
// stale revision. nil, nil lets the merge go ahead.
func mergeRefusal(ctx context.Context, txq *store.Queries, survivor, absorbed store.CustomerForMergeRow, expected *int32) (*gen.CustomerConflictProblem, error) {
	switch {
	case survivor.ID == absorbed.ID:
		return mergeConflict("Cannot merge a customer into itself", "merge_self",
			"A customer cannot absorb itself. Choose the duplicate to merge into this one."), nil
	case survivor.Type != absorbed.Type:
		return mergeConflict("Customer types differ", "merge_type_mismatch", fmt.Sprintf(
			"%s is %s and %s is %s. A merge never changes what a customer is; change one of their types first.",
			customerLabel(absorbed.CustomerNumber, absorbed.Name), customerTypePhrase(absorbed.Type),
			customerLabel(survivor.CustomerNumber, survivor.Name), customerTypePhrase(survivor.Type))), nil
	case survivor.AnonymisedAt != nil:
		// Before the archived survivor's refusal, whose advice — restore it
		// first — the restore itself would then refuse: an anonymised customer
		// is read-only (customers GDPR design D4), and absorbing writes it.
		return &customerAnonymised(*survivor.AnonymisedAt).problem, nil
	case survivor.Status == "archived":
		return mergeConflict("Customer is archived", "merge_into_archived", fmt.Sprintf(
			"%s is archived. Restore it before merging another customer into it.",
			customerLabel(survivor.CustomerNumber, survivor.Name))), nil
	case absorbed.AnonymisedAt != nil:
		// An anonymised customer is read-only (customers GDPR design D4), and a
		// merge writes it; there is nothing of a person left in it to fold in.
		return &customerAnonymised(*absorbed.AnonymisedAt).problem, nil
	case absorbed.MergedIntoCustomerID != nil:
		// The marker's foreign key guarantees the row exists; it is read, not
		// locked — the refusal only names it.
		into, err := txq.GetCustomer(ctx, *absorbed.MergedIntoCustomerID)
		if err != nil {
			return nil, fmt.Errorf("read the customer %d was merged into: %w", absorbed.ID, err)
		}
		return mergeConflict("Customer already merged", "merge_already_merged", fmt.Sprintf(
			"%s was already merged into %s.",
			customerLabel(absorbed.CustomerNumber, absorbed.Name), customerLabel(into.CustomerNumber, into.Name))), nil
	case expected != nil && *expected != survivor.Revision:
		conflict := customerRevisionConflict(*expected, survivor.Revision)
		return &conflict, nil
	}
	return nil, nil
}

// absorbedSnapshot is customer.merged's absorbed payload, from the absorbed
// customer's row as it was read under its lock.
func absorbedSnapshot(c store.CustomerForMergeRow) (mergedCustomerSnapshot, error) {
	rate, err := floatPtrFromNumeric(c.DefaultBillRate)
	if err != nil {
		return mergedCustomerSnapshot{}, err
	}
	return mergedCustomerSnapshot{
		ID: c.ID, CustomerNumber: c.CustomerNumber, Name: c.Name, Type: c.Type, Status: c.Status,
		Identity:    identitySnapshot(identityFromRow(c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType)),
		ContactInfo: contactInfoFromRow(c.Email, c.Phone, c.Website),
		BillingProfile: billingProfileFromRow(c.InvoiceEmail, c.ReminderEmail, c.PaymentTermsDays, c.Currency, c.Language,
			c.InvoiceDelivery, c.ReminderDelivery, c.PeppolID, c.Gln, c.BuyerReference, rate),
		OwnerUserID: c.OwnerUserID,
		GroupID:     c.GroupID,
	}, nil
}

// moveOwnRecords moves this module's tables from the absorbed customer to the
// survivor (design D3), in the one order the constraints allow, and answers
// this module's four kinds. The caller holds both rows locked.
func moveOwnRecords(ctx context.Context, txq *store.Queries, from, into int32, now time.Time) ([]contracts.RepointedReferences, error) {
	// Contacts: the associations are copied (the survivor's own row winning for
	// a shared contact), then the roles (primaries resolved in the statement),
	// and only then are the absorbed rows deleted — the roles' composite foreign
	// key cascades them away with their associations.
	if err := txq.InsertMergedAssociations(ctx, store.InsertMergedAssociationsParams{IntoCustomerID: into, FromCustomerID: from}); err != nil {
		return nil, fmt.Errorf("copy the contact associations: %w", err)
	}
	if err := txq.InsertMergedContactRoles(ctx, store.InsertMergedContactRolesParams{IntoCustomerID: into, FromCustomerID: from}); err != nil {
		return nil, fmt.Errorf("union the contact roles: %w", err)
	}
	contacts, err := txq.DeleteCustomerAssociations(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("remove the absorbed associations: %w", err)
	}
	addresses, err := txq.MoveCustomerAddresses(ctx, store.MoveCustomerAddressesParams{IntoCustomerID: into, FromCustomerID: from, Now: now})
	if err != nil {
		return nil, fmt.Errorf("move the addresses: %w", err)
	}
	entries, err := txq.MoveTimelineEntries(ctx, store.MoveTimelineEntriesParams{IntoCustomerID: into, FromCustomerID: from})
	if err != nil {
		return nil, fmt.Errorf("move the timeline: %w", err)
	}
	if err := txq.MoveTimelineRevisions(ctx, from); err != nil {
		return nil, fmt.Errorf("move the timeline revisions: %w", err)
	}
	tags, err := txq.MergeCustomerTags(ctx, store.MergeCustomerTagsParams{IntoCustomerID: into, FromCustomerID: from})
	if err != nil {
		return nil, fmt.Errorf("union the tags: %w", err)
	}
	// The registry record and the Peppol answer describe an identity the
	// survivor either shares or does not have; the survivor keeps its own, and
	// a refresh or a re-check fetches either again.
	if err := txq.DeleteCustomerRegistryRecord(ctx, from); err != nil {
		return nil, fmt.Errorf("delete the absorbed registry record: %w", err)
	}
	if err := txq.DeleteCustomerPeppolLookup(ctx, from); err != nil {
		return nil, fmt.Errorf("delete the absorbed Peppol answer: %w", err)
	}
	return []contracts.RepointedReferences{
		{Kind: mergeKindContacts, Count: contacts},
		{Kind: mergeKindAddresses, Count: addresses},
		{Kind: mergeKindTimelineEntries, Count: entries},
		{Kind: mergeKindTags, Count: tags.MovedCount},
	}, nil
}

// mergeCustomers is one attempt at the whole merge, inside tx: the locks, the
// ladder, this module's moves, every holder, both rows' writes and the two
// events. A refusal returns its body with errMergeRefused, so db.WithTx rolls
// the attempt back and the handler still has something to answer with.
func (s *server) mergeCustomers(ctx context.Context, tx pgx.Tx, into, from int32, expected *int32, now time.Time, act actor) ([]contracts.RepointedReferences, *gen.CustomerConflictProblem, error) {
	txq := store.New(tx)
	for _, id := range mergeLockOrder(into, from) {
		if _, err := txq.LockCustomer(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errMergeCustomerNotFound
		} else if err != nil {
			return nil, nil, fmt.Errorf("lock customer %d: %w", id, err)
		}
	}
	// Read only now, under both locks: a merge that queued behind another merge
	// of the same pair must see the marker the first one wrote.
	survivor, err := txq.CustomerForMerge(ctx, into)
	if err != nil {
		return nil, nil, err
	}
	absorbed := survivor
	if from != into {
		if absorbed, err = txq.CustomerForMerge(ctx, from); err != nil {
			return nil, nil, err
		}
	}
	refusal, err := mergeRefusal(ctx, txq, survivor, absorbed, expected)
	if err != nil {
		return nil, nil, err
	}
	if refusal != nil {
		return nil, refusal, errMergeRefused
	}
	snapshot, err := absorbedSnapshot(absorbed)
	if err != nil {
		return nil, nil, err
	}

	moved, err := moveOwnRecords(ctx, txq, from, into, now)
	if err != nil {
		return nil, nil, err
	}
	for _, holder := range s.deps.CustomerReferenceHolders {
		refs, err := holder.RepointCustomer(ctx, tx, from, into)
		if err != nil {
			return nil, nil, fmt.Errorf("re-point another module's references: %w", err)
		}
		moved = append(moved, refs...)
	}

	bumped, err := txq.BumpCustomerRevision(ctx, store.BumpCustomerRevisionParams{ID: into, Now: now, ExpectedRevision: expected})
	if err != nil {
		return nil, nil, err
	}
	if bumped == 0 {
		// Unreachable: the revision was compared under this transaction's own
		// lock. The WHERE repeats it all the same, and a row that answers
		// nothing here is a bug, not a conflict to report.
		return nil, nil, fmt.Errorf("customer %d changed under its own lock", into)
	}
	if err := txq.MarkCustomerMerged(ctx, store.MarkCustomerMergedParams{ID: from, IntoCustomerID: into, Now: now}); err != nil {
		return nil, nil, err
	}
	if err := txq.FlattenMergedIntoChain(ctx, store.FlattenMergedIntoChainParams{FromCustomerID: from, IntoCustomerID: into, Now: now}); err != nil {
		return nil, nil, err
	}
	if err := recordCustomerMerged(ctx, txq, now, into, snapshot, moved, act.Kind, act.Display, act.UserID); err != nil {
		return nil, nil, err
	}
	survivorRef := customerRefSnapshot{ID: survivor.ID, CustomerNumber: survivor.CustomerNumber, Name: survivor.Name}
	if err := recordCustomerMergedAway(ctx, txq, now, from, survivorRef, act.Kind, act.Display, act.UserID); err != nil {
		return nil, nil, err
	}
	return moved, nil, nil
}

// PostCustomersByIdMerge Merge another customer into this one
// (POST /api/v1/customers/{id}/merge)
//
// Order: (1) the body's sourceId, 400 before any database access; (2) the
// actor, resolved before the transaction opens — every refusal is only
// knowable under the locks, so one wasted directory call on those paths is
// accepted, the attach's reasoning (contacts.go); (3) the transaction, retried
// on a deadlock: the locks and their 404, the ladder's 409s, the moves, the
// holders, the writes, the events; (4) after commit, the survivor read and
// decorated from the pool — never inside the transaction, since decorating
// asks the user directory.
func (s *server) PostCustomersByIdMerge(ctx context.Context, req gen.PostCustomersByIdMergeRequestObject) (gen.PostCustomersByIdMergeResponseObject, error) {
	if req.Body == nil || req.Body.SourceId <= 0 {
		return gen.PostCustomersByIdMerge400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid merge",
			map[string][]string{"sourceId": {"sourceId must be the id of the customer to merge into this one"}})), nil
	}
	into, from := req.Id, req.Body.SourceId

	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	var moved []contracts.RepointedReferences
	var refusal *gen.CustomerConflictProblem
	err = db.RetrySerializable(ctx, mergeWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			moved, refusal, err = s.mergeCustomers(ctx, tx, into, from, req.Body.Revision, now, act)
			return err
		})
	})
	switch {
	case errors.Is(err, errMergeCustomerNotFound):
		return gen.PostCustomersByIdMerge404Response{}, nil
	case errors.Is(err, errMergeRefused):
		return gen.PostCustomersByIdMerge409ApplicationProblemPlusJSONResponse(*refusal), nil
	case isUniqueOrExclusionViolation(err):
		// Under both customer locks no other writer can make one of the
		// merge's own rows collide, and every holder's statement tolerates the
		// survivor already having a row. A unique or exclusion violation here
		// therefore means the merge's own SQL is wrong — its primaries logic,
		// most likely — and that is a bug to hear about loudly: a 500 logged
		// as an error, not the generic 409 httpx.WriteError would make of it,
		// which a client could not tell from a revision conflict. Its text
		// only, not the error itself, so the PostgreSQL error no longer
		// surfaces as one.
		return nil, fmt.Errorf("customers: merge customer %d into %d broke a constraint it must keep: %s", from, into, err.Error())
	case err != nil:
		return nil, fmt.Errorf("customers: merge customer %d into %d: %w", from, into, err)
	}

	q := store.New(s.deps.Pool)
	row, err := q.GetCustomer(ctx, into)
	if err != nil {
		return nil, fmt.Errorf("customers: read the merged customer: %w", err)
	}
	summary, err := q.CustomerTimelineSummary(ctx, into)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	customer := fromCustomerRow(row, summary)
	dec, err := s.decorate(ctx, q, customer)
	if err != nil {
		return nil, err
	}
	moves := make([]gen.CustomerMergeMove, 0, len(moved))
	for _, m := range moved {
		moves = append(moves, gen.CustomerMergeMove{Kind: m.Kind, Count: m.Count})
	}
	return gen.PostCustomersByIdMerge200JSONResponse{
		Customer: safeCustomerResponse(customer, s.hasPermission(ctx, legalIdentityView), dec),
		Moved:    moves,
	}, nil
}
