package staff

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the photo's way to the person's account (photo sync design
// §4): HR's photo made the linked user's managed avatar in identity — at
// once after the photo's write commits, and reconciled by the accounts
// worker on every claim of the identity link — and the effective picture
// the Staff page shows, HR's photo else the linked user's own avatar. Every
// call is made outside any transaction; a failure is logged, never the
// request's, and never names an address or carries the bytes.

// managedAvatarSource is the source Staff names on the managed avatar it
// sets (contracts.ManagedAvatar.Source), which identity answers as
// AvatarEntry.ManagedBy.
const managedAvatarSource = "staff"

// photoStampLayout is the managed avatar's stamp: the person's
// photo_updated_at as stored, in UTC, RFC 3339 with its fraction. The stamp
// is the reconciliation key — the worker compares the stamp identity answers
// with this one as strings — so the inline write and the worker both format
// it with photoStamp, from the same stored value.
const photoStampLayout = time.RFC3339Nano

// photoStamp is the stamp of HR's photo set at at.
func photoStamp(at time.Time) string { return at.UTC().Format(photoStampLayout) }

// managedStamp is the AvatarEntry.Stamp identity answers while the managed
// avatar Staff set for HR's photo of at stands.
func managedStamp(at time.Time) string { return "managed:" + photoStamp(at) }

// syncManagedAvatar makes avatar the user link names' managed avatar, or
// clears it with nil, for the request's actor who — after the photo's write
// has committed, on no connection. Nothing without a link; a failure is
// logged and left to the worker's reconciliation.
func (s *server) syncManagedAvatar(ctx context.Context, who actor, personID int64, link *string, avatar *contracts.ManagedAvatar) {
	if link == nil {
		return
	}
	user, err := uuid.Parse(*link)
	if err != nil {
		s.logger().ErrorContext(ctx, "staff: a self-service link names no user id", "person_id", personID)
		return
	}
	s.setManagedAvatar(context.WithoutCancel(ctx), contracts.Actor{UserID: who.UserID}, personID, user, avatar)
}

// setManagedAvatar sets or clears user's managed avatar for actor, under the
// call timeout, and logs a refusal or a failure: an Owner's or a
// SystemAdmin's picture is theirs (reading 2: logged, no item), and anything
// else is tried again by the worker's next claim.
func (s *server) setManagedAvatar(ctx context.Context, actor contracts.Actor, personID int64, user uuid.UUID, avatar *contracts.ManagedAvatar) {
	_, err := withCallTimeout(ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.accountsSetManagedAvatar(ctx, actor, user, avatar)
	})
	switch {
	case err == nil:
	case errors.Is(err, contracts.ErrAccountPrivileged), errors.Is(err, errAccountsUnavailable):
		s.logger().InfoContext(ctx, "staff: the person's photo is not the account's picture", "person_id", personID, "reason", err.Error())
	default:
		s.logger().WarnContext(ctx, "staff: the account's picture could not be written", "person_id", personID, "error", err.Error())
	}
}

// nudgeAccountLink makes the person's identity link due now
// (NudgeAccountLink), after HR's photo or the self-service link changed and
// committed, so the accounts worker's next poll — not the next midnight —
// reconciles the managed avatar and pushes the picture to the directory. A
// failure is logged: the link is claimed on its schedule all the same.
func (s *server) nudgeAccountLink(ctx context.Context, personID int64) {
	ctx = context.WithoutCancel(ctx)
	if err := store.New(s.deps.Pool).NudgeAccountLink(ctx, store.NudgeAccountLinkParams{PersonID: personID, Now: s.deps.Clock()}); err != nil {
		s.logger().WarnContext(ctx, "staff: the person's account link could not be made due", "person_id", personID, "error", err.Error())
	}
}

// followLink puts the account pictures right once the person's self-service
// link changed from was to link and committed (photo sync design §4): the
// user the link no longer names loses the managed avatar Staff set for this
// person, and the user it names now is given HR's photo, when row — the
// person as the link's transaction read them — has one, its bytes read from
// the store. Each write is logged on failure and left to the worker, never
// the request's; the identity link is nudged so the worker comes soon.
func (s *server) followLink(ctx context.Context, who actor, row store.GetPersonRow, was, link *string) {
	same := was != nil && link != nil && *was == *link
	if same || (was == nil && link == nil) {
		return
	}
	if was != nil {
		s.syncManagedAvatar(ctx, who, row.ID, was, nil)
	}
	if link != nil && row.PhotoObjectKey != nil && row.PhotoContentType != nil && row.PhotoUpdatedAt != nil {
		data, err := s.readPhoto(context.WithoutCancel(ctx), *row.PhotoObjectKey)
		if err != nil {
			s.logger().WarnContext(ctx, "staff: read the person's photo for their newly linked account", "person_id", row.ID, "error", err.Error())
		} else {
			s.syncManagedAvatar(ctx, who, row.ID, link, &contracts.ManagedAvatar{
				ContentType: *row.PhotoContentType, Data: data, Source: managedAvatarSource, Stamp: photoStamp(*row.PhotoUpdatedAt),
			})
		}
	}
	s.nudgeAccountLink(ctx, row.ID)
}

// linkedAvatar is the effective avatar of the user link names, read through
// the directory on no connection; nil without a link, without an avatar, or
// when the directory cannot answer — which is no picture, logged, never the
// request's failure.
func (s *server) linkedAvatar(ctx context.Context, personID int64, link *string) *contracts.AvatarEntry {
	return readLinkedAvatar(ctx, s, personID, link, s.usersAvatar)
}

// linkedAvatarInfo is linkedAvatar without the bytes.
func (s *server) linkedAvatarInfo(ctx context.Context, personID int64, link *string) *contracts.AvatarInfo {
	return readLinkedAvatar(ctx, s, personID, link, s.usersAvatarInfo)
}

// readLinkedAvatar reads the user link names with read, as linkedAvatar
// says.
func readLinkedAvatar[T any](ctx context.Context, s *server, personID int64, link *string, read func(context.Context, uuid.UUID) (*T, error)) *T {
	if link == nil {
		return nil
	}
	user, err := uuid.Parse(*link)
	if err != nil {
		s.logger().ErrorContext(ctx, "staff: a self-service link names no user id", "person_id", personID)
		return nil
	}
	entry, err := read(ctx, user)
	if err != nil {
		s.logger().WarnContext(ctx, "staff: the linked user's avatar could not be read", "person_id", personID, "error", err.Error())
		return nil
	}
	return entry
}

// effectivePhoto is the person's picture as the person response shows it:
// HR's photo on row (record), else the linked user's avatar (account), its
// time the avatar's; both nil when there is neither. The avatar — its
// metadata alone — is read only for an audience that sees the photo, after
// any transaction, and never for an erased person, whose link is no longer
// theirs to show.
func (s *server) effectivePhoto(ctx context.Context, a audience, row store.GetPersonRow, link *string) (*time.Time, *gen.StaffPersonPhotoSource) {
	if row.PhotoUpdatedAt != nil {
		source := gen.PhotoFromRecord
		return row.PhotoUpdatedAt, &source
	}
	if !a.show("photoUpdatedAt") || row.ErasedAt != nil {
		return nil, nil
	}
	info := s.linkedAvatarInfo(ctx, row.ID, link)
	if info == nil {
		return nil, nil
	}
	at, source := info.UpdatedAt, gen.PhotoFromAccount
	return &at, &source
}

// reconcileManagedAvatar puts the user's managed avatar right on a claim of
// the person's identity link (photo sync design §4, reading 5): a photo on
// the person whose stamp is not the one identity holds is set again, read
// from the store; no photo, but a managed avatar Staff set, is cleared. ref
// is the user as identity reported them this claim — nothing for a user it
// does not know, or an erased person, whose erase cleared it. An Owner's or
// a SystemAdmin's picture is theirs: a managed avatar Staff set before they
// became one is cleared, and none is set. A failure is logged and changes
// nothing on the link: the next claim tries again.
func (s *server) reconcileManagedAvatar(ctx context.Context, run accountsRun, user uuid.UUID, ref *contracts.AccountRef, f accountFacts) {
	if ref == nil || f.decide.Erased {
		return
	}
	log := s.logger().With("worker", accountsWorkerName, "person_id", f.row.ID)
	entry, err := withCallTimeout(ctx, func(ctx context.Context) (*contracts.AvatarInfo, error) {
		return s.usersAvatarInfo(ctx, user)
	})
	if err != nil {
		log.WarnContext(ctx, "staff: read the linked user's avatar", "error", err.Error())
		return
	}
	row := f.row
	if ref.Privileged {
		if entry != nil && entry.ManagedBy == managedAvatarSource {
			s.setManagedAvatar(ctx, run.actor, row.ID, user, nil)
		}
		return
	}
	if row.PhotoObjectKey == nil || row.PhotoContentType == nil || row.PhotoUpdatedAt == nil {
		if entry != nil && entry.ManagedBy == managedAvatarSource {
			s.setManagedAvatar(ctx, run.actor, row.ID, user, nil)
		}
		return
	}
	if entry != nil && entry.ManagedBy != "" && entry.Stamp == managedStamp(*row.PhotoUpdatedAt) {
		return
	}
	data, err := s.readPhoto(ctx, *row.PhotoObjectKey)
	if err != nil {
		log.WarnContext(ctx, "staff: read the person's photo for their account", "error", err.Error())
		return
	}
	s.setManagedAvatar(ctx, run.actor, row.ID, user, &contracts.ManagedAvatar{
		ContentType: *row.PhotoContentType, Data: data, Source: managedAvatarSource, Stamp: photoStamp(*row.PhotoUpdatedAt),
	})
}
