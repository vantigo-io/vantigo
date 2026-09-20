package expenses

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// This file is design §3.3's categories: expenses:manage's create and replace
// over expenses.categories, and the list every expense form reads. A category
// is never deleted — one that is no longer wanted is deactivated, so the
// expenses booked on it keep their name — and no two of them share a name,
// however it is cased.

// categoryNameIndex is the unique index over lower(name); a create or a change
// it refuses is the name field error.
const categoryNameIndex = "ux_categories_name_lower"

// categoryNameTaken is the name message when another category already holds it.
func categoryNameTaken(name string) string {
	return fmt.Sprintf("A category named '%s' already exists", name)
}

// validateCategoryPosition is the picker's own rule, the one projects applies
// to a task among its siblings: positions are 1-based, so anything below one
// is a mistake. There is no upper bound — a position past the end of the list
// means last, which is what a picker dragged to the bottom sends.
func validateCategoryPosition(position *int32) string {
	if position == nil || *position >= 1 {
		return ""
	}
	return "A position must be 1 or greater"
}

// moveWithin is the new order of the category list: ids with moved taken out
// and put back at position, which is 1-based. A position past the end is the
// end — a picker dragged to the bottom sends whatever number the list happened
// to have — and a moved that is not in ids, which is a category just created,
// is simply inserted.
//
// It is projects' own moveWithin for a task among its siblings, written again
// rather than shared: depguard forbids this module from importing another's,
// and the platform has no home for it yet. The semantics are deliberately
// identical, so the two pickers behave the same way for the person using them.
func moveWithin(ids []int32, moved int32, position int32) []int32 {
	rest := make([]int32, 0, len(ids)+1)
	for _, id := range ids {
		if id != moved {
			rest = append(rest, id)
		}
	}
	at := int(position) - 1
	if at > len(rest) {
		at = len(rest)
	}
	out := make([]int32, 0, len(rest)+1)
	out = append(out, rest[:at]...)
	out = append(out, moved)
	out = append(out, rest[at:]...)
	return out
}

// parseCategoryName is the name rule: required, trimmed, and at most the
// column's width. It answers the trimmed name and the message to report.
func parseCategoryName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", "A name is required"
	case utf8.RuneCountInString(name) > categoryNameMaxLength:
		return "", fmt.Sprintf("A name cannot be longer than %d characters, the given value was %d characters",
			categoryNameMaxLength, utf8.RuneCountInString(name))
	}
	return name, ""
}

// listCategoryRows is every category row in the order the picker offers them,
// active and not: a stored expense must still be able to name its own.
func listCategoryRows(ctx context.Context, q *store.Queries) ([]store.ExpensesCategory, error) {
	rows, err := q.ListCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("expenses: list the categories: %w", err)
	}
	return rows, nil
}

// listCategories is listCategoryRows on the wire.
func listCategories(ctx context.Context, q *store.Queries) ([]gen.ExpensesCategoryResponse, error) {
	rows, err := listCategoryRows(ctx, q)
	if err != nil {
		return nil, err
	}
	return categoryResponses(rows), nil
}

// GetExpensesCategories List expense categories
// (GET /api/v1/expenses/categories)
//
// Every expenses:access holder reads them: the expense form cannot be drawn
// without them.
func (s *server) GetExpensesCategories(ctx context.Context, _ gen.GetExpensesCategoriesRequestObject) (gen.GetExpensesCategoriesResponseObject, error) {
	out, err := listCategories(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	return gen.GetExpensesCategories200JSONResponse(out), nil
}

// PostExpensesCategories Add an expense category
// (POST /api/v1/expenses/categories)
//
// A category with no position goes last; one with a position is inserted there
// and the rest of the list moves around it. Uniqueness is the index's rule, not
// a read-then-write check, so two administrators adding the same name race to
// one row and one name error — the transaction that loses rolls back, and the
// numbering rolls back with it.
func (s *server) PostExpensesCategories(ctx context.Context, req gen.PostExpensesCategoriesRequestObject) (gen.PostExpensesCategoriesResponseObject, error) {
	body := gen.ExpensesCategoryRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	name, msg := parseCategoryName(body.Name)
	if msg != "" {
		return gen.PostExpensesCategories400ApplicationProblemPlusJSONResponse(
			invalidCategory(fieldError("name", msg))), nil
	}
	if msg := validateCategoryPosition(body.Position); msg != "" {
		return gen.PostExpensesCategories400ApplicationProblemPlusJSONResponse(
			invalidCategory(fieldError("position", msg))), nil
	}

	active := true
	if body.Active != nil {
		active = *body.Active
	}
	var created store.ExpensesCategory
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		ids, err := txq.CategoryIDsInOrder(ctx)
		if err != nil {
			return fmt.Errorf("expenses: lock the categories: %w", err)
		}
		// With no position it goes last, which is already where the insert puts
		// it: the list is dense, so n+1 is the end and nothing else moves.
		position := int32(len(ids) + 1)
		if body.Position != nil {
			position = *body.Position
		}
		if created, err = txq.InsertCategory(ctx, store.InsertCategoryParams{
			Name: name, Active: active, Position: position, Now: s.deps.Clock(),
		}); err != nil {
			return err
		}
		if body.Position == nil {
			return nil
		}
		return s.renumberCategories(ctx, txq, moveWithin(ids, created.ID, position), &created)
	})
	if db.IsUniqueViolation(err, categoryNameIndex) {
		return gen.PostExpensesCategories400ApplicationProblemPlusJSONResponse(
			invalidCategory(fieldError("name", categoryNameTaken(name)))), nil
	}
	if err != nil {
		return nil, fmt.Errorf("expenses: add a category: %w", err)
	}
	return gen.PostExpensesCategories201JSONResponse(categoryResponse(created)), nil
}

// renumberCategories writes order back as a dense 1..n and re-reads into, so
// the answer carries the place the category actually ended up in rather than
// the number the caller asked for — those differ whenever the request named a
// slot past the end of the list.
func (s *server) renumberCategories(ctx context.Context, txq *store.Queries, order []int32,
	into *store.ExpensesCategory,
) error {
	if err := txq.RenumberCategories(ctx, store.RenumberCategoriesParams{
		Ids: order, Now: s.deps.Clock(),
	}); err != nil {
		return fmt.Errorf("expenses: renumber the categories: %w", err)
	}
	row, err := txq.GetCategory(ctx, into.ID)
	if err != nil {
		return fmt.Errorf("expenses: re-read a category after renumbering: %w", err)
	}
	*into = row
	return nil
}

// PutExpensesCategoriesById Change an expense category
// (PUT /api/v1/expenses/categories/{id})
//
// A full replace of the name, whether it may be chosen, and where it sits. An
// unknown id is a 404 before the body is judged. A category already in use may
// be renamed freely — only onto another category's name it may not.
//
// The position is a *place in the list*, not a number the row keeps: the server
// owns the numbering, exactly as projects owns a task's place among its
// siblings. Storing whatever the caller sent would let two categories share a
// slot, and then the listing's tie-break on the name decides the order — so a
// "move up" that sends the neighbour's number would leave the category exactly
// where it was. Instead the whole list is locked in one transaction, the
// category is taken out of it and put back at position-1 (the end, for a
// position past it), and every row is renumbered 1..n. A replace that leaves
// the position alone renumbers nothing.
func (s *server) PutExpensesCategoriesById(ctx context.Context, req gen.PutExpensesCategoriesByIdRequestObject) (gen.PutExpensesCategoriesByIdResponseObject, error) {
	body := gen.ExpensesCategoryUpdateRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	q := store.New(s.deps.Pool)
	if _, err := q.GetCategory(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutExpensesCategoriesById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("expenses: get a category: %w", err)
	}
	name, msg := parseCategoryName(body.Name)
	if msg != "" {
		return gen.PutExpensesCategoriesById400ApplicationProblemPlusJSONResponse(
			invalidCategory(fieldError("name", msg))), nil
	}
	if msg := validateCategoryPosition(&body.Position); msg != "" {
		return gen.PutExpensesCategoriesById400ApplicationProblemPlusJSONResponse(
			invalidCategory(fieldError("position", msg))), nil
	}

	var (
		updated store.ExpensesCategory
		gone    bool
	)
	err := s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		ids, err := txq.CategoryIDsInOrder(ctx)
		if err != nil {
			return fmt.Errorf("expenses: lock the categories: %w", err)
		}
		// The place it holds right now, read under the same lock the renumber
		// writes under, so a move that raced this one is taken into account.
		current, err := txq.GetCategory(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			gone = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("expenses: get a category: %w", err)
		}
		if updated, err = txq.UpdateCategory(ctx, store.UpdateCategoryParams{
			ID: req.Id, Name: name, Active: body.Active, Position: body.Position, Now: s.deps.Clock(),
		}); err != nil {
			return err
		}
		if body.Position == current.Position {
			return nil
		}
		return s.renumberCategories(ctx, txq, moveWithin(ids, req.Id, body.Position), &updated)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Deleted since the read — which nothing in this module does, but a
		// direct write to the database might.
		return gen.PutExpensesCategoriesById404Response{}, nil
	case db.IsUniqueViolation(err, categoryNameIndex):
		return gen.PutExpensesCategoriesById400ApplicationProblemPlusJSONResponse(
			invalidCategory(fieldError("name", categoryNameTaken(name)))), nil
	case err != nil:
		return nil, fmt.Errorf("expenses: change a category: %w", err)
	case gone:
		return gen.PutExpensesCategoriesById404Response{}, nil
	}
	return gen.PutExpensesCategoriesById200JSONResponse(categoryResponse(updated)), nil
}
