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
// A category with no position goes last. Uniqueness is the index's rule, not a
// read-then-write check, so two administrators adding the same name race to one
// row and one name error.
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

	q := store.New(s.deps.Pool)
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	position := int32(0)
	if body.Position != nil {
		position = *body.Position
	} else {
		next, err := q.NextCategoryPosition(ctx)
		if err != nil {
			return nil, fmt.Errorf("expenses: find the next category position: %w", err)
		}
		position = next
	}

	created, err := q.InsertCategory(ctx, store.InsertCategoryParams{
		Name: name, Active: active, Position: position, Now: s.deps.Clock(),
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

// PutExpensesCategoriesById Change an expense category
// (PUT /api/v1/expenses/categories/{id})
//
// A full replace of the name, whether it may be chosen, and where it sits. An
// unknown id is a 404 before the body is judged. A category already in use may
// be renamed freely — only onto another category's name it may not.
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

	updated, err := q.UpdateCategory(ctx, store.UpdateCategoryParams{
		ID: req.Id, Name: name, Active: body.Active, Position: body.Position, Now: s.deps.Clock(),
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
	}
	return gen.PutExpensesCategoriesById200JSONResponse(categoryResponse(updated)), nil
}
