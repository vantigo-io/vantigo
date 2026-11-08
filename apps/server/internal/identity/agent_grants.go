package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// checkAgentGrant is Check for a request whose context carries an agent grant
// (virtual employees design V6). No cookie and no session is read: the grant
// is loaded, its user must be an active agent, and the answer is that user's
// own principal — its roles, no session, no second factor — with Via set,
// evaluated as a person's session would be. The grant is refused when it is
// expired or revoked, or its user is not an agent, is disabled or holds the
// Owner or SystemAdmin role (which 00141 stops it being given; this is the
// same line drawn where the rule is evaluated); and, before anything is read,
// when the rule is a scim rule, a session rule or a policy rule of any name
// (the Owner tier among them): only a permission rule, or an anonymous one,
// is answered.
//
// A grant that does not hold is ErrUnauthenticated, as a session that does not
// is, except that an agent that may no longer act is ErrForbidden. Under an
// anonymous rule a grant that does not hold is a caller with none.
func (a *Access) checkAgentGrant(ctx context.Context, grantID uuid.UUID, rule contracts.Rule) (contracts.Principal, error) {
	// An agent is admitted by a permission its roles hold and by nothing less:
	// a session rule or a policy alone (ActiveAccount, Business) would let it
	// reach the self-service endpoints of a person's account, and the Owner
	// tier and SCIM are never its.
	switch rule.Kind {
	case contracts.RuleSCIM, contracts.RuleSession, contracts.RulePolicy:
		return contracts.Principal{}, contracts.ErrForbidden
	}
	refuse := func(err error) (contracts.Principal, error) {
		if rule.Kind == contracts.RuleAnonymous {
			return contracts.Principal{}, nil
		}
		return contracts.Principal{}, err
	}

	now := a.now()
	grant, err := a.q.GetCurrentAgentGrant(ctx, store.GetCurrentAgentGrantParams{ID: grantID, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return refuse(contracts.ErrUnauthenticated)
	}
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: resolve agent grant: %w", err)
	}
	u, err := a.q.GetAgentPrincipal(ctx, grant.UserID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && u.Kind != contracts.UserKindAgent) {
		return refuse(contracts.ErrUnauthenticated)
	}
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: resolve agent grant: %w", err)
	}
	if !isActive(u.IsDisabled, u.LockoutEnd, now) || slices.Contains(u.RoleNames, RoleOwner) || slices.Contains(u.RoleNames, RoleSystemAdmin) {
		return refuse(contracts.ErrForbidden)
	}

	roles := orderRoles(u.RoleNames)
	p := contracts.Principal{
		UserID: grant.UserID,
		Roles:  roles,
		Via: &contracts.Via{
			Kind:       contracts.ViaAgent,
			AgentID:    grant.AgentID,
			RunID:      grant.RunID,
			CustomerID: grant.CustomerID,
			ApprovedBy: grant.ApprovedByUserID,
		},
	}
	sub := subject{userID: grant.UserID, roles: roles, disabled: u.IsDisabled, lockoutEnd: u.LockoutEnd}
	return a.evaluate(ctx, p, sub, rule, now)
}

// agentGrants is identity's contracts.AgentGrants.
type agentGrants struct{ a *Access }

var _ contracts.AgentGrants = agentGrants{}

func newAgentGrants(a *Access, _ module.Deps) contracts.AgentGrants {
	return agentGrants{a: a}
}

// Issue makes a grant for one run of an active agent, living
// contracts.MaxAgentGrantTTL. Grants that ended more than a day ago are purged
// as one is issued, which keeps an agent's rows to the last day's runs.
func (g agentGrants) Issue(ctx context.Context, in contracts.IssueAgentGrant) (uuid.UUID, error) {
	now := g.a.now()
	u, err := g.a.q.GetAgentPrincipal(ctx, in.AgentUserID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && u.Kind != contracts.UserKindAgent) {
		return uuid.Nil, contracts.ErrNotAgent
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: issue agent grant: %w", err)
	}
	if !isActive(u.IsDisabled, u.LockoutEnd, now) {
		return uuid.Nil, contracts.ErrAgentInactive
	}

	if err := g.a.q.PurgeDeadAgentGrants(ctx, store.PurgeDeadAgentGrantsParams{UserID: in.AgentUserID, Now: now}); err != nil {
		return uuid.Nil, fmt.Errorf("identity: purge agent grants: %w", err)
	}
	id := uuid.New()
	if err := g.a.q.InsertAgentGrant(ctx, store.InsertAgentGrantParams{
		ID:               id,
		UserID:           in.AgentUserID,
		AgentID:          in.AgentID,
		RunID:            in.RunID,
		CustomerID:       in.CustomerID,
		ApprovedByUserID: in.ApprovedBy,
		Now:              now,
		ExpiresAt:        now.Add(contracts.MaxAgentGrantTTL),
	}); err != nil {
		return uuid.Nil, fmt.Errorf("identity: issue agent grant: %w", err)
	}
	return id, nil
}

// Revoke ends one grant.
func (g agentGrants) Revoke(ctx context.Context, grantID uuid.UUID) error {
	if err := g.a.q.RevokeAgentGrant(ctx, store.RevokeAgentGrantParams{ID: grantID, Now: g.a.now()}); err != nil {
		return fmt.Errorf("identity: revoke agent grant: %w", err)
	}
	return nil
}

// RevokeForUser ends every grant of an agent's user.
func (g agentGrants) RevokeForUser(ctx context.Context, userID uuid.UUID) error {
	if err := g.a.q.RevokeUserAgentGrants(ctx, store.RevokeUserAgentGrantsParams{UserID: userID, Now: g.a.now()}); err != nil {
		return fmt.Errorf("identity: revoke the agent's grants: %w", err)
	}
	return nil
}

// Describe answers the run a current grant of an active agent stands for.
func (g agentGrants) Describe(ctx context.Context, grantID uuid.UUID) (contracts.AgentRun, error) {
	now := g.a.now()
	grant, err := g.a.q.GetCurrentAgentGrant(ctx, store.GetCurrentAgentGrantParams{ID: grantID, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.AgentRun{}, contracts.ErrAgentGrantNotCurrent
	}
	if err != nil {
		return contracts.AgentRun{}, fmt.Errorf("identity: describe agent grant: %w", err)
	}
	u, err := g.a.q.GetAgentPrincipal(ctx, grant.UserID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (u.Kind != contracts.UserKindAgent ||
		!isActive(u.IsDisabled, u.LockoutEnd, now) || slices.Contains(u.RoleNames, RoleOwner) || slices.Contains(u.RoleNames, RoleSystemAdmin))) {
		return contracts.AgentRun{}, contracts.ErrAgentGrantNotCurrent
	}
	if err != nil {
		return contracts.AgentRun{}, fmt.Errorf("identity: describe agent grant: %w", err)
	}
	return contracts.AgentRun{
		UserID: grant.UserID, AgentID: grant.AgentID, RunID: grant.RunID,
		CustomerID: grant.CustomerID, ApprovedBy: grant.ApprovedByUserID,
	}, nil
}
