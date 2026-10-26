package planning

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/planning/store"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// namedUsers is contracts.UserDirectory answering one user by name.
type namedUsers struct {
	contracts.UserDirectory
	id   uuid.UUID
	name string
}

func (u namedUsers) User(_ context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	if id != u.id {
		return nil, nil
	}
	return &contracts.UserEntry{ID: id, DisplayName: u.name, Active: true}, nil
}

// TestActorFor is the actor snapshot of S23: the caller's name as the
// directory answers it before the transaction, "Unknown user" for a user it
// no longer knows, and the system for a request without a user.
func TestActorFor(t *testing.T) {
	planner := uuid.New()
	s := &server{deps: module.Deps{Users: namedUsers{id: planner, name: "Kari Planlegger"}}}
	as := func(id uuid.UUID) context.Context {
		return contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: id})
	}

	got, err := s.actorFor(as(planner))
	if err != nil || got.Kind != actorKindUser || got.Display != "Kari Planlegger" || got.UserID == nil || *got.UserID != planner {
		t.Errorf("the planner = %+v, %v; want the user Kari Planlegger", got, err)
	}
	gone := uuid.New()
	if got, err := s.actorFor(as(gone)); err != nil || got.Display != unknownUserDisplay || got.UserID == nil || *got.UserID != gone {
		t.Errorf("a user the directory does not know = %+v, %v; want Unknown user with the id", got, err)
	}
	if got, err := s.actorFor(context.Background()); err != nil || got.Kind != actorKindSystem || got.Display != systemActorDisplayName || got.UserID != nil {
		t.Errorf("no principal = %+v, %v; want the system", got, err)
	}
}

// TestRecordEvent writes an event and a change inside one locked
// transaction and reads them back through ListEvents' three filters: the
// entity, the person, the project, the payload and the actor as written; an
// empty change writes nothing.
func TestRecordEvent(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	s := &server{deps: module.Deps{Pool: pool, Clock: func() time.Time { return at }}}
	planner := uuid.New()
	who := actor{Kind: actorKindUser, Display: "Kari Planlegger", UserID: &planner}
	person, project := int64(1001), int32(2001)
	e := event{EntityKind: entityAllocation, EntityID: 5001, PersonID: &person, ProjectID: &project, Kind: "allocation_created"}

	err := s.withPersonLock(ctx, person, func(ctx context.Context, txq *store.Queries) error {
		if err := s.recordEvent(ctx, txq, who, e); err != nil {
			return err
		}
		changed := e
		changed.Kind = "allocation_changed"
		if err := s.recordChanges(ctx, txq, who, changed, changes{"load": {From: 50.0, To: 80.0}}); err != nil {
			return err
		}
		return s.recordChanges(ctx, txq, who, changed, nil)
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	q := store.New(pool)
	for name, params := range map[string]store.ListEventsParams{
		"by person":     {PersonID: &person, MaxEvents: 10},
		"by project":    {ProjectID: &project, MaxEvents: 10},
		"by allocation": {AllocationID: &e.EntityID, MaxEvents: 10},
	} {
		rows, err := q.ListEvents(ctx, params)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(rows) != 2 {
			t.Fatalf("%s: %d events, want the two written (an empty change writes none)", name, len(rows))
		}
		for _, r := range rows {
			if r.ActorKind != actorKindUser || r.ActorDisplay == nil || *r.ActorDisplay != "Kari Planlegger" ||
				r.ActorUserID == nil || *r.ActorUserID != planner || !r.OccurredAt.Equal(at) || r.EntityKind != entityAllocation {
				t.Errorf("%s: event %+v, want Kari's at %s on the allocation", name, r, at)
			}
		}
		var payload struct {
			Fields map[string]fieldChange `json:"fields"`
		}
		if err := json.Unmarshal(rows[0].Payload, &payload); err != nil || payload.Fields["load"].To != 80.0 {
			t.Errorf("%s: newest payload %s, want the load's change", name, rows[0].Payload)
		}
	}
	other := int64(1999)
	if rows, err := q.ListEvents(ctx, store.ListEventsParams{PersonID: &other, MaxEvents: 10}); err != nil || len(rows) != 0 {
		t.Errorf("another person's events = %v, %v; want none", rows, err)
	}
}
