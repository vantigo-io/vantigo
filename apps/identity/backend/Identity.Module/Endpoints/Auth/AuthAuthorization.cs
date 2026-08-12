using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authorization;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Contracts.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Endpoints.Auth;

internal sealed class BusinessAccessRequirement : IAuthorizationRequirement;

internal sealed class ActiveAccountRequirement : IAuthorizationRequirement;

internal sealed class MfaAuthenticatedRequirement : IAuthorizationRequirement;

internal sealed class AuthorizationManagementRequirement : IAuthorizationRequirement;

/// <summary>
/// Checks the persistent account state on every authorization evaluation. This
/// prevents a stale cookie from retaining business access after an Owner disables
/// the account; claims alone are intentionally not authoritative here.
/// </summary>
internal sealed class ActiveAccountHandler(AccountsDbContext dbContext)
    : AuthorizationHandler<ActiveAccountRequirement>
{
    protected override async Task HandleRequirementAsync(
        AuthorizationHandlerContext context,
        ActiveAccountRequirement requirement)
    {
        if (context.User.Identity?.IsAuthenticated != true)
        {
            return;
        }

        var userIdValue = context.User.FindFirst(System.Security.Claims.ClaimTypes.NameIdentifier)?.Value;
        if (!Guid.TryParse(userIdValue, out var userId))
        {
            return;
        }

        if (await dbContext.Users.AsNoTracking()
                .AnyAsync(user => user.Id == userId && !user.IsDisabled))
        {
            context.Succeed(requirement);
        }
    }
}

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

/// <summary>
/// Management reads and mutations are available to Owners and to users with an
/// active Owner-created delegation. MFA is a separate requirement on the policy;
/// this handler only decides whether the caller has management authority.
/// </summary>
internal sealed class AuthorizationManagementHandler(AccountsDbContext dbContext, IPermissionCatalog catalog)
    : AuthorizationHandler<AuthorizationManagementRequirement>
{
    protected override async Task HandleRequirementAsync(
        AuthorizationHandlerContext context,
        AuthorizationManagementRequirement requirement)
    {
        if (context.User.Identity?.IsAuthenticated != true) return;
        var value = context.User.FindFirst(System.Security.Claims.ClaimTypes.NameIdentifier)?.Value;
        if (!Guid.TryParse(value, out var userId)) return;

        var ownerRoleIds = await dbContext.Roles.AsNoTracking()
            .Where(role => role.Name == AuthRoles.Owner)
            .Select(role => role.Id)
            .Take(2)
            .ToListAsync();
        if (ownerRoleIds.Count == 1 && await dbContext.UserRoles.AsNoTracking()
                .AnyAsync(assignment => assignment.UserId == userId && assignment.RoleId == ownerRoleIds[0]))
        {
            context.Succeed(requirement);
            return;
        }

        var activeDelegations = await dbContext.AuthorizationDelegations.AsNoTracking().Where(delegation =>
                delegation.GranteeUserId == userId && delegation.RevokedAt == null &&
                (delegation.ExpiresAt == null || delegation.ExpiresAt > DateTimeOffset.UtcNow))
            .ToListAsync();
        foreach (var delegation in activeDelegations)
        {
            var roleIds = await dbContext.AuthorizationDelegationRoles.AsNoTracking()
                .Where(item => item.DelegationId == delegation.Id)
                .Select(item => item.RoleId)
                .Distinct()
                .ToListAsync();
            var validRoleCount = await dbContext.Roles.AsNoTracking()
                .Join(dbContext.RoleMetadata.AsNoTracking(), role => role.Id, metadata => metadata.RoleId,
                    (role, metadata) => new { role.Id, metadata.IsSystem, metadata.IsBuiltIn })
                .CountAsync(item => roleIds.Contains(item.Id) && !item.IsSystem && !item.IsBuiltIn);
            if (validRoleCount != roleIds.Count)
                continue;

            var permissionKeys = await dbContext.AuthorizationDelegationPermissions.AsNoTracking()
                .Where(item => item.DelegationId == delegation.Id)
                .Select(item => item.PermissionKey)
                .Distinct()
                .ToListAsync();
            if (permissionKeys.All(key => catalog.Contains(key) && catalog.GetRequired(key).Delegable))
            {
                context.Succeed(requirement);
                return;
            }
        }
    }
}