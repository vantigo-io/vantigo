using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

internal static class ScimControlPlaneEndpoints
{
    internal static void Map(IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/api/v1/identity/access/scim")
            .WithTags("SCIM control plane")
            .RequireAuthorization(Vantigo.Contracts.Identity.AuthPolicies.OwnerManagement);
        group.MapGet("", ListConnections);
        group.MapPost("", CreateConnection);
        group.MapPost("/{id:guid}/enable", Enable);
        group.MapPost("/{id:guid}/disable", Disable);
        group.MapPost("/{id:guid}/rotate", Rotate);
        group.MapPost("/{id:guid}/revoke", Revoke);
        group.MapDelete("/{id:guid}", Delete);
        group.MapGet("/{id:guid}/users", ListUsers);
        group.MapPut("/{id:guid}/users/{userId:guid}/override", SetOverride);
    }

    private static Task<IResult> ListConnections([FromServices] ScimControlPlaneService service, CancellationToken token) => service.ListAsync(token);
    private static Task<IResult> CreateConnection([FromBody] ScimConnectionRequest request, [FromServices] ScimControlPlaneService service, CancellationToken token) => service.CreateAsync(request, token);
    private static Task<IResult> Enable(Guid id, [FromBody] ScimEnableRequest request, [FromServices] ScimControlPlaneService service, CancellationToken token) => service.SetEnabledAsync(id, true, request.ConcurrencyStamp, token);
    private static Task<IResult> Disable(Guid id, [FromBody] ScimDisableRequest request, [FromServices] ScimControlPlaneService service, CancellationToken token) => service.SetEnabledAsync(id, false, request.ConcurrencyStamp, token);
    private static Task<IResult> Rotate(Guid id, [FromBody] ScimRotateRequest request, [FromServices] ScimControlPlaneService service, CancellationToken token) => service.RotateAsync(id, request, token);
    private static Task<IResult> Revoke(Guid id, [FromBody] ScimRevokeRequest request, [FromServices] ScimControlPlaneService service, CancellationToken token) => service.RevokeAsync(id, request, token);
    private static Task<IResult> Delete(Guid id, [FromBody] ScimDeleteRequest request, [FromServices] ScimControlPlaneService service, CancellationToken token) => service.DeleteAsync(id, request.ConcurrencyStamp, token);
    private static Task<IResult> ListUsers(Guid id, [FromServices] ScimControlPlaneService service, CancellationToken token) => service.ListUsersAsync(id, token);
    private static Task<IResult> SetOverride(Guid id, Guid userId, [FromBody] ScimOverrideRequest request, [FromServices] ScimControlPlaneService service, CancellationToken token) => service.SetOverrideAsync(id, userId, request, token);
}

public sealed record ScimConnectionRequest(Guid FederationConnectionId, string? Mode)
{
    public bool TryGetProvisioningMode(out ScimProvisioningMode mode)
    {
        if (string.Equals(Mode, nameof(ScimProvisioningMode.Authoritative), StringComparison.OrdinalIgnoreCase))
        {
            mode = ScimProvisioningMode.Authoritative;
            return true;
        }

        if (string.Equals(Mode, nameof(ScimProvisioningMode.Additive), StringComparison.OrdinalIgnoreCase))
        {
            mode = ScimProvisioningMode.Additive;
            return true;
        }

        mode = default;
        return false;
    }
}
public sealed record ScimEnableRequest(string ConcurrencyStamp);
public sealed record ScimDisableRequest(string ConcurrencyStamp);
public sealed record ScimRotateRequest(string ConcurrencyStamp);
public sealed record ScimRevokeRequest(string ConcurrencyStamp);
public sealed record ScimDeleteRequest(string? ConcurrencyStamp);
public sealed record ScimOverrideRequest(ScimLifecycleOverride? Override, string Reason, string ETag);