package projects

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// directory is this module's contracts.ProjectDirectory, the one sanctioned
// way another module reads projects' data (design §6). It is read-only and
// holds nothing but the queries: Compose builds it once, before any module
// mounts, and hands it to every module including this one.
type directory struct {
	q *store.Queries
}

var _ contracts.ProjectDirectory = (*directory)(nil)

// newDirectory is Module's Projects: the constructor Compose calls with the
// dependencies it was given.
func newDirectory(d module.Deps) contracts.ProjectDirectory {
	return &directory{q: store.New(d.Pool)}
}

// directoryProjectRow is the shape every directory query resolving a project
// shares: sqlc emits a distinct row type per query even when the selected
// columns are identical, so each call site converts its own row into this
// one shape and toProjectEntry does the actual, single-written mapping (the
// duplication a review of Project/ProjectsForUser flagged).
type directoryProjectRow struct {
	ID              int32
	Code, Name      string
	CustomerID      *int32
	Status          string
	BillingType     string
	Currency        *string
	DefaultBillRate pgtype.Numeric
}

// toProjectEntry converts a directoryProjectRow into the contract's
// ProjectEntry: OpenForWork derived from Status, DefaultBillRate read off
// its numeric column the same way every other optional decimal in this
// module is (floatPtrFromNumeric).
func toProjectEntry(row directoryProjectRow) (contracts.ProjectEntry, error) {
	defaultBillRate, err := floatPtrFromNumeric(row.DefaultBillRate)
	if err != nil {
		return contracts.ProjectEntry{}, fmt.Errorf("projects: directory project: %w", err)
	}
	return contracts.ProjectEntry{
		ID:              row.ID,
		Code:            row.Code,
		Name:            row.Name,
		CustomerID:      row.CustomerID,
		Status:          row.Status,
		OpenForWork:     row.Status == statusActive,
		BillingType:     row.BillingType,
		Currency:        row.Currency,
		DefaultBillRate: defaultBillRate,
	}, nil
}

// Project looks up a project by id, cancelled and completed ones included: a
// module holding a historical reference to a project (a logged hour, say)
// must still be able to name it.
func (d *directory) Project(ctx context.Context, id int32) (*contracts.ProjectEntry, error) {
	row, err := d.q.DirectoryProject(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: directory project: %w", err)
	}
	entry, err := toProjectEntry(directoryProjectRow{
		ID: row.ID, Code: row.Code, Name: row.Name, CustomerID: row.CustomerID,
		Status: row.Status, BillingType: row.BillingType, Currency: row.Currency, DefaultBillRate: row.DefaultBillRate,
	})
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// Role reports the role userID holds on projectID, "" when they hold none. A
// missing assignment reaches this as pgx.ErrNoRows rather than a zero value,
// so it is mapped to "" here rather than confused with a stored role.
func (d *directory) Role(ctx context.Context, projectID int32, userID uuid.UUID) (string, error) {
	role, err := d.q.DirectoryRoleForUser(ctx, store.DirectoryRoleForUserParams{ProjectID: projectID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("projects: directory role: %w", err)
	}
	return role, nil
}

// BillingLine looks up one of a project's billing lines by id, scoped to
// projectID: a line that exists but belongs to another project answers
// (nil, nil), the same as a lineID nobody has. An inactive line still
// resolves, so old hours stay priced.
func (d *directory) BillingLine(ctx context.Context, projectID, lineID int32) (*contracts.BillingLineEntry, error) {
	row, err := d.q.DirectoryBillingLine(ctx, store.DirectoryBillingLineParams{ID: lineID, ProjectID: projectID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: directory billing line: %w", err)
	}
	fixedAmount, err := floatPtrFromNumeric(row.FixedAmount)
	if err != nil {
		return nil, fmt.Errorf("projects: directory billing line: %w", err)
	}
	discountPercent, err := floatPtrFromNumeric(row.DiscountPercent)
	if err != nil {
		return nil, fmt.Errorf("projects: directory billing line: %w", err)
	}
	entry := contracts.BillingLineEntry{
		ID:              row.ID,
		ProjectID:       row.ProjectID,
		Code:            row.Code,
		VariantID:       row.VariantID,
		PricingMode:     row.PricingMode,
		FixedAmount:     fixedAmount,
		DiscountPercent: discountPercent,
		Active:          row.Active,
	}
	return &entry, nil
}

// ProjectsForUser lists every project userID holds a role on, whatever its
// status, ordered by code.
func (d *directory) ProjectsForUser(ctx context.Context, userID uuid.UUID) ([]contracts.ProjectEntry, error) {
	rows, err := d.q.DirectoryProjectsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("projects: directory projects for user: %w", err)
	}
	entries := make([]contracts.ProjectEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := toProjectEntry(directoryProjectRow{
			ID: row.ID, Code: row.Code, Name: row.Name, CustomerID: row.CustomerID,
			Status: row.Status, BillingType: row.BillingType, Currency: row.Currency, DefaultBillRate: row.DefaultBillRate,
		})
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Projects looks up every project in ids, in any status, ordered by code. An
// id nobody has is simply absent from the result rather than an error or a
// hole in the slice.
func (d *directory) Projects(ctx context.Context, ids []int32) ([]contracts.ProjectEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := d.q.DirectoryProjects(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("projects: directory projects: %w", err)
	}
	entries := make([]contracts.ProjectEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := toProjectEntry(directoryProjectRow{
			ID: row.ID, Code: row.Code, Name: row.Name, CustomerID: row.CustomerID,
			Status: row.Status, BillingType: row.BillingType, Currency: row.Currency, DefaultBillRate: row.DefaultBillRate,
		})
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// ProjectByCode looks up a project by its code, upper-casing code first: a
// code is always stored upper-cased (validateProjectCode), so this is what
// makes the lookup case-insensitive from a caller's point of view.
func (d *directory) ProjectByCode(ctx context.Context, code string) (*contracts.ProjectEntry, error) {
	row, err := d.q.DirectoryProjectByCode(ctx, strings.ToUpper(code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: directory project by code: %w", err)
	}
	entry, err := toProjectEntry(directoryProjectRow{
		ID: row.ID, Code: row.Code, Name: row.Name, CustomerID: row.CustomerID,
		Status: row.Status, BillingType: row.BillingType, Currency: row.Currency, DefaultBillRate: row.DefaultBillRate,
	})
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// BillingLines lists every billing line on projectID, active and inactive,
// ordered by code. An old, inactive line stays in the result so hours
// already logged against it stay priced.
func (d *directory) BillingLines(ctx context.Context, projectID int32) ([]contracts.BillingLineEntry, error) {
	rows, err := d.q.DirectoryBillingLines(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("projects: directory billing lines: %w", err)
	}
	entries := make([]contracts.BillingLineEntry, 0, len(rows))
	for _, row := range rows {
		fixedAmount, err := floatPtrFromNumeric(row.FixedAmount)
		if err != nil {
			return nil, fmt.Errorf("projects: directory billing lines: %w", err)
		}
		discountPercent, err := floatPtrFromNumeric(row.DiscountPercent)
		if err != nil {
			return nil, fmt.Errorf("projects: directory billing lines: %w", err)
		}
		entries = append(entries, contracts.BillingLineEntry{
			ID:              row.ID,
			ProjectID:       row.ProjectID,
			Code:            row.Code,
			VariantID:       row.VariantID,
			PricingMode:     row.PricingMode,
			FixedAmount:     fixedAmount,
			DiscountPercent: discountPercent,
			Active:          row.Active,
		})
	}
	return entries, nil
}

// directoryTaskRow is the shape every directory query resolving a task
// shares, the same one-shared-conversion pattern directoryProjectRow uses.
type directoryTaskRow struct {
	ID, ProjectID  int32
	Title, Status  string
	AssigneeUserID *uuid.UUID
	DueDate        pgtype.Date
}

// toTaskEntry converts a directoryTaskRow into the contract's TaskEntry.
func toTaskEntry(row directoryTaskRow) contracts.TaskEntry {
	var dueDate *time.Time
	if row.DueDate.Valid {
		dueDate = &row.DueDate.Time
	}
	return contracts.TaskEntry{
		ID:             row.ID,
		ProjectID:      row.ProjectID,
		Title:          row.Title,
		Status:         row.Status,
		AssigneeUserID: row.AssigneeUserID,
		DueDate:        dueDate,
	}
}

// Task looks up a task by id. There is no soft-delete flag on
// projects.tasks, so a deleted task is (nil, nil), the same as one that
// never existed.
func (d *directory) Task(ctx context.Context, id int32) (*contracts.TaskEntry, error) {
	row, err := d.q.DirectoryTask(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("projects: directory task: %w", err)
	}
	entry := toTaskEntry(directoryTaskRow{
		ID: row.ID, ProjectID: row.ProjectID, Title: row.Title, Status: row.Status,
		AssigneeUserID: row.AssigneeUserID, DueDate: row.DueDate,
	})
	return &entry, nil
}

// OpenTasksForUser lists userID's tasks whose status is not "done", across
// every project, ordered by due date (nulls last), project id and position —
// the order "my tasks" (design §4.1) wants.
func (d *directory) OpenTasksForUser(ctx context.Context, userID uuid.UUID) ([]contracts.TaskEntry, error) {
	rows, err := d.q.DirectoryOpenTasksForUser(ctx, &userID)
	if err != nil {
		return nil, fmt.Errorf("projects: directory open tasks for user: %w", err)
	}
	entries := make([]contracts.TaskEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, toTaskEntry(directoryTaskRow{
			ID: row.ID, ProjectID: row.ProjectID, Title: row.Title, Status: row.Status,
			AssigneeUserID: row.AssigneeUserID, DueDate: row.DueDate,
		}))
	}
	return entries, nil
}

// CanLogTime reports whether userID may log time against projectID: the
// project must be active and userID must hold the member or manager role on
// it, exactly the condition Time will gate logging on.
func (d *directory) CanLogTime(ctx context.Context, projectID int32, userID uuid.UUID) (bool, error) {
	ok, err := d.q.DirectoryCanLogTime(ctx, store.DirectoryCanLogTimeParams{ProjectID: projectID, UserID: userID})
	if err != nil {
		return false, fmt.Errorf("projects: directory can log time: %w", err)
	}
	return ok, nil
}
