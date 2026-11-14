package accounting

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// server implements gen.StrictServerInterface. Each area is its own file:
// setup.go (the meta and Set up the books), accounts.go (the chart, the
// groupings and the roles), taxcodes.go, vouchers.go (the journal, reversal
// and attachments), lock.go, opening.go, openitems.go and reports.go; the
// posting engine every write goes through is posting.go.
type server struct {
	deps module.Deps
	// objects is the module's scoped object store, the attachments;
	// objectsErr the error it was built with.
	objects    storage.ObjectStore
	objectsErr error
}

var _ gen.StrictServerInterface = (*server)(nil)

func newServer(d module.Deps) *server {
	objects, objectsErr := moduleObjectStore(d)
	return &server{deps: d, objects: objects, objectsErr: objectsErr}
}

// has reports whether the caller holds a permission key. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}

// callerID is the signed-in caller; the router has authenticated every
// operation, so a handler always has one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

func (s *server) logger() *slog.Logger {
	if s.deps.Logger != nil {
		return s.deps.Logger
	}
	return slog.Default()
}

func (s *server) now() time.Time {
	if s.deps.Clock != nil {
		return s.deps.Clock()
	}
	return time.Now()
}

// today is the business calendar's day, at midnight UTC.
func (s *server) today(ctx context.Context) (time.Time, error) {
	if s.deps.Calendar == nil {
		n := s.now().UTC()
		return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	day, err := s.deps.Calendar.Today(ctx, s.now())
	if err != nil {
		return time.Time{}, fmt.Errorf("accounting: read the business day: %w", err)
	}
	return day, nil
}

// withTx runs fn in one READ COMMITTED transaction on the module's pool;
// every write goes through it. No call out of the module is made inside it.
func (s *server) withTx(ctx context.Context, fn func(tx pgx.Tx, q *store.Queries) error) error {
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(tx, store.New(tx))
	})
}

// withReadTx runs fn in one REPEATABLE READ, read-only transaction: one
// snapshot for a read of several tables, a report's.
func (s *server) withReadTx(ctx context.Context, fn func(q *store.Queries) error) error {
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return fn(store.New(tx))
	})
}

// ungated are the operations that answer before the books are set up (R12).
var ungated = map[string]bool{"GetAccountingMeta": true, "SetUpAccountingBooks": true}

// setupGate answers 409 books_not_set_up for every other operation until Set
// up the books has run, so nothing else runs before it (R12).
func (s *server) setupGate(f gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
	if ungated[operationID] {
		return f
	}
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		if _, err := store.New(s.deps.Pool).GetSettings(ctx); err != nil {
			if noRows(err) {
				return nil, conflict(codeBooksNotSetUp, "The books are not set up",
					"Set up the books first: the first day they cover, the fiscal year and the VAT term.")
			}
			return nil, err
		}
		return f(ctx, w, r, request)
	}
}

// settings is the settings row; the gate has made sure it exists.
func (s *server) settings(ctx context.Context) (store.AccountingSetting, error) {
	return store.New(s.deps.Pool).GetSettings(ctx)
}

// day is a contract date as the midnight-UTC time every date column is read as.
func day(d openapi_types.Date) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// apiDate is a date column as the contract's date.
func apiDate(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t} }

// apiDatePtr is a nullable date column as the contract's.
func apiDatePtr(t *time.Time) *openapi_types.Date {
	if t == nil {
		return nil
	}
	d := apiDate(*t)
	return &d
}

// datePtr is a nullable contract date as a column's.
func datePtr(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := day(*d)
	return &t
}

// fiscalYear is the fiscal year date falls in: the year in which the fiscal
// year that contains it begins.
func fiscalYear(date time.Time, startMonth int32) int32 {
	y := date.Year()
	if int32(date.Month()) < startMonth {
		y--
	}
	return int32(y)
}

// fiscalYearStart is the first day of the fiscal year date falls in.
func fiscalYearStart(date time.Time, startMonth int32) time.Time {
	return time.Date(int(fiscalYear(date, startMonth)), time.Month(startMonth), 1, 0, 0, 0, 0, time.UTC)
}

// display is a voucher's number as shown: 2027-000123.
func display(year, number int32) string { return fmt.Sprintf("%d-%06d", year, number) }

// monthEnd is the last day of date's month.
func monthEnd(date time.Time) time.Time {
	return time.Date(date.Year(), date.Month()+1, 0, 0, 0, 0, 0, time.UTC)
}

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
