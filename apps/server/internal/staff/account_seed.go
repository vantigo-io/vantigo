package staff

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the account kinds' seed (Entra design D5, plan reading 4):
// once per installation, every person's account_kind and the settings'
// default_account_kind are set from the authority mode identity recorded —
// local → local, federated → external — so an installation upgraded from
// the installation-wide mode keeps the rule it had. A migration may not read
// identity's schema (schema_test.go), so Staff seeds: at mount, in an API
// process, and at the start of every accounts cycle, until
// settings.account_kind_seeded says it is done. The mode is asked on no
// connection; the seed itself runs under the settings row's lock, so two
// processes seed once.
//
// Until the seed has run, every person's account_kind reads local, the
// column's default, which is not the rule a federated installation had: so
// nothing is judged on a kind while the kinds are unseeded (review M1). The
// accounts cycle runs no step but a deactivate, which reads no kind; Run now
// for a person, a change of a person's kind and a change of the default kind
// answer 503 accounts_unavailable (accountKindsUnseeded). Each tries the
// seed first.

// accountSeedTimeout bounds the seed at mount, which has no request's
// context to end it.
const accountSeedTimeout = 30 * time.Second

// seedAccountKindsAtMount is the seed at mount: on a background context
// under accountSeedTimeout, a failure logged and left to the next cycle.
func (s *server) seedAccountKindsAtMount() {
	if s.deps.Pool == nil || s.deps.Accounts == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), accountSeedTimeout)
	defer cancel()
	s.seedAccountKinds(ctx)
}

// seedAccountKinds seeds the kinds when they were not seeded yet, and
// answers whether they are seeded now: false when no module provides
// accounts, which nothing can seed; true at once when the kinds were seeded.
// The mode unavailable, or mismatched in this process, is logged and tried
// next time, and so is a failure to write the seed: false.
func (s *server) seedAccountKinds(ctx context.Context) bool {
	if s.deps.Accounts == nil {
		return false
	}
	log := s.logger()
	set, err := store.New(s.deps.Pool).GetSettings(ctx)
	if err != nil {
		log.ErrorContext(ctx, "staff: read the settings to seed the account kinds", "error", err.Error())
		return false
	}
	if set.AccountKindSeeded {
		return true
	}
	mode, err := withCallTimeout(ctx, s.accountsMode)
	if err != nil {
		log.WarnContext(ctx, "staff: the authority mode could not be read; the account kinds are seeded later", "error", err.Error())
		return false
	}
	kind := contracts.AccountKindLocal
	if mode != contracts.AccountModeLocal {
		kind = contracts.AccountKindExternal
	}
	seeded := false
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("staff: lock the settings: %w", err)
		}
		if current.AccountKindSeeded {
			return nil
		}
		if err := txq.SeedAccountKinds(ctx, kind); err != nil {
			return fmt.Errorf("staff: seed the account kinds: %w", err)
		}
		if err := txq.MarkAccountKindsSeeded(ctx, store.MarkAccountKindsSeededParams{DefaultAccountKind: kind, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("staff: mark the account kinds seeded: %w", err)
		}
		seeded = true
		return nil
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.ErrorContext(ctx, "staff: seed the account kinds", "error", err.Error())
		}
		return false
	}
	if seeded {
		log.InfoContext(ctx, "staff: the account kinds were seeded from the authority mode", "kind", kind)
	}
	return true
}

// accountKindsUnseeded is the 503 a request that would judge or change an
// account kind answers while the kinds are unseeded: nothing was changed.
func accountKindsUnseeded() gen.StaffProblem {
	problem := accountsUnavailable()
	detail := "The account kinds are not yet seeded from identity's authority mode, so no person's account kind can be judged or changed. Nothing was changed; try again once identity answers."
	problem.Detail = &detail
	return problem
}
