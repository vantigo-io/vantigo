using System.Security.Claims;

using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;

using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Identity;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Endpoints.Auth;

internal static class AuthorizationManagementEndpoints
{
    internal static void MapAuthorizationManagementEndpoints(IEndpointRouteBuilder app)
    {
        var access = app.MapGroup("/api/v1/identity/access")
            .WithTags("Authorization");
        access.AddEndpointFilter(async (context, next) =>
        {
            try
            {
                return await next(context);
            }
            catch (Exception exception) when (AuthorizationConflict.IsExpected(exception))
            {
                return TypedResults.Conflict(new
                {
                    code = "authorization_conflict",
                    message = "The authorization state changed concurrently.",
                });
            }
        });

        access.MapGet("/catalog", Catalog).RequireAuthorization(AuthPolicies.AuthorizationManagement);
        access.MapGet("/roles", ListRoles).RequireAuthorization(AuthPolicies.AuthorizationManagement);
        access.MapPost("/roles", CreateRole).RequireAuthorization(AuthPolicies.AuthorizationManagement);
        access.MapPut("/roles/{id:guid}", UpdateRole).RequireAuthorization(AuthPolicies.AuthorizationManagement);
        access.MapDelete("/roles/{id:guid}", DeleteRole).RequireAuthorization(AuthPolicies.AuthorizationManagement);
        access.MapPut("/roles/{id:guid}/permissions", ReplacePermissions).RequireAuthorization(AuthPolicies.AuthorizationManagement);
        access.MapPut("/users/{id:guid}/roles", AssignRoles).RequireAuthorization(AuthPolicies.AuthorizationManagement);
        access.MapGet("/users", ListAssignableUsers).RequireAuthorization(AuthPolicies.AuthorizationManagement);
        access.MapGet("/users/{id:guid}", EffectiveAccess).RequireAuthorization(AuthPolicies.AuthorizationManagement);
        access.MapGet("/audit", Audit).RequireAuthorization(AuthPolicies.OwnerManagement);

        var owner = access.MapGroup("");
        owner.MapGet("/delegations", ListDelegations).RequireAuthorization(AuthPolicies.OwnerManagement);
        owner.MapPost("/delegations", CreateDelegation).RequireAuthorization(AuthPolicies.OwnerManagement);
        owner.MapPut("/delegations/{id:guid}", UpdateDelegation).RequireAuthorization(AuthPolicies.OwnerManagement);
        owner.MapPost("/delegations/{id:guid}/revoke", RevokeDelegation).RequireAuthorization(AuthPolicies.OwnerManagement);

        IdentityControlPlaneEndpoints.MapIdentityControlPlaneEndpoints(app);

        app.MapGet("/api/v1/identity/access/me", EffectiveAccessForCurrentUser)
            .RequireAuthorization();
    }

    private static IResult Catalog(IPermissionCatalog catalog) => TypedResults.Ok(catalog.Permissions);

    private static async Task<IResult> ListRoles(
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        // Keep the authorization decision independent from this projection. A
        // delegated request must never turn malformed role metadata or a nested
        // collection projection into a 500 after policy evaluation has passed.
        var roles = await dbContext.Roles.AsNoTracking()
            .Join(dbContext.RoleMetadata.AsNoTracking(), role => role.Id, metadata => metadata.RoleId,
                (role, metadata) => new
                {
                    role.Id,
                    role.Name,
                    role.NormalizedName,
                    metadata.DisplayName,
                    metadata.Description,
                    metadata.IsSystem,
                    metadata.IsBuiltIn,
                    metadata.StewardUserId,
                    Version = metadata.ConcurrencyStamp,
                })
            .OrderBy(item => item.Name)
            .ToListAsync(cancellationToken);
        var roleIds = roles.Select(role => role.Id).ToArray();
        var permissions = await dbContext.RolePermissions.AsNoTracking()
            .Where(item => roleIds.Contains(item.RoleId))
            .ToListAsync(cancellationToken);
        var permissionsByRole = permissions.GroupBy(item => item.RoleId)
            .ToDictionary(group => group.Key, group => group.Select(item => item.PermissionKey).ToArray());
        return TypedResults.Ok(roles.Select(role => new
        {
            role.Id,
            role.Name,
            role.NormalizedName,
            role.DisplayName,
            role.Description,
            role.IsSystem,
            role.IsBuiltIn,
            role.StewardUserId,
            role.Version,
            Permissions = permissionsByRole.GetValueOrDefault(role.Id, []),
        }).ToArray());
    }

    private static async Task<IResult> CreateRole(
        [FromBody] RoleUpsertRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        RoleManager<IdentityRole<Guid>> roleManager,
        AuthorizationMutationService mutations,
        IPermissionCatalog catalog,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var validation = ValidateRoleRequest(request, catalog);
        if (validation is not null) return validation;

        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return TypedResults.Unauthorized();
        var authorization = await mutations.AuthorizeRoleCreateAsync(actor.Id, request!.PermissionKeys!, request.DelegationId, cancellationToken);
        if (authorization.Error is not null) return authorization.Error;

        try
        {
            await using var transaction = await dbContext.Database.BeginTransactionAsync(
                System.Data.IsolationLevel.Serializable, cancellationToken);
            var name = request.Name!.Trim();
            var normalizedName = roleManager.NormalizeKey(name);
            if (await dbContext.Roles.AnyAsync(role => role.NormalizedName == normalizedName, cancellationToken))
                return Conflict("role_exists", "The normalized role name is already in use.");

            var role = new IdentityRole<Guid>
            {
                Name = name,
                NormalizedName = normalizedName,
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            };
            dbContext.Roles.Add(role);
            var metadata = new RoleMetadata
            {
                RoleId = role.Id,
                DisplayName = request.DisplayName!.Trim(),
                Description = request.Description!.Trim(),
                IsSystem = false,
                IsBuiltIn = false,
                StewardUserId = actor.Id,
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            };
            dbContext.RoleMetadata.Add(metadata);
            dbContext.RolePermissions.AddRange(request.PermissionKeys!.Distinct(StringComparer.Ordinal)
                .Select(key => new RolePermission { RoleId = role.Id, PermissionKey = key }));

            var delegationId = authorization.Scope?.DelegationId;
            if (delegationId.HasValue)
                dbContext.AuthorizationDelegationRoles.Add(new AuthorizationDelegationRole
                {
                    DelegationId = delegationId.Value,
                    RoleId = role.Id,
                });

            await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, null, role.Id, "role.created", new
            {
                RoleId = role.Id,
                Name = (string?)null,
                DisplayName = (string?)null,
                Description = (string?)null,
                IsSystem = false,
                IsBuiltIn = false,
                StewardUserId = (Guid?)null,
                Permissions = Array.Empty<string>(),
            }, new
            {
                role.Name,
                metadata.DisplayName,
                metadata.Description,
                Permissions = request.PermissionKeys,
            }, cancellationToken);

            await transaction.CommitAsync(cancellationToken);

            return TypedResults.Created($"/api/v1/identity/access/roles/{role.Id}", new
            {
                role.Id,
                role.Name,
                Version = metadata.ConcurrencyStamp,
                Permissions = request.PermissionKeys,
            });
        }
        catch (Exception exception) when (AuthorizationConflict.IsExpected(exception))
        {
            return Conflict("role_conflict", "The role conflicts with a concurrent authorization change.");
        }
    }

    private static async Task<IResult> UpdateRole(
        Guid id,
        [FromBody] RoleUpsertRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationMutationService mutations,
        IPermissionCatalog catalog,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var validation = ValidateRoleRequest(request, catalog);
        if (validation is not null) return validation;

        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return TypedResults.Unauthorized();
        var role = await dbContext.Roles.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        var metadata = await dbContext.RoleMetadata.SingleOrDefaultAsync(item => item.RoleId == id, cancellationToken);
        if (role is null || metadata is null) return TypedResults.NotFound();
        if (metadata.IsSystem || metadata.IsBuiltIn)
            return Conflict("system_role", "Protected system and built-in roles cannot be changed.");
        if (string.IsNullOrWhiteSpace(request!.ConcurrencyStamp) ||
            !string.Equals(metadata.ConcurrencyStamp, request.ConcurrencyStamp, StringComparison.Ordinal))
            return Conflict("role_conflict", "The role changed concurrently; refresh its version.");
        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        var guard = await mutations.AuthorizeRoleEditAsync(actor.Id, id, request.Name!.Trim(), request.PermissionKeys!, cancellationToken);
        if (guard is not null) return guard;

        try
        {
            await mutations.AcquireRoleMutationLockAsync(id, cancellationToken);
            var beforePermissions = await dbContext.RolePermissions.Where(item => item.RoleId == id)
                .Select(item => item.PermissionKey).ToArrayAsync(cancellationToken);
            if (await dbContext.AccessGroupRoleMappings.AsNoTracking().AnyAsync(item => item.RoleId == id, cancellationToken) &&
                (string.Equals(request.Name!.Trim(), AuthRoles.Owner, StringComparison.Ordinal) ||
                 request.PermissionKeys!.Any(key => catalog.Contains(key) && !catalog.GetRequired(key).Delegable)))
                return TypedResults.Json(new
                {
                    code = "role_group_protected_permission",
                    message = "A role mapped to an access group cannot receive Owner or protected authorization-management permissions.",
                }, statusCode: StatusCodes.Status403Forbidden);
            var before = new { role.Name, metadata.DisplayName, metadata.Description, Permissions = beforePermissions };
            var normalizedName = request.Name!.Trim().ToUpperInvariant();
            if (await dbContext.Roles.AnyAsync(item => item.Id != id && item.NormalizedName == normalizedName, cancellationToken))
                return Conflict("role_exists", "The normalized role name is already in use.");

            role.Name = request.Name.Trim();
            role.NormalizedName = normalizedName;
            metadata.DisplayName = request.DisplayName!.Trim();
            metadata.Description = request.Description!.Trim();
            metadata.ConcurrencyStamp = Guid.NewGuid().ToString("N");
            var oldPermissions = await dbContext.RolePermissions.Where(item => item.RoleId == id).ToListAsync(cancellationToken);
            dbContext.RolePermissions.RemoveRange(oldPermissions);
            dbContext.RolePermissions.AddRange(request.PermissionKeys!.Distinct(StringComparer.Ordinal)
                .Select(key => new RolePermission { RoleId = id, PermissionKey = key }));
            await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, null, id, "role.updated", before, new
            {
                role.Name,
                metadata.DisplayName,
                metadata.Description,
                Permissions = request.PermissionKeys,
            }, cancellationToken);
            await transaction.CommitAsync(cancellationToken);

            return TypedResults.Ok(new
            {
                role.Id,
                role.Name,
                metadata.DisplayName,
                metadata.Description,
                Version = metadata.ConcurrencyStamp,
                Permissions = request.PermissionKeys,
            });
        }
        catch (Exception exception) when (AuthorizationConflict.IsExpected(exception))
        {
            return Conflict("role_conflict", "The role changed concurrently.");
        }

    }

    private static async Task<IResult> DeleteRole(
        Guid id,
        [FromBody] RoleMutationRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationMutationService mutations,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return TypedResults.Unauthorized();
        try
        {
            await using var transaction = await dbContext.Database.BeginTransactionAsync(
                System.Data.IsolationLevel.Serializable, cancellationToken);
            var role = await dbContext.Roles.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
            var metadata = await dbContext.RoleMetadata.SingleOrDefaultAsync(item => item.RoleId == id, cancellationToken);
            if (role is null || metadata is null) return TypedResults.NotFound();
            if (metadata.IsSystem || metadata.IsBuiltIn)
                return Conflict("system_role", "Protected system and built-in roles cannot be deleted.");
            if (string.IsNullOrWhiteSpace(request?.ConcurrencyStamp) ||
                !string.Equals(metadata.ConcurrencyStamp, request.ConcurrencyStamp, StringComparison.Ordinal))
                return Conflict("role_conflict", "The role changed concurrently; refresh its version.");
            var guard = await mutations.AuthorizeRoleDeleteAsync(actor.Id, id, cancellationToken);
            if (guard is not null) return guard;
            // AuthorizeRoleDeleteAsync acquires and validates the role-keyed lock.
            // Recheck after the lock because a concurrent mapping mutation may have
            // committed between the initial load and the authorization decision.
            if (await dbContext.AccessGroupRoleMappings.AsNoTracking().AnyAsync(item => item.RoleId == id, cancellationToken))
                return TypedResults.Json(new
                {
                    code = "role_mapped",
                    message = "Roles mapped to access groups cannot be deleted; remove every mapping first.",
                }, statusCode: StatusCodes.Status403Forbidden);
            if (await dbContext.UserRoles.AnyAsync(item => item.RoleId == id, cancellationToken))
                return Conflict("role_assigned", "Assigned roles cannot be deleted.");
            dbContext.Roles.Remove(role);
            await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, null, id, "role.deleted",
                new { role.Name, metadata.Description }, "{}", cancellationToken);
            await transaction.CommitAsync(cancellationToken);
        }
        catch (Exception exception) when (AuthorizationConflict.IsExpected(exception))
        {
            return Conflict("role_conflict", "The role changed concurrently.");
        }
        return TypedResults.NoContent();
    }

    private static Task<IResult> ReplacePermissions(
        Guid id,
        [FromBody] PermissionReplacementRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationMutationService mutations,
        IPermissionCatalog catalog,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken) =>
        UpdateRole(id, request is null ? null : new RoleUpsertRequest(
            request.Name, request.DisplayName, request.Description, request.PermissionKeys, null, request.ConcurrencyStamp),
            principal, httpContext, dbContext, userManager, mutations, catalog, auditWriter, cancellationToken);

    private static async Task<IResult> AssignRoles(
        Guid id,
        [FromBody] RoleAssignmentRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationMutationService mutations,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return TypedResults.Unauthorized();
        if (actor.Id == id) return BadRequest("self_change", "A user cannot change their own roles.");
        var target = await dbContext.Users.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (target is null) return TypedResults.NotFound();
        if (string.IsNullOrWhiteSpace(request?.ConcurrencyStamp) ||
            !string.Equals(target.ConcurrencyStamp, request.ConcurrencyStamp, StringComparison.Ordinal))
            return Conflict("user_conflict", "The user changed concurrently; refresh its version.");

        try
        {
            await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
            var roleIds = request.RoleIds?.Distinct().ToArray() ?? [];
            var existing = await dbContext.UserRoles.Where(item => item.UserId == id).ToListAsync(cancellationToken);
            var existingRoleIds = existing.Select(item => item.RoleId).ToArray();
            var existingMetadata = await dbContext.RoleMetadata.AsNoTracking()
                .Where(item => existingRoleIds.Contains(item.RoleId)).ToListAsync(cancellationToken);
            var existingCustomRoleIds = existingRoleIds.Where(roleId =>
                existingMetadata.SingleOrDefault(item => item.RoleId == roleId) is { IsSystem: false, IsBuiltIn: false }).ToArray();
            var roles = await dbContext.Roles.Where(role => roleIds.Contains(role.Id)).ToListAsync(cancellationToken);
            var metadata = await dbContext.RoleMetadata.Where(item => roleIds.Contains(item.RoleId)).ToListAsync(cancellationToken);
            if (roles.Count != roleIds.Length || metadata.Count != roleIds.Length || metadata.Any(item => item.IsSystem || item.IsBuiltIn))
                return BadRequest("invalid_roles", "Only custom application roles may be assigned by this route.");
            var requestedCustomRoleIds = metadata
                .Where(item => !item.IsSystem && !item.IsBuiltIn)
                .Select(item => item.RoleId)
                .ToArray();
            var authorization = await mutations.AuthorizeAssignmentAsync(
                actor.Id, id, existingCustomRoleIds, requestedCustomRoleIds, request.DelegationId, cancellationToken);
            if (authorization.Error is not null) return authorization.Error;
            var before = await auditWriter.CaptureUserAsync(dbContext, id, cancellationToken);
            var systemRoleIds = existingMetadata.Where(item => item.IsSystem || item.IsBuiltIn)
                .Select(item => item.RoleId).ToHashSet();
            var removableCustomRoleIds = authorization.RemovableCustomRoleIds.ToHashSet();
            dbContext.UserRoles.RemoveRange(existing.Where(item =>
                systemRoleIds.Contains(item.RoleId) ? false :
                !requestedCustomRoleIds.Contains(item.RoleId) && removableCustomRoleIds.Contains(item.RoleId)));
            var existingIds = existing.Select(item => item.RoleId).ToHashSet();
            dbContext.UserRoles.AddRange(roleIds.Where(roleId => !existingIds.Contains(roleId))
                .Select(roleId => new ApplicationUserRole { UserId = id, RoleId = roleId }));
            target.SecurityStamp = Guid.NewGuid().ToString("N");
            target.ConcurrencyStamp = Guid.NewGuid().ToString("N");
            var effectiveRoleIds = existing.Where(item => systemRoleIds.Contains(item.RoleId) ||
                    requestedCustomRoleIds.Contains(item.RoleId) ||
                    !removableCustomRoleIds.Contains(item.RoleId))
                .Select(item => item.RoleId).Concat(requestedCustomRoleIds).Distinct().ToArray();
            var effectiveRoleNames = await dbContext.Roles.AsNoTracking()
                .Where(item => effectiveRoleIds.Contains(item.Id)).Select(item => item.Name).ToArrayAsync(cancellationToken);
            var effectivePermissionKeys = await dbContext.RolePermissions.AsNoTracking()
                .Where(item => effectiveRoleIds.Contains(item.RoleId)).Select(item => item.PermissionKey)
                .Distinct().ToArrayAsync(cancellationToken);
            var after = new UserAuthorizationSnapshot(id, effectiveRoleNames,
                effectiveRoleNames.Contains(AuthRoles.Owner, StringComparer.Ordinal) ? ["*"] : effectivePermissionKeys);
            await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, id, null, "user.roles-replaced", before, after, cancellationToken);
            await transaction.CommitAsync(cancellationToken);
        }
        catch (Exception exception) when (AuthorizationConflict.IsExpected(exception))
        {
            return Conflict("user_conflict", "The user roles changed concurrently.");
        }
        var responseRoleIds = await dbContext.UserRoles.AsNoTracking()
            .Where(item => item.UserId == id).Select(item => item.RoleId).ToArrayAsync(cancellationToken);
        return TypedResults.Ok(new { UserId = id, RoleIds = responseRoleIds, Version = target.ConcurrencyStamp });
    }

    private static async Task<IResult> EffectiveAccessForCurrentUser(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        AuthorizationMutationService mutations,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null) return TypedResults.Unauthorized();
        var payload = await BuildEffectiveAccess(user.Id, dbContext, cancellationToken);
        if (payload is null) return TypedResults.NotFound();
        var owner = await mutations.IsOwnerAsync(user.Id, cancellationToken);
        var scopes = await mutations.ActiveScopesAsync(user.Id, cancellationToken);
        var delegationScopes = scopes.Select(scope => new
        {
            Id = scope.DelegationId,
            scope.CanCreateRoles,
            GrantablePermissionKeys = scope.GrantablePermissionKeys.OrderBy(key => key).ToArray(),
            StewardedRoleIds = scope.StewardedRoleIds.OrderBy(id => id).ToArray(),
            AssignableRoleIds = scope.AssignableRoleIds.OrderBy(id => id).ToArray(),
        }).ToArray();
        return TypedResults.Ok(new
        {
            UserId = payload.Id,
            payload.Roles,
            payload.RoleIds,
            payload.Permissions,
            payload.Version,
            canManageAuthorization = await mutations.CanManageAuthorizationAsync(user.Id, cancellationToken),
            administrationScope = owner
                ? new { IsOwner = true, DelegationScopes = delegationScopes }
                : scopes.Count == 0 ? null : new
                {
                    IsOwner = false,
                    DelegationScopes = delegationScopes,
                },
        });
    }

    private static async Task<IResult> ListAssignableUsers(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        AuthorizationMutationService mutations,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return TypedResults.Unauthorized();
        var canManage = await mutations.CanManageAuthorizationAsync(actor.Id, cancellationToken);
        if (!canManage) return TypedResults.Forbid();
        var owner = await mutations.IsOwnerAsync(actor.Id, cancellationToken);
        var scopes = owner ? [] : await mutations.ActiveScopesAsync(actor.Id, cancellationToken);

        var users = await dbContext.Users.AsNoTracking()
            .OrderBy(user => user.DisplayName)
            .ThenBy(user => user.Id)
            .Select(user => new
            {
                user.Id,
                user.DisplayName,
                user.Email,
                user.IsDisabled,
                user.LockoutEnd,
                user.ConcurrencyStamp,
            })
            .ToListAsync(cancellationToken);
        var userIds = users.Select(user => user.Id).ToArray();
        var assignments = await dbContext.UserRoles.AsNoTracking()
            .Where(item => userIds.Contains(item.UserId))
            .Join(dbContext.Roles.AsNoTracking(), assignment => assignment.RoleId, role => role.Id,
                (assignment, role) => new { assignment.UserId, role.Id, role.Name })
            .ToListAsync(cancellationToken);
        var delegationUsers = await dbContext.AuthorizationDelegations.AsNoTracking()
            .Where(item => userIds.Contains(item.GranteeUserId) && item.RevokedAt == null &&
                (item.ExpiresAt == null || item.ExpiresAt > DateTimeOffset.UtcNow))
            .Select(item => item.GranteeUserId)
            .Distinct()
            .ToListAsync(cancellationToken);
        var rows = users
            .Where(user => owner ||
                !assignments.Any(item => item.UserId == user.Id && item.Name == AuthRoles.Owner) &&
                !delegationUsers.Contains(user.Id))
            .Select(user => new
            {
                user.Id,
                user.DisplayName,
                user.Email,
                user.IsDisabled,
                Active = !user.IsDisabled && !AuthAccountState.IsLockedOut(user.LockoutEnd, DateTimeOffset.UtcNow),
                Version = user.ConcurrencyStamp,
                Roles = assignments.Where(item => item.UserId == user.Id)
                    .OrderBy(item => item.Name)
                    .Select(item => new { item.Id, item.Name })
                    .ToArray(),
            })
            .ToArray();
        return TypedResults.Ok(rows);
    }

    private static async Task<IResult> EffectiveAccess(Guid id, ClaimsPrincipal principal, AuthorizationMutationService mutations, AccountsDbContext dbContext, CancellationToken cancellationToken) =>
        !Guid.TryParse(principal.FindFirstValue(ClaimTypes.NameIdentifier), out var actorId) ||
        !await mutations.CanInspectTargetAsync(actorId, id, cancellationToken)
            ? TypedResults.NotFound()
            : (await BuildEffectiveAccess(id, dbContext, cancellationToken)) is { } payload
            ? TypedResults.Ok(payload)
            : TypedResults.NotFound();

    private static async Task<EffectiveAccessProjection?> BuildEffectiveAccess(Guid id, AccountsDbContext dbContext, CancellationToken cancellationToken)
    {
        var user = await dbContext.Users.AsNoTracking().SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (user is null) return null;
        var roles = await dbContext.UserRoles.AsNoTracking().Where(item => item.UserId == id)
            .Join(dbContext.Roles.AsNoTracking(), assignment => assignment.RoleId, role => role.Id,
                (_, role) => new { role.Id, role.Name }).ToListAsync(cancellationToken);
        var isOwner = roles.Any(role => role.Name == AuthRoles.Owner);
        var permissions = isOwner
            ? ["*"]
            : await dbContext.RolePermissions.AsNoTracking().Where(item => roles.Select(role => role.Id).Contains(item.RoleId))
                .Select(item => item.PermissionKey).Distinct().OrderBy(key => key).ToListAsync(cancellationToken);
        return new EffectiveAccessProjection(user.Id, roles.Select(role => role.Name).ToArray(),
            roles.Select(role => role.Id).ToArray(), permissions, user.ConcurrencyStamp);
    }

    private sealed record EffectiveAccessProjection(
        Guid Id,
        IReadOnlyCollection<string?> Roles,
        IReadOnlyCollection<Guid> RoleIds,
        IReadOnlyCollection<string> Permissions,
        string? Version);

    private static async Task<IResult> Audit(AccountsDbContext dbContext, CancellationToken cancellationToken) =>
        TypedResults.Ok(await dbContext.AuthorizationAuditEvents.AsNoTracking()
            .OrderByDescending(item => item.OccurredAt).Take(500).ToListAsync(cancellationToken));

    private static async Task<IResult> ListDelegations(AccountsDbContext dbContext, CancellationToken cancellationToken) =>
        TypedResults.Ok(await dbContext.AuthorizationDelegations.AsNoTracking()
            .OrderByDescending(item => item.Id)
            .Select(item => new
            {
                item.Id,
                item.GranteeUserId,
                item.ExpiresAt,
                item.RevokedAt,
                Version = item.ConcurrencyStamp,
                item.CanCreateRoles,
                PermissionKeys = dbContext.AuthorizationDelegationPermissions
                    .Where(permission => permission.DelegationId == item.Id).Select(permission => permission.PermissionKey),
                StewardedRoleIds = dbContext.AuthorizationDelegationRoles
                    .Where(role => role.DelegationId == item.Id).Select(role => role.RoleId),
            }).ToListAsync(cancellationToken));

    private static async Task<IResult> CreateDelegation(
        [FromBody] DelegationRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        IPermissionCatalog catalog,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return TypedResults.Unauthorized();
        var validation = await ValidateDelegation(request, actor.Id, dbContext, catalog, cancellationToken);
        if (validation is not null) return validation;
        var delegation = new AuthorizationDelegation
        {
            GranteeUserId = request!.GranteeUserId,
            CreatedByUserId = actor.Id,
            ExpiresAt = request.ExpiresAt,
            CanCreateRoles = request.CanCreateRoles,
            ConcurrencyStamp = Guid.NewGuid().ToString("N"),
        };
        try
        {
            await using var transaction = await dbContext.Database.BeginTransactionAsync(
                System.Data.IsolationLevel.Serializable, cancellationToken);
            dbContext.AuthorizationDelegations.Add(delegation);
            AddDelegationChildren(dbContext, delegation.Id, request);
            await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, request.GranteeUserId, null, "delegation.created",
                new { Delegation = (object?)null }, new
                {
                    delegation.Id,
                    delegation.GranteeUserId,
                    delegation.CreatedByUserId,
                    delegation.ExpiresAt,
                    delegation.RevokedAt,
                    delegation.CanCreateRoles,
                    PermissionKeys = request.PermissionKeys,
                    StewardedRoleIds = request.StewardedRoleIds,
                }, cancellationToken);
            await transaction.CommitAsync(cancellationToken);
            return TypedResults.Created($"/api/v1/identity/access/delegations/{delegation.Id}", new
            {
                delegation.Id,
                Version = delegation.ConcurrencyStamp,
            });
        }
        catch (Exception exception) when (AuthorizationConflict.IsExpected(exception))
        {
            return Conflict("delegation_conflict", "The delegation changed concurrently.");
        }
    }

    private static async Task<IResult> UpdateDelegation(
        Guid id,
        [FromBody] DelegationRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        IPermissionCatalog catalog,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return TypedResults.Unauthorized();
        if (request?.ConcurrencyStamp is null) return Conflict("delegation_conflict", "A delegation version is required.");
        var delegation = await dbContext.AuthorizationDelegations.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (delegation is null) return TypedResults.NotFound();
        if (!string.Equals(delegation.ConcurrencyStamp, request.ConcurrencyStamp, StringComparison.Ordinal))
            return Conflict("delegation_conflict", "The delegation changed concurrently.");
        var validation = await ValidateDelegation(request, actor.Id, dbContext, catalog, cancellationToken);
        if (validation is not null) return validation;
        try
        {
            await using var transaction = await dbContext.Database.BeginTransactionAsync(
                System.Data.IsolationLevel.Serializable, cancellationToken);
            var before = await auditWriter.CaptureDelegationAsync(dbContext, id, cancellationToken);
            delegation.GranteeUserId = request.GranteeUserId;
            delegation.ExpiresAt = request.ExpiresAt;
            delegation.CanCreateRoles = request.CanCreateRoles;
            delegation.ConcurrencyStamp = Guid.NewGuid().ToString("N");
            var permissions = await dbContext.AuthorizationDelegationPermissions.Where(item => item.DelegationId == id).ToListAsync(cancellationToken);
            var roles = await dbContext.AuthorizationDelegationRoles.Where(item => item.DelegationId == id).ToListAsync(cancellationToken);
            dbContext.AuthorizationDelegationPermissions.RemoveRange(permissions);
            dbContext.AuthorizationDelegationRoles.RemoveRange(roles);
            AddDelegationChildren(dbContext, id, request);
            var after = new
            {
                delegation.Id,
                delegation.GranteeUserId,
                delegation.CreatedByUserId,
                delegation.ExpiresAt,
                delegation.RevokedAt,
                delegation.CanCreateRoles,
                PermissionKeys = request.PermissionKeys,
                StewardedRoleIds = request.StewardedRoleIds,
            };
            await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, delegation.GranteeUserId, null,
                "delegation.updated", before!, after, cancellationToken);
            await transaction.CommitAsync(cancellationToken);
        }
        catch (Exception exception) when (AuthorizationConflict.IsExpected(exception)) { return Conflict("delegation_conflict", "The delegation changed concurrently."); }
        return TypedResults.Ok(new { delegation.Id, Version = delegation.ConcurrencyStamp });
    }

    private static async Task<IResult> RevokeDelegation(
        Guid id,
        [FromBody] DelegationMutationRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return TypedResults.Unauthorized();
        var delegation = await dbContext.AuthorizationDelegations.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (delegation is null) return TypedResults.NotFound();
        if (request?.ConcurrencyStamp is null || !string.Equals(delegation.ConcurrencyStamp, request.ConcurrencyStamp, StringComparison.Ordinal))
            return Conflict("delegation_conflict", "The delegation changed concurrently.");
        try
        {
            await using var transaction = await dbContext.Database.BeginTransactionAsync(
                System.Data.IsolationLevel.Serializable, cancellationToken);
            var before = await auditWriter.CaptureDelegationAsync(dbContext, id, cancellationToken);
            delegation.RevokedAt = DateTimeOffset.UtcNow;
            delegation.ConcurrencyStamp = Guid.NewGuid().ToString("N");
            await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, delegation.GranteeUserId, null, "delegation.revoked", before!, new
            {
                delegation.Id,
                delegation.GranteeUserId,
                delegation.CreatedByUserId,
                delegation.ExpiresAt,
                delegation.RevokedAt,
                delegation.CanCreateRoles,
                PermissionKeys = await dbContext.AuthorizationDelegationPermissions.Where(item => item.DelegationId == id).Select(item => item.PermissionKey).ToArrayAsync(cancellationToken),
                StewardedRoleIds = await dbContext.AuthorizationDelegationRoles.Where(item => item.DelegationId == id).Select(item => item.RoleId).ToArrayAsync(cancellationToken),
            }, cancellationToken);
            await transaction.CommitAsync(cancellationToken);
        }
        catch (Exception exception) when (AuthorizationConflict.IsExpected(exception)) { return Conflict("delegation_conflict", "The delegation changed concurrently."); }
        return TypedResults.Ok(new { delegation.Id, Version = delegation.ConcurrencyStamp, delegation.RevokedAt });
    }

    private static async Task<IResult?> ValidateDelegation(
        DelegationRequest? request,
        Guid actorId,
        AccountsDbContext dbContext,
        IPermissionCatalog catalog,
        CancellationToken cancellationToken)
    {
        if (request is null || request.GranteeUserId == actorId || request.PermissionKeys is null || request.StewardedRoleIds is null ||
            request.ExpiresAt is not null && request.ExpiresAt <= DateTimeOffset.UtcNow)
            return BadRequest("invalid_delegation", "A delegation must target another user and have valid scope.");
        var target = await dbContext.Users.AsNoTracking().SingleOrDefaultAsync(item => item.Id == request.GranteeUserId, cancellationToken);
        if (target is null) return TypedResults.NotFound();
        var targetIsOwner = await dbContext.UserRoles.Join(dbContext.Roles, item => item.RoleId, role => role.Id,
            (item, role) => new { item.UserId, role.Name }).AnyAsync(item => item.UserId == target.Id && item.Name == AuthRoles.Owner, cancellationToken);
        if (targetIsOwner) return BadRequest("invalid_delegation_target", "Owner accounts cannot be delegated.");
        if (request.PermissionKeys.Any(key => !catalog.Contains(key) || !catalog.GetRequired(key).Delegable))
            return BadRequest("invalid_delegation_permissions", "Delegations may contain only registered delegable permissions.");
        var roles = await dbContext.RoleMetadata.Where(item => request.StewardedRoleIds.Contains(item.RoleId)).ToListAsync(cancellationToken);
        if (roles.Count != request.StewardedRoleIds.Count || roles.Any(item => item.IsSystem || item.IsBuiltIn))
            return BadRequest("invalid_delegation_roles", "Only custom roles may be stewarded.");
        return null;
    }

    private static void AddDelegationChildren(AccountsDbContext dbContext, Guid delegationId, DelegationRequest request)
    {
        dbContext.AuthorizationDelegationPermissions.AddRange(request.PermissionKeys!.Distinct(StringComparer.Ordinal)
            .Select(key => new AuthorizationDelegationPermission { DelegationId = delegationId, PermissionKey = key }));
        dbContext.AuthorizationDelegationRoles.AddRange(request.StewardedRoleIds!.Distinct()
            .Select(roleId => new AuthorizationDelegationRole { DelegationId = delegationId, RoleId = roleId }));
    }

    private static IResult? ValidateRoleRequest(RoleUpsertRequest? request, IPermissionCatalog catalog)
    {
        if (request is null || string.IsNullOrWhiteSpace(request.Name) || string.IsNullOrWhiteSpace(request.DisplayName) ||
            string.IsNullOrWhiteSpace(request.Description) || request.PermissionKeys is null ||
            request.PermissionKeys.Any(key => !catalog.Contains(key)))
            return BadRequest("invalid_role", "Role metadata and registered permission keys are required.");
        return null;
    }

    private static IResult Conflict(string code, string message) => TypedResults.Conflict(new { code, message });
    private static IResult BadRequest(string code, string message) => TypedResults.BadRequest(new { code, message });
}

internal sealed record RoleUpsertRequest(
    string? Name,
    string? DisplayName,
    string? Description,
    IReadOnlyCollection<string>? PermissionKeys,
    Guid? DelegationId = null,
    string? ConcurrencyStamp = null);

internal sealed record RoleMutationRequest(string? ConcurrencyStamp);

internal sealed record PermissionReplacementRequest(
    string? Name,
    string? DisplayName,
    string? Description,
    IReadOnlyCollection<string>? PermissionKeys,
    string? ConcurrencyStamp = null);

internal sealed record RoleAssignmentRequest(
    IReadOnlyCollection<Guid>? RoleIds,
    string? ConcurrencyStamp,
    Guid? DelegationId = null);

internal sealed record DelegationRequest(
    Guid GranteeUserId,
    DateTimeOffset? ExpiresAt,
    IReadOnlyCollection<string>? PermissionKeys,
    IReadOnlyCollection<Guid>? StewardedRoleIds,
    bool CanCreateRoles,
    string? ConcurrencyStamp = null);

internal sealed record DelegationMutationRequest(string? ConcurrencyStamp);