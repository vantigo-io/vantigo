package entra

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the provider's status (Entra design D11, plan reading 2): what
// Provider answers Staff and GET /connection shows, read from the
// connection's row and the secrets' and nothing else — no network — since
// Staff's GET /meta asks it on every request. The connection test and the
// lifecycle's refusals write the row's status; the reasons derived from the
// secrets' dates and the sensitive streak are read here.

// The status reasons, each within Staff's bound (reasonPattern).
const (
	reasonCredentialInvalid = codeCredentialInvalid
	reasonTenantUnreachable = codeTenantUnreachable
	// reasonConsentMissing: the token carried no roles at all, or Graph
	// refused the organisation for want of a permission.
	reasonConsentMissing = "consent_missing"
	// reasonSecretExpired: secrets exist, none unrevoked is unexpired.
	reasonSecretExpired = "secret_expired"
	// reasonSecretExpiring: the valid secret that ends last ends within
	// secretExpiringWithin.
	reasonSecretExpiring = "secret_expiring"
	// reasonRoleMissing: sensitiveDeniedThreshold sensitive writes in a row
	// were refused (plan reading 4).
	reasonRoleMissing = "role_missing"
)

const (
	// secretExpiringWithin is how early secret_expiring is said.
	secretExpiringWithin = 30 * 24 * time.Hour
	// sensitiveDeniedThreshold is the streak that reads as role_missing.
	sensitiveDeniedThreshold = 3
)

// reasonPattern is Staff's bound on a reason code.
var reasonPattern = regexp.MustCompile(`^[a-z_]{1,40}$`)

// providerInfo is the provider as Provider names it, read from the
// connection's row and the secrets at now.
func providerInfo(ctx context.Context, q *store.Queries, now time.Time) (contracts.ProviderInfo, error) {
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return contracts.ProviderInfo{}, fmt.Errorf("entra: read the connection: %w", err)
	}
	creds, err := q.ListCredentials(ctx)
	if err != nil {
		return contracts.ProviderInfo{}, fmt.Errorf("entra: read the secrets: %w", err)
	}
	status, reason := statusOf(conn, creds, now)
	return contracts.ProviderInfo{Key: providerKey, Name: providerName, Status: status, Reason: reason}, nil
}

// statusOf is the connection's status and its reason at now:
//
//   - unconfigured while the tenant or the client is missing, or no secret
//     has been added (or every one is revoked);
//   - failing with secret_expired when secrets are there but none
//     unrevoked is unexpired;
//   - else the row's status: connected with secret_expiring when the valid
//     secret ending last ends within 30 days, role_missing while the
//     sensitive streak is at its threshold, else the row's reason; failing
//     with the row's reason.
//
// A reason outside reasonPattern is dropped rather than answered.
func statusOf(conn store.EntraConnection, creds []store.EntraCredential, now time.Time) (string, string) {
	if conn.TenantID == nil || conn.ClientID == nil {
		return contracts.ProviderUnconfigured, ""
	}
	unrevoked, latest := 0, time.Time{}
	for _, c := range creds {
		if c.RevokedAt != nil {
			continue
		}
		unrevoked++
		if c.ExpiresAt.After(now) && c.ExpiresAt.After(latest) {
			latest = c.ExpiresAt
		}
	}
	switch {
	case unrevoked == 0:
		return contracts.ProviderUnconfigured, ""
	case latest.IsZero():
		return contracts.ProviderFailing, reasonSecretExpired
	}
	reason := ""
	if conn.StatusReason != nil {
		reason = *conn.StatusReason
	}
	switch conn.Status {
	case contracts.ProviderConnected:
		switch {
		case latest.Sub(now) <= secretExpiringWithin:
			reason = reasonSecretExpiring
		case conn.SensitiveDeniedStreak >= sensitiveDeniedThreshold:
			reason = reasonRoleMissing
		}
	case contracts.ProviderFailing:
	default:
		return contracts.ProviderUnconfigured, ""
	}
	if !reasonPattern.MatchString(reason) {
		reason = ""
	}
	return conn.Status, reason
}
