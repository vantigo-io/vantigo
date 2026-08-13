using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

internal static class IdentityControlPlaneEndpoints
{
    internal static void MapIdentityControlPlaneEndpoints(IEndpointRouteBuilder app)
    {
        var access = app.MapGroup("/api/v1/identity/access")
            .WithTags("Identity control plane")
            .RequireAuthorization(Vantigo.Contracts.Identity.AuthPolicies.OwnerManagement);
        access.AddEndpointFilter(async (context, next) =>
        {
            try { return await next(context); }
            catch (Exception exception) when (exception is DbUpdateConcurrencyException || AuthorizationConflict.IsExpected(exception))
            { return Error(StatusCodes.Status409Conflict, "concurrency_conflict", "The resource changed concurrently."); }
        });

        var groups = access.MapGroup("/groups");
        groups.MapGet("", ListGroups);
        groups.MapGet("/{id:guid}", GetGroup);
        groups.MapPost("", CreateGroup);
        groups.MapPut("/{id:guid}", UpdateGroup);
        groups.MapDelete("/{id:guid}", DeleteGroup);
        groups.MapPost("/{groupId:guid}/members/{userId:guid}", AddMember);
        groups.MapPut("/{groupId:guid}/members/{userId:guid}", AddMember);
        groups.MapDelete("/{groupId:guid}/members/{userId:guid}", RemoveMember);
        groups.MapPost("/{groupId:guid}/role-mappings/{roleId:guid}", AddRoleMapping);
        groups.MapPut("/{groupId:guid}/role-mappings/{roleId:guid}", AddRoleMapping);
        groups.MapDelete("/{groupId:guid}/role-mappings/{roleId:guid}", RemoveRoleMapping);
    }

    private static async Task<IResult> ListGroups(AccessGroupManagementService service, CancellationToken token)
    {
        var groups = await service.ListAsync(token);
        var result = new List<AccessGroupResponse>(groups.Count);
        foreach (var group in groups) result.Add(await ToResponse(service, group, token));
        return TypedResults.Ok(result.ToArray());
    }

    private static async Task<IResult> GetGroup(Guid id, AccessGroupManagementService service, CancellationToken token)
    {
        var group = await service.GetAsync(id, token);
        return group is null || group.Source != AccessGroupSource.Local
            ? TypedResults.NotFound() : TypedResults.Ok(await ToResponse(service, group, token));
    }

    private static async Task<IResult> CreateGroup([FromBody] AccessGroupRequest? request,
        AccessGroupManagementService service, AccountsDbContext db, CancellationToken token)
    {
        var name = Normalize(request?.DisplayName);
        if (name is null) return Error(400, "invalid_group", "A safe display name is required.");
        if (await db.AccessGroups.AnyAsync(group => group.DisplayName == name, token))
            return Error(409, "group_exists", "An access group with this display name already exists.");
        try
        {
            var group = await service.CreateAsync(name, request!.IsActive, token);
            return TypedResults.Created($"/api/v1/identity/access/groups/{group.Id}", await ToResponse(service, group, token));
        }
        catch (DbUpdateException) { return Error(409, "group_conflict", "The access group conflicts with another change."); }
    }

    private static async Task<IResult> UpdateGroup(Guid id, [FromBody] AccessGroupRequest? request,
        AccessGroupManagementService service, AccountsDbContext db, CancellationToken token)
    {
        var name = Normalize(request?.DisplayName);
        if (name is null) return Error(400, "invalid_group", "A safe display name is required.");
        var current = await service.GetAsync(id, token);
        if (current is null) return TypedResults.NotFound();
        if (string.IsNullOrWhiteSpace(request!.ConcurrencyStamp)) return Error(400, "concurrency_required", "A concurrency stamp is required.");
        if (!string.Equals(request.ConcurrencyStamp, current.ConcurrencyStamp, StringComparison.Ordinal)) return Error(409, "group_conflict", "The access group changed concurrently.");
        if (await db.AccessGroups.AnyAsync(group => group.Id != id && group.DisplayName == name, token)) return Error(409, "group_exists", "An access group with this display name already exists.");
        try
        {
            var group = await service.UpdateAsync(id, name, request.IsActive, request.ConcurrencyStamp, token);
            return group is null ? TypedResults.NotFound() : TypedResults.Ok(await ToResponse(service, group, token));
        }
        catch (AccessGroupValidationException exception) { return Error(exception.StatusCode, exception.Code, exception.Message); }
    }

    private static async Task<IResult> DeleteGroup(Guid id, [FromBody] MutationRequest? request,
        AccessGroupManagementService service, CancellationToken token)
    {
        if (await service.GetAsync(id, token) is null) return TypedResults.NotFound();
        if (string.IsNullOrWhiteSpace(request?.ConcurrencyStamp)) return Error(400, "concurrency_required", "A concurrency stamp is required.");
        try { return await service.DeleteAsync(id, request.ConcurrencyStamp, token) ? TypedResults.NoContent() : TypedResults.NotFound(); }
        catch (AccessGroupValidationException exception) { return Error(exception.StatusCode, exception.Code, exception.Message); }
    }

    private static Task<IResult> AddMember(Guid groupId, Guid userId, [FromBody] MutationRequest? request, AccessGroupManagementService service, CancellationToken token) => MemberMutation(groupId, userId, request?.ConcurrencyStamp, true, service, token);
    private static Task<IResult> RemoveMember(Guid groupId, Guid userId, [FromBody] MutationRequest? request, AccessGroupManagementService service, CancellationToken token) => MemberMutation(groupId, userId, request?.ConcurrencyStamp, false, service, token);

    private static async Task<IResult> MemberMutation(Guid groupId, Guid userId, string? stamp, bool add, AccessGroupManagementService service, CancellationToken token)
    {
        if (string.IsNullOrWhiteSpace(stamp)) return Error(400, "concurrency_required", "A concurrency stamp is required.");
        try
        {
            var group = add ? await service.AddMemberAsync(groupId, userId, stamp, token) : await service.RemoveMemberAsync(groupId, userId, stamp, token);
            return group is null || group.Source != AccessGroupSource.Local ? TypedResults.NotFound() : TypedResults.Ok(await ToResponse(service, group, token));
        }
        catch (AccessGroupValidationException exception) { return Error(exception.StatusCode, exception.Code, exception.Message); }
    }

    private static Task<IResult> AddRoleMapping(Guid groupId, Guid roleId, [FromBody] MutationRequest? request, AccessGroupManagementService service, CancellationToken token) => RoleMappingMutation(groupId, roleId, request?.ConcurrencyStamp, request?.ScimConnectionId, true, service, token);
    private static Task<IResult> RemoveRoleMapping(Guid groupId, Guid roleId, [FromBody] MutationRequest? request, AccessGroupManagementService service, CancellationToken token) => RoleMappingMutation(groupId, roleId, request?.ConcurrencyStamp, request?.ScimConnectionId, false, service, token);

    private static async Task<IResult> RoleMappingMutation(Guid groupId, Guid roleId, string? stamp, Guid? scimId, bool add, AccessGroupManagementService service, CancellationToken token)
    {
        if (string.IsNullOrWhiteSpace(stamp)) return Error(400, "concurrency_required", "A concurrency stamp is required.");
        try
        {
            var group = add ? await service.AddRoleMappingAsync(groupId, roleId, stamp, scimId, token) : await service.RemoveRoleMappingAsync(groupId, roleId, stamp, scimId, token);
            return group is null ? TypedResults.NotFound() : TypedResults.Ok(await ToResponse(service, group, token));
        }
        catch (AccessGroupValidationException exception) { return Error(exception.StatusCode, exception.Code, exception.Message); }
    }

    private static async Task<AccessGroupResponse> ToResponse(AccessGroupManagementService service, AccessGroup group, CancellationToken token)
    {
        var details = await service.DetailsAsync(group, token);
        return new(group.Id, group.DisplayName, group.Source.ToString(), group.ScimConnectionId, group.IsActive, group.CreatedAt, group.UpdatedAt, group.ConcurrencyStamp, details.MemberUserIds, details.RoleIds);
    }

    private static string? Normalize(string? value)
    {
        if (string.IsNullOrWhiteSpace(value)) return null;
        var normalized = value.Trim();
        return normalized.Length <= 200 && !normalized.Any(char.IsControl) ? normalized : null;
    }

    private static IResult Error(int status, string code, string message) => TypedResults.Json(new { code, message }, statusCode: status);
}

internal sealed record AccessGroupRequest(string? DisplayName, bool IsActive, string? ConcurrencyStamp = null);
internal sealed record MutationRequest(string? ConcurrencyStamp, Guid? ScimConnectionId = null);
internal sealed record AccessGroupResponse(Guid Id, string DisplayName, string Source, Guid? ScimConnectionId, bool IsActive, DateTimeOffset CreatedAt, DateTimeOffset UpdatedAt, string ConcurrencyStamp, IReadOnlyCollection<Guid> MemberUserIds, IReadOnlyCollection<Guid> RoleIds);