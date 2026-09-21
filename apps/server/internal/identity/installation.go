package identity

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The bootstrap states the management endpoint reports.
const (
	BootstrapPending   = "pending"   // no Owner, and no invitation that can be accepted
	BootstrapInvited   = "invited"   // an Owner invitation is waiting to be accepted
	BootstrapCompleted = "completed" // an Owner exists
)

// InstallationStatus is what a control plane needs to know about identity.
type InstallationStatus struct {
	Bootstrap string
	// Users is every account. ActiveUsers is the accounts that can sign in:
	// not disabled and not locked out, the same count the Owner's system
	// status shows. It says nothing about how recently anyone signed in.
	Users       int
	ActiveUsers int
}

// ReadInstallationStatus reads the status outside any request: it needs no
// session and no *server.
func ReadInstallationStatus(ctx context.Context, d module.Deps) (InstallationStatus, error) {
	q := store.New(d.Pool)
	now := d.Clock()

	consumed, err := q.BootstrapConsumed(ctx)
	if err != nil {
		return InstallationStatus{}, fmt.Errorf("identity: installation status: %w", err)
	}
	st := InstallationStatus{Bootstrap: BootstrapCompleted}
	if !consumed {
		st.Bootstrap = BootstrapPending
		// An Owner invitation cannot exist without BOOTSTRAP_OWNER_EMAIL: the
		// /setup flow issues none, so the count is necessarily 0 and querying
		// would only cost a round trip. With it set, "invited" must mean the
		// CONFIGURED address holds a live invitation, not merely that some
		// Owner invitation does — between a corrected value and the restart
		// that re-issues to it, an unscoped count would still be counting the
		// invitation addressed to the OLD address.
		if d.Config.BootstrapOwnerEmail != "" {
			pending, err := q.CountPendingOwnerInvitationsForEmail(ctx, store.CountPendingOwnerInvitationsForEmailParams{
				NormalizedEmail: normalizeEmail(d.Config.BootstrapOwnerEmail),
				Now:             now,
			})
			if err != nil {
				return InstallationStatus{}, fmt.Errorf("identity: installation status: %w", err)
			}
			if pending > 0 {
				st.Bootstrap = BootstrapInvited
			}
		}
	}

	counts, err := q.GetOwnerSystemStatusCounts(ctx, store.GetOwnerSystemStatusCountsParams{
		Now:         now,
		ScimEnabled: d.Config.SCIM != nil,
	})
	if err != nil {
		return InstallationStatus{}, fmt.Errorf("identity: installation status: %w", err)
	}
	st.Users, st.ActiveUsers = int(counts.Total), int(counts.Active)
	return st, nil
}
