using System.Security.Claims;

using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Authorization.Policy;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Endpoints.Auth;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Authorization;

public sealed class PermissionRequirement(string permissionKey) : IAuthorizationRequirement
{
    public string PermissionKey { get; } = permissionKey;
}

public sealed class PermissionPolicyProvider(
    IOptions<AuthorizationOptions> options,
    IPermissionCatalog catalog) : DefaultAuthorizationPolicyProvider(options)
{
    public override Task<AuthorizationPolicy?> GetPolicyAsync(string policyName)
    {
        const string prefix = "permission:";
        if (!policyName.StartsWith(prefix, StringComparison.Ordinal))
            return base.GetPolicyAsync(policyName);

        var key = policyName[prefix.Length..];
        if (!catalog.Contains(key))
            throw new InvalidOperationException($"The permission policy '{key}' is not registered in the startup catalog.");
        return Task.FromResult<AuthorizationPolicy?>(new AuthorizationPolicyBuilder()
            .AddRequirements(new PermissionRequirement(key), new ActiveAccountRequirement(), new BusinessAccessRequirement())
            .Build());
    }
}

public sealed class PermissionAuthorizationHandler(AccountsDbContext dbContext, IPermissionCatalog catalog)
    : AuthorizationHandler<PermissionRequirement>
{
    protected override async Task HandleRequirementAsync(
        AuthorizationHandlerContext context,
        PermissionRequirement requirement)
    {
        if (!catalog.Contains(requirement.PermissionKey) || context.User.Identity?.IsAuthenticated != true)
            return;

        var userIdValue = context.User.FindFirstValue(ClaimTypes.NameIdentifier);
        if (!Guid.TryParse(userIdValue, out var userId)) return;
        var activeTenantValue = context.User.FindFirstValue(TenantMembershipService.ActiveTenantClaim);
        var hasActiveTenant = Guid.TryParse(activeTenantValue, out var activeTenantId);

        var user = await dbContext.Users.AsNoTracking()
            .Where(item => item.Id == userId)
            .Select(item => new { item.Id, item.IsDisabled, item.LockoutEnd })
            .SingleOrDefaultAsync();
        if (user is null || user.IsDisabled || AuthAccountState.IsLockedOut(user.LockoutEnd, DateTimeOffset.UtcNow))
            return;

        var roles = await dbContext.UserRoles.AsNoTracking()
            .Where(item => item.UserId == userId)
            // Global role assignments remain valid; tenant assignments must match the active tenant.
            .Where(item => !hasActiveTenant || item.TenantId == null || item.TenantId == activeTenantId)
            .Join(dbContext.Roles.AsNoTracking(), assignment => assignment.RoleId, role => role.Id,
                (assignment, role) => role.Name)
            .ToListAsync();
        if (roles.Any(role => string.Equals(role, AuthRoles.Owner, StringComparison.Ordinal)))
        {
            context.Succeed(requirement);
            return;
        }

        var roleIds = await dbContext.UserRoles.AsNoTracking()
            .Where(item => item.UserId == userId)
            .Select(item => item.RoleId)
            .ToArrayAsync();
        var groupRoleIds = await dbContext.AccessGroupMemberships.AsNoTracking()
            .Where(membership => membership.UserId == userId &&
                (!hasActiveTenant || membership.TenantId == null || membership.TenantId == activeTenantId) &&
                (membership.Override == AccessGroupMembershipOverride.ForceMember ||
                 membership.Override == null && membership.IsUpstreamPresent))
            .Join(dbContext.AccessGroups.AsNoTracking().Where(group => group.IsActive),
                membership => membership.GroupId, group => group.Id, (membership, _) => membership.GroupId)
            .Join(dbContext.AccessGroups.AsNoTracking(), groupId => groupId, group => group.Id,
                (groupId, group) => new { groupId, group.Source })
            .Join(dbContext.AccessGroupRoleMappings.AsNoTracking(), item => item.groupId, mapping => mapping.GroupId,
                (item, mapping) => new { mapping.RoleId, mapping.TenantId, MappingSource = mapping.Source, GroupSource = item.Source })
            // Group role mappings use the same null=system fallback as user roles.
            .Where(item => !hasActiveTenant || item.TenantId == null || item.TenantId == activeTenantId)
            .Where(item => item.MappingSource == item.GroupSource)
            .Select(item => item.RoleId)
            .ToArrayAsync();
        var effectiveRoleIds = roleIds.Concat(groupRoleIds).Distinct().ToArray();
        if (await dbContext.RolePermissions.AsNoTracking()
                .AnyAsync(item => effectiveRoleIds.Contains(item.RoleId) && item.PermissionKey == requirement.PermissionKey))
            context.Succeed(requirement);
    }
}