package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/mail"
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
// nothing but the queries, Access's reader of the single sign-on settings and
// the mail sender Notify delivers through: Compose builds it once, before any
// module mounts, and hands it to every module including this one.
type userDirectory struct {
	q *store.Queries
	// sso says, per call, whether SCIM is on, which makes a directory's
	// deactivation of a non-Owner count against PermissionHolders and Notify.
	// It is Access's own reader, so the save that evicts it (an Owner's
	// write on the single sign-on settings) reaches the directory at once.
	sso *ssoSettingsReader
	// mail is the installation's mail driver, nil when none was composed.
	mail mail.Sender
}

var _ contracts.UserDirectory = (*userDirectory)(nil)

// newUserDirectory is Module's Users: the constructor Compose calls with the
// dependencies it was given, over a's reader of the single sign-on settings.
func newUserDirectory(a *Access, d module.Deps) contracts.UserDirectory {
	return &userDirectory{q: store.New(d.Pool), sso: a.sso, mail: d.Mail}
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
	entry := userEntry(row.ID, row.DisplayName, row.IsDisabled, row.PreferredLanguage)
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
		entries = append(entries, userEntry(row.ID, row.DisplayName, row.IsDisabled, row.PreferredLanguage))
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
		entries = append(entries, userEntry(row.ID, row.DisplayName, row.IsDisabled, row.PreferredLanguage))
	}
	return entries, nil
}

// PermissionHolders lists the active users holding key, ordered by display
// name, in one query (DirectoryPermissionHolders says who counts). A
// placeholder address — a directory's or an identity provider's, which no
// mail reaches (publicEmail) — is answered empty; the holder is kept.
func (d *userDirectory) PermissionHolders(ctx context.Context, key string) ([]contracts.Holder, error) {
	scimEnabled, err := d.sso.scimEnabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: user directory permission holders: %w", err)
	}
	rows, err := d.q.DirectoryPermissionHolders(ctx, store.DirectoryPermissionHoldersParams{
		ScimEnabled:   scimEnabled,
		PermissionKey: key,
	})
	if err != nil {
		return nil, fmt.Errorf("identity: user directory permission holders: %w", err)
	}
	holders := make([]contracts.Holder, 0, len(rows))
	for _, row := range rows {
		holder := contracts.Holder{ID: row.ID, DisplayName: row.DisplayName, PreferredLanguage: row.PreferredLanguage}
		if email := publicEmail(row.Email); email != nil {
			holder.Email = *email
		}
		holders = append(holders, holder)
	}
	return holders, nil
}

// Notify mails the user one of n's texts (noticeText says which), at
// identity's address for them, as a plain text mail from SMTP_FROM — the way
// deliverInvitation sends. A user who is not active (DirectoryUserForNotice),
// is unknown, or whose only address is a placeholder no mail reaches
// (publicEmail), and a notice with no text, are not mailed and not an error;
// nor is an installation composed without a mail sender. The log driver
// counts as a sender: it records that a mail went. A failed send is an error
// naming the user's id only, never the driver's own error, which can name the
// recipient (go-mail lists the affected recipients).
func (d *userDirectory) Notify(ctx context.Context, userID uuid.UUID, n contracts.Notice) error {
	if d.mail == nil || len(n.Texts) == 0 {
		return nil
	}
	scimEnabled, err := d.sso.scimEnabled(ctx)
	if err != nil {
		return fmt.Errorf("identity: user directory notice for user %s: %w", userID, err)
	}
	row, err := d.q.DirectoryUserForNotice(ctx, store.DirectoryUserForNoticeParams{ID: userID, ScimEnabled: scimEnabled})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("identity: user directory notice for user %s: %w", userID, err)
	}
	to := publicEmail(row.Email)
	if to == nil {
		return nil
	}
	text := noticeText(n, row.PreferredLanguage)
	if err := d.mail.Send(ctx, mail.Message{To: *to, Subject: text.Subject, TextBody: text.Body}); err != nil {
		return fmt.Errorf("identity: notice to user %s could not be sent", userID)
	}
	return nil
}

// noticeText is the text of n Notify sends a user preferring language: that
// language's, then English, then the first by language key, so the choice
// among the rest is stable. n has at least one text.
func noticeText(n contracts.Notice, language string) contracts.NoticeText {
	if text, ok := n.Texts[language]; ok && language != "" {
		return text
	}
	if text, ok := n.Texts["en"]; ok {
		return text
	}
	keys := make([]string, 0, len(n.Texts))
	for key := range n.Texts {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return n.Texts[keys[0]]
}

// userEntry builds a contracts.UserEntry from the four columns User, Users
// and SearchUsers ever expose: they never see email, roles, or MFA state.
func userEntry(id uuid.UUID, displayName string, isDisabled bool, preferredLanguage string) contracts.UserEntry {
	return contracts.UserEntry{ID: id, DisplayName: displayName, Active: !isDisabled, PreferredLanguage: preferredLanguage}
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

// Avatar answers the user's effective avatar: the managed one while it
// stands, else the user's own; nil when they have none or do not exist.
func (d *userDirectory) Avatar(ctx context.Context, userID uuid.UUID) (*contracts.AvatarEntry, error) {
	row, err := d.q.GetEffectiveAvatar(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: user directory avatar: %w", err)
	}
	return &contracts.AvatarEntry{
		ContentType: row.ContentType, Data: row.Data, Stamp: row.Stamp, ManagedBy: row.ManagedBy, UpdatedAt: row.UpdatedAt,
	}, nil
}

// AvatarInfo answers Avatar's entry without the bytes; nil when the user has
// none or does not exist.
func (d *userDirectory) AvatarInfo(ctx context.Context, userID uuid.UUID) (*contracts.AvatarInfo, error) {
	row, err := d.q.GetEffectiveAvatarInfo(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: user directory avatar info: %w", err)
	}
	return &contracts.AvatarInfo{ContentType: row.ContentType, Stamp: row.Stamp, ManagedBy: row.ManagedBy, UpdatedAt: row.UpdatedAt}, nil
}
