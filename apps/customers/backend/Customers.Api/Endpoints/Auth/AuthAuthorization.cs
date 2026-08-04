using Microsoft.AspNetCore.Authorization;

namespace Vantigo.Customers.Api.Endpoints.Auth;

internal static class AuthPolicies
{
    internal const string Owner = "Owner";
    internal const string OwnerManagement = "OwnerManagement";
    internal const string Business = "Business";
}

internal sealed class BusinessAccessRequirement : IAuthorizationRequirement;

internal sealed class MfaAuthenticatedRequirement : IAuthorizationRequirement;

internal sealed class BusinessAccessHandler(IConfiguration configuration)
    : AuthorizationHandler<BusinessAccessRequirement>
{
    protected override Task HandleRequirementAsync(
        AuthorizationHandlerContext context,
        BusinessAccessRequirement requirement)
    {
        if (!context.User.Identity?.IsAuthenticated ?? true)
        {
            return Task.CompletedTask;
        }

        if (!context.User.IsInRole(AuthRoles.Owner) ||
            !configuration.GetValue<bool>("Authentication:Owners:RequireMfa"))
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

internal sealed class MfaAuthenticatedHandler(IConfiguration configuration)
    : AuthorizationHandler<MfaAuthenticatedRequirement>
{
    protected override Task HandleRequirementAsync(
        AuthorizationHandlerContext context,
        MfaAuthenticatedRequirement requirement)
    {
        if (!configuration.GetValue<bool>("Authentication:Owners:RequireMfa") ||
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