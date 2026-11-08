package identity

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/imagemeta"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// maxAgentNameRunes is identity.users.display_name's CHECK bound.
const maxAgentNameRunes = 200

// agentAccounts is identity's contracts.AgentAccounts (virtual employees
// design V5): the agents module's only way to make and change a user of kind
// agent. What an agent may never have — a credential, a session, the Owner and
// SystemAdmin roles, a delegation — is refused where it would be given (the
// sign-in, recovery, invitation, link and SCIM paths read kind) and again by
// the database (00141's triggers).
type agentAccounts struct {
	a    *Access
	deps module.Deps
}

var _ contracts.AgentAccounts = (*agentAccounts)(nil)

func newAgentAccounts(a *Access, d module.Deps) contracts.AgentAccounts {
	return &agentAccounts{a: a, deps: d}
}

// agentAddress is the address of the agent user id: it never routes.
func agentAddress(id uuid.UUID) string {
	return "agent-" + id.String() + "@" + contracts.AgentAddressDomain
}

// cleanAgentName is displayName trimmed, or false when it is not 1 to 200
// characters.
func cleanAgentName(displayName string) (string, bool) {
	name := strings.TrimSpace(displayName)
	n := utf8.RuneCountInString(name)
	return name, n >= 1 && n <= maxAgentNameRunes
}

// Create makes the agent's user. It never sets pending_since: a user with it
// set may not act, and an agent must.
func (g *agentAccounts) Create(ctx context.Context, displayName string) (uuid.UUID, error) {
	name, ok := cleanAgentName(displayName)
	if !ok {
		return uuid.Nil, contracts.ErrInvalidAgentName
	}
	id := uuid.New()
	address := agentAddress(id)
	if err := g.a.q.InsertAgentUser(ctx, store.InsertAgentUserParams{
		ID:              id,
		Email:           address,
		NormalizedEmail: normalizeEmail(address),
		DisplayName:     name,
		Version:         uuid.New(),
		Now:             g.a.now(),
	}); err != nil {
		return uuid.Nil, fmt.Errorf("identity: create agent user: %w", err)
	}
	return id, nil
}

// Rename changes the agent's display name.
func (g *agentAccounts) Rename(ctx context.Context, userID uuid.UUID, displayName string) error {
	name, ok := cleanAgentName(displayName)
	if !ok {
		return contracts.ErrInvalidAgentName
	}
	n, err := g.a.q.RenameAgentUser(ctx, store.RenameAgentUserParams{
		ID: userID, DisplayName: name, Version: uuid.New(), Now: g.a.now(),
	})
	if err != nil {
		return fmt.Errorf("identity: rename agent user: %w", err)
	}
	if n == 0 {
		return contracts.ErrNotAgent
	}
	return nil
}

// SetAvatar stores the agent's own picture, checked and stripped of its
// metadata as a person's upload is.
func (g *agentAccounts) SetAvatar(ctx context.Context, userID uuid.UUID, contentType string, data []byte) error {
	if len(data) == 0 || len(data) > maxAvatarImageBytes {
		return contracts.ErrInvalidAgentAvatar
	}
	contentType, ok := avatarImageType(contentType, data)
	if !ok {
		return contracts.ErrInvalidAgentAvatar
	}
	data, err := imagemeta.Strip(contentType, data)
	if err != nil {
		return contracts.ErrInvalidAgentAvatar
	}
	isAgent, err := g.a.q.IsAgentUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("identity: agent avatar: %w", err)
	}
	if !isAgent {
		return contracts.ErrNotAgent
	}
	if err := g.a.q.UpsertProfileAvatar(ctx, store.UpsertProfileAvatarParams{
		UserID: userID, Data: data, ContentType: contentType, Now: g.a.now(),
	}); err != nil {
		return fmt.Errorf("identity: agent avatar: %w", err)
	}
	return nil
}

// Disable stops the agent acting and ends every grant it holds, in one
// transaction.
func (g *agentAccounts) Disable(ctx context.Context, userID uuid.UUID) error {
	return g.setDisabled(ctx, userID, true)
}

// Enable lets a disabled agent act again.
func (g *agentAccounts) Enable(ctx context.Context, userID uuid.UUID) error {
	return g.setDisabled(ctx, userID, false)
}

func (g *agentAccounts) setDisabled(ctx context.Context, userID uuid.UUID, disabled bool) error {
	var disabledBy *string
	if disabled {
		owner := disabledByOwnerValue
		disabledBy = &owner
	}
	return db.WithTx(ctx, g.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := g.a.now()
		n, err := q.SetAgentUserDisabled(ctx, store.SetAgentUserDisabledParams{
			ID: userID, IsDisabled: disabled, DisabledBy: disabledBy, Version: uuid.New(), Now: now,
		})
		if err != nil {
			return fmt.Errorf("identity: set agent user disabled: %w", err)
		}
		if n == 0 {
			return contracts.ErrNotAgent
		}
		if disabled {
			if err := q.RevokeUserAgentGrants(ctx, store.RevokeUserAgentGrantsParams{UserID: userID, Now: now}); err != nil {
				return fmt.Errorf("identity: revoke the agent's grants: %w", err)
			}
		}
		return nil
	})
}
