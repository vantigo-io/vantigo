using System.Security.Claims;

using Microsoft.AspNetCore.Http;
using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.Authorization;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public sealed class AccessGroupManagementService(
    AccountsDbContext dbContext,
    IPermissionCatalog catalog,
    AuthorizationMutationService authorizationMutations,
    AuthorizationAuditWriter auditWriter,
    IHttpContextAccessor httpContextAccessor)
{
    public Task<List<AccessGroup>> ListAsync(CancellationToken cancellationToken) =>
        dbContext.AccessGroups.AsNoTracking()
            .OrderBy(group => group.DisplayName)
            .ThenBy(group => group.Id)
            .ToListAsync(cancellationToken);

    public Task<AccessGroup?> GetAsync(Guid id, CancellationToken cancellationToken) =>
        dbContext.AccessGroups.SingleOrDefaultAsync(group => group.Id == id, cancellationToken);

    public async Task<AccessGroup> CreateAsync(
        string displayName,
        bool isActive,
        CancellationToken cancellationToken)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var now = DateTimeOffset.UtcNow;
        var group = new AccessGroup
        {
            DisplayName = displayName,
            Source = AccessGroupSource.Local,
            ExternalId = null,
            IsActive = isActive,
            CreatedAt = now,
            UpdatedAt = now,
            ConcurrencyStamp = NewConcurrencyStamp(),
        };
        dbContext.AccessGroups.Add(group);
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("access_group.created", null, group, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return group;
    }

    public async Task<AccessGroup?> UpdateAsync(
        Guid id,
        string displayName,
        bool isActive,
        string concurrencyStamp,
        CancellationToken cancellationToken)
    {
        var group = await dbContext.AccessGroups.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (group is null) return null;
        if (group.Source == AccessGroupSource.Scim)
            await RejectScimMutationAsync("access_group.update_rejected", group, null, cancellationToken);
        if (!string.Equals(group.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();
        var before = Snapshot(group);
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);

        group.DisplayName = displayName;
        group.IsActive = isActive;
        group.UpdatedAt = DateTimeOffset.UtcNow;
        group.ConcurrencyStamp = NewConcurrencyStamp();
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("access_group.updated", before, group, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return group;
    }

    public async Task<bool> DeleteAsync(Guid id, string concurrencyStamp, CancellationToken cancellationToken)
    {
        var group = await dbContext.AccessGroups.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (group is null) return false;
        if (group.Source == AccessGroupSource.Scim)
            await RejectScimMutationAsync("access_group.delete_rejected", group, null, cancellationToken);
        if (!string.Equals(group.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();
        var before = Snapshot(group);
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);

        dbContext.AccessGroups.Remove(group);
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("access_group.deleted", before, null, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return true;
    }

    public async Task<AccessGroup?> AddMemberAsync(Guid groupId, Guid userId, string concurrencyStamp, CancellationToken cancellationToken)
    {
        var group = await dbContext.AccessGroups.SingleOrDefaultAsync(item => item.Id == groupId, cancellationToken);
        if (group is null) return null;
        if (group.Source == AccessGroupSource.Scim)
            await RejectScimMutationAsync("access_group.member_add_rejected", group, userId, cancellationToken);
        if (group.Source != AccessGroupSource.Local) return group;
        if (!string.Equals(group.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();
        var before = Snapshot(group);
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        if (!await dbContext.Users.AnyAsync(user => user.Id == userId, cancellationToken))
            throw new AccessGroupValidationException("user_not_found", "The selected user does not exist.");

        var membership = await dbContext.AccessGroupMemberships.SingleOrDefaultAsync(
            item => item.GroupId == groupId && item.UserId == userId, cancellationToken);
        if (membership is null)
        {
            membership = new AccessGroupMembership
            {
                GroupId = groupId,
                UserId = userId,
            };
            dbContext.AccessGroupMemberships.Add(membership);
        }

        membership.Source = AccessGroupSource.Local;
        membership.IsUpstreamPresent = false;
        membership.Override = AccessGroupMembershipOverride.ForceMember;
        membership.UpdatedAt = DateTimeOffset.UtcNow;
        group.UpdatedAt = DateTimeOffset.UtcNow;
        group.ConcurrencyStamp = NewConcurrencyStamp();
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("access_group.member_added", before, new { Group = Snapshot(group), UserId = userId }, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return group;
    }

    public async Task<AccessGroup?> RemoveMemberAsync(Guid groupId, Guid userId, string concurrencyStamp, CancellationToken cancellationToken)
    {
        var group = await dbContext.AccessGroups.SingleOrDefaultAsync(item => item.Id == groupId, cancellationToken);
        if (group is null) return null;
        if (group.Source == AccessGroupSource.Scim)
            await RejectScimMutationAsync("access_group.member_remove_rejected", group, userId, cancellationToken);
        if (group.Source != AccessGroupSource.Local) return group;
        if (!string.Equals(group.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();
        var before = Snapshot(group);
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);

        var membership = await dbContext.AccessGroupMemberships.SingleOrDefaultAsync(
            item => item.GroupId == groupId && item.UserId == userId, cancellationToken);
        if (membership is not null)
            dbContext.AccessGroupMemberships.Remove(membership);
        group.UpdatedAt = DateTimeOffset.UtcNow;
        group.ConcurrencyStamp = NewConcurrencyStamp();
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("access_group.member_removed", before, new { Group = Snapshot(group), UserId = userId }, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return group;
    }

    public async Task<AccessGroup?> AddRoleMappingAsync(
        Guid groupId,
        Guid roleId,
        string concurrencyStamp,
        Guid? expectedScimConnectionId,
        CancellationToken cancellationToken)
    {
        var group = await dbContext.AccessGroups.SingleOrDefaultAsync(item => item.Id == groupId, cancellationToken);
        if (group is null) return null;
        ValidateGroupScope(group, expectedScimConnectionId);
        if (!string.Equals(group.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();
        var before = Snapshot(group);
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        await authorizationMutations.AcquireRoleMutationLockAsync(roleId, cancellationToken);

        var role = await dbContext.Roles.AsNoTracking().SingleOrDefaultAsync(item => item.Id == roleId, cancellationToken);
        if (role is null)
            throw new AccessGroupValidationException("role_not_found", "The selected role does not exist.");

        var metadata = await dbContext.RoleMetadata.AsNoTracking().SingleOrDefaultAsync(
            item => item.RoleId == roleId, cancellationToken);
        if (metadata is null || metadata.IsSystem || metadata.IsBuiltIn ||
            string.Equals(role.Name, AuthRoles.Owner, StringComparison.Ordinal))
        {
            throw new AccessGroupValidationException(
                "protected_role",
                "Owner, system, and built-in roles cannot be mapped to access groups.");
        }

        var rolePermissionKeys = await dbContext.RolePermissions.AsNoTracking()
            .Where(item => item.RoleId == roleId)
            .Select(item => item.PermissionKey)
            .ToListAsync(cancellationToken);
        if (rolePermissionKeys.Any(IsAuthorizationManagementPermission))
        {
            throw new AccessGroupValidationException(
                "protected_role",
                "Roles carrying authorization-management permissions cannot be mapped to access groups.");
        }

        var mapping = await dbContext.AccessGroupRoleMappings.SingleOrDefaultAsync(
            item => item.GroupId == groupId && item.RoleId == roleId, cancellationToken);
        if (mapping is null)
        {
            dbContext.AccessGroupRoleMappings.Add(new AccessGroupRoleMapping
            {
                GroupId = groupId,
                RoleId = roleId,
                Source = group.Source,
                CreatedAt = DateTimeOffset.UtcNow,
            });
        }

        group.UpdatedAt = DateTimeOffset.UtcNow;
        group.ConcurrencyStamp = NewConcurrencyStamp();
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("access_group.role_mapped", before, new { Group = Snapshot(group), RoleId = roleId }, cancellationToken);
        await transaction.CommitAsync(cancellationToken);

        return group;
    }

    public async Task<AccessGroup?> RemoveRoleMappingAsync(
        Guid groupId,
        Guid roleId,
        string concurrencyStamp,
        Guid? expectedScimConnectionId,
        CancellationToken cancellationToken)
    {
        var group = await dbContext.AccessGroups.SingleOrDefaultAsync(item => item.Id == groupId, cancellationToken);
        if (group is null) return null;
        ValidateGroupScope(group, expectedScimConnectionId);
        if (!string.Equals(group.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();
        var before = Snapshot(group);
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        await authorizationMutations.AcquireRoleMutationLockAsync(roleId, cancellationToken);

        var mapping = await dbContext.AccessGroupRoleMappings.SingleOrDefaultAsync(
            item => item.GroupId == groupId && item.RoleId == roleId, cancellationToken);
        if (mapping is not null)
            dbContext.AccessGroupRoleMappings.Remove(mapping);
        group.UpdatedAt = DateTimeOffset.UtcNow;
        group.ConcurrencyStamp = NewConcurrencyStamp();
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("access_group.role_unmapped", before, new { Group = Snapshot(group), RoleId = roleId }, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return group;
    }

    public async Task<AccessGroupDetails> DetailsAsync(AccessGroup group, CancellationToken cancellationToken)
    {
        var memberIds = await dbContext.AccessGroupMemberships.AsNoTracking()
            .Where(item => item.GroupId == group.Id &&
                (item.Override == AccessGroupMembershipOverride.ForceMember ||
                 item.Override == null && item.IsUpstreamPresent))
            .Select(item => item.UserId)
            .OrderBy(id => id)
            .ToArrayAsync(cancellationToken);
        var roleIds = await dbContext.AccessGroupRoleMappings.AsNoTracking()
            .Where(item => item.GroupId == group.Id)
            .Select(item => item.RoleId)
            .OrderBy(id => id)
            .ToArrayAsync(cancellationToken);
        return new AccessGroupDetails(group, memberIds, roleIds);
    }

    private bool IsAuthorizationManagementPermission(string permissionKey) =>
        catalog.Contains(permissionKey) &&
        !catalog.GetRequired(permissionKey).Delegable;

    private static string NewConcurrencyStamp() => Guid.NewGuid().ToString("N");

    private static void ValidateGroupScope(AccessGroup group, Guid? expectedScimConnectionId)
    {
        if (group.Source == AccessGroupSource.Scim)
        {
            if (!group.ScimConnectionId.HasValue)
                throw new AccessGroupValidationException("scim_scope_invalid", "The SCIM group is not bound to a SCIM connection.");
            if (expectedScimConnectionId != group.ScimConnectionId)
                throw new AccessGroupValidationException("scim_scope_conflict", "The SCIM group belongs to a different SCIM connection.");
        }
        else if (expectedScimConnectionId.HasValue)
        {
            throw new AccessGroupValidationException("scim_scope_conflict", "A local group cannot be scoped to a SCIM connection.");
        }
    }

    private async Task RejectScimMutationAsync(
        string action,
        AccessGroup group,
        Guid? userId,
        CancellationToken cancellationToken)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        await AuditAsync(action, Snapshot(group), new
        {
            GroupId = group.Id,
            group.ScimConnectionId,
            UserId = userId,
            Rejected = true,
            Reason = "scim_group_content_is_protocol_owned",
        }, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        throw new AccessGroupValidationException(
            "scim_group_managed",
            "SCIM group content is managed by the SCIM protocol and cannot be changed through the Owner group API.",
            StatusCodes.Status409Conflict);
    }

    private async Task AuditAsync(string action, object? before, object? after, CancellationToken cancellationToken)
    {
        var context = httpContextAccessor.HttpContext ?? throw new InvalidOperationException("An HTTP context is required for an identity audit event.");
        var actor = Guid.TryParse(context.User.FindFirstValue(ClaimTypes.NameIdentifier), out var actorId)
            ? actorId
            : (Guid?)null;
        await auditWriter.WriteAsync(dbContext, context, actor, null, null, action,
            before ?? new { }, after ?? new { }, cancellationToken);
    }

    private static object Snapshot(AccessGroup group) => new
    {
        group.Id,
        group.DisplayName,
        Source = group.Source.ToString(),
        group.IsActive,
        group.ScimConnectionId,
        group.CreatedAt,
        group.UpdatedAt,
        group.ConcurrencyStamp,
    };
}

public sealed record AccessGroupDetails(
    AccessGroup Group,
    IReadOnlyCollection<Guid> MemberUserIds,
    IReadOnlyCollection<Guid> RoleIds);

public sealed class AccessGroupValidationException(
    string code,
    string message,
    int statusCode = StatusCodes.Status400BadRequest) : Exception(message)
{
    public string Code { get; } = code;
    public int StatusCode { get; } = statusCode;
}