package customers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is a private person's anonymisation (customers GDPR design D4):
// PUT and DELETE /customers/{id}/anonymisation, which put it on a day a person
// chose and take it off again, what else takes it off, and — from
// anonymiseCustomer down, run by the worker in anonymisation_worker.go — the
// anonymisation itself. There is no default day anywhere in it: Norwegian
// bookkeeping rules keep accounting material for years after the fiscal year,
// and the person scheduling knows what was invoiced; the module does not.

// errAnonymisationRefused aborts a scheduling transaction on a refusal read
// under the lock; the body travels beside it, as errMergeRefused's does.
var errAnonymisationRefused = errors.New("customers: anonymisation refused")

// personalDataCustomerActive is the refusal for a customer that is not
// archived (design D4): an ongoing relationship is not anonymised out from
// under itself, so the relationship is ended first, deliberately, by archiving.
func personalDataCustomerActive(number int64, name, status string) *gen.CustomerConflictProblem {
	return mergeConflict("Customer is not archived", "personal_data_customer_active", fmt.Sprintf(
		"%s is %s. Archive it before scheduling its anonymisation: an ongoing relationship is not anonymised out from under it.",
		customerLabel(number, name), status))
}

// scheduleRefusal is design D4's refusals after the read-only one, in order:
// a business, then a customer that is not archived. nil lets the schedule go
// ahead.
func scheduleRefusal(number int64, name, customerType, status string) *gen.CustomerConflictProblem {
	switch {
	case customerType != "person":
		return personalDataNotAPerson(number, name)
	case status != "archived":
		return personalDataCustomerActive(number, name, status)
	}
	return nil
}

// validateAnonymiseOn is the body's one field: a yyyy-MM-dd day, parsed the way
// every date of this module is (parseISODate), today in UTC or later. Today is
// allowed — the worker takes it on its next cycle — and a day in the past is
// not: it would read as "already due" to a person who meant something else.
func validateAnonymiseOn(raw string, now time.Time) (time.Time, string) {
	on, err := parseISODate(raw)
	if err != nil {
		return time.Time{}, "AnonymiseOn must be an ISO date (yyyy-MM-dd)"
	}
	if on.Before(civilDate(now)) {
		return time.Time{}, "An anonymisation date cannot be in the past"
	}
	return on, ""
}

// customerAfterWrite is the customer as GET /customers/{id} answers it, read
// and decorated from the pool once a write has committed — never inside the
// transaction, since decorating asks the user directory.
func (s *server) customerAfterWrite(ctx context.Context, id int32) (gen.SafeCustomerResponse, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetCustomer(ctx, id)
	if err != nil {
		return gen.SafeCustomerResponse{}, fmt.Errorf("customers: read customer %d: %w", id, err)
	}
	summary, err := q.CustomerTimelineSummary(ctx, id)
	if err != nil {
		return gen.SafeCustomerResponse{}, fmt.Errorf("customers: timeline summary: %w", err)
	}
	customer := fromCustomerRow(row, summary)
	dec, err := s.decorate(ctx, q, customer)
	if err != nil {
		return gen.SafeCustomerResponse{}, err
	}
	return safeCustomerResponse(customer, s.hasPermission(ctx, legalIdentityView), dec), nil
}

// cancelAnonymisationSchedule calls a schedule off as part of another write
// that takes the customer out of what may be anonymised — a restore
// (writeCustomerCore), a change of type away from person (customer_type.go)
// or its merge into another customer (merge.go) — in that write's transaction, under the lock it already holds, and records
// it (design D4, this plan's reading): left in place, a date would fire the
// night the customer was archived again, months after anybody meant it. The
// caller's own UPDATE advanced the revision; DropCustomerAnonymiseOn does not
// advance it twice. Nothing scheduled, nothing happens.
func cancelAnonymisationSchedule(ctx context.Context, txq *store.Queries, id int32, locked store.LockCustomerRow, now time.Time, act actor) error {
	if !locked.AnonymiseOn.Valid {
		return nil
	}
	if err := txq.DropCustomerAnonymiseOn(ctx, id); err != nil {
		return fmt.Errorf("call off the anonymisation of %d: %w", id, err)
	}
	return recordAnonymisationCancelled(ctx, txq, now, id, locked.AnonymiseOn.Time, act.Kind, act.Display, act.UserID)
}

// PutCustomersByIdAnonymisation Schedule a private person's anonymisation
// (PUT /api/v1/customers/{id}/anonymisation)
//
// Order: (1) the day, 400 before any database access; (2) the customer on the
// pool, its 404, and — answered there, before the actor costs a directory call
// — a business, a customer that is not archived, and the day already
// scheduled; a read-only customer's refusal is left to the lock, which words
// it; (3) the actor; (4) the transaction: the lock and the read-only refusal,
// the two refusals again under it, the no-op again, the write and the event;
// (5) the customer, read after commit.
func (s *server) PutCustomersByIdAnonymisation(ctx context.Context, req gen.PutCustomersByIdAnonymisationRequestObject) (gen.PutCustomersByIdAnonymisationResponseObject, error) {
	raw := ""
	if req.Body != nil {
		raw = req.Body.AnonymiseOn
	}
	on, msg := validateAnonymiseOn(raw, s.deps.Clock())
	if msg != "" {
		return gen.PutCustomersByIdAnonymisation400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid anonymisation",
			map[string][]string{"anonymiseOn": {msg}})), nil
	}

	current, err := store.New(s.deps.Pool).CustomerForPersonalData(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdAnonymisation404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	if current.AnonymisedAt == nil && current.MergedIntoCustomerID == nil {
		if problem := scheduleRefusal(current.CustomerNumber, current.Name, current.Type, current.Status); problem != nil {
			return gen.PutCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(*problem), nil
		}
		if current.AnonymiseOn.Valid && current.AnonymiseOn.Time.Equal(on) {
			return s.putAnonymisationAnswer(ctx, req.Id)
		}
	}

	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}
	now := s.deps.Clock()
	var refusal *gen.CustomerConflictProblem
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		locked, err := lockWritableCustomer(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if refusal = scheduleRefusal(current.CustomerNumber, current.Name, locked.Type, locked.Status); refusal != nil {
			return errAnonymisationRefused
		}
		var previous *time.Time
		if locked.AnonymiseOn.Valid {
			if locked.AnonymiseOn.Time.Equal(on) {
				return nil
			}
			was := locked.AnonymiseOn.Time
			previous = &was
		}
		if err := txq.SetCustomerAnonymiseOn(ctx, store.SetCustomerAnonymiseOnParams{
			ID: req.Id, AnonymiseOn: pgtype.Date{Time: on, Valid: true}, Now: now,
		}); err != nil {
			return err
		}
		return recordAnonymisationScheduled(ctx, txq, now, req.Id, on, previous, act.Kind, act.Display, act.UserID)
	})
	switch {
	case isReadOnlyCustomer(err):
		return gen.PutCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(readOnlyProblem(err)), nil
	case errors.Is(err, errAnonymisationRefused):
		return gen.PutCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(*refusal), nil
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PutCustomersByIdAnonymisation404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: schedule the anonymisation of %d: %w", req.Id, err)
	}
	return s.putAnonymisationAnswer(ctx, req.Id)
}

func (s *server) putAnonymisationAnswer(ctx context.Context, id int32) (gen.PutCustomersByIdAnonymisationResponseObject, error) {
	answer, err := s.customerAfterWrite(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.PutCustomersByIdAnonymisation200JSONResponse(answer), nil
}

// DeleteCustomersByIdAnonymisation Cancel a private person's anonymisation
// (DELETE /api/v1/customers/{id}/anonymisation)
//
// Order: (1) the customer on the pool, its 404, an anonymised one's 409, and
// nothing scheduled — answered as it is, before the actor; (2) the actor; (3)
// under the lock, the same two again, the write and the event. It takes
// LockCustomer, not lockWritableCustomer: calling a schedule off is the one
// write a merged-away customer takes — a merge calls a schedule off itself,
// but a day already on a merged-away row (one scheduled before that rule)
// would otherwise be irrevocable — and the merge refusal is the only thing
// lockWritableCustomer would add besides the anonymised one asked here.
func (s *server) DeleteCustomersByIdAnonymisation(ctx context.Context, req gen.DeleteCustomersByIdAnonymisationRequestObject) (gen.DeleteCustomersByIdAnonymisationResponseObject, error) {
	current, err := store.New(s.deps.Pool).CustomerForPersonalData(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersByIdAnonymisation404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	if current.AnonymisedAt != nil {
		return gen.DeleteCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(customerAnonymised(*current.AnonymisedAt).problem), nil
	}
	if !current.AnonymiseOn.Valid {
		return s.deleteAnonymisationAnswer(ctx, req.Id)
	}

	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		locked, err := txq.LockCustomer(ctx, req.Id)
		if err != nil {
			return err
		}
		if locked.AnonymisedAt != nil {
			return customerAnonymised(*locked.AnonymisedAt)
		}
		if !locked.AnonymiseOn.Valid {
			return nil
		}
		if err := txq.SetCustomerAnonymiseOn(ctx, store.SetCustomerAnonymiseOnParams{ID: req.Id, Now: now}); err != nil {
			return err
		}
		return recordAnonymisationCancelled(ctx, txq, now, req.Id, locked.AnonymiseOn.Time, act.Kind, act.Display, act.UserID)
	})
	switch {
	case isReadOnlyCustomer(err):
		return gen.DeleteCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(readOnlyProblem(err)), nil
	case errors.Is(err, pgx.ErrNoRows):
		return gen.DeleteCustomersByIdAnonymisation404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: cancel the anonymisation of %d: %w", req.Id, err)
	}
	return s.deleteAnonymisationAnswer(ctx, req.Id)
}

func (s *server) deleteAnonymisationAnswer(ctx context.Context, id int32) (gen.DeleteCustomersByIdAnonymisationResponseObject, error) {
	answer, err := s.customerAfterWrite(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.DeleteCustomersByIdAnonymisation200JSONResponse(answer), nil
}

// anonymisedText is what the person's words become (design D4), and
// anonymisedName what the customer is called afterwards.
const (
	anonymisedText = "[anonymised]"
	anonymisedName = "Anonymised person"
)

// The four kinds this module reports first in customer.anonymised.
const (
	anonymiseKindAddresses           = "customers.addresses"
	anonymiseKindContactAssociations = "customers.contactAssociations"
	anonymiseKindContacts            = "customers.contacts"
	anonymiseKindTimelineEntries     = "customers.timelineEntries"
)

// personalPayloadKeys are the top-level payload keys that carry the person
// (design D4), across every event this module writes: the customer's name
// (customer.created's customerName, a snapshot's name), its identity
// (identity, legalIdentity), its contact info and billing profile, every
// before/after/changes snapshot, the merge's absorbed block and its into, and a
// contact's name, title, phone and email (contacts_timeline.go), an address's
// label and one-line display. Every other key — customerId, the ids, dates,
// counts, statuses, roles — is kept, so an entry still says what kind of thing
// happened to which record when. Applied to every payload alike: a key in this
// list that holds nothing personal on some event (a status change's before) is
// taken all the same, rather than a per-event list that a new event type could
// slip past.
var personalPayloadKeys = []string{
	"name", "customerName", "identity", "legalIdentity", "contactInfo", "billingProfile",
	"before", "after", "changes", "absorbed", "into",
	"displayName", "firstName", "middleName", "lastName", "title", "phone", "email",
	"label", "display",
}

// keptSummaryEventTypes are the generated events whose summary is built from
// nothing about the person — a status, a type, a fixed sentence, a tag or group
// name, a staff member's name, a date — and so is kept (this plan's reading of
// design D4). Every other entry's summary, a manual one's and every generated
// one not listed, becomes anonymisedText: "Customer created: Kari Nordmann"
// would otherwise keep the name the payload beside it just lost. An allow-list,
// so an event type added later is anonymised until somebody decides otherwise.
var keptSummaryEventTypes = []string{
	"customer.status_changed", "customer.type_changed", "customer.contact_info_updated",
	"customer.billing_profile_updated", "customer.peppol_lookup", "customer.tags_changed",
	"customer.group_changed", "customer.owner_changed",
	"customer.anonymisation_scheduled", "customer.anonymisation_cancelled", "customer.anonymised",
}

// anonymisationWriteAttempts is how often one customer's anonymisation runs
// before a deadlock it keeps losing is logged as that customer's failure — the
// merge's three, for the merge's reason: the anonymisation takes the customer
// row first and then locks each contact it detached, while DELETE
// /customers/contacts/{id} takes the contact first and its customers after.
// The two can cycle, and the loser runs again from a fresh snapshot.
const anonymisationWriteAttempts = 3

// anonymiseCustomer is one due customer's whole anonymisation (design D4) in
// one transaction, retried on a deadlock, answering whether it anonymised
// anything: false when, under the lock, the customer turned out not to be due
// after all. The actor is the system's, verbatim — a worker has no principal,
// and resolving one would be a directory call for nothing.
func (s *server) anonymiseCustomer(ctx context.Context, id int32) (bool, error) {
	now := s.deps.Clock()
	var done bool
	err := db.RetrySerializable(ctx, anonymisationWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			done, err = s.anonymiseInTx(ctx, tx, id, now)
			return err
		})
	})
	return done, err
}

// anonymiseInTx is one attempt, inside tx. The customer's lock comes first and
// its state is read again under it: a cancel, a restore or a change of type
// may have landed since the batch was selected. A merge chain is one person
// (design D4), so every customer merged into this one is locked too and
// anonymised in the same transaction — its own row, its own timeline, its own
// event; its customer.merged snapshot is on this customer's timeline, rewritten
// with the rest of it. The others are locked after this customer, in ascending
// id order, so the whole is not ascending: a merge naming this customer and its
// survivor locks the other way round, and the deadlock that can make is the one
// db.RetrySerializable retries. A chain cannot grow while
// this customer is archived (a merge refuses an archived survivor), so reading
// it once under the first lock is enough. A customer that was merged away after
// it was scheduled takes its snapshot off its survivor's customer.merged entry,
// and nothing else of the survivor's: the survivor is locked for that write.
func (s *server) anonymiseInTx(ctx context.Context, tx pgx.Tx, id int32, now time.Time) (bool, error) {
	txq := store.New(tx)
	locked, err := txq.LockCustomer(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock customer %d: %w", id, err)
	}
	due := locked.AnonymiseOn.Valid && !locked.AnonymiseOn.Time.After(civilDate(now))
	if locked.AnonymisedAt != nil || !due || locked.Type != "person" || locked.Status != "archived" {
		return false, nil
	}

	chain, err := txq.CustomersMergedInto(ctx, id)
	if err != nil {
		return false, fmt.Errorf("read the customers merged into %d: %w", id, err)
	}
	others := slices.Clone(chain)
	if locked.MergedIntoCustomerID != nil {
		others = append(others, *locked.MergedIntoCustomerID)
	}
	slices.Sort(others)
	for _, other := range others {
		if _, err := txq.LockCustomer(ctx, other); err != nil {
			return false, fmt.Errorf("lock customer %d: %w", other, err)
		}
	}

	for _, member := range append([]int32{id}, chain...) {
		if err := s.anonymiseOne(ctx, tx, txq, member, locked.AnonymiseOn, now); err != nil {
			return false, err
		}
	}
	if locked.MergedIntoCustomerID != nil {
		snapshot := store.AnonymiseAbsorbedSnapshotsParams{Anonymised: anonymisedText, SurvivorID: *locked.MergedIntoCustomerID, AbsorbedID: id}
		if err := txq.AnonymiseAbsorbedSnapshots(ctx, snapshot); err != nil {
			return false, fmt.Errorf("take customer %d's snapshot off its survivor: %w", id, err)
		}
		if err := txq.AnonymiseAbsorbedSnapshotRevisions(ctx, store.AnonymiseAbsorbedSnapshotRevisionsParams(snapshot)); err != nil {
			return false, fmt.Errorf("take customer %d's snapshot off its survivor's revisions: %w", id, err)
		}
	}
	return true, nil
}

// anonymiseOne is design D4's list for one customer, in the order the
// constraints and the event want: what hangs off the row (addresses, the
// Peppol answer, the registry record a person never has but a retyped business
// might), the contacts (associations detached, the contacts they alone held
// deleted), the timeline and its revisions rewritten and its open follow-ups
// closed, every module's eraser in Compose order on this transaction, then the
// row — cleared, marked, its revision advanced — and last the event, which is
// recorded after the rewrite and so keeps its words. on is the day the
// anonymisation was scheduled for: a customer merged into the one scheduled
// takes that day, whatever its own schedule said.
func (s *server) anonymiseOne(ctx context.Context, tx pgx.Tx, txq *store.Queries, id int32, on pgtype.Date, now time.Time) error {
	addresses, err := txq.DeleteAllCustomerAddresses(ctx, id)
	if err != nil {
		return fmt.Errorf("delete customer %d's addresses: %w", id, err)
	}
	if err := txq.DeleteCustomerPeppolLookup(ctx, id); err != nil {
		return fmt.Errorf("delete customer %d's Peppol answer: %w", id, err)
	}
	if err := txq.DeleteCustomerRegistryRecord(ctx, id); err != nil {
		return fmt.Errorf("delete customer %d's registry record: %w", id, err)
	}
	contactIDs, err := txq.DetachCustomerContacts(ctx, id)
	if err != nil {
		return fmt.Errorf("detach customer %d's contacts: %w", id, err)
	}
	var orphans int64
	if len(contactIDs) > 0 {
		if err := txq.LockContactsForAnonymisation(ctx, contactIDs); err != nil {
			return fmt.Errorf("lock customer %d's detached contacts: %w", id, err)
		}
		if orphans, err = txq.DeleteOrphanedContacts(ctx, contactIDs); err != nil {
			return fmt.Errorf("delete the contacts only customer %d had: %w", id, err)
		}
	}
	rewrite := store.AnonymiseTimelineEntriesParams{
		CustomerID: id, Anonymised: anonymisedText, PersonalKeys: personalPayloadKeys, KeptSummaryTypes: keptSummaryEventTypes,
	}
	entries, err := txq.AnonymiseTimelineEntries(ctx, rewrite)
	if err != nil {
		return fmt.Errorf("rewrite customer %d's timeline: %w", id, err)
	}
	if err := txq.AnonymiseTimelineRevisions(ctx, store.AnonymiseTimelineRevisionsParams(rewrite)); err != nil {
		return fmt.Errorf("rewrite customer %d's timeline revisions: %w", id, err)
	}
	if err := txq.CloseAnonymisedFollowUps(ctx, store.CloseAnonymisedFollowUpsParams{CustomerID: id, Now: now}); err != nil {
		return fmt.Errorf("close customer %d's open follow-ups: %w", id, err)
	}

	erased := []contracts.ErasedData{
		{Kind: anonymiseKindAddresses, Count: addresses},
		{Kind: anonymiseKindContactAssociations, Count: int64(len(contactIDs))},
		{Kind: anonymiseKindContacts, Count: orphans},
		{Kind: anonymiseKindTimelineEntries, Count: entries},
	}
	for _, holder := range s.deps.CustomerPersonalData {
		kinds, err := holder.Data.EraseCustomerData(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("erase %s's data about customer %d: %w", holder.Module, id, err)
		}
		erased = append(erased, kinds...)
	}

	if err := txq.AnonymiseCustomerRow(ctx, store.AnonymiseCustomerRowParams{ID: id, Name: anonymisedName, AnonymiseOn: on, Now: now}); err != nil {
		return fmt.Errorf("clear customer %d's row: %w", id, err)
	}
	system := generatedFallbackActor
	return recordCustomerAnonymised(ctx, txq, now, id, erased, system.Kind, system.Display, system.UserID)
}
