package knowledge

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/gen"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations (knowledge design K3–K16). Each area has its own file:
// articles.go (create, read, change, save, move, the text), access.go (the
// materialised access and its operations), versions.go, trash.go,
// favourites.go, search.go and users.go.
//
// No call that leaves the module — the user directory, a permission check —
// is made inside a transaction: each is made before it, or after it to name
// the people in an answer.
type server struct {
	deps    module.Deps
	objects objects
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server { return &server{deps: d, objects: newObjects(d)} }

// callerID is the signed-in caller of one request; the router has already
// authenticated every operation, so a handler always has one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

// viaAI reports whether the caller is acting through the Assistant for a
// person, or as an agent (K16; virtual employees design V9a): search then
// leaves hidden articles out, and the text operation answers 404 for one. Any
// Via counts, so a kind of principal added later is covered without a change
// here.
func viaAI(ctx context.Context) bool {
	p, ok := contracts.PrincipalFrom(ctx)
	return ok && p.Via != nil
}

// canManage reports whether the caller holds knowledge:manage (K2). It is a
// permission check, made before any transaction.
func (s *server) canManage(ctx context.Context) bool {
	return contracts.HasPermission(ctx, s.deps.Access, permManage)
}

// withTx runs fn in one READ COMMITTED transaction. A write that changes the
// tree or its access takes the tree's lock first (lockTree), and each of its
// reads after that sees what the writes before it committed (K5).
func (s *server) withTx(ctx context.Context, fn func(tx pgx.Tx, q *store.Queries) error) error {
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		return fn(tx, store.New(tx))
	})
}

// withReadTx runs fn in one REPEATABLE READ, read-only transaction: one
// snapshot for a read that spans several queries.
func (s *server) withReadTx(ctx context.Context, fn func(q *store.Queries) error) error {
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return fn(store.New(tx))
	})
}

// actors names the users in ids as the user directory names them now; a user
// it no longer knows is left out, and is shown as a former user (K17).
func (s *server) actors(ctx context.Context, ids ...*uuid.UUID) (map[uuid.UUID]string, error) {
	seen := map[uuid.UUID]bool{}
	var want []uuid.UUID
	for _, id := range ids {
		if id != nil && *id != uuid.Nil && !seen[*id] {
			seen[*id] = true
			want = append(want, *id)
		}
	}
	names := map[uuid.UUID]string{}
	if len(want) == 0 || s.deps.Users == nil {
		return names, nil
	}
	found, err := s.deps.Users.Users(ctx, want)
	if err != nil {
		return nil, fmt.Errorf("knowledge: name the people: %w", err)
	}
	for _, u := range found {
		names[u.ID] = u.DisplayName
	}
	return names, nil
}

// actorOf is id as a KnowledgeActor, nil for none or a former user.
func actorOf(names map[uuid.UUID]string, id *uuid.UUID) *gen.KnowledgeActor {
	if id == nil {
		return nil
	}
	name, ok := names[*id]
	if !ok {
		return nil
	}
	return &gen.KnowledgeActor{UserId: *id, DisplayName: name}
}

// actor names one user, nil when the directory does not know them or cannot
// be asked: it decorates a refusal, which is answered either way.
func (s *server) actor(ctx context.Context, id *uuid.UUID) *gen.KnowledgeActor {
	names, err := s.actors(ctx, id)
	if err != nil {
		return nil
	}
	return actorOf(names, id)
}
