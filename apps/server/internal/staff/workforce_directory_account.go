package staff

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// DirectoryAccount is the caller's own account in an external directory
// (calendar Outlook sync design O3): the provider link of the person whose
// identity link names the principal in ctx — a request's, or the user
// contracts.AsUser names, so a worker may ask with no request — with an
// external id, enabled or disabled. The person is found under the predicate
// personForUser applies, in any state the worker left the identity link, a
// disabled one included (the calendar asks to clean a leaver's mailbox up),
// but never one blocked as someone else's account; a person with two provider
// links answers the oldest. It needs no Staff key, being the principal's own
// account id and nothing more, and never answers for another user. Nothing
// for a bare context, a user with no person, or no such link.
func (w *workforce) DirectoryAccount(ctx context.Context) (*contracts.DirectoryAccount, error) {
	user := callerID(ctx)
	if user == uuid.Nil {
		return nil, nil
	}
	row, err := w.q.WorkforceDirectoryAccount(ctx, user.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: read the directory account of user %s: %w", user, err)
	}
	return &contracts.DirectoryAccount{Provider: row.Provider, ExternalID: row.ExternalID, Enabled: row.State == "enabled"}, nil
}
