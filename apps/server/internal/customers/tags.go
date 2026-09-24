package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the tags half of phase 4 delivery A (owner and tags design D2,
// D3): the vocabulary — GET/POST /customers/tags, PUT/DELETE
// /customers/tags/{tagId} — and PUT /customers/{id}/tags, which replaces one
// customer's set.
//
// It is communications' Tags area (internal/communications/tags.go) with three
// deliberate differences, and each one is a decision rather than a variation:
//
//  1. **Uniqueness ignores case** (migration 00024's index on lower(name)). A
//     tag is a vocabulary word; an installation holding both 'VIP' and 'vip'
//     has a filter that silently splits its customers in two.
//  2. **A customer's tags are REPLACED as a set**, not linked and unlinked one
//     at a time. The UI for tags is a multi-select, and a multi-select's write
//     is "here is the set now" — which also means there is no revision here
//     and none is accepted: tags are off the customer row (design D2), so
//     nothing bumps and two concurrent replaces are last-wins, which is what
//     replacing a set means. Last-wins is a promise about the OUTCOME, not
//     licence to let two replaces interleave: the replace is a delete followed
//     by an insert, so its transaction takes the customer row's own
//     FOR NO KEY UPDATE first and the two run one after the other (final fix
//     wave C1, PutCustomersByIdTags below).
//  3. **The list carries a customerCount**, because design D3's delete
//     confirmation has to say what it will affect and is already showing the
//     list.
//
// The vocabulary's own writes record no timeline event at all — renaming a tag
// says nothing about the customers carrying it (design D2: the tag is the
// vocabulary, not the customer) — while the set replace records
// customer.tags_changed on the customer whose set moved.

// tagSummaryOf is CustomerTagSummary's projection. customer_count arrives as a
// bigint from a count(*) sub-select and the contract's field is int32: a
// vocabulary with two billion uses of one tag is not a case worth a wider
// type, and the narrowing is here, once, rather than at each call site.
func tagSummaryOf(id uuid.UUID, name string, color *string, customerCount int64) gen.CustomerTagSummary {
	return gen.CustomerTagSummary{Id: id, Name: name, Color: color, CustomerCount: int32(customerCount)}
}

// tagExistsConflict is the 409 both the create and the rename answer for a
// name another tag already holds, ignoring case. It carries code tag_exists —
// communications' own code for the same refusal, and the second coded conflict
// in this module after the registry's two (registry.go) — so a UI can say "you
// already have that tag" instead of showing a bare 409.
func tagExistsConflict() gen.CustomerConflictProblem {
	title := "Tag already exists"
	detail := "A tag with this name already exists. Tag names are compared without regard to case."
	code := "tag_exists"
	status := int32(http.StatusConflict)
	return gen.CustomerConflictProblem{Title: &title, Detail: &detail, Code: &code, Status: &status}
}

// tagNotFound is the field error for an id in tagIds that no tag holds, worded
// as ownerNotFound (owner.go) words its own. A field error and not a 404: the
// customer exists and the caller may edit it, so what is wrong is the body.
func tagNotFound(id uuid.UUID) string {
	return fmt.Sprintf("Tag %s does not exist", id)
}

// customerTagsTagFK is the foreign key customers.customer_tags.tag_id carries
// (migration 00024, named by PostgreSQL's own convention). Named here because
// the same insert has a SECOND foreign key — customer_id — and the two mean
// different things to a caller: a missing tag is a field error on tagIds, a
// missing customer is not, so PutCustomersByIdTags matches this one by name
// rather than catching any 23503.
const customerTagsTagFK = "customer_tags_tag_id_fkey"

// tagWriteAttempts is how often a tag write's transaction runs before the
// deadlock it keeps losing escapes as a 500 (final fix wave I1). Three, as
// identity's own serializableAttempts settled on.
//
// The deadlock is real and is between the two writes in this file rather than
// anything exotic: DELETE /customers/tags/{tagId} locks the tags row and then,
// through the join table's ON DELETE CASCADE, its customer_tags rows, while
// PutCustomersByIdTags' insert locks customer_tags rows and then takes
// FOR KEY SHARE on the tags rows its foreign key points at — the two lock
// orders are opposite, so a tag deleted at the same moment as a customer being
// re-tagged with it can form a cycle. PostgreSQL breaks it by killing one side
// (40P01), and being the victim of a lock-order cycle is not something either
// caller did wrong: the retry runs the loser again, from a fresh snapshot, in
// which one of the two writes has simply already happened.
const tagWriteAttempts = 3

// missingTagMessages is the tagIds field error for every id in wanted that
// resolved does not hold, in the request's own order so the message list is
// deterministic. PutCustomersByIdTags calls it twice — once for the resolve
// before the write, once for the foreign-key violation after it — so the two
// refusals are worded identically rather than by two copies of the same loop.
func missingTagMessages(wanted []uuid.UUID, resolved []store.CustomersTag) []string {
	known := make(map[uuid.UUID]bool, len(resolved))
	for _, t := range resolved {
		known[t.ID] = true
	}
	var messages []string
	for _, id := range wanted {
		if !known[id] {
			messages = append(messages, tagNotFound(id))
		}
	}
	return messages
}

// validateTagRequest is POST/PUT /customers/tags' shared validator: both
// fields checked independently and both errors reported together, keyed by the
// request's own field names — the module's all-errors-at-once shape
// (validateContactInfo, validateLegalIdentity). An absent or blank colour is
// null, never an error; only a colour outside the palette is.
func validateTagRequest(name string, color *string) (string, *string, map[string][]string) {
	errs := map[string][]string{}
	normalizedName, nameErr := validateTagName(name)
	if nameErr != "" {
		errs["name"] = []string{nameErr}
	}
	var normalizedColor *string
	if color != nil && strings.TrimSpace(*color) != "" {
		c, colorErr := validateTagColor(*color)
		if colorErr != "" {
			errs["color"] = []string{colorErr}
		} else {
			normalizedColor = &c
		}
	}
	if len(errs) > 0 {
		return "", nil, errs
	}
	return normalizedName, normalizedColor, nil
}

// GetCustomersTags List every tag
// (GET /api/v1/customers/tags)
func (s *server) GetCustomersTags(ctx context.Context, _ gen.GetCustomersTagsRequestObject) (gen.GetCustomersTagsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListCustomerTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("customers: list tags: %w", err)
	}
	data := make([]gen.CustomerTagSummary, 0, len(rows))
	for _, row := range rows {
		data = append(data, tagSummaryOf(row.ID, row.Name, row.Color, row.CustomerCount))
	}
	return gen.GetCustomersTags200JSONResponse(data), nil
}

// PostCustomersTags Create a tag
// (POST /api/v1/customers/tags)
//
// The duplicate check is the INSERT's own unique violation rather than a
// SELECT first: two callers creating 'VIP' at the same moment is exactly the
// race a check-then-insert loses, and the index is the only thing that can
// decide it. db.IsUniqueViolation names the constraint, so a uuid collision on
// the primary key stays a 500 the caller must hear about rather than being
// reported as a duplicate name.
func (s *server) PostCustomersTags(ctx context.Context, req gen.PostCustomersTagsRequestObject) (gen.PostCustomersTagsResponseObject, error) {
	body := gen.CustomerTagRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	name, color, errs := validateTagRequest(body.Name, body.Color)
	if errs != nil {
		return gen.PostCustomersTags400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid tag", errs)), nil
	}

	q := store.New(s.deps.Pool)
	tag, err := q.InsertCustomerTag(ctx, store.InsertCustomerTagParams{ID: uuid.New(), Name: name, Color: color})
	if err != nil {
		if db.IsUniqueViolation(err, "ux_customers_tags_name_lower") {
			return gen.PostCustomersTags409ApplicationProblemPlusJSONResponse(tagExistsConflict()), nil
		}
		return nil, fmt.Errorf("customers: create tag: %w", err)
	}
	// A tag nobody carries yet: the count is 0 by construction, so this needs
	// no second read.
	return gen.PostCustomersTags201JSONResponse(tagSummaryOf(tag.ID, tag.Name, tag.Color, 0)), nil
}

// PutCustomersTagsByTagId Rename or recolour a tag
// (PUT /api/v1/customers/tags/{tagId})
//
// No timeline event anywhere: renaming a tag records nothing on the customers
// that carry it (design D2). Renaming a tag to the name it already has is a
// plain 200, not a conflict with itself — the unique index compares
// lower(name) and the row being updated is the row being compared against, so
// the database says so too.
//
// One statement, UPDATE … RETURNING with the count (final fix wave M3): the
// body this endpoint answers is what the Manage tags modal keeps on screen, so
// the count has to be the one the list would report, and reading it back
// separately left a window in which a tag deleted just after the rename turned
// a successful write into a 404.
func (s *server) PutCustomersTagsByTagId(ctx context.Context, req gen.PutCustomersTagsByTagIdRequestObject) (gen.PutCustomersTagsByTagIdResponseObject, error) {
	body := gen.CustomerTagRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	name, color, errs := validateTagRequest(body.Name, body.Color)
	if errs != nil {
		return gen.PutCustomersTagsByTagId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid tag", errs)), nil
	}

	q := store.New(s.deps.Pool)
	updated, err := q.UpdateCustomerTagRow(ctx, store.UpdateCustomerTagRowParams{ID: req.TagId, Name: name, Color: color})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PutCustomersTagsByTagId404Response{}, nil
	case db.IsUniqueViolation(err, "ux_customers_tags_name_lower"):
		return gen.PutCustomersTagsByTagId409ApplicationProblemPlusJSONResponse(tagExistsConflict()), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update tag: %w", err)
	}
	return gen.PutCustomersTagsByTagId200JSONResponse(tagSummaryOf(updated.ID, updated.Name, updated.Color, updated.CustomerCount)), nil
}

// DeleteCustomersTagsByTagId Delete a tag and remove it from every customer
// (DELETE /api/v1/customers/tags/{tagId})
//
// One statement: customer_tags goes with it through the table's own ON DELETE
// CASCADE (migration 00024), which is why there is no loop here and no
// transaction. Not idempotent — deleting an already-absent tag is a 404, the
// same asymmetry communications' own tag removal has — because "it is gone"
// and "it was never there" are different answers to someone who just clicked
// Delete twice.
//
// No timeline event on the customers that lose the tag: the customers did not
// change their minds, the vocabulary did (design D2).
//
// The one statement is still retried on a deadlock (final fix wave I1): its
// cascade locks the tags row before the customer_tags rows, which is the
// opposite order PutCustomersByIdTags' insert takes them in, so a delete racing
// a re-tag with the same tag can be picked as PostgreSQL's deadlock victim.
// Nothing here is worth failing for that — see tagWriteAttempts.
func (s *server) DeleteCustomersTagsByTagId(ctx context.Context, req gen.DeleteCustomersTagsByTagIdRequestObject) (gen.DeleteCustomersTagsByTagIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	var rows int64
	err := db.RetrySerializable(ctx, tagWriteAttempts, func() error {
		var err error
		rows, err = q.DeleteCustomerTag(ctx, req.TagId)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("customers: delete tag: %w", err)
	}
	if rows == 0 {
		return gen.DeleteCustomersTagsByTagId404Response{}, nil
	}
	return gen.DeleteCustomersTagsByTagId204Response{}, nil
}

// replaceCustomerTags is PutCustomersByIdTags' transaction body once the
// customer lock is held: the set replaced — every link deleted, the wanted ones
// inserted in one statement — and customer.tags_changed with what was added and
// removed. A tag deleted in between is a foreign-key violation on
// customerTagsTagFK, the caller's to map. The CSV importer replaces a row's
// tags through it.
func replaceCustomerTags(ctx context.Context, txq *store.Queries, id int32, wanted []uuid.UUID, added, removed []tagSnapshot, now time.Time, act actor) error {
	if err := txq.DeleteCustomerTagLinks(ctx, id); err != nil {
		return err
	}
	if err := txq.InsertCustomerTagLinks(ctx, store.InsertCustomerTagLinksParams{CustomerID: id, TagIds: wanted}); err != nil {
		return err
	}
	return recordCustomerTagsChanged(ctx, txq, now, id, added, removed, act.Kind, act.Display, act.UserID)
}

// PutCustomersByIdTags Replace a customer's tags
// (PUT /api/v1/customers/{id}/tags)
//
// Ordering: (1) the customer's existence, 404 — CustomerExists on the pool;
// (2) the ids resolved in one query, 400 keyed tagIds for any the vocabulary
// does not hold; (3) the current set read and diffed, and a request that changes
// nothing answered right there; (4) the actor; (5) inside one transaction: the
// customer row locked FOR NO KEY UPDATE, then the replace and its event.
//
// **Why the transaction locks the customer row** (final fix wave C1). The
// replace is a DELETE followed by an INSERT, and under READ COMMITTED two of
// them on one customer do not produce last-wins on their own: the second
// transaction's DELETE waits on the first's row locks, resumes with a statement
// snapshot taken before the first committed, therefore deletes nothing, and its
// INSERT then trips customer_tags' primary key on every id the two sets share —
// a 500 where the contract promises the later writer simply wins (and, for two
// disjoint sets, the union of both instead of the second). Tags are off the
// customer row, so this lock is not about the row's own data and takes nothing
// but NO KEY UPDATE: it is a serialization point, the same one every address
// write uses (queries/addresses.sql's LockCustomer), and it is what makes
// "last-wins" true rather than merely intended. pgx.ErrNoRows from it is the
// same 404 step (1) answers, for a customer deleted in between.
//
// The pre-check in step (1) is NOT redundant with that lock. A request that
// changes nothing never opens the transaction at all, and it must still answer
// 404 for a customer that does not exist — with an empty tagIds against a
// missing customer, the diff alone cannot tell "no tags" from "no customer".
//
// **Why the diff happens before the transaction, not inside it.** The actor is
// a directory lookup and must be resolved outside any transaction (actor.go),
// and it must not be resolved at all for a request that writes nothing — a
// multi-select whose caller changed their mind sends the set the customer
// already has, and that is a read, not a write. Both rules can only hold if
// "did the set move" is answered on the pool first, which this endpoint is
// uniquely free to do: it is last-wins by design (design D2, no revision), so a
// concurrent replace landing between this read and the write below is not a lost
// update — it is the earlier writer losing, which is what replacing a set means.
// The one thing the race can cost is an event whose added/removed is computed
// against a set that moved in between; two simultaneous replaces can therefore
// both claim to have added the same tag. That is the honest consequence of
// last-wins and is cheaper than reading the set again under the lock for a
// perfectly-ordered timeline.
//
// The empty-to-empty case is a real request and is covered by the same check:
// no tags before, none after, nothing written and nothing recorded.
//
// What being free to read first costs, and where that is paid: a tag deleted
// between step (2) and the insert in step (5) raises a foreign-key violation
// on a set step (2) had just called valid. That violation is mapped back to
// step (2)'s own tagIds field error rather than escaping as a 500 — the resolve
// exists to make a missing tag a field error, and a millisecond's difference in
// when it went missing is not a distinction a caller can use
// (tags_concurrency_test.go).
func (s *server) PutCustomersByIdTags(ctx context.Context, req gen.PutCustomersByIdTagsRequestObject) (gen.PutCustomersByIdTagsResponseObject, error) {
	body := gen.PutCustomerTagsRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	if _, err := q.CustomerExists(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdTags404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: check customer exists: %w", err)
	}

	// Distinct ids, order preserved for a deterministic field error: a request
	// naming one tag twice asks for the same set as one naming it once.
	wanted := make([]uuid.UUID, 0, len(body.TagIds))
	seen := make(map[uuid.UUID]bool, len(body.TagIds))
	for _, id := range body.TagIds {
		if seen[id] {
			continue
		}
		seen[id] = true
		wanted = append(wanted, id)
	}

	resolved, err := q.CustomerTagsByIDs(ctx, wanted)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve tags: %w", err)
	}
	if len(resolved) != len(wanted) {
		return gen.PutCustomersByIdTags400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid tags", map[string][]string{"tagIds": missingTagMessages(wanted, resolved)})), nil
	}

	after := make([]gen.CustomerTag, 0, len(resolved))
	afterSnapshots := make([]tagSnapshot, 0, len(resolved))
	for _, t := range resolved {
		after = append(after, gen.CustomerTag{Id: t.ID, Name: t.Name, Color: t.Color})
		afterSnapshots = append(afterSnapshots, tagSnapshot{TagID: t.ID, Name: t.Name})
	}
	answer := gen.PutCustomersByIdTags200JSONResponse(gen.CustomerTagsResponse{Tags: after})

	current, err := q.CustomerTagsForCustomers(ctx, []int32{req.Id})
	if err != nil {
		return nil, fmt.Errorf("customers: read current customer tags: %w", err)
	}
	before := make([]tagSnapshot, 0, len(current))
	for _, l := range current {
		before = append(before, tagSnapshot{TagID: l.ID, Name: l.Name})
	}
	added, removed := tagSetDiff(before, afterSnapshots)
	if len(added) == 0 && len(removed) == 0 {
		// Nothing moved: the same no-op rule every write in this module follows
		// (customers foundation design D5), applied to a set. The two statements
		// below would be a delete and a re-insert of identical rows, the event
		// would claim a change that did not happen, and the actor lookup would
		// be a directory call made for a request that writes nothing.
		return answer, nil
	}

	now := s.deps.Clock()
	// Resolved here and not earlier: before the transaction opens (customers
	// foundation design D1), and only now that a write is certain to follow.
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	// Retried on a deadlock with DELETE /customers/tags/{tagId}, whose cascade
	// takes the same two tables' locks in the opposite order (tagWriteAttempts).
	// Nothing inside leaves the database: the actor is already resolved above, so
	// a retry repeats the lock, the delete, the insert and the event, and
	// nothing else.
	err = db.RetrySerializable(ctx, tagWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			if _, err := lockWritableCustomer(ctx, txq, req.Id); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return errCustomerNotFound
				}
				return err
			}
			return replaceCustomerTags(ctx, txq, req.Id, wanted, added, removed, now, act)
		})
	})
	if isMergedAway(err) {
		return gen.PutCustomersByIdTags409ApplicationProblemPlusJSONResponse(mergedAwayProblem(err)), nil
	}
	if errors.Is(err, errCustomerNotFound) {
		return gen.PutCustomersByIdTags404Response{}, nil
	}
	if db.IsForeignKeyViolation(err, customerTagsTagFK) {
		// A tag named in the request was deleted between the resolve above and
		// this insert — the one window the resolve cannot close, since nothing
		// here locks the vocabulary. Re-resolve and answer the field error the
		// resolve itself would have given a moment later: the caller asked to
		// carry a tag that does not exist, which is a fact about their body
		// whichever side of the insert it became true on.
		remaining, rerr := q.CustomerTagsByIDs(ctx, wanted)
		if rerr != nil {
			return nil, fmt.Errorf("customers: re-resolve tags after a foreign-key violation: %w", rerr)
		}
		if messages := missingTagMessages(wanted, remaining); len(messages) > 0 {
			return gen.PutCustomersByIdTags400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
				"Invalid tags", map[string][]string{"tagIds": messages})), nil
		}
		// Every id resolves again, so the tag the insert tripped over has been
		// re-created since and there is no field error to report. The write did
		// fail, and falling through to the 500 says so rather than inventing an
		// empty list of reasons.
	}
	if err != nil {
		return nil, fmt.Errorf("customers: replace customer tags: %w", err)
	}

	return answer, nil
}
