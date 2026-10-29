package entra

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// OutlookToken is the person's delegated access token for the account oid,
// as the module built on d answers it to a mailbox call (outlook_token.go):
// removal asks it for a removal, which a closing connection still allows.
// Nothing in production calls the token source until delivery B; the
// external tests reach it here.
func OutlookToken(ctx context.Context, d module.Deps, oid uuid.UUID, removal bool) (string, error) {
	return newOutlookTokens(d).token(ctx, oid, removal)
}

// OutlookMe is GET /me with the person's delegated token for oid, through
// the module's Graph client and the token source's handling of a 401: the
// account's id as Graph answers it.
func OutlookMe(ctx context.Context, d module.Deps, oid uuid.UUID) (string, error) {
	t := newOutlookTokens(d)
	var me struct {
		ID string `json:"id"`
	}
	if err := t.c.getAs(ctx, t.source(oid, false), "/me", &me); err != nil {
		return "", err
	}
	return me.ID, nil
}

// ConnectionNeeded is the reason err asks for the person to connect again,
// and whether it does.
func ConnectionNeeded(err error) (string, bool) {
	var n *contracts.ConnectionNeededError
	if errors.As(err, &n) && errors.Is(err, contracts.ErrConnectionNeeded) {
		return n.Reason, true
	}
	return "", false
}

// The client's failures a test compares with.
var (
	ErrCredentialInvalid = errCredentialInvalid
	ErrTenantUnreachable = errTenantUnreachable
)
