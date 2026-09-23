package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
//     replacing a set means.
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

	// The count is re-read rather than assumed: a rename must answer the same
	// customerCount the list would, and this endpoint's own body is what the
	// Manage tags modal keeps on screen afterwards.
	row, err := q.GetCustomerTag(ctx, updated.ID)
	if err != nil {
		return nil, fmt.Errorf("customers: read tag after update: %w", err)
	}
	return gen.PutCustomersTagsByTagId200JSONResponse(tagSummaryOf(row.ID, row.Name, row.Color, row.CustomerCount)), nil
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
func (s *server) DeleteCustomersTagsByTagId(ctx context.Context, req gen.DeleteCustomersTagsByTagIdRequestObject) (gen.DeleteCustomersTagsByTagIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.DeleteCustomerTag(ctx, req.TagId)
	if err != nil {
		return nil, fmt.Errorf("customers: delete tag: %w", err)
	}
	if rows == 0 {
		return gen.DeleteCustomersTagsByTagId404Response{}, nil
	}
	return gen.DeleteCustomersTagsByTagId204Response{}, nil
}

// PutCustomersByIdTags Replace a customer's tags
// (PUT /api/v1/customers/{id}/tags)
//
// Ordering: (1) the customer's existence, 404 — CustomerExists, deliberately
// not LockCustomer, because this write takes no lock on the customer row
// (design D2); (2) the ids resolved in one query, 400 keyed tagIds for any the
// vocabulary does not hold; (3) the current set read and diffed, and a request
// that changes nothing answered right there; (4) the actor; (5) the replace and
// its event in one transaction.
//
// **Why the diff happens before the transaction, not inside it.** The actor is
// a directory lookup and must be resolved outside any transaction (actor.go),
// and it must not be resolved at all for a request that writes nothing — a
// multi-select whose caller changed their mind sends the set the customer
// already has, and that is a read, not a write. Both rules can only hold if
// "did the set move" is answered on the pool first, which this endpoint is
// uniquely free to do: it is last-wins by design (design D2, no revision and no
// lock), so a concurrent replace landing between this read and the write below
// is not a lost update — it is the earlier writer losing, which is what
// replacing a set means. The one thing the race can cost is an event whose
// added/removed is computed against a set that moved in between; two
// simultaneous replaces can therefore both claim to have added the same tag.
// That is the honest consequence of last-wins and is cheaper than the lock a
// perfectly-ordered timeline would need.
//
// The empty-to-empty case is a real request and is covered by the same check:
// no tags before, none after, nothing written and nothing recorded.
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
		return gen.PutCustomersByIdTags400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid tags", map[string][]string{"tagIds": messages})), nil
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

	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.DeleteCustomerTagLinks(ctx, req.Id); err != nil {
			return err
		}
		if err := txq.InsertCustomerTagLinks(ctx, store.InsertCustomerTagLinksParams{CustomerID: req.Id, TagIds: wanted}); err != nil {
			return err
		}
		return recordCustomerTagsChanged(ctx, txq, now, req.Id, added, removed, act.Kind, act.Display, act.UserID)
	})
	if err != nil {
		return nil, fmt.Errorf("customers: replace customer tags: %w", err)
	}

	return answer, nil
}
