package google

import (
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the connection's status (Google Workspace design G15): what
// the row holds, and what a read answers of it.

// The statuses, the same words as contracts.Provider* plus
// pending_delegation, which only this module has.
const (
	statusUnconfigured      = contracts.ProviderUnconfigured
	statusPendingDelegation = "pending_delegation"
	statusConnected         = contracts.ProviderConnected
	statusFailing           = contracts.ProviderFailing
)

// The status reasons.
const (
	// reasonNotDelegated: every scope the module needs was refused — the
	// delegation is not entered yet, or not applied yet (pending_delegation).
	reasonNotDelegated = "not_delegated"
	// reasonCredentialInvalid: Google refused every live key, or the admin
	// to impersonate (failing).
	reasonCredentialInvalid = "credential_invalid"
	// reasonAdminRefused: the admin to impersonate is not allowed an Admin
	// SDK call the module needs, a 403 (failing).
	reasonAdminRefused = "admin_refused"
	// reasonGoogleUnreachable: Google did not answer (failing).
	reasonGoogleUnreachable = "google_unreachable"
)

// The credential kinds, google.credentials.kind, and the connection's
// credential_kind values.
const (
	credentialKindKey          = "service_account_key"
	credentialKindClientSecret = "oauth_client_secret"
	// connectionKindHosted is the credential kind the schema admits and
	// this version refuses (G13).
	connectionKindHosted = "vantigo_hosted"
)

// statusOf is the connection's status and reason as a read answers them:
// unconfigured while no live key is stored or no admin is named, whatever
// the last test wrote; the row's own otherwise.
func statusOf(conn store.GoogleConnection, creds []store.GoogleCredential) (string, string) {
	if !configured(conn, creds) {
		return statusUnconfigured, ""
	}
	return conn.Status, deref(conn.StatusReason)
}

// configured reports whether a test can be made: an admin to impersonate
// named and a live key stored.
func configured(conn store.GoogleConnection, creds []store.GoogleCredential) bool {
	return conn.AdminSubject != nil && len(liveKeys(creds)) > 0
}
