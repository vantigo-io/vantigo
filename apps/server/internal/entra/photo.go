package entra

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // a PNG is one of the two pictures Vantigo holds
	"net/http"

	"golang.org/x/image/draw"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the account's photo (Staff photo sync, spec §4 "Staff →
// Entra", reading 4): the picture Vantigo holds converted to what Graph
// takes — a JPEG under 4 MB, the longer side at most 1024 px — and PUT to
// /users/{id}/photo/$value as image/jpeg; cleared by a DELETE of the same
// path. A picture the tenant refuses (400, 403, 413 — and a 404 on the PUT,
// the user having just been read, is the photo's or the mailbox's), or one
// that cannot be decoded, is no failure: the account reports
// photo_not_written until a photo is written or cleared. A clear the tenant
// refuses is reported the same way. The permission is User.ReadWrite.All, among
// the eight already consented.

// The conversion's bounds.
const (
	// photoMaxSide is the longest side the photo is written at.
	photoMaxSide = 1024
	// photoMaxBytes is the most the JPEG may weigh: Graph says "limited to
	// 4 MB", read as decimal megabytes to stay under it either way.
	photoMaxBytes = 4_000_000
	// photoMaxSourceSide is the longest side of a picture decoded: what
	// Staff and identity admit. A larger one is not decoded at all.
	photoMaxSourceSide = 4096
)

// photoQualities are the JPEG qualities tried in turn until the photo is
// under photoMaxBytes.
var photoQualities = []int{85, 70, 55}

// The conversion's failures; each is reported photo_not_written.
var (
	// errPhotoUndecodable: the bytes are no JPEG or PNG Go decodes, or one
	// larger than photoMaxSourceSide a side.
	errPhotoUndecodable = errors.New("entra: the photo cannot be decoded")
	// errPhotoTooLarge: the photo weighs more than photoMaxBytes at the
	// lowest quality.
	errPhotoTooLarge = errors.New("entra: the photo is larger than 4 MB at the lowest quality")
)

// convertPhoto is p as Graph takes it: decoded (JPEG or PNG, whatever the
// content type says), scaled with Catmull-Rom so the longer side is at most
// photoMaxSide, drawn over white (a PNG's transparency has no place in a
// JPEG), and encoded as a JPEG at the first of photoQualities that weighs at
// most photoMaxBytes. Nothing of the original's metadata survives.
func convertPhoto(p contracts.Photo) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(p.Data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errPhotoUndecodable, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > photoMaxSourceSide || cfg.Height > photoMaxSourceSide {
		return nil, fmt.Errorf("%w: %d×%d px", errPhotoUndecodable, cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(p.Data))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errPhotoUndecodable, err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if longer := max(w, h); longer > photoMaxSide {
		w = max(1, w*photoMaxSide/longer)
		h = max(1, h*photoMaxSide/longer)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	}
	var out bytes.Buffer
	for _, q := range photoQualities {
		out.Reset()
		if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: q}); err != nil {
			return nil, fmt.Errorf("entra: encode the photo: %w", err)
		}
		if out.Len() <= photoMaxBytes {
			return out.Bytes(), nil
		}
	}
	return nil, errPhotoTooLarge
}

// photoRefused reports whether err is the tenant refusing the photo itself:
// a 400, a 403 or a 413.
func photoRefused(err error) bool {
	return isStatus(err, http.StatusBadRequest) || isStatus(err, http.StatusForbidden) ||
		isStatus(err, http.StatusRequestEntityTooLarge)
}

// photoPutRefused reports whether err is the tenant refusing the photo's
// PUT: what photoRefused names, and a 404 — openUser has just read the
// user, so the 404 is the photo's or the mailbox's, not the user's.
func photoPutRefused(err error) bool {
	return photoRefused(err) || isStatus(err, http.StatusNotFound)
}

// SetPhoto writes the account's photo (contracts.IdentityProvisioner.SetPhoto),
// converted by convertPhoto. A picture that cannot be converted, or that the
// tenant refuses, answers nil: nothing (more) is written, the account
// reports photo_not_written and the event carries
// refused:photo_not_written. A photo written clears the code; the event
// carries photo_written. Each call writes: the caller decides when a push is
// due. An unconfirmed adoption is ErrExternalUnconfirmed, nothing written.
func (p *provisioner) SetPhoto(ctx context.Context, actor contracts.Actor, externalID string, photo contracts.Photo) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	s, u, id, row, err := p.openUser(ctx, externalID, contracts.ExternalSpec{}, true)
	if err != nil {
		return err
	}
	if !trusted(row) {
		return p.unconfirmed(ctx, s, actor, actionUserPhotoSet, id, u, row)
	}
	data, err := convertPhoto(photo)
	if err == nil {
		err = p.client.putRaw(ctx, s.cred, userPath(id, "/photo/$value"), "image/jpeg", data)
	}
	var reported, codes []string
	switch {
	case err == nil:
		codes = []string{"photo_written"}
	case errors.Is(err, errPhotoUndecodable), errors.Is(err, errPhotoTooLarge), photoPutRefused(err):
		p.client.logger.InfoContext(ctx, "entra: the photo was not written", "object_id", id.String(), "error", err.Error())
		reported = []string{contracts.ReportedPhotoNotWritten}
		codes = []string{"refused:" + contracts.ReportedPhotoNotWritten}
	default:
		return err
	}
	_, err = p.settle(ctx, s, actor, actionUserPhotoSet, id, u.UserPrincipalName, accountState(u, row),
		mergeCodes(row.Reported, photoJudges, reported), codes)
	return err
}

// ClearPhoto removes the account's photo
// (contracts.IdentityProvisioner.ClearPhoto): a user with none (404) is
// done. The account no longer reports photo_not_written; the event carries
// photo_cleared. A clear the tenant refuses (photoRefused) answers nil, as
// SetPhoto's refusal does: the account reports photo_not_written and the
// event carries refused:photo_not_written. An unconfirmed adoption is
// ErrExternalUnconfirmed, nothing written.
func (p *provisioner) ClearPhoto(ctx context.Context, actor contracts.Actor, externalID string) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	s, u, id, row, err := p.openUser(ctx, externalID, contracts.ExternalSpec{}, true)
	if err != nil {
		return err
	}
	if !trusted(row) {
		return p.unconfirmed(ctx, s, actor, actionUserPhotoCleared, id, u, row)
	}
	var reported []string
	codes := []string{"photo_cleared"}
	err = p.client.del(ctx, s.cred, userPath(id, "/photo/$value"))
	switch {
	case err == nil, isStatus(err, http.StatusNotFound):
	case photoRefused(err):
		p.client.logger.InfoContext(ctx, "entra: the photo was not cleared", "object_id", id.String(), "error", err.Error())
		reported = []string{contracts.ReportedPhotoNotWritten}
		codes = []string{"refused:" + contracts.ReportedPhotoNotWritten}
	default:
		return err
	}
	_, err = p.settle(ctx, s, actor, actionUserPhotoCleared, id, u.UserPrincipalName, accountState(u, row),
		mergeCodes(row.Reported, photoJudges, reported), codes)
	return err
}

// accountState is the state a write that changes neither leaves row in: as
// Graph answers u, deleted when the row says so.
func accountState(u graphUser, row store.EntraAccount) string {
	switch {
	case row.State == stateDeleted:
		return stateDeleted
	case u.AccountEnabled:
		return stateEnabled
	}
	return stateDisabled
}
