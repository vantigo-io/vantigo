using System.Security.Claims;
using System.Text.Json;

using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.Identity;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Authorization;

/// <summary>
/// Writes authorization audit records as part of the caller's current EF
/// transaction. Callers must invoke this before committing their mutation.
/// </summary>
public sealed class AuthorizationAuditWriter
{
    public async Task WriteAsync(
        AccountsDbContext dbContext,
        HttpContext httpContext,
        Guid? actorUserId,
        Guid? targetUserId,
        Guid? targetRoleId,
        string action,
        object before,
        object after,
        CancellationToken cancellationToken = default)
    {
        dbContext.AuthorizationAuditEvents.Add(new AuthorizationAuditEvent
        {
            ActorUserId = actorUserId,
            TargetUserId = targetUserId,
            TargetRoleId = targetRoleId,
            Action = action,
            Details = JsonSerializer.Serialize(new { action }),
            BeforeJson = JsonSerializer.Serialize(before),
            AfterJson = JsonSerializer.Serialize(after),
            CorrelationId = httpContext.TraceIdentifier,
            MfaAuthenticated = httpContext.User.Claims.Any(IsMfaClaim),
            OccurredAt = DateTimeOffset.UtcNow,
        });

        // This is deliberately part of the caller's transaction. A database
        // failure here must prevent the authorization mutation from committing.
        await dbContext.SaveChangesAsync(cancellationToken);
    }

    public async Task<UserAuthorizationSnapshot> CaptureUserAsync(
        AccountsDbContext dbContext,
        Guid userId,
        CancellationToken cancellationToken = default)
    {
        var roles = await dbContext.UserRoles.AsNoTracking()
            .Where(item => item.UserId == userId)
            .Join(dbContext.Roles.AsNoTracking(), assignment => assignment.RoleId, role => role.Id,
                (_, role) => new { role.Id, role.Name })
            .ToListAsync(cancellationToken);
        var roleIds = roles.Select(role => role.Id).ToArray();
        var user = await dbContext.Users.AsNoTracking()
            .Where(item => item.Id == userId)
            .Select(item => new { item.IsDisabled })
            .SingleOrDefaultAsync(cancellationToken);
        var permissions = await dbContext.RolePermissions.AsNoTracking()
            .Where(item => roleIds.Contains(item.RoleId))
            .Select(item => item.PermissionKey)
            .Distinct()
            .OrderBy(item => item)
            .ToArrayAsync(cancellationToken);
        return new UserAuthorizationSnapshot(
            userId,
            roles.Select(role => role.Name).OrderBy(role => role, StringComparer.Ordinal).ToArray(),
            roles.Any(role => role.Name == AuthRoles.Owner) ? ["*"] : permissions,
            user?.IsDisabled ?? false);
    }

    public async Task<RoleAuthorizationSnapshot?> CaptureRoleAsync(
        AccountsDbContext dbContext,
        Guid roleId,
        CancellationToken cancellationToken = default)
    {
        var role = await dbContext.Roles.AsNoTracking().SingleOrDefaultAsync(item => item.Id == roleId, cancellationToken);
        var metadata = await dbContext.RoleMetadata.AsNoTracking().SingleOrDefaultAsync(item => item.RoleId == roleId, cancellationToken);
        if (role is null || metadata is null) return null;
        var permissions = await dbContext.RolePermissions.AsNoTracking()
            .Where(item => item.RoleId == roleId)
            .Select(item => item.PermissionKey)
            .OrderBy(item => item)
            .ToArrayAsync(cancellationToken);
        return new RoleAuthorizationSnapshot(
            role.Id,
            role.Name,
            metadata.DisplayName,
            metadata.Description,
            metadata.IsSystem,
            metadata.IsBuiltIn,
            metadata.StewardUserId,
            permissions);
    }

    public async Task<DelegationAuthorizationSnapshot?> CaptureDelegationAsync(
        AccountsDbContext dbContext,
        Guid delegationId,
        CancellationToken cancellationToken = default)
    {
        var delegation = await dbContext.AuthorizationDelegations.AsNoTracking()
            .SingleOrDefaultAsync(item => item.Id == delegationId, cancellationToken);
        if (delegation is null) return null;
        var permissions = await dbContext.AuthorizationDelegationPermissions.AsNoTracking()
            .Where(item => item.DelegationId == delegationId)
            .Select(item => item.PermissionKey)
            .OrderBy(item => item)
            .ToArrayAsync(cancellationToken);
        var roles = await dbContext.AuthorizationDelegationRoles.AsNoTracking()
            .Where(item => item.DelegationId == delegationId)
            .Select(item => item.RoleId)
            .OrderBy(item => item)
            .ToArrayAsync(cancellationToken);
        return new DelegationAuthorizationSnapshot(
            delegation.Id,
            delegation.GranteeUserId,
            delegation.CreatedByUserId,
            delegation.ExpiresAt,
            delegation.RevokedAt,
            delegation.CanCreateRoles,
            permissions,
            roles);
    }

    private static bool IsMfaClaim(Claim claim) =>
        (claim.Type is "amr" or ClaimTypes.AuthenticationMethod) &&
        string.Equals(claim.Value, "mfa", StringComparison.OrdinalIgnoreCase);
}

public sealed record UserAuthorizationSnapshot(
    Guid UserId,
    IReadOnlyCollection<string?> Roles,
    IReadOnlyCollection<string> PermissionKeys,
    bool IsDisabled = false,
    bool IsDeleted = false);

public sealed record RoleAuthorizationSnapshot(
    Guid RoleId,
    string? Name,
    string DisplayName,
    string Description,
    bool IsSystem,
    bool IsBuiltIn,
    Guid? StewardUserId,
    IReadOnlyCollection<string> PermissionKeys);

public sealed record DelegationAuthorizationSnapshot(
    Guid Id,
    Guid GranteeUserId,
    Guid CreatedByUserId,
    DateTimeOffset? ExpiresAt,
    DateTimeOffset? RevokedAt,
    bool CanCreateRoles,
    IReadOnlyCollection<string> PermissionKeys,
    IReadOnlyCollection<Guid> StewardedRoleIds);