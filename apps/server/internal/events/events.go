// Package events is the platform's domain-events outbox (orders design O1–O3,
// docs/superpowers/specs/2026-11-05-orders-design.md): a module publishes an
// event on its own transaction with Publish, and handlers — a many-provider
// slot, module.Module.EventHandlers — receive it later, each on a delivery's
// own transaction. The tables are platform.events and
// platform.event_deliveries (migration 00117).
//
// It is a platform package: it imports no business module.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// EventRef names one event a handler takes: its name and the payload version
// it accepts.
type EventRef struct {
	Name    string
	Version int
}

// Handler is one handler of the outbox (orders design O2).
//
// Name is unique across the composition, "<module>.<what>". Events are the
// events and versions it takes. Handle runs on the delivery's own
// transaction tx and writes only its module's own schema on it, so a handler
// whose effects stay in the database runs exactly once. It never leaves the
// process — nothing calls mail or any other external service inside tx — and
// it locks only its own rows and reads no contract while tx holds locks. It
// never begins, commits or rolls back tx. An error rolls the delivery back
// and it is retried.
type Handler struct {
	Name   string
	Events []EventRef
	Handle func(ctx context.Context, tx pgx.Tx, ev Delivered) error
}

// Delivered is a published event as a handler receives it: the row of
// platform.events, its payload the event's JSON wire form.
type Delivered struct {
	ID          int64
	Name        string
	Version     int
	Module      string
	AggregateID string
	Payload     []byte
	OccurredAt  time.Time
}

const insertEventSQL = `INSERT INTO platform.events (name, version, module, aggregate_id, payload, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6)`

// Publish inserts ev, published by module at occurredAt, into the outbox on
// the publisher's own transaction tx, so it commits with the state change
// that caused it or not at all. It never begins, commits or rolls back tx,
// and never takes a pool (module-boundaries rule 10's discipline). The
// payload is ev marshalled as JSON.
func Publish(ctx context.Context, tx pgx.Tx, module string, ev contracts.Event, occurredAt time.Time) error {
	if ev == nil {
		return errors.New("events: publish a nil event")
	}
	name, version, aggregate := ev.EventName(), ev.EventVersion(), ev.AggregateID()
	if name == "" || version < 1 || module == "" || aggregate == "" {
		return fmt.Errorf("events: publish %q v%d by %q for %q: name, version, module and aggregate id are required", name, version, module, aggregate)
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("events: marshal %s v%d: %w", name, version, err)
	}
	if _, err := tx.Exec(ctx, insertEventSQL, name, version, module, aggregate, payload, occurredAt); err != nil {
		return fmt.Errorf("events: publish %s v%d: %w", name, version, err)
	}
	return nil
}
