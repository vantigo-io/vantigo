package projects

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// customerPersonalData is this module's contracts.CustomerPersonalData
// (customers GDPR design D2): what was done for a private person, handed over,
// and kept when they are anonymised — invoiced work stays, and no customer
// name is stored here to blank.
type customerPersonalData struct {
	pool *pgxpool.Pool
}

var _ contracts.CustomerPersonalData = customerPersonalData{}

// newCustomerPersonalData is Module's CustomerPersonalData.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return customerPersonalData{pool: d.Pool}
}

type projectsSection struct {
	Projects []exportedProject `json:"projects"`
}

type exportedProject struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	StartDate *string `json:"startDate,omitempty"`
	EndDate   *string `json:"endDate,omitempty"`
}

// dateOnly is a date column as the API writes one, or nil.
func dateOnly(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := d.Time.Format(time.DateOnly)
	return &s
}

// ExportCustomerData answers nil for a customer no project bills to.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	rows, err := store.New(p.pool).CustomerProjectsForExport(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("projects: read customer %d's projects: %w", customerID, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	section := projectsSection{Projects: make([]exportedProject, 0, len(rows))}
	for _, r := range rows {
		section.Projects = append(section.Projects, exportedProject{
			Code: r.Code, Name: r.Name, Status: r.Status, StartDate: dateOnly(r.StartDate), EndDate: dateOnly(r.EndDate),
		})
	}
	return section, nil
}

// EraseCustomerData keeps everything and says so (design D2). A project named
// after the person is free text this delivery does not rewrite (the design's
// out-of-scope list); the project names its customer through the directory,
// which answers the anonymised name.
func (customerPersonalData) EraseCustomerData(context.Context, pgx.Tx, int32) ([]contracts.ErasedData, error) {
	return []contracts.ErasedData{{Kind: customerReferenceKindProjects, Count: 0}}, nil
}
