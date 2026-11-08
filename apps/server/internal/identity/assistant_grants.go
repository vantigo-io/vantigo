package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// grantRefusedPolicies are the policies an assistant grant never satisfies,
// whatever the person behind it holds: the Owner tier, delegated
// authorization management and the system administrator. The assistant
// dispatcher already treats these operations as never callable (the Assistant
// design S4); this is the same line drawn a second time, where the rule is
// evaluated, so an operation the dispatcher mis-classified is still refused.
var grantRefusedPolicies = []string{"Owner", "OwnerManagement", "AuthorizationManagement", "SystemAdmin"}

// checkGrant is Check for a request whose context carries an assistant grant.
// No cookie is read: the grant is loaded with its session, and the answer is
// the session's own principal — same user, same session, same roles, same MFA
// state — with Via set, the rule evaluated exactly as for that session. The
// grant is refused when it is expired or revoked, its session has ended (a
// sign-out, the session's idle or absolute limit, a disabled user), or its user
// is locked out; and, before anything is read, when the rule is a scim rule or
// a policy naming grantRefusedPolicies.
//
// A grant that does not hold is ErrUnauthenticated, as a session that does not
// is, except that a user who may no longer act is ErrForbidden, as a locked
// session's permission check is. Under an anonymous rule, which admits a
// request with no session, a grant that does not hold is a caller with none.
func (a *Access) checkGrant(ctx context.Context, grantID uuid.UUID, rule contracts.Rule) (contracts.Principal, error) {
	switch rule.Kind {
	case contracts.RuleSCIM:
		return contracts.Principal{}, contracts.ErrForbidden
	case contracts.RulePolicy:
		for _, name := range rule.Names {
			if slices.Contains(grantRefusedPolicies, name) {
				return contracts.Principal{}, contracts.ErrForbidden
			}
		}
	}
	refuse := func(err error) (contracts.Principal, error) {
		if rule.Kind == contracts.RuleAnonymous {
			return contracts.Principal{}, nil
		}
		return contracts.Principal{}, err
	}

	now := a.now()
	grant, err := a.q.GetCurrentAssistantGrant(ctx, store.GetCurrentAssistantGrantParams{ID: grantID, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return refuse(contracts.ErrUnauthenticated)
	}
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: resolve assistant grant: %w", err)
	}

	sso, err := a.sso.get(ctx)
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: resolve assistant grant: %w", err)
	}
	bounds := a.cfg.Sessions
	row, err := a.q.GetSessionByID(ctx, store.GetSessionByIDParams{
		ID:                 grant.SessionID,
		ScimEnabled:        sso.SCIM != nil,
		Now:                now,
		Idle:               interval(bounds.Idle),
		PrivilegedIdle:     interval(bounds.PrivilegedIdle),
		Absolute:           interval(bounds.Absolute),
		PrivilegedAbsolute: interval(bounds.PrivilegedAbsolute),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return refuse(contracts.ErrUnauthenticated)
	}
	if err != nil {
		return contracts.Principal{}, fmt.Errorf("identity: resolve assistant grant: %w", err)
	}
	// GetSessionByID and GetSessionByTokenHash select the same columns.
	s := store.GetSessionByTokenHashRow(row)
	if s.UserID != grant.UserID {
		return refuse(contracts.ErrUnauthenticated)
	}
	if !isActive(s.IsDisabled, s.LockoutEnd, now) {
		return refuse(contracts.ErrForbidden)
	}
	if err := a.slide(ctx, s, now); err != nil {
		return contracts.Principal{}, err
	}

	p := sessionPrincipal(s)
	p.Via = &contracts.Via{Kind: contracts.ViaAssistant, ConversationID: grant.ConversationID, RunID: grant.RunID}
	return a.evaluate(ctx, p, sessionSubject(s), rule, now)
}

// assistantGrants is identity's contracts.AssistantGrants.
type assistantGrants struct{ a *Access }

var _ contracts.AssistantGrants = assistantGrants{}

func newAssistantGrants(a *Access, _ module.Deps) contracts.AssistantGrants {
	return assistantGrants{a: a}
}

// Issue binds a grant to the session of the request in ctx. The request is
// resolved afresh through its session cookie rather than trusting a principal
// in ctx, which any module could construct; a request that is itself made
// through a grant, or has no live session, is ErrNoSessionRequest, so a grant
// can never issue another. Grants that ended more than a day ago are purged
// as one is issued, which keeps a person's rows to the last day's messages.
func (g assistantGrants) Issue(ctx context.Context, conversationID, runID uuid.UUID, ttl time.Duration) (uuid.UUID, error) {
	if ttl <= 0 || ttl > contracts.MaxAssistantGrantTTL {
		return uuid.Nil, contracts.ErrInvalidGrantTTL
	}
	r, ok := contracts.RequestFrom(ctx)
	if !ok {
		return uuid.Nil, contracts.ErrNoSessionRequest
	}
	if _, viaGrant := contracts.GrantFrom(r.Context()); viaGrant {
		return uuid.Nil, contracts.ErrNoSessionRequest
	}
	now := g.a.now()
	s, ok, err := g.a.authenticate(ctx, r, now)
	if err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, contracts.ErrNoSessionRequest
	}

	if err := g.a.q.PurgeDeadAssistantGrants(ctx, store.PurgeDeadAssistantGrantsParams{UserID: s.UserID, Now: now}); err != nil {
		return uuid.Nil, fmt.Errorf("identity: purge assistant grants: %w", err)
	}
	id := uuid.New()
	if err := g.a.q.InsertAssistantGrant(ctx, store.InsertAssistantGrantParams{
		ID:             id,
		UserID:         s.UserID,
		SessionID:      s.ID,
		ConversationID: conversationID,
		RunID:          runID,
		Now:            now,
		ExpiresAt:      now.Add(ttl),
	}); err != nil {
		return uuid.Nil, fmt.Errorf("identity: issue assistant grant: %w", err)
	}
	return id, nil
}

// Revoke ends one grant.
func (g assistantGrants) Revoke(ctx context.Context, grantID uuid.UUID) error {
	if err := g.a.q.RevokeAssistantGrant(ctx, store.RevokeAssistantGrantParams{ID: grantID, Now: g.a.now()}); err != nil {
		return fmt.Errorf("identity: revoke assistant grant: %w", err)
	}
	return nil
}

// RevokeForUser ends every grant of a user.
func (g assistantGrants) RevokeForUser(ctx context.Context, userID uuid.UUID) error {
	if err := g.a.q.RevokeUserAssistantGrants(ctx, store.RevokeUserAssistantGrantsParams{UserID: userID, Now: g.a.now()}); err != nil {
		return fmt.Errorf("identity: revoke the user's assistant grants: %w", err)
	}
	return nil
}
