package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // a PNG is one of the two pictures Vantigo holds
	"net/http"
	"strings"

	"golang.org/x/image/draw"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the account's photo (Google Workspace design G21): the
// picture Vantigo holds cropped to its centre square, scaled to 96×96 — what
// the Directory keeps —, encoded as a JPEG and written with
// users.photos.update in Google's web-safe base64 (+ as -, / as _, = as *).
// A picture Google refuses (U13), or one that cannot be decoded, is no
// failure: the account reports photo_not_written until a photo is written
// or cleared. users.photos.delete clears it.

// The conversion's bounds.
const (
	// photoSide is the side of the square the Directory keeps.
	photoSide = 96
	// photoMaxSourceSide is the longest side of a picture decoded: what
	// Staff and identity admit.
	photoMaxSourceSide = 4096
	// photoQuality is the JPEG's quality.
	photoQuality = 90
)

// errPhotoUndecodable: the bytes are no JPEG or PNG Go decodes, or one
// larger than photoMaxSourceSide a side. Reported photo_not_written.
var errPhotoUndecodable = errors.New("google: the photo cannot be decoded")

// convertPhoto is p as the Directory keeps it: decoded (JPEG or PNG,
// whatever the content type says), its centre square drawn over white at
// 96×96 with Catmull-Rom, encoded as a JPEG. Nothing of the original's
// metadata survives.
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
	side := min(b.Dx(), b.Dy())
	origin := image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2)
	crop := image.Rectangle{Min: origin, Max: origin.Add(image.Pt(side, side))}
	dst := image.NewRGBA(image.Rect(0, 0, photoSide, photoSide))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: photoQuality}); err != nil {
		return nil, fmt.Errorf("google: encode the photo: %w", err)
	}
	return out.Bytes(), nil
}

// webSafeBase64 is data in the alphabet users.photos takes: standard base64
// with + as -, / as _ and the padding = as *.
func webSafeBase64(data []byte) string {
	return strings.NewReplacer("+", "-", "/", "_", "=", "*").Replace(base64.StdEncoding.EncodeToString(data))
}

// photoRefused reports whether err is Google refusing the photo itself: a
// 400, a 403, a 404 (the user was just read, so it is the photo's) or a
// 413.
func photoRefused(err error) bool {
	return isStatus(err, http.StatusBadRequest) || isStatus(err, http.StatusForbidden) ||
		isStatus(err, http.StatusNotFound) || isStatus(err, http.StatusRequestEntityTooLarge)
}

// SetPhoto writes the account's photo (contracts.IdentityProvisioner.
// SetPhoto), converted by convertPhoto. A picture that cannot be converted,
// or that Google refuses, answers nil: the account reports
// photo_not_written and the event carries refused:photo_not_written. A
// photo written clears the code; the event carries photo_written. An
// unconfirmed adoption is ErrExternalUnconfirmed, and an administrator
// ErrExternalPrivileged, nothing written.
func (p *provisioner) SetPhoto(ctx context.Context, actor contracts.Actor, externalID string, photo contracts.Photo) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	s, u, row, err := p.openUser(ctx, externalID, contracts.ExternalSpec{}, true)
	if err != nil {
		return unavailable(err)
	}
	if !trusted(row) {
		return p.unconfirmed(ctx, s, actor, actionUserPhotoSet, u, row)
	}
	if u.privileged() {
		return p.refused(ctx, s, actor, actionUserPhotoSet, u.ID, privilegedRefusal())
	}
	data, err := convertPhoto(photo)
	if err == nil {
		err = p.keys.c.send(ctx, s.users, http.MethodPut, userURL(u.ID, "/photos/thumbnail"), map[string]any{
			"photoData": webSafeBase64(data), "mimeType": "JPEG", "width": photoSide, "height": photoSide,
		}, nil)
	}
	var reported, codes []string
	switch {
	case err == nil:
		codes = []string{"photo_written"}
	case errors.Is(err, errPhotoUndecodable), photoRefused(err):
		p.keys.c.logger.InfoContext(ctx, "google: the photo was not written", "user_id", u.ID, "error", err.Error())
		reported = []string{contracts.ReportedPhotoNotWritten}
		codes = []string{"refused:" + contracts.ReportedPhotoNotWritten}
	default:
		return unavailable(err)
	}
	_, err = p.settle(ctx, s, actor, actionUserPhotoSet, u, accountState(u, row), mergeCodes(row.Reported, photoJudges, reported), codes)
	return err
}

// ClearPhoto removes the account's photo (contracts.IdentityProvisioner.
// ClearPhoto): a user with none (404) is done. A clear Google refuses
// answers nil, reported photo_not_written as SetPhoto's refusal is. An
// unconfirmed adoption is ErrExternalUnconfirmed, and an administrator
// ErrExternalPrivileged, nothing written.
func (p *provisioner) ClearPhoto(ctx context.Context, actor contracts.Actor, externalID string) error {
	if !actor.Valid() {
		return contracts.ErrInvalidActor
	}
	s, u, row, err := p.openUser(ctx, externalID, contracts.ExternalSpec{}, true)
	if err != nil {
		return unavailable(err)
	}
	if !trusted(row) {
		return p.unconfirmed(ctx, s, actor, actionUserPhotoCleared, u, row)
	}
	if u.privileged() {
		return p.refused(ctx, s, actor, actionUserPhotoCleared, u.ID, privilegedRefusal())
	}
	var reported []string
	codes := []string{"photo_cleared"}
	err = p.keys.c.send(ctx, s.users, http.MethodDelete, userURL(u.ID, "/photos/thumbnail"), nil, nil)
	switch {
	case err == nil, isStatus(err, http.StatusNotFound):
	case photoRefused(err):
		p.keys.c.logger.InfoContext(ctx, "google: the photo was not cleared", "user_id", u.ID, "error", err.Error())
		reported = []string{contracts.ReportedPhotoNotWritten}
		codes = []string{"refused:" + contracts.ReportedPhotoNotWritten}
	default:
		return unavailable(err)
	}
	_, err = p.settle(ctx, s, actor, actionUserPhotoCleared, u, accountState(u, row), mergeCodes(row.Reported, photoJudges, reported), codes)
	return err
}
