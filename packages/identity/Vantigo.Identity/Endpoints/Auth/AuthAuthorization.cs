using Microsoft.AspNetCore.Authorization;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Identity.Endpoints.Auth;

public static class AuthPolicies
{
    public const string Owner = "Owner";
    public const string OwnerManagement = "OwnerManagement";
    public const string Business = "Business";
}

internal sealed class BusinessAccessRequirement : IAuthorizationRequirement;

internal sealed class MfaAuthenticatedRequirement : IAuthorizationRequirement;

internal sealed class BusinessAccessHandler(IOptions<VantigoAuthenticationOptions> options)
    : AuthorizationHandler<BusinessAccessRequirement>
{
    private readonly VantigoAuthenticationOptions authentication = options.Value;

    protected override Task HandleRequirementAsync(
        AuthorizationHandlerContext context,
        BusinessAccessRequirement requirement)
    {
        if (!context.User.Identity?.IsAuthenticated ?? true)
        {
            return Task.CompletedTask;
        }

        if (!context.User.IsInRole(AuthRoles.Owner) ||
            !authentication.Owners.RequireMfa)
        {
            context.Succeed(requirement);
        }
        else if (context.User.Claims.Any(IsMfaClaim))
        {
            context.Succeed(requirement);
        }

        return Task.CompletedTask;
    }

    private static bool IsMfaClaim(System.Security.Claims.Claim claim) =>
        (claim.Type == "amr" || claim.Type == System.Security.Claims.ClaimTypes.AuthenticationMethod) &&
        string.Equals(claim.Value, "mfa", StringComparison.OrdinalIgnoreCase);
}

internal sealed class MfaAuthenticatedHandler(IOptions<VantigoAuthenticationOptions> options)
    : AuthorizationHandler<MfaAuthenticatedRequirement>
{
    private readonly VantigoAuthenticationOptions authentication = options.Value;

    protected override Task HandleRequirementAsync(
        AuthorizationHandlerContext context,
        MfaAuthenticatedRequirement requirement)
    {
        if (!authentication.Owners.RequireMfa ||
            context.User.Claims.Any(IsMfaClaim))
        {
            context.Succeed(requirement);
        }

        return Task.CompletedTask;
    }

    private static bool IsMfaClaim(System.Security.Claims.Claim claim) =>
        (claim.Type == "amr" || claim.Type == System.Security.Claims.ClaimTypes.AuthenticationMethod) &&
        string.Equals(claim.Value, "mfa", StringComparison.OrdinalIgnoreCase);
}