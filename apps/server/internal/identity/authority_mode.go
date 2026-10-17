package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The authority mode (staff foundation design S23; staff accounts plan,
// pre-flight I6): federated when an identity provider owns the accounts —
// workforce OIDC or SCIM is configured — else local. The API process derives
// it from its configuration and records it at identity's mount, so the
// account provisioning provider of a worker-only process, which may have
// been given other settings, reads the recorded value and stops on a
// mismatch (contracts.ErrModeMismatch) instead of acting in a mode the API
// does not.

// recordModeTimeout bounds the mount's recording of the mode.
const recordModeTimeout = 15 * time.Second

// deriveAuthorityMode is the mode cfg's own settings derive.
func deriveAuthorityMode(cfg *config.Config) string {
	if cfg != nil && (cfg.OIDC != nil || cfg.SCIM != nil) {
		return contracts.AccountModeFederated
	}
	return contracts.AccountModeLocal
}

// authorityModeAudit is authority-mode.changed's before and after.
type authorityModeAudit struct {
	Mode string `json:"mode"`
}

// recordAuthorityMode records d's derived mode in identity.system_settings
// with when it was recorded, in one transaction holding the singleton row:
// the same mode again changes nothing; a change from a recorded mode is
// audited authority-mode.changed, with no actor and no request, the modes
// before and after. The row's maintenance settings are left as they are,
// and a missing row is created with maintenance off.
func recordAuthorityMode(ctx context.Context, d module.Deps) error {
	mode, now := deriveAuthorityMode(d.Config), d.Clock()
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
