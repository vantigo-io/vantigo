using System.Buffers.Binary;
using System.Security.Claims;
using System.Security.Cryptography;

using Microsoft.AspNetCore.Http;
using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Authorization;

/// <summary>
/// Single guard for authorization mutations. Delegation is authority to manage
/// roles only; it is never treated as a permission grant to business data.
/// </summary>
public sealed class AuthorizationMutationService(AccountsDbContext dbContext, IPermissionCatalog catalog)
{
    public async Task AcquireRoleMutationLockAsync(Guid roleId, CancellationToken cancellationToken)
    {
        await dbContext.Database.ExecuteSqlRawAsync(
            "SELECT pg_advisory_xact_lock(CAST({0} AS bigint))",
            [(object)RoleMutationLockKey(roleId)], cancellationToken);
    }

    public static long RoleMutationLockKey(Guid roleId)
    {
        var digest = SHA256.HashData(roleId.ToByteArray());
        return BinaryPrimitives.ReadInt64BigEndian(digest.AsSpan(0, sizeof(long)));
    }

    public async Task<(IResult? Error, AuthorizationScope? Scope)> AuthorizeRoleCreateAsync(
        Guid actorId,
        IReadOnlyCollection<string> keys,
        Guid? requestedDelegationId,
        CancellationToken cancellationToken)
    {
        if (await IsOwnerAsync(actorId, cancellationToken))
            return requestedDelegationId.HasValue
                ? (Forbidden("delegation_not_allowed", "Owners cannot select a delegation."), null)
                : (null, null);

        if (!requestedDelegationId.HasValue)
            return (Forbidden("delegation_required", "A delegation must be selected."), null);

        var scope = (await ActiveScopesAsync(actorId, cancellationToken))
            .SingleOrDefault(item => item.DelegationId == requestedDelegationId.Value);
        if (scope is null || !scope.CanCreateRoles)
            return (Forbidden("role_create_denied", "The selected delegation cannot create this role."), null);
        return KeysWithinScope(scope, keys)
            ? (null, scope)
            : (Forbidden("role_boundary_exceeded", "The role permissions exceed the selected delegation boundary."), null);
    }

    public async Task<IResult?> AuthorizeRoleEditAsync(
        Guid actorId,
        Guid roleId,
        string roleName,
        IReadOnlyCollection<string> keys,
        CancellationToken cancellationToken)
    {
        await AcquireRoleMutationLockAsync(roleId, cancellationToken);
        var currentPermissionKeys = await dbContext.RolePermissions.AsNoTracking()
            .Where(item => item.RoleId == roleId).Select(item => item.PermissionKey).ToArrayAsync(cancellationToken);
        var effectivePermissionKeys = currentPermissionKeys.Concat(keys).Distinct(StringComparer.Ordinal).ToArray();
        if (await dbContext.AccessGroupRoleMappings.AsNoTracking()
                .AnyAsync(item => item.RoleId == roleId, cancellationToken) &&
            (string.Equals(roleName, AuthRoles.Owner, StringComparison.Ordinal) ||
             effectivePermissionKeys.Any(IsProtectedPermission)))
        {
            return Forbidden("role_group_protected_permission",
                "A role mapped to an access group cannot receive Owner or protected authorization-management permissions.");
        }

        if (await IsOwnerAsync(actorId, cancellationToken)) return null;
        var role = await dbContext.RoleMetadata.AsNoTracking().SingleOrDefaultAsync(item => item.RoleId == roleId, cancellationToken);
        var scopes = await ActiveScopesAsync(actorId, cancellationToken);
        if (role is null || role.IsSystem || role.IsBuiltIn || !scopes.Any(scope =>
                scope.StewardedRoleIds.Contains(roleId) && KeysWithinScope(scope, effectivePermissionKeys)))
            return Forbidden("role_edit_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be changed.");
        return null;
    }

    public async Task<IResult?> AuthorizeRoleDeleteAsync(Guid actorId, Guid roleId, CancellationToken cancellationToken)
    {
        await AcquireRoleMutationLockAsync(roleId, cancellationToken);
        if (await dbContext.AccessGroupRoleMappings.AsNoTracking()
                .AnyAsync(item => item.RoleId == roleId, cancellationToken))
        {
            return Forbidden("role_mapped", "Roles mapped to access groups cannot be deleted; remove every mapping first.");
        }

        if (await IsOwnerAsync(actorId, cancellationToken)) return null;
        var role = await dbContext.RoleMetadata.AsNoTracking().SingleOrDefaultAsync(item => item.RoleId == roleId, cancellationToken);
        var permissionKeys = await dbContext.RolePermissions.AsNoTracking()
            .Where(item => item.RoleId == roleId).Select(item => item.PermissionKey).ToArrayAsync(cancellationToken);
        var scopes = await ActiveScopesAsync(actorId, cancellationToken);
        return role is not null && !role.IsSystem && !role.IsBuiltIn && scopes.Any(scope =>
                scope.StewardedRoleIds.Contains(roleId) && KeysWithinScope(scope, permissionKeys))
            ? null
            : Forbidden("role_delete_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be deleted.");
    }

    public async Task<AssignmentAuthorizationDecision> AuthorizeAssignmentAsync(
        Guid actorId,
        Guid targetUserId,
        IReadOnlyCollection<Guid> existingCustomRoleIds,
        IReadOnlyCollection<Guid> requestedCustomRoleIds,
        Guid? requestedDelegationId,
        CancellationToken cancellationToken)
    {
        if (actorId == targetUserId)
            return new(Forbidden("self_change", "A delegate cannot change their own access."), []);
        if (await IsOwnerAsync(actorId, cancellationToken))
        {
            if (requestedDelegationId.HasValue)
                return new(Forbidden("delegation_not_allowed", "Owners cannot select a delegation."), []);
            return new(null, existingCustomRoleIds.Except(requestedCustomRoleIds).ToArray());
        }
        var targetOwner = await IsOwnerAsync(targetUserId, cancellationToken);
        if (targetOwner || await HasActiveDelegationAsync(targetUserId, cancellationToken))
            return new(Forbidden("assignment_target_denied", "Delegates may assign only ordinary users."), []);
        var scopes = await ActiveScopesAsync(actorId, cancellationToken);
        if (!requestedDelegationId.HasValue)
            return new(Forbidden("delegation_required", "A delegation must be selected."), []);
        var scope = scopes.SingleOrDefault(item => item.DelegationId == requestedDelegationId.Value);
        if (scope is null)
            return new(Forbidden("delegation_invalid", "The selected delegation is not active and valid."), []);

        var roles = await dbContext.RoleMetadata.AsNoTracking()
            .Where(item => requestedCustomRoleIds.Contains(item.RoleId)).ToListAsync(cancellationToken);
        if (roles.Count != requestedCustomRoleIds.Count || roles.Any(item => item.IsSystem || item.IsBuiltIn))
            return new(Forbidden("system_role_denied", "System and built-in roles cannot be assigned by delegates."), []);

        var existing = existingCustomRoleIds.ToHashSet();
        var additions = requestedCustomRoleIds.Where(roleId => !existing.Contains(roleId));
        if (additions.Any(roleId => !scope.AssignableRoleIds.Contains(roleId)))
            return new(Forbidden("assignment_denied", "The requested role is outside the selected delegation scope."), []);

        // An omitted role is removed only when it is individually manageable. Roles
        // outside every scope are returned to the endpoint for preservation.
        var removals = existingCustomRoleIds
            .Except(requestedCustomRoleIds)
            .Where(scope.AssignableRoleIds.Contains)
            .ToArray();
        return new(null, removals);
    }

    public async Task<bool> IsOwnerAsync(Guid userId, CancellationToken cancellationToken) =>
        await dbContext.UserRoles.AsNoTracking().Join(dbContext.Roles.AsNoTracking(), item => item.RoleId, role => role.Id,
            (_, role) => role).AnyAsync(role => role.Name == AuthRoles.Owner && dbContext.UserRoles.Any(assignment => assignment.UserId == userId && assignment.RoleId == role.Id), cancellationToken);

    public async Task<bool> CanManageDelegationsAsync(Guid actorId, CancellationToken cancellationToken) =>
        await IsOwnerAsync(actorId, cancellationToken);

    public async Task<bool> CanManageAuthorizationAsync(Guid actorId, CancellationToken cancellationToken) =>
        await IsOwnerAsync(actorId, cancellationToken) || (await ActiveScopesAsync(actorId, cancellationToken)).Count > 0;

    public async Task<bool> CanTargetAssignmentAsync(
        Guid actorId,
        Guid targetUserId,
        CancellationToken cancellationToken) =>
        await IsOwnerAsync(actorId, cancellationToken) ||
        !await IsOwnerAsync(targetUserId, cancellationToken) &&
        !await HasActiveDelegationAsync(targetUserId, cancellationToken);

    public async Task<IReadOnlyCollection<AuthorizationScope>> ActiveScopesAsync(
        Guid userId,
        CancellationToken cancellationToken)
    {
        if (await IsOwnerAsync(userId, cancellationToken)) return [];
        var delegations = await dbContext.AuthorizationDelegations.AsNoTracking()
            .Where(item => item.GranteeUserId == userId && item.RevokedAt == null &&
                (item.ExpiresAt == null || item.ExpiresAt > DateTimeOffset.UtcNow))
            .ToListAsync(cancellationToken);
        if (delegations.Count == 0) return [];

        // Roles, permission keys, and role validity are batched over all active
        // delegations, so evaluating N delegations stays a fixed number of
        // queries — this runs on the hot path of every authorized request.
        var delegationIds = delegations.Select(delegation => delegation.Id).ToArray();
        var rolesByDelegation = (await dbContext.AuthorizationDelegationRoles.AsNoTracking()
                .Where(item => delegationIds.Contains(item.DelegationId))
                .Select(item => new { item.DelegationId, item.RoleId })
                .ToListAsync(cancellationToken))
            .GroupBy(item => item.DelegationId)
            .ToDictionary(byDelegation => byDelegation.Key, byDelegation => byDelegation.Select(item => item.RoleId).ToArray());
        var keysByDelegation = (await dbContext.AuthorizationDelegationPermissions.AsNoTracking()
                .Where(item => delegationIds.Contains(item.DelegationId))
                .Select(item => new { item.DelegationId, item.PermissionKey })
                .ToListAsync(cancellationToken))
            .GroupBy(item => item.DelegationId)
            .ToDictionary(byDelegation => byDelegation.Key, byDelegation => byDelegation.Select(item => item.PermissionKey).ToArray());
        var referencedRoleIds = rolesByDelegation.Values.SelectMany(roleIds => roleIds).Distinct().ToArray();
        var validRoleIds = (await dbContext.Roles.AsNoTracking()
                .Join(dbContext.RoleMetadata.AsNoTracking(), role => role.Id, metadata => metadata.RoleId,
                    (role, metadata) => new { role.Id, metadata.IsSystem, metadata.IsBuiltIn })
                .Where(item => referencedRoleIds.Contains(item.Id) && !item.IsSystem && !item.IsBuiltIn)
                .Select(item => item.Id)
                .ToListAsync(cancellationToken))
            .ToHashSet();

        var scopes = new List<AuthorizationScope>();
        foreach (var delegation in delegations)
        {
            var roleIds = rolesByDelegation.GetValueOrDefault(delegation.Id, []);
            var keys = keysByDelegation.GetValueOrDefault(delegation.Id, []);
            if (!roleIds.All(validRoleIds.Contains)) continue;
            if (!keys.All(key => catalog.Contains(key) && catalog.GetRequired(key).Delegable)) continue;
            scopes.Add(new AuthorizationScope(delegation.Id, delegation.CanCreateRoles, keys, roleIds, []));
        }

        if (scopes.Count == 0) return scopes;

        // Assignable roles are likewise resolved for every scope at once.
        var stewardedRoleIds = scopes.SelectMany(scope => scope.StewardedRoleIds).Distinct().ToArray();
        var eligibleRoleIds = (await dbContext.RoleMetadata.AsNoTracking()
                .Where(item => !item.IsSystem && !item.IsBuiltIn && stewardedRoleIds.Contains(item.RoleId))
                .Select(item => item.RoleId)
                .ToListAsync(cancellationToken))
            .ToHashSet();
        var permissionsByRole = (await dbContext.RolePermissions.AsNoTracking()
                .Where(item => eligibleRoleIds.Contains(item.RoleId))
                .ToListAsync(cancellationToken))
            .GroupBy(item => item.RoleId)
            .ToDictionary(byRole => byRole.Key, byRole => byRole.Select(item => item.PermissionKey).ToArray());

        return scopes.Select(scope => scope with
        {
            AssignableRoleIds = scope.StewardedRoleIds.Distinct()
                .Where(eligibleRoleIds.Contains)
                .Where(roleId => !permissionsByRole.TryGetValue(roleId, out var keys) || KeysWithinScope(scope, keys))
                .ToArray(),
        }).ToArray();
    }

    public async Task<bool> HasActiveDelegationAsync(Guid userId, CancellationToken cancellationToken) =>
        (await ActiveScopesAsync(userId, cancellationToken)).Count > 0;

    public async Task<HashSet<Guid>> AssignableRoleIdsAsync(Guid actorId, CancellationToken cancellationToken)
    {
        if (await IsOwnerAsync(actorId, cancellationToken))
            return (await dbContext.RoleMetadata.AsNoTracking().Where(item => !item.IsSystem && !item.IsBuiltIn)
                .Select(item => item.RoleId).ToListAsync(cancellationToken)).ToHashSet();
        return (await ActiveScopesAsync(actorId, cancellationToken)).SelectMany(scope => scope.AssignableRoleIds).ToHashSet();
    }

    public async Task<bool> CanInspectTargetAsync(Guid actorId, Guid targetUserId, CancellationToken cancellationToken)
    {
        if (actorId == targetUserId) return false;
        if (await IsOwnerAsync(actorId, cancellationToken)) return true;
        if (await IsOwnerAsync(targetUserId, cancellationToken) || await HasActiveDelegationAsync(targetUserId, cancellationToken)) return false;
        var allowedScopes = await ActiveScopesAsync(actorId, cancellationToken);
        var targetRoleIds = await dbContext.UserRoles.AsNoTracking().Where(item => item.UserId == targetUserId)
            .Join(dbContext.RoleMetadata.AsNoTracking(), item => item.RoleId, metadata => metadata.RoleId,
                (_, metadata) => metadata).Where(metadata => !metadata.IsSystem && !metadata.IsBuiltIn)
            .Select(metadata => metadata.RoleId).ToListAsync(cancellationToken);
        return allowedScopes.Any(scope => targetRoleIds.All(scope.AssignableRoleIds.Contains));
    }

    public async Task<Guid?> ActiveDelegationIdAsync(Guid granteeId, CancellationToken cancellationToken) =>
        (await ActiveScopesAsync(granteeId, cancellationToken)).Select(scope => (Guid?)scope.DelegationId).FirstOrDefault();

    private bool KeysWithinScope(AuthorizationScope scope, IReadOnlyCollection<string> keys) =>
        keys.All(key => catalog.Contains(key) && catalog.GetRequired(key).Delegable &&
            scope.GrantablePermissionKeys.Contains(key, StringComparer.Ordinal));

    private bool IsProtectedPermission(string key) =>
        catalog.Contains(key) && !catalog.GetRequired(key).Delegable;

    private async Task<IReadOnlyCollection<Guid>> AssignableRoleIdsForScopeAsync(
        AuthorizationScope scope, CancellationToken cancellationToken)
    {
        var roleIds = await dbContext.RoleMetadata.AsNoTracking()
            .Where(item => !item.IsSystem && !item.IsBuiltIn && scope.StewardedRoleIds.Contains(item.RoleId))
            .Select(item => item.RoleId).ToListAsync(cancellationToken);
        var permissions = await dbContext.RolePermissions.AsNoTracking()
            .Where(item => roleIds.Contains(item.RoleId)).ToListAsync(cancellationToken);
        var permissionsByRole = permissions.GroupBy(item => item.RoleId)
            .ToDictionary(group => group.Key, group => group.Select(item => item.PermissionKey).ToArray());
        return roleIds.Where(roleId => !permissionsByRole.TryGetValue(roleId, out var keys) || KeysWithinScope(scope, keys))
            .ToArray();
    }

    private async Task<bool> IsValidDelegationAsync(Guid delegationId, CancellationToken cancellationToken)
    {
        var roleIds = await dbContext.AuthorizationDelegationRoles.AsNoTracking()
            .Where(item => item.DelegationId == delegationId).Select(item => item.RoleId).Distinct().ToListAsync(cancellationToken);
        var validRoleCount = await dbContext.Roles.AsNoTracking()
            .Join(dbContext.RoleMetadata.AsNoTracking(), role => role.Id, metadata => metadata.RoleId,
                (role, metadata) => new { role.Id, metadata.IsSystem, metadata.IsBuiltIn })
            .CountAsync(item => roleIds.Contains(item.Id) && !item.IsSystem && !item.IsBuiltIn, cancellationToken);
        if (validRoleCount != roleIds.Count) return false;

        var permissionKeys = await dbContext.AuthorizationDelegationPermissions.AsNoTracking()
            .Where(item => item.DelegationId == delegationId).Select(item => item.PermissionKey).Distinct().ToListAsync(cancellationToken);
        return permissionKeys.All(key => catalog.Contains(key) && catalog.GetRequired(key).Delegable);
    }

    private static IResult Forbidden(string code, string message) => TypedResults.Json(new { code, message }, statusCode: StatusCodes.Status403Forbidden);

}

public sealed record AuthorizationScope(
    Guid DelegationId,
    bool CanCreateRoles,
    IReadOnlyCollection<string> GrantablePermissionKeys,
    IReadOnlyCollection<Guid> StewardedRoleIds,
    IReadOnlyCollection<Guid> AssignableRoleIds);

public sealed record AssignmentAuthorizationDecision(
    IResult? Error,
    IReadOnlyCollection<Guid> RemovableCustomRoleIds);