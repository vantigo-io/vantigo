package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own file.
type server struct {
	deps  module.Deps
	gates gateCache // what the gate knows of each agent, by the agent's version
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server { return &server{deps: d} }

// callerID is the signed-in caller of one request. The router has already
// authenticated every operation, so a handler always has one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

// isOwner says the caller satisfies the Owner policy. Raising a level, choosing
// an Auto mode and binding a channel need it besides agents:manage (V12). Any
// failure reads as no, so the check fails closed.
func (s *server) isOwner(ctx context.Context) bool {
	r, ok := contracts.RequestFrom(ctx)
	if !ok {
		return false
	}
	_, err := s.deps.Access.Check(r, contracts.Rule{Kind: contracts.RulePolicy, Names: []string{"Owner"}})
	return err == nil
}

// errVersion marks an If-Match version that has moved on.
var errVersion = errors.New("agents: the version has moved on")

// parseVersion reads an If-Match: a whole number, bare or quoted.
func parseVersion(raw string) (int32, bool) {
	v := strings.TrimSpace(raw)
	v = strings.TrimPrefix(v, "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < 0 {
		return 0, false
	}
	return int32(n), true
}

func oneOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// audit records an Owner's change with before and after, in the transaction of
// the change (agents.events). Identity's audit is identity's alone, and a
// module may not write another's schema, so the module keeps its own record.
func (s *server) audit(ctx context.Context, q *store.Queries, actor uuid.UUID, action, target string, before, after any) error {
	b, err := json.Marshal(before)
	if err != nil {
		return err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return err
	}
	var t *string
	if target != "" {
		t = &target
	}
	if err := q.InsertEvent(ctx, store.InsertEventParams{At: s.deps.Clock(), ActorUserID: actor, Action: action, Target: t, Before: b, After: a}); err != nil {
		return fmt.Errorf("agents: record the change: %w", err)
	}
	return nil
}

// isNoRows says err is "no row", as a read of one that is not there answers.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// warn logs a failure that is not the caller's to act on, such as a step undone
// after another failed.
func (s *server) warn(msg string, args ...any) {
	if s.deps.Logger != nil {
		s.deps.Logger.Warn(msg, args...)
	}
}
