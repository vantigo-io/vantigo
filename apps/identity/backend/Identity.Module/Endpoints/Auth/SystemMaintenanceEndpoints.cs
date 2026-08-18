using System.Security.Claims;

using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Caching.Memory;

using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Endpoints.Auth;

/// <summary>Anonymous maintenance status and system-admin maintenance controls.</summary>
internal static class SystemMaintenanceEndpoints
{
    private const string StatusCacheKey = "identity-system-maintenance-status";
    private static readonly TimeSpan StatusCacheDuration = TimeSpan.FromSeconds(20);

    internal static void Map(IEndpointRouteBuilder app)
    {
        var system = app.MapGroup("/api/v1/identity/system")
            .WithTags("Identity system");

        system.MapGet("/status", GetStatus)
            .WithSummary("Get the current system maintenance status")
            .AllowAnonymous();

        system.MapPut("/maintenance", PutMaintenance)
            .WithSummary("Update system maintenance mode")
            .RequireAuthorization(AuthPolicies.SystemAdmin);
    }

    private static async Task<IResult> GetStatus(
        AccountsDbContext dbContext,
        IMemoryCache cache,
        CancellationToken cancellationToken)
    {
        var status = await cache.GetOrCreateAsync(StatusCacheKey, async entry =>
        {
            entry.AbsoluteExpirationRelativeToNow = StatusCacheDuration;
            var setting = await dbContext.SystemSettings.AsNoTracking()
                .SingleOrDefaultAsync(item => item.Id == SystemSetting.SingletonId, cancellationToken);
            return ToStatus(setting);
        });

        return TypedResults.Ok(status ?? new SystemMaintenanceStatus(false, null));
    }

    private static async Task<IResult> PutMaintenance(
        [FromBody] SystemMaintenanceRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        AccountsDbContext dbContext,
        IMemoryCache cache,
        CancellationToken cancellationToken)
    {
        if (request is null)
            return Error("invalid_request", "A maintenance mode request is required.");
        if (request.Message is { Length: > 500 })
            return Error("invalid_message", "The maintenance message must be at most 500 characters.");

        var actor = await userManager.GetUserAsync(principal);
        if (actor is null)
            return Error("unauthenticated", "Authentication is required.");

        var setting = await dbContext.SystemSettings
            .SingleOrDefaultAsync(item => item.Id == SystemSetting.SingletonId, cancellationToken);
        if (setting is null)
        {
            setting = new SystemSetting { Id = SystemSetting.SingletonId };
            dbContext.SystemSettings.Add(setting);
        }

        setting.MaintenanceEnabled = request.Enabled;
        setting.MaintenanceMessage = request.Message;
        setting.UpdatedAt = DateTimeOffset.UtcNow;
        setting.UpdatedBy = actor.Email ?? actor.Id.ToString();
        await dbContext.SaveChangesAsync(cancellationToken);

        cache.Remove(StatusCacheKey);
        return TypedResults.Ok(ToStatus(setting));
    }

    private static SystemMaintenanceStatus ToStatus(SystemSetting? setting) =>
        setting is null
            ? new SystemMaintenanceStatus(false, null)
            : new SystemMaintenanceStatus(setting.MaintenanceEnabled, setting.MaintenanceMessage);

    private static IResult Error(string code, string message) =>
        TypedResults.Json(new { code, message }, statusCode: StatusCodes.Status400BadRequest);
}

internal sealed record SystemMaintenanceRequest(bool Enabled, string? Message);

internal sealed record SystemMaintenanceStatus(bool Maintenance, string? Message);