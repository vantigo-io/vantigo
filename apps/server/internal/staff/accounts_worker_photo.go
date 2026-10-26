package staff

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the push of the account's picture to the provider's
// directory (photo sync design §4 "Staff → Entra", plan reading 10): on a
// provider link whose chain has nothing more to do on an enabled account,
// the linked user's effective avatar — HR's photo as identity holds it, else
// the user's own — is compared by its stamp with the one last pushed
// (photo_pushed_stamp), and written or removed when they differ, within the
// claim's three calls. The first push of no picture at all removes nothing:
// Vantigo wrote nothing to the directory, so whatever photo the tenant
// already had stays, and "none" is recorded. A refusal is the account's
// reported photo_not_written, not a failure; a failure is the link's, tried
// again at the next claim.

// accountStepPhoto is the step the push is recorded as on the provider
// link's events.
const accountStepPhoto accountStepKind = "photo"

// The codes the push records on account_external_updated — a picture
// written, one removed, either refused by the directory — and the stamp of
// no picture at all (photo sync design §3.2).
const (
	codePhotoPushed  = "photo_pushed"
	codePhotoCleared = "photo_cleared"
	codePhotoRefused = "photo_refused"
	photoStampNone   = "none"
)

// pushPhoto pushes the account's picture for the provider link r, on the
// claim's lease, after calls of the chain's own: the linked user's effective
// avatar's stamp read through the directory without the bytes (nothing
// without a user, nothing when it cannot be read — logged, read again at the
// next claim), "none" without one, compared with the one last pushed; the
// bytes read only when a picture is due; then SetPhoto or ClearPhoto — no
// ClearPhoto when nothing was ever pushed — the stamp recorded and the
// account read again so a refusal is on the link at once. It answers false
// when the claim's lease lapsed, and the link is then left as it was.
func (s *server) pushPhoto(ctx context.Context, run accountsRun, claim store.ClaimAccountLinkRow, lease uuid.UUID, p contracts.IdentityProvisioner, r *providerResult, f accountFacts, ident providerIdentity, calls int) bool {
	if ident.user == nil || r.externalID == nil {
		return true
	}
	log := s.logger().With("worker", accountsWorkerName, "person_id", claim.PersonID, "provider", r.key)
	user := *ident.user
	info, err := withCallTimeout(ctx, func(ctx context.Context) (*contracts.AvatarInfo, error) {
		return s.usersAvatarInfo(ctx, user)
	})
	if err != nil {
		log.WarnContext(ctx, "staff: read the linked user's avatar for the directory", "error", err.Error())
		return true
	}
	effective := photoStampNone
	if info != nil {
		effective = info.Stamp
	}
	if r.photoPushedStamp != nil && *r.photoPushedStamp == effective {
		return true
	}
	if info == nil && r.photoPushedStamp == nil {
		// Nothing was ever pushed and there is nothing to push: Vantigo
		// removes nothing it did not write.
		r.photoPushedStamp = &effective
		return true
	}
	if calls >= accountsMaxCalls {
		r.soon = true
		return true
	}
	var entry *contracts.AvatarEntry
	if info != nil {
		if entry, err = withCallTimeout(ctx, func(ctx context.Context) (*contracts.AvatarEntry, error) {
			return s.usersAvatar(ctx, user)
		}); err != nil {
			log.WarnContext(ctx, "staff: read the linked user's avatar for the directory", "error", err.Error())
			return true
		}
		// The picture as read with its bytes, which may have changed since;
		// gone since, the next claim judges it.
		if entry == nil || (r.photoPushedStamp != nil && *r.photoPushedStamp == entry.Stamp) {
			return true
		}
		effective = entry.Stamp
	}
	if !s.renewAccountLease(ctx, claim.ID, lease) {
		log.InfoContext(ctx, "staff: an account claim's lease lapsed; the provider chain stops", "step", string(accountStepPhoto))
		return false
	}
	externalID := *r.externalID
	code := codePhotoPushed
	if entry == nil {
		code = codePhotoCleared
	}
	_, err = withCallTimeout(ctx, func(ctx context.Context) (struct{}, error) {
		if entry == nil {
			return struct{}{}, s.providerClearPhoto(ctx, p, run.actor, externalID)
		}
		return struct{}{}, s.providerSetPhoto(ctx, p, run.actor, externalID, contracts.Photo{ContentType: entry.ContentType, Data: entry.Data})
	})
	if err != nil {
		if _, _, answer := providerCallCode(err); !answer {
			log.WarnContext(ctx, "staff: a provider call failed", "step", string(accountStepPhoto), "error", err.Error())
		}
		if l := f.provider.link; calls == 0 && l != nil && l.LastError != nil {
			// Nothing else was called: the failure the link carries is the
			// push's own at the last claim, which the chain's "nothing to do"
			// lifted, and the attempts count on from it.
			r.lastError, r.attempts = l.LastError, l.Attempts
		}
		r.failCall(accountStepPhoto, err)
		return true
	}
	r.photoPushedStamp = &effective
	acc, err := withCallTimeout(ctx, func(ctx context.Context) (*contracts.ExternalAccount, error) {
		return s.providerAccount(ctx, p, externalID)
	})
	switch {
	case err != nil:
		// The push stood; the next claim's read records what the account reports.
		log.WarnContext(ctx, "staff: read an external account after its photo", "error", err.Error())
	case acc != nil:
		r.setCodes(acc.Reported, f.provider.spec.managerMissing)
		if slices.Contains(acc.Reported, contracts.ReportedPhotoNotWritten) {
			code = codePhotoRefused
		}
	}
	r.event(eventAccountExternalUpdated, accountStepPhoto, code)
	return true
}
