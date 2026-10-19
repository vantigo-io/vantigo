package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The authority mode (staff foundation design S23; Entra design D1):
// federated when an identity provider owns the accounts — workforce OIDC or
// SCIM is on in identity.sso_settings — else local. Every process, the API
// and a worker alike, derives it from that one row, so no two can disagree:
// the value identity.system_settings records is the trail of changes, no
// longer a guard (contracts.ErrModeMismatch has no sender).

// recordModeTimeout bounds the mount's recording of the mode.
const recordModeTimeout = 15 * time.Second

// deriveAuthorityMode is the mode settings derive.
func deriveAuthorityMode(settings ssoSettings) string {
	if settings.OIDC != nil || settings.SCIM != nil {
		return contracts.AccountModeFederated
	}
	return contracts.AccountModeLocal
}

// authorityModeAudit is authority-mode.changed's before and after.
type authorityModeAudit struct {
	Mode string `json:"mode"`
}

// recordAuthorityMode records the mode settings derive in identity.system_settings
// with when it was recorded, in one transaction holding the singleton row:
// the same mode again changes nothing; a change from a recorded mode is
// audited authority-mode.changed, with no actor and no request, the modes
// before and after. The row's maintenance settings are left as they are,
// and a missing row is created with maintenance off.
func recordAuthorityMode(ctx context.Context, d module.Deps, settings ssoSettings) error {
	mode, now := deriveAuthorityMode(settings), d.Clock()
	err := db.WithTx(ctx, d.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.EnsureSystemSettings(ctx, now); err != nil {
			return err
		}
		recorded, err := q.LockAuthorityMode(ctx)
		if err != nil {
			return err
		}
		if recorded != nil && *recorded == mode {
			return nil
		}
		if err := q.SetAuthorityMode(ctx, store.SetAuthorityModeParams{AuthorityMode: mode, Now: now}); err != nil {
			return err
		}
		if recorded == nil {
			return nil
		}
		return writeAudit(ctx, q, nil, now, auditEvent{
			action: "authority-mode.changed",
			before: authorityModeAudit{Mode: *recorded},
			after:  authorityModeAudit{Mode: mode},
		})
	})
	if err != nil {
		return fmt.Errorf("identity: record the authority mode: %w", err)
	}
	return nil
}

// authorityMode is the mode the provider acts in: the derivation of the
// single sign-on row read on q, uncached, so a provisioning write in its
// transaction sees the committed row. It never answers
// contracts.ErrModeMismatch.
func authorityMode(ctx context.Context, q *store.Queries) (string, error) {
	settings, err := readSSOSettings(ctx, q, nil)
	if err != nil {
		return "", fmt.Errorf("identity: read the authority mode: %w", err)
	}
	return deriveAuthorityMode(settings), nil
}
