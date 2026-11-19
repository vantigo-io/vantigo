package contracts

import (
	"context"

	"github.com/google/uuid"
)

// BrandTargets is what Identity needs of Staff to assign a brand to a legal
// entity (brands design B8, B9): a read-only, in-process port, so that identity
// resolves "the person has an employment in force today under this legal
// entity" without importing Staff (barred by depguard) or reading its
// PostgreSQL schema (barred by internal/db/schema_test.go). Staff is its sole
// provider (Module.BrandTargets); Compose wires it into every module's Deps
// after the business calendar, whose zone decides "today". It is nil when
// Staff is disabled, which identity reads as "there are no legal-entity
// targets" — never as "the person belongs to no entity".
//
// It performs no authorization, as Timesheets performs none: it is
// consumer-gated, and identity decides who may see what before it calls.
//
// A provider fails rather than under-reports: an error means "the entities
// could not be read", never "there are none".
type BrandTargets interface {
	// LegalEntities is every legal entity, archived ones marked, by name, for
	// the rule editor's picker and for flagging a rule whose entity has been
	// archived.
	LegalEntities(ctx context.Context) ([]LegalEntityTarget, error)

	// LegalEntitiesOf answers, for each identity user id, the ids of the legal
	// entities under which the person linked to that user has an employment in
	// force today (through the identity link, the employment and its business
	// unit). A user with none, or with no linked person, is absent from the
	// map; no user ids answer an empty map.
	LegalEntitiesOf(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]string, error)
}

// LegalEntityTarget is one legal entity as a brand rule's picker shows it. ID
// is Staff's id as text, because a rule's target is text in another schema.
type LegalEntityTarget struct {
	ID       string
	Name     string
	Archived bool
}
