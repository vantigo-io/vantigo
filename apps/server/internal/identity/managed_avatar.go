package identity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/imagemeta"
)

// The managed avatar (staff photo sync design S1, §3.1): Staff's photo of a
// person, set through AccountProvisioning.SetManagedAvatar, stands in front
// of the user's own avatar — the effective avatar every read answers — and
// keeps the user from changing theirs while it stands. The user's own row in
// profile_avatars is never touched, so clearing the managed one shows it
// again.

// avatarManagedMessage answers the user's own avatar write while a managed
// avatar stands.
const avatarManagedMessage = "Your photo is kept by Staff and cannot be changed here."

// avatarManagedBody is POST, PUT and DELETE /account/avatar's 409 while a
// managed avatar stands.
var avatarManagedBody = authErrorBody("avatar_managed", avatarManagedMessage, nil)

// accountAvatar is what an AccountResponse says of the caller's avatar:
// whether an effective one exists, and whether it is managed.
type accountAvatar struct {
	has, managed bool
}

// avatarState reads accountAvatar for userID.
func (s *server) avatarState(ctx context.Context, userID uuid.UUID) (accountAvatar, error) {
	has, err := s.q.HasEffectiveAvatar(ctx, userID)
	if err != nil {
		return accountAvatar{}, err
	}
	managed := false
	if has {
		if managed, err = s.q.HasManagedAvatar(ctx, userID); err != nil {
			return accountAvatar{}, err
		}
	}
	return accountAvatar{has: has, managed: managed}, nil
}

// callerAvatarManaged reports whether a managed avatar stands for the
// caller, which refuses their own avatar's upload before its body is read.
// The check is not atomic with the write that follows: a managed avatar Staff
// sets in between lets that one upload through. That is harmless — the user's
// own row is written behind the managed one, which stands in front of it
// for every read, and is only shown again once Staff clears it.
func (s *server) callerAvatarManaged(ctx context.Context) (bool, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return false, err
	}
	managed, err := s.q.HasManagedAvatar(ctx, p.UserID)
	if err != nil {
		return false, fmt.Errorf("identity: avatar upload: %w", err)
	}
	return managed, nil
}

// managedAvatarAudit is a managed avatar as user.avatar-managed and
// user.avatar-unmanaged record it: where it comes from, the caller's stamp,
// the type and the size — never the bytes.
type managedAvatarAudit struct {
	Source      string `json:"source"`
	Stamp       string `json:"stamp"`
	ContentType string `json:"contentType"`
	Bytes       int    `json:"bytes"`
}

// SetManagedAvatar sets the user's managed avatar, or with a nil avatar
// clears it. In one write: the user locked (ErrUserGone when there is
// none), an Owner's or a SystemAdmin's set refused (ErrAccountPrivileged:
// their picture is theirs), whether or not Staff manages the account (design
// §8 reading 3) — a clear is always allowed, so a user made an Owner after
// Staff set theirs is left with their own; then the image judged as an
// upload is (judgeAvatarImage, at most 5 MiB) and its source, which must be
// Staff — ErrAvatarInvalid with the reason — and its metadata stripped
// (imagemeta.Strip), as an upload's is. A set is audited
// user.avatar-managed, a clear that removed one user.avatar-unmanaged. The
// same avatar again, or a clear with none, changes nothing and writes no
// audit row.
func (p *provisioning) SetManagedAvatar(ctx context.Context, actor contracts.Actor, userID uuid.UUID, avatar *contracts.ManagedAvatar) error {
	return p.write(ctx, actor, func(q *store.Queries, _ string, now time.Time) error {
		u, err := lockUser(ctx, q, userID)
		if err != nil {
			return err
		}
		if u.Privileged && avatar != nil {
			return contracts.ErrAccountPrivileged
		}
		var next *managedAvatarAudit
		var contentType string
		var data []byte
		if avatar != nil {
			if contentType, err = judgeManagedAvatar(avatar); err != nil {
				return err
			}
			// Staff strips its photo already; stripped again here, so no
			// avatar identity stores carries metadata whoever sent it.
			if data, err = imagemeta.Strip(contentType, avatar.Data); err != nil {
				return fmt.Errorf("%w: %w", contracts.ErrAvatarInvalid, err)
			}
			next = &managedAvatarAudit{Source: avatar.Source, Stamp: avatar.Stamp, ContentType: contentType, Bytes: len(data)}
		}
		current, err := q.GetManagedAvatar(ctx, userID)
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var before *managedAvatarAudit
		if found {
			before = &managedAvatarAudit{Source: current.Source, Stamp: current.SourceStamp, ContentType: current.ContentType, Bytes: len(current.Data)}
		}
		by, system := auditActor(actor)
		switch {
		case avatar == nil && !found:
			return nil
		case avatar == nil:
			if err := q.DeleteManagedAvatar(ctx, userID); err != nil {
				return err
			}
			return writeAudit(ctx, q, nil, now, auditEvent{
				actor: by, system: system, targetUser: &userID, action: "user.avatar-unmanaged", before: before, after: nil,
			})
		case found && *before == *next && bytes.Equal(current.Data, data):
			return nil
		}
		if err := q.UpsertManagedAvatar(ctx, store.UpsertManagedAvatarParams{
			UserID: userID, Data: data, ContentType: contentType, Source: avatar.Source, SourceStamp: avatar.Stamp, Now: now,
		}); err != nil {
			return err
		}
		return writeAudit(ctx, q, nil, now, auditEvent{
			actor: by, system: system, targetUser: &userID, action: "user.avatar-managed", before: before, after: next,
		})
	})
}

// judgeManagedAvatar is the managed avatar's content type when identity
// admits it — Staff's, a PNG or a JPEG declared as its bytes are, at most
// 5 MiB and 1 to 4096 px a side, as an upload is judged — else
// ErrAvatarInvalid with the reason.
func judgeManagedAvatar(a *contracts.ManagedAvatar) (string, error) {
	if a.Source != staffModule {
		return "", fmt.Errorf("%w: the source %q is not %s", contracts.ErrAvatarInvalid, a.Source, staffModule)
	}
	if n := len(a.Data); n == 0 || n > maxAvatarImageBytes {
		return "", fmt.Errorf("%w: %d bytes, not 1 to %d", contracts.ErrAvatarInvalid, n, maxAvatarImageBytes)
	}
	contentType, err := judgeAvatarImage(a.ContentType, a.Data)
	if err != nil {
		return "", fmt.Errorf("%w: %w", contracts.ErrAvatarInvalid, err)
	}
	return contentType, nil
}
