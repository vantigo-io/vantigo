package identity

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
)

// This file is the Owner's single sign-on endpoints (Entra design D2): the
// workforce OIDC provider and the SCIM credential, read and written on
// identity.sso_settings. The contract and the row are in place; the
// handlers answer an error until the endpoints are built.

// errSSOSettingsNotImplemented is what every handler below answers until
// then.
var errSSOSettingsNotImplemented = errors.New("identity: not implemented in task 2")

// GetIdentityOwnerSso answers the single sign-on settings.
// (GET /api/v1/identity/owner/sso)
func (s *server) GetIdentityOwnerSso(context.Context, gen.GetIdentityOwnerSsoRequestObject) (gen.GetIdentityOwnerSsoResponseObject, error) {
	// Task 4 implements this.
	return nil, errSSOSettingsNotImplemented
}

// PutIdentityOwnerSsoOidc replaces the workforce OIDC provider.
// (PUT /api/v1/identity/owner/sso/oidc)
func (s *server) PutIdentityOwnerSsoOidc(context.Context, gen.PutIdentityOwnerSsoOidcRequestObject) (gen.PutIdentityOwnerSsoOidcResponseObject, error) {
	// Task 4 implements this.
	return nil, errSSOSettingsNotImplemented
}

// DeleteIdentityOwnerSsoOidc turns the workforce OIDC provider off.
// (DELETE /api/v1/identity/owner/sso/oidc)
func (s *server) DeleteIdentityOwnerSsoOidc(context.Context, gen.DeleteIdentityOwnerSsoOidcRequestObject) (gen.DeleteIdentityOwnerSsoOidcResponseObject, error) {
	// Task 4 implements this.
	return nil, errSSOSettingsNotImplemented
}

// PostIdentityOwnerSsoScimToken issues a SCIM token.
// (POST /api/v1/identity/owner/sso/scim/token)
func (s *server) PostIdentityOwnerSsoScimToken(context.Context, gen.PostIdentityOwnerSsoScimTokenRequestObject) (gen.PostIdentityOwnerSsoScimTokenResponseObject, error) {
	// Task 4 implements this.
	return nil, errSSOSettingsNotImplemented
}

// DeleteIdentityOwnerSsoScimPrevious ends a SCIM token rotation's overlap.
// (DELETE /api/v1/identity/owner/sso/scim/previous)
func (s *server) DeleteIdentityOwnerSsoScimPrevious(context.Context, gen.DeleteIdentityOwnerSsoScimPreviousRequestObject) (gen.DeleteIdentityOwnerSsoScimPreviousResponseObject, error) {
	// Task 4 implements this.
	return nil, errSSOSettingsNotImplemented
}

// DeleteIdentityOwnerSsoScim turns SCIM off.
// (DELETE /api/v1/identity/owner/sso/scim)
func (s *server) DeleteIdentityOwnerSsoScim(context.Context, gen.DeleteIdentityOwnerSsoScimRequestObject) (gen.DeleteIdentityOwnerSsoScimResponseObject, error) {
	// Task 4 implements this.
	return nil, errSSOSettingsNotImplemented
}
