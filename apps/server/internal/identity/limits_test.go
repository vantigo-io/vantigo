package identity

import (
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// TestNamedPoliciesKeepDotNetsLimits pins the nine policies to .NET's
// windows and limits (inventory §12), all with the authentication message
// and a Retry-After header.
func TestNamedPoliciesKeepDotNetsLimits(t *testing.T) {
	cases := []struct {
		policy ratelimit.Policy
		name   string
		limit  int
		window time.Duration
	}{
		{policyLogin, "Login", 100, time.Minute},
		{policyBootstrap, "Bootstrap", 20, time.Minute},
		{policyInvitations, "Invitations", 30, time.Minute},
		{policyInvitationAcceptance, "InvitationAcceptance", 20, time.Minute},
		{policyPasswordRecovery, "PasswordRecovery", 10, 15 * time.Minute},
		{policyMfa, "Mfa", 20, 5 * time.Minute},
		{policyPasskeyLogin, "PasskeyLogin", 30, 5 * time.Minute},
		{policyUserManagement, "UserManagement", 30, time.Minute},
		{policyOwnerAvatarRead, "OwnerAvatarRead", 300, time.Minute},
	}
	for _, c := range cases {
		want := ratelimit.Policy{Name: c.name, Limit: c.limit, Window: c.window,
			Message: "Too many authentication attempts. Please try again later."}
		if c.policy != want {
			t.Errorf("policy %s = %+v, want %+v", c.name, c.policy, want)
		}
	}
}

// TestLoginAttemptThrottleKeepsDotNetsLimit pins the per-account login
// throttle to .NET's LoginAttemptThrottle (ten failures a minute) with no
// Retry-After, and the two operations this area rate-limits by IP.
func TestLoginAttemptThrottleKeepsDotNetsLimit(t *testing.T) {
	want := ratelimit.Policy{Name: "login-attempts", Limit: 10, Window: time.Minute,
		Message: "Too many authentication attempts. Please try again later.", NoRetryAfter: true}
	if policyLoginAttempts != want {
		t.Errorf("policyLoginAttempts = %+v, want %+v", policyLoginAttempts, want)
	}
	if limits["postIdentityLogin"] != policyLogin || limits["postIdentityBootstrap"] != policyBootstrap {
		t.Errorf("limits = %+v, want Login on postIdentityLogin and Bootstrap on postIdentityBootstrap", limits)
	}
}

// TestAccountPasswordLimitKeepsDotNetsPolicy pins the brief's
// postIdentityAccountPassword→PasswordRecovery entry
// (EA/AccountSettingsEndpoints.cs:47-48).
func TestAccountPasswordLimitKeepsDotNetsPolicy(t *testing.T) {
	if limits["postIdentityAccountPassword"] != policyPasswordRecovery {
		t.Errorf("limits[postIdentityAccountPassword] = %+v, want policyPasswordRecovery", limits["postIdentityAccountPassword"])
	}
}

// TestUserInvitationAndRecoveryLimitsKeepDotNetsPolicies pins the brief's
// limits for owner user management, invitations and password recovery
// (EA/AuthAccountEndpoints.cs:41-100, inventory §12): UserManagement on
// every /owner/users operation but the avatar, which has its own policy.
func TestUserInvitationAndRecoveryLimitsKeepDotNetsPolicies(t *testing.T) {
	want := map[string]ratelimit.Policy{
		"getIdentityOwnerUsers":                   policyUserManagement,
		"postIdentityOwnerUsers":                  policyUserManagement,
		"putIdentityOwnerUsersById":               policyUserManagement,
		"deleteIdentityOwnerUsersById":            policyUserManagement,
		"postIdentityOwnerUsersByIdDisable":       policyUserManagement,
		"postIdentityOwnerUsersByIdEnable":        policyUserManagement,
		"postIdentityOwnerUsersByIdPassword":      policyUserManagement,
		"postIdentityOwnerUsersByIdPasswordReset": policyUserManagement,
		"getIdentityOwnerUsersByIdAvatar":         policyOwnerAvatarRead,
		"getIdentityOwnerInvitations":             policyInvitations,
		"postIdentityOwnerInvitations":            policyInvitations,
		"postIdentityOwnerInvitationsByIdResend":  policyInvitations,
		"postIdentityOwnerInvitationsByIdRevoke":  policyInvitations,
		"getIdentityInvitationsValidate":          policyInvitationAcceptance,
		"postIdentityInvitationsAccept":           policyInvitationAcceptance,
		"postIdentityPasswordRecoveryRequest":     policyPasswordRecovery,
		"postIdentityPasswordRecoveryReset":       policyPasswordRecovery,
	}
	for op, p := range want {
		if got, ok := limits[op]; !ok || got != p {
			t.Errorf("limits[%s] = %+v, want %s", op, got, p.Name)
		}
	}
}
