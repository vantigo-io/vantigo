package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// minSearchLimit and maxSearchLimit clamp SearchUsers' limit: at least one
// row when a caller asks for none or a negative count, never more than
// maxSearchLimit regardless of what a caller asks for.
const (
	minSearchLimit = 1
	maxSearchLimit = 50
)

// userDirectory is this module's contracts.UserDirectory, the one sanctioned
// way another module reads identity's user data. It is read-only and holds
// nothing but the queries: Compose builds it once, before any module mounts,
// and hands it to every module including this one.
type userDirectory struct {
	q *store.Queries
}

var _ contracts.UserDirectory = (*userDirectory)(nil)

// newUserDirectory is Module's Users: the constructor Compose calls with the
// dependencies it was given.
func newUserDirectory(d module.Deps) contracts.UserDirectory {
	return &userDirectory{q: store.New(d.Pool)}
}

// User looks up a user by id.
func (d *userDirectory) User(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	row, err := d.q.DirectoryUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: user directory user: %w", err)
	}
	entry := userEntry(row.ID, row.DisplayName, row.IsDisabled)
	return &entry, nil
}

// Users looks up users by id. An id that does not exist is simply absent
// from the result, not an error. An empty ids answers an empty result
// without querying: ANY($1) on an empty array is a valid but pointless
// round trip.
func (d *userDirectory) Users(ctx context.Context, ids []uuid.UUID) ([]contracts.UserEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := d.q.DirectoryUsers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("identity: user directory users: %w", err)
	}
	entries := make([]contracts.UserEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, userEntry(row.ID, row.DisplayName, row.IsDisabled))
	}
	return entries, nil
}

// SearchUsers finds active users whose display name contains query,
// case-insensitively, ordered by display name and capped at limit (clamped
// to between 1 and 50). A literal %, _ or \ in query is escaped first, so it
// matches only that literal character rather than as an ILIKE wildcard.
func (d *userDirectory) SearchUsers(ctx context.Context, query string, limit int) ([]contracts.UserEntry, error) {
	limit = clampSearchLimit(limit)
	rows, err := d.q.DirectorySearchUsers(ctx, store.DirectorySearchUsersParams{
		Pattern: escapeLike(query),
		MaxRows: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("identity: user directory search users: %w", err)
	}
	entries := make([]contracts.UserEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, userEntry(row.ID, row.DisplayName, row.IsDisabled))
	}
	return entries, nil
}

// userEntry builds a contracts.UserEntry from the three columns the
// directory ever exposes: it never sees email, roles, or MFA state.
func userEntry(id uuid.UUID, displayName string, isDisabled bool) contracts.UserEntry {
	return contracts.UserEntry{ID: id, DisplayName: displayName, Active: !isDisabled}
}

// clampSearchLimit keeps limit within [minSearchLimit, maxSearchLimit],
// regardless of what a caller asks for.
func clampSearchLimit(limit int) int {
	if limit < minSearchLimit {
		return minSearchLimit
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}

// likeEscaper escapes the three characters that are special to ILIKE's
// pattern language with a backslash, ESCAPE '\' as the query declares, so a
// caller's literal %, _ or \ is matched literally rather than as a wildcard.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLike is query with every ILIKE-special character escaped.
func escapeLike(query string) string {
	return likeEscaper.Replace(query)
}
