package contracts

import (
	"context"

	"github.com/google/uuid"
)

// ProjectEntry is a project as another module may reference it: enough to
// name it, know who it bills to and whether it is still open for work,
// never enough to manage it — that stays behind projects' own contract and
// permissions.
type ProjectEntry struct {
	ID          int32
	Code, Name  string
	CustomerID  *int32
	Status      string
	OpenForWork bool // Status == "active"
	BillingType string
}

// BillingLineEntry is one of a project's billing lines as another module may
// reference it: enough to know what it prices and how, never enough to
// manage it — that stays behind projects' own contract and permissions.
type BillingLineEntry struct {
	ID, ProjectID   int32
	Code            string
	VariantID       int32
	PricingMode     string
	FixedAmount     *float64
	DiscountPercent *float64
	Active          bool
}

// ProjectDirectory is the one sanctioned way a module reads projects' data:
// a read-only, in-process port over projects, roles and billing lines, so a
// module can name a project, check whether a user may act on it, or price
// one of its billing lines, without either importing the projects package
// (barred by depguard) or reading its PostgreSQL schema (barred by
// internal/db/schema_test.go). projects implements it; Compose wires that
// implementation into every module's Deps before any Mount runs (see
// Module.Projects). It is nil when projects is disabled.
//
// A missing row is (nil, nil) from Project and BillingLine — never an
// error. A caller tells "does not exist" from "the lookup failed" by
// checking err, never by treating a nil result as failure. Role instead
// answers "" for "no role", since "" is not itself a valid role name and so
// cannot be confused with one.
type ProjectDirectory interface {
	// Project looks up a project by ID. It returns (nil, nil) if id does
	// not exist.
	Project(ctx context.Context, id int32) (*ProjectEntry, error)
	// Role reports the role userID holds on projectID, "" when the user
	// holds none.
	Role(ctx context.Context, projectID int32, userID uuid.UUID) (string, error)
	// BillingLine looks up one of a project's billing lines by ID. It
	// returns (nil, nil) if lineID does not exist on projectID.
	BillingLine(ctx context.Context, projectID, lineID int32) (*BillingLineEntry, error)
	// ProjectsForUser lists every project userID holds a role on.
	ProjectsForUser(ctx context.Context, userID uuid.UUID) ([]ProjectEntry, error)
}
