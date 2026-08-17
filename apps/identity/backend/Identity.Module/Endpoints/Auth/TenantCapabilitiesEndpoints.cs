using System.Security.Claims;

using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

/// <summary>Exposes the active tenant's module capabilities to regular members.</summary>
internal static class TenantCapabilitiesEndpoints
{
    internal static void MapTenantCapabilitiesEndpoints(IEndpointRouteBuilder app)
    {
        app.MapGet("/api/v1/identity/tenants/current/capabilities", CurrentTenantCapabilities)
            .WithTags("Tenants")
            .RequireAuthorization();
    }

    private static async Task<IResult> CurrentTenantCapabilities(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        TenantMembershipService tenantMembershipService,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null) return TypedResults.Unauthorized();

        var tenantId = await tenantMembershipService.ResolveActiveTenantIdAsync(user, principal, cancellationToken);
        if (tenantId is not Guid activeTenantId)
            return TypedResults.NotFound(new { code = "no_active_tenant", message = "No active tenant is selected." });

        var enabledModules = await dbContext.Tenants.AsNoTracking()
            .Where(tenant => tenant.Id == activeTenantId && tenant.Status == TenantStatus.Active)
            .Select(tenant => tenant.EnabledModules)
            .FirstOrDefaultAsync(cancellationToken);
        if (enabledModules is null)
            return TypedResults.NotFound(new { code = "no_active_tenant", message = "The active tenant is unavailable." });

        // Module enablement is navigation/pricing metadata; authorization stays
        // enforced independently by permissions and the tenancy middleware.
        var modules = TenantModuleCatalog.KnownModuleKeys
            .OrderBy(key => key, StringComparer.Ordinal)
            .Select(key => new
            {
                Key = key,
                Enabled = enabledModules.Contains(key, StringComparer.Ordinal),
                // Placeholder for future per-module tenant configuration.
                Config = new { },
            })
            .ToArray();
        return TypedResults.Ok(new { TenantId = activeTenantId, Modules = modules });
    }
}