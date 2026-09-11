// Package identity is the identity module: sign-in, sessions, users,
// invitations, account settings, authorization management, OIDC and SCIM,
// serving openapi/identity.yaml under /api/v1/identity/. Its Access is the
// contracts.Access every module's router evaluates x-vantigo-access with.
package identity

import (
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// authRateLimitMessage is the message of every identity rate-limit rejection
// (EA/AuthRateLimitingServiceCollectionExtensions.cs:15-27).
const authRateLimitMessage = "Too many authentication attempts. Please try again later."

// The nine named policies, each a fixed window per client IP with .NET's
// limit and window (EA/AuthRateLimitingServiceCollectionExtensions.cs,
// inventory §12).
var (
	policyLogin                = ratelimit.Policy{Name: "Login", Limit: 100, Window: time.Minute, Message: authRateLimitMessage}
	policyBootstrap            = ratelimit.Policy{Name: "Bootstrap", Limit: 20, Window: time.Minute, Message: authRateLimitMessage}
	policyInvitations          = ratelimit.Policy{Name: "Invitations", Limit: 30, Window: time.Minute, Message: authRateLimitMessage}
	policyInvitationAcceptance = ratelimit.Policy{Name: "InvitationAcceptance", Limit: 20, Window: time.Minute, Message: authRateLimitMessage}
	policyPasswordRecovery     = ratelimit.Policy{Name: "PasswordRecovery", Limit: 10, Window: 15 * time.Minute, Message: authRateLimitMessage}
	policyMfa                  = ratelimit.Policy{Name: "Mfa", Limit: 20, Window: 5 * time.Minute, Message: authRateLimitMessage}
	policyPasskeyLogin         = ratelimit.Policy{Name: "PasskeyLogin", Limit: 30, Window: 5 * time.Minute, Message: authRateLimitMessage}
	policyUserManagement       = ratelimit.Policy{Name: "UserManagement", Limit: 30, Window: time.Minute, Message: authRateLimitMessage}
	policyOwnerAvatarRead      = ratelimit.Policy{Name: "OwnerAvatarRead", Limit: 300, Window: time.Minute, Message: authRateLimitMessage}
)

// limits maps each rate-limited operationId to its policy. The router
// applies the limit before the access check, as .NET's limiter runs before
// authentication (HOST/Program.cs:120-136). Each area adds its operations
// as it implements them.
var limits = map[string]ratelimit.Policy{}

// Module is identity as a platform module: its contract mounted under
// /api/v1/identity/ with a as the access layer of every operation, and
// identity:manage in the permission catalog, as the .NET host registered it
// (HOST/Program.cs:203-206). a must also be the Deps.Access every other
// module is composed with (see NewAccess).
func Module(a *Access) module.Module {
	return module.Module{
		Name: "identity",
		Permissions: []contracts.Permission{{
			Key:         "identity:manage",
			Display:     "Manage identity",
			Description: "Full identity administration.",
			Sensitive:   true,
			Delegable:   false,
		}},
		Mount: func(d module.Deps) (http.Handler, error) { return mount(a, d) },
	}
}

// mount registers every contract operation on the platform router, which
// wraps each in its rate limit and access rule before the generated wrapper
// decodes it. It fails when the router reports a problem: an operation
// never registered, a rule that does not parse, a permission missing from
// the catalog, or a Limits entry naming no operation.
func mount(a *Access, d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:     d.Doc,
		Access:  a,
		Limiter: d.Limiter,
		Limits:  limits,
		Catalog: d.Catalog,
	})
	strict := gen.NewStrictHandlerWithOptions(newServer(a, d), nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(writeInvalidRequest),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: module.DecodeError(writeInvalidRequest),
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
