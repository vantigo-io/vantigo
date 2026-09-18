package projects

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	entry := contracts.ProjectEntry{
		ID:          row.ID,
		Code:        row.Code,
		Name:        row.Name,
		CustomerID:  row.CustomerID,
		Status:      row.Status,
		OpenForWork: row.Status == statusActive,
		BillingType: row.BillingType,
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
		entries = append(entries, contracts.ProjectEntry{
			ID:          row.ID,
			Code:        row.Code,
			Name:        row.Name,
			CustomerID:  row.CustomerID,
			Status:      row.Status,
			OpenForWork: row.Status == statusActive,
			BillingType: row.BillingType,
		})
	}
	return entries, nil
}
