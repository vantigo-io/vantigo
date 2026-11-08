package contracts

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AgentMemory is what the virtual employees remember about a customer, as the
// customer page reads it (virtual employees design V27, delivery B): a
// read-only, in-process port, so that Customers can show the notes in a
// collapsed section without importing the agents module (barred by depguard) or
// reading its schema (barred by internal/db/schema_test.go). Agents is its sole
// provider (Module.AgentMemory); Compose wires it into every module's Deps
// before any Mount runs. It is nil when Agents is disabled, which a consumer
// reads as "nothing is remembered" and shows nothing for.
//
// It performs no authorization: it is consumer-gated. The notes are what the
// agent wrote about a customer from that customer's conversations, so the
// consumer decides who may see them (Customers asks for customers:view and
// agents:view before it calls) and calls with the request's context, never
// inside a transaction of its own. Deleting a note is Agents' own operation,
// under its own permission.
type AgentMemory interface {
	// AboutCustomer answers the notes the agents hold about one customer, one
	// entry per agent, the agents ordered by name and each one's notes newest
	// first. A customer nothing is remembered about answers an empty slice.
	AboutCustomer(ctx context.Context, customerID int32) ([]AgentMemoryEntry, error)
}

// AgentMemoryEntry is one agent's notes about a customer.
type AgentMemoryEntry struct {
	AgentID   uuid.UUID
	AgentName string
	Notes     []AgentMemoryNote
}

// AgentMemoryNote is one note: its id in Agents, which deleting it names, the
// text the agent wrote and when.
type AgentMemoryNote struct {
	ID        uuid.UUID
	Text      string
	CreatedAt time.Time
}
