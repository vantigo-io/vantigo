package identity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // registers image/jpeg with image.DecodeConfig
	_ "image/png"  // registers image/png with image.DecodeConfig
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"

	"github.com/google/uuid"
)

// accountAvatarPath is GET/PUT/POST/DELETE /account/avatar's contract path,
// and the literal AccountResponse.avatarUrl and AvatarResponse.url answer
// with, base path prefixed (as bootstrap's Location is, sessionLocation).
const accountAvatarPath = "/api/v1/identity/account/avatar"

// The avatar bounds (EA/AccountSettingsEndpoints.cs:27-28,35): the image
// itself at most 5 MiB, the whole multipart request at most that plus 64
// KiB of multipart framing, and each side at most 4096 pixels.
const (
	maxAvatarImageBytes   = 5 * 1024 * 1024
	maxAvatarRequestBytes = maxAvatarImageBytes + 64*1024
	maxAvatarDimension    = 4096
)

// invalidAvatarMessage answers every way an avatar upload is rejected: no
// "avatar" part, a declared type the magic bytes disagree with, a type
// image.DecodeConfig cannot read as PNG or JPEG, dimensions over 4096 on
// either side, or a body past either size bound (decision: the contract
// documents no 413 for these operations, so an oversized body is this same
// 400, not a distinct code — see avatarBodyLimits).
const invalidAvatarMessage = "The avatar must be a correctly detected PNG or JPEG image of at most 5 MB and 4096x4096 pixels."

// avatarImageFormats maps a sniffed magic-byte type to the format name
// image.DecodeConfig reports for it.
var avatarImageFormats = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpeg",
}

// avatarBodyLimits raises the router's request-body cap
// (module.RouterOptions.BodyLimits) for POST/PUT /account/avatar to
// maxAvatarRequestBytes. The router puts its http.MaxBytesReader in place
// before the generated strict server's multipart decoder
// (r.MultipartReader(), called on the raw body) ever reads it, so this is
// the one cap on an upload: there is no second, smaller one in front of it
// or behind it. Once the cap is hit, every later Read (ours, draining a
// preceding part, or the multipart framing itself) returns
// *http.MaxBytesError, which avatarPart's read failure folds into the same
// 400 invalid_avatar an oversized or malformed image gets.
var avatarBodyLimits = map[string]int64{
	"postIdentityAccountAvatar": maxAvatarRequestBytes,
	"putIdentityAccountAvatar":  maxAvatarRequestBytes,
}

// GetIdentityAccount describes the caller's own account
// (EA/AccountSettingsEndpoints.cs:76-91): id, display name, email (null for
// an OIDC synthetic address), preferred language, and the avatar URL when
// one is stored.
func (s *server) GetIdentityAccount(ctx context.Context, _ gen.GetIdentityAccountRequestObject) (gen.GetIdentityAccountResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.q.GetUserByID(ctx, p.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The session check joined the user a moment ago; it has gone since
		// (GetIdentitySession answers this race the same way).
		return gen.GetIdentityAccount401JSONResponse(authErrorBody("unauthenticated", "The session is not authenticated.", nil)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: account: %w", err)
	}
	hasAvatar, err := s.q.HasProfileAvatar(ctx, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("identity: account: %w", err)
	}
	return gen.GetIdentityAccount200JSONResponse(s.accountResponse(u.ID, u.DisplayName, u.Email, u.PreferredLanguage, hasAvatar)), nil
}

// PutIdentityAccount, PutIdentityAccountProfile and PatchIdentityAccountProfile
// share one handler: .NET maps all three (MapPut "", MapPut "/profile",
// MapPatch "/profile") to the same UpdateProfile
// (EA/AccountSettingsEndpoints.cs:93-131).
func (s *server) PutIdentityAccount(ctx context.Context, req gen.PutIdentityAccountRequestObject) (gen.PutIdentityAccountResponseObject, error) {
	r, err := s.updateProfile(ctx, req.Body)
	if err != nil {
		return nil, err
	}
	switch r.status {
	case http.StatusOK:
		return gen.PutIdentityAccount200JSONResponse(r.account), nil
	case http.StatusUnauthorized:
		return gen.PutIdentityAccount401JSONResponse(authErrorBody(r.code, r.message, nil)), nil
	default:
		return gen.PutIdentityAccount400JSONResponse(authErrorBody(r.code, r.message, r.fields)), nil
	}
}

func (s *server) PutIdentityAccountProfile(ctx context.Context, req gen.PutIdentityAccountProfileRequestObject) (gen.PutIdentityAccountProfileResponseObject, error) {
	r, err := s.updateProfile(ctx, req.Body)
	if err != nil {
		return nil, err
	}
	switch r.status {
	case http.StatusOK:
		return gen.PutIdentityAccountProfile200JSONResponse(r.account), nil
	case http.StatusUnauthorized:
		return gen.PutIdentityAccountProfile401JSONResponse(authErrorBody(r.code, r.message, nil)), nil
	default:
		return gen.PutIdentityAccountProfile400JSONResponse(authErrorBody(r.code, r.message, r.fields)), nil
	}
}

func (s *server) PatchIdentityAccountProfile(ctx context.Context, req gen.PatchIdentityAccountProfileRequestObject) (gen.PatchIdentityAccountProfileResponseObject, error) {
	r, err := s.updateProfile(ctx, req.Body)
	if err != nil {
		return nil, err
	}
	switch r.status {
	case http.StatusOK:
		return gen.PatchIdentityAccountProfile200JSONResponse(r.account), nil
	case http.StatusUnauthorized:
		return gen.PatchIdentityAccountProfile401JSONResponse(authErrorBody(r.code, r.message, nil)), nil
	default:
		return gen.PatchIdentityAccountProfile400JSONResponse(authErrorBody(r.code, r.message, r.fields)), nil
	}
}

// profileUpdateResult is updateProfile's answer, generic over the three
// operations that share it; each wrapper maps status to its own generated
// response type.
type profileUpdateResult struct {
	status  int
	account gen.AccountResponse
	code    string
	message string
	fields  map[string][]string
}

// updateProfile validates and applies a profile edit
// (EA/AccountSettingsEndpoints.cs:93-131, :905-917): displayName is
// required and at most 200 characters (trimmed); preferredLanguage, when
// non-blank and not "automatic", must be "en" or "nb" (case-insensitively);
// anything else is 400 invalid_request. It deliberately does not rotate a
// stamp or revoke sessions (spec *Sessions*, decision above).
func (s *server) updateProfile(ctx context.Context, body *gen.AccountProfileRequest) (profileUpdateResult, error) {
	if fields := validateProfile(body); fields != nil {
		return profileUpdateResult{status: http.StatusBadRequest, code: "invalid_request", message: profileInvalidMessage, fields: fields}, nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return profileUpdateResult{}, err
	}
	row, err := s.q.UpdateAccountProfile(ctx, store.UpdateAccountProfileParams{
		ID:                p.UserID,
		DisplayName:       strings.TrimSpace(deref(body.DisplayName)),
		PreferredLanguage: normalizePreferredLanguage(body.PreferredLanguage),
		Now:               s.deps.Clock(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return profileUpdateResult{status: http.StatusUnauthorized, code: "unauthenticated", message: "The session is not authenticated."}, nil
	}
	if err != nil {
		return profileUpdateResult{}, fmt.Errorf("identity: update profile: %w", err)
	}
	hasAvatar, err := s.q.HasProfileAvatar(ctx, p.UserID)
	if err != nil {
		return profileUpdateResult{}, fmt.Errorf("identity: update profile: %w", err)
	}
	return profileUpdateResult{
		status:  http.StatusOK,
		account: s.accountResponse(row.ID, row.DisplayName, row.Email, row.PreferredLanguage, hasAvatar),
	}, nil
}

// profileInvalidMessage is .NET's UpdateProfile validation-failure message
// (EA/AccountSettingsEndpoints.cs:104).
const profileInvalidMessage = "The profile request is invalid."

// validateProfile is .NET's ValidateProfile (EA/AccountSettingsEndpoints.cs:905-917).
func validateProfile(body *gen.AccountProfileRequest) map[string][]string {
	fields := map[string][]string{}
	var displayName string
	if body != nil {
		displayName = deref(body.DisplayName)
	}
	trimmed := strings.TrimSpace(displayName)
	if trimmed == "" || utf16Length(trimmed) > maxDisplayNameLength {
		fields["displayName"] = []string{"Display name is required and must be at most 200 characters."}
	}
	if body != nil && body.PreferredLanguage != nil {
		lang := strings.TrimSpace(*body.PreferredLanguage)
		if lang != "" && !strings.EqualFold(lang, "en") && !strings.EqualFold(lang, "nb") && !strings.EqualFold(lang, "automatic") {
			fields["preferredLanguage"] = []string{"Preferred language must be Automatic, en, or nb."}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// normalizePreferredLanguage is .NET's NormalizeLanguage
// (EA/AccountSettingsEndpoints.cs:900-903): null, blank or "automatic"
// (case-insensitively) becomes nil; otherwise the trimmed value, lowercased.
// validateProfile has already confirmed a non-nil result is "en" or "nb",
// the only values identity.users.preferred_language's CHECK allows.
func normalizePreferredLanguage(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" || strings.EqualFold(trimmed, "automatic") {
		return nil
	}
	lower := strings.ToLower(trimmed)
	return &lower
}

// accountResponse is a user as GET/PUT /account and PUT|PATCH
// /account/profile show one (EA/AccountSettingsEndpoints.cs:699-701).
func (s *server) accountResponse(id uuid.UUID, displayName, email string, preferredLanguage *string, hasAvatar bool) gen.AccountResponse {
	var avatarURL *string
	if hasAvatar {
		u := s.deps.Config.BasePath + accountAvatarPath
		avatarURL = &u
	}
	return gen.AccountResponse{
		Id:                id,
		DisplayName:       displayName,
		Email:             publicEmail(email),
		PreferredLanguage: preferredLanguage,
		AvatarUrl:         avatarURL,
	}
}

// Account password change's messages (EA/AccountSettingsEndpoints.cs:669-687,919-926).
const (
	passwordInvalidMessage           = "The password request is invalid."
	passwordCouldNotBeChangedMessage = "The password could not be changed."
	localPasswordUnavailableMessage  = "This OIDC-only account does not have a local password."
	reauthenticationRequiredMessage  = "The current password is invalid."
)

// maxNewPasswordLength is .NET's AccountPasswordRequest bound
// (EA/AccountSettingsEndpoints.cs:924).
const maxNewPasswordLength = 256

// PostIdentityAccountPassword changes the caller's local password
// (EA/AccountSettingsEndpoints.cs:133-168). In order: 400 invalid_request
// for a missing or oversized field; 409 local_password_unavailable when the
// account has no local password (an OIDC-only account); 400
// reauthentication_required for a wrong current password; 400
// identity_validation_failed with fields when the new password fails the
// policy. A success stores the new hash, bumps version, revokes every other
// session while keeping the caller's, and deletes every password-reset
// token of this user (spec *Sessions* and *Credentials*; .NET only
// refreshed the caller's cookie and relied on the stamp rotation to end
// other sessions and invalidate reset tokens implicitly).
func (s *server) PostIdentityAccountPassword(ctx context.Context, req gen.PostIdentityAccountPasswordRequestObject) (gen.PostIdentityAccountPasswordResponseObject, error) {
	var body gen.AccountPasswordRequest
	if req.Body != nil {
		body = *req.Body
	}
	if fields := validateAccountPassword(&body); fields != nil {
		return gen.PostIdentityAccountPassword400JSONResponse(authErrorBody("invalid_request", passwordInvalidMessage, fields)), nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.q.GetUserByID(ctx, p.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostIdentityAccountPassword401JSONResponse(authErrorBody("unauthenticated", "The session is not authenticated.", nil)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: change password: %w", err)
	}
	if u.PasswordHash == nil {
		return gen.PostIdentityAccountPassword409JSONResponse(authErrorBody("local_password_unavailable", localPasswordUnavailableMessage, nil)), nil
	}
	ok, err := verifyPassword(*u.PasswordHash, deref(body.CurrentPassword))
	if err != nil {
		return nil, fmt.Errorf("identity: change password: %w", err)
	}
	if !ok {
		return gen.PostIdentityAccountPassword400JSONResponse(authErrorBody("reauthentication_required", reauthenticationRequiredMessage, nil)), nil
	}
	newPassword := deref(body.NewPassword)
	if problems := validatePassword(newPassword, s.deps.Config.IsDevelopment()); problems != nil {
		return gen.PostIdentityAccountPassword400JSONResponse(authErrorBody("identity_validation_failed", passwordCouldNotBeChangedMessage, problems)), nil
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return nil, fmt.Errorf("identity: change password: %w", err)
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.UpdateAccountPassword(ctx, store.UpdateAccountPasswordParams{ID: p.UserID, PasswordHash: &hash, Version: uuid.New(), Now: now}); err != nil {
			return err
		}
		if err := q.DeleteUserPasswordResetTokens(ctx, p.UserID); err != nil {
			return err
		}
		return s.access.revokeOtherSessions(ctx, q, p.UserID, p.SessionID)
	})
	if err != nil {
		return nil, fmt.Errorf("identity: change password: %w", err)
	}
	return gen.PostIdentityAccountPassword200JSONResponse{Success: true}, nil
}

// validateAccountPassword is .NET's ValidatePassword
// (EA/AccountSettingsEndpoints.cs:919-926): both fields present, the new
// one at most 256 characters. It does not trim: an all-whitespace password
// passes here and fails the policy instead, as .NET's IsNullOrEmpty did.
func validateAccountPassword(body *gen.AccountPasswordRequest) map[string][]string {
	fields := map[string][]string{}
	if deref(body.CurrentPassword) == "" {
		fields["currentPassword"] = []string{"Current password is required."}
	}
	switch newPassword := deref(body.NewPassword); {
	case newPassword == "":
		fields["newPassword"] = []string{"New password is required."}
	case utf16Length(newPassword) > maxNewPasswordLength:
		fields["newPassword"] = []string{"New password must be 256 characters or fewer."}
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// PostIdentityAccountAvatar and PutIdentityAccountAvatar upload the
// caller's avatar; .NET maps both methods to the same UploadAvatar
// (EA/AccountSettingsEndpoints.cs:50-55,170-243).
func (s *server) PostIdentityAccountAvatar(ctx context.Context, req gen.PostIdentityAccountAvatarRequestObject) (gen.PostIdentityAccountAvatarResponseObject, error) {
	body, ok, err := s.uploadAvatar(ctx, req.Body)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PostIdentityAccountAvatar400JSONResponse(authErrorBody("invalid_avatar", invalidAvatarMessage, nil)), nil
	}
	return gen.PostIdentityAccountAvatar200JSONResponse(body), nil
}

func (s *server) PutIdentityAccountAvatar(ctx context.Context, req gen.PutIdentityAccountAvatarRequestObject) (gen.PutIdentityAccountAvatarResponseObject, error) {
	body, ok, err := s.uploadAvatar(ctx, req.Body)
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.PutIdentityAccountAvatar400JSONResponse(authErrorBody("invalid_avatar", invalidAvatarMessage, nil)), nil
	}
	return gen.PutIdentityAccountAvatar200JSONResponse(body), nil
}

// uploadAvatar validates and stores an avatar upload. ok is false for
// every way the brief rejects one: no "avatar" part, a declared type that
// disagrees with the sniffed magic bytes or is not PNG/JPEG,
// image.DecodeConfig failing or reporting more than 4096 pixels on a side,
// or a read past either size bound (avatarPart, avatarBodyLimits). A
// success upserts profile_avatars, incrementing its version.
func (s *server) uploadAvatar(ctx context.Context, body *multipart.Reader) (gen.AvatarResponse, bool, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return gen.AvatarResponse{}, false, err
	}
	if body == nil {
		return gen.AvatarResponse{}, false, nil
	}
	data, declared, found := avatarPart(body)
	if !found {
		return gen.AvatarResponse{}, false, nil
	}
	contentType, ok := avatarImageType(declared, data)
	if !ok {
		return gen.AvatarResponse{}, false, nil
	}
	if err := s.q.UpsertProfileAvatar(ctx, store.UpsertProfileAvatarParams{
		UserID: p.UserID, Data: data, ContentType: contentType, Now: s.deps.Clock(),
	}); err != nil {
		return gen.AvatarResponse{}, false, fmt.Errorf("identity: avatar upload: %w", err)
	}
	return gen.AvatarResponse{Uploaded: true, Url: s.deps.Config.BasePath + accountAvatarPath}, true, nil
}

// avatarPart reads the multipart part named "avatar" (the contract's only
// documented field; unlike .NET, which falls back to whatever single file
// the form carries, this requires the exact name the contract promises).
// data is capped at maxAvatarImageBytes: a read failure — an oversized
// part, a malformed multipart body, or the router's request cap
// (avatarBodyLimits) firing while draining a preceding part — as well as an
// empty or still-too-large part, all report found=false.
func avatarPart(mr *multipart.Reader) (data []byte, declared string, found bool) {
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, "", false
		}
		if part.FormName() != "avatar" {
			_ = part.Close()
			continue
		}
		declared = part.Header.Get("Content-Type")
		data, err = io.ReadAll(io.LimitReader(part, maxAvatarImageBytes+1))
		_ = part.Close()
		return data, declared, err == nil && len(data) > 0 && len(data) <= maxAvatarImageBytes
	}
}

// avatarImageType reports data's type when it is a genuine PNG or JPEG: the
// type sniffed from its magic bytes (http.DetectContentType) must equal
// declared, and image.DecodeConfig — which reads only the header, never
// decompressing the image, so a decompression bomb costs nothing — must
// decode it as that same format within maxAvatarDimension on each side
// (EA/AccountSettingsEndpoints.cs:211-216,776-898).
func avatarImageType(declared string, data []byte) (string, bool) {
	sniffed := http.DetectContentType(data)
	format, known := avatarImageFormats[sniffed]
	if !known || !strings.EqualFold(declared, sniffed) {
		return "", false
	}
	cfg, gotFormat, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || gotFormat != format {
		return "", false
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxAvatarDimension || cfg.Height > maxAvatarDimension {
		return "", false
	}
	return sniffed, true
}

// avatarResponseHeaders sets the private, unsniffable, inline headers every
// successful GET /account/avatar answers with
// (EA/AccountSettingsEndpoints.cs:267-269), before the generated response's
// own Visit writes Content-Type and the status.
type avatarResponseHeaders struct {
	body gen.GetIdentityAccountAvatarResponseObject
}

func (r avatarResponseHeaders) VisitGetIdentityAccountAvatarResponse(w http.ResponseWriter) error {
	setAvatarHeaders(w)
	return r.body.VisitGetIdentityAccountAvatarResponse(w)
}

// setAvatarHeaders sets the headers every avatar read answers with, the
// account's own and an Owner's read of another user's alike.
func setAvatarHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
}

// GetIdentityAccountAvatar serves the caller's stored avatar bytes, or 404
// when there is none (EA/AccountSettingsEndpoints.cs:245-271).
func (s *server) GetIdentityAccountAvatar(ctx context.Context, _ gen.GetIdentityAccountAvatarRequestObject) (gen.GetIdentityAccountAvatarResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.q.GetProfileAvatar(ctx, p.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetIdentityAccountAvatar404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: avatar: %w", err)
	}
	var body gen.GetIdentityAccountAvatarResponseObject
	if row.ContentType == "image/png" {
		body = gen.GetIdentityAccountAvatar200ImagepngResponse{Body: bytes.NewReader(row.Data), ContentLength: int64(len(row.Data))}
	} else {
		body = gen.GetIdentityAccountAvatar200ImagejpegResponse{Body: bytes.NewReader(row.Data), ContentLength: int64(len(row.Data))}
	}
	return avatarResponseHeaders{body: body}, nil
}

// DeleteIdentityAccountAvatar hard-deletes the caller's avatar, if any
// (EA/AccountSettingsEndpoints.cs:273-289): idempotent, always 204.
func (s *server) DeleteIdentityAccountAvatar(ctx context.Context, _ gen.DeleteIdentityAccountAvatarRequestObject) (gen.DeleteIdentityAccountAvatarResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.q.DeleteProfileAvatar(ctx, p.UserID); err != nil {
		return nil, fmt.Errorf("identity: delete avatar: %w", err)
	}
	return gen.DeleteIdentityAccountAvatar204Response{}, nil
}
