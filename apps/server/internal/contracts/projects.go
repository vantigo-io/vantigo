package contracts

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ProjectEntry is a project as another module may reference it: enough to
// name it, know who it bills to and whether it is still open for work,
// never enough to manage it — that stays behind projects' own contract and
// permissions. Currency and DefaultBillRate are financial fields (design
// §3.1, D8): only a consumer that itself gates on a financial-viewer
// permission should surface them.
type ProjectEntry struct {
	ID              int32
	Code, Name      string
	CustomerID      *int32
	Status          string
	OpenForWork     bool // Status == "active"
	BillingType     string
	Currency        *string
	DefaultBillRate *float64
}

// TaskEntry is a project task as another module may reference it: enough to
// name it, know who it is assigned to and when it is due, never enough to
// manage it — that stays behind projects' own contract and permissions.
type TaskEntry struct {
	ID, ProjectID  int32
	Title, Status  string
	AssigneeUserID *uuid.UUID
	DueDate        *time.Time
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
	// Projects looks up every project in ids, in any status. An id that does
	// not exist is simply omitted from the result, rather than reported as
	// an error or a hole in the slice.
	Projects(ctx context.Context, ids []int32) ([]ProjectEntry, error)
	// ProjectByCode looks up a project by its code, case-insensitively
	// (codes are stored upper-cased, and this upper-cases code before
	// looking it up). It returns (nil, nil) if no project has that code.
	ProjectByCode(ctx context.Context, code string) (*ProjectEntry, error)
	// BillingLines lists every billing line on projectID, active and
	// inactive, ordered by code. A caller that only wants the active ones
	// filters the result itself.
	BillingLines(ctx context.Context, projectID int32) ([]BillingLineEntry, error)
	// Task looks up a task by ID. It returns (nil, nil) if id does not
	// exist.
	Task(ctx context.Context, id int32) (*TaskEntry, error)
	// OpenTasksForUser lists userID's tasks whose status is not "done",
	// across every project, ordered by due date (nulls last), project ID
	// and position.
	OpenTasksForUser(ctx context.Context, userID uuid.UUID) ([]TaskEntry, error)
	// CanLogTime reports whether userID may log time against projectID:
	// the project must be active and userID must hold the member or
	// manager role on it.
	CanLogTime(ctx context.Context, projectID int32, userID uuid.UUID) (bool, error)
}
