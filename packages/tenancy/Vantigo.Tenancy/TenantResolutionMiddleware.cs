using System.Security.Claims;

using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy;

/// <summary>
/// Resolves the ambient tenant for each request.
/// Multi mode: the tenant slug route value ("tenantSlug") is resolved via
/// <see cref="ITenantDirectory"/> and the authenticated user's membership is
/// verified; requests without a resolvable tenant proceed unresolved and any
/// tenant-scoped work fails closed.
/// Single mode: the default tenant is always used.
/// </summary>
public sealed class TenantResolutionMiddleware(RequestDelegate next)
{
    public const string TenantSlugRouteKey = "tenantSlug";

    public async Task InvokeAsync(
        HttpContext context,
        IOptions<TenancyOptions> tenancyOptions,
        ITenantDirectory tenantDirectory)
    {
        if (!tenancyOptions.Value.IsMultiTenant)
        {
            var defaultTenant = await tenantDirectory.GetDefaultTenantAsync(context.RequestAborted);
            using var scope = AmbientTenantContext.Enter(defaultTenant);
            await next(context);
            return;
        }

        if (context.Request.RouteValues.TryGetValue(TenantSlugRouteKey, out var slugValue) &&
            slugValue is string slug && !string.IsNullOrWhiteSpace(slug))
        {
            var tenantId = await tenantDirectory.FindActiveBySlugAsync(slug, context.RequestAborted);
            if (tenantId is null)
            {
                context.Response.StatusCode = StatusCodes.Status404NotFound;
                return;
            }

            var userId = GetUserId(context.User);
            if (userId is null ||
                !await tenantDirectory.IsMemberAsync(userId.Value, tenantId.Value, context.RequestAborted))
            {
                // 404 (not 403) so non-members cannot probe which tenant slugs exist.
                context.Response.StatusCode = StatusCodes.Status404NotFound;
                return;
            }

            using var scope = AmbientTenantContext.Enter(tenantId.Value);
            var module = context.GetEndpoint()?.Metadata.GetMetadata<TenantModuleMetadata>();
            if (module is not null &&
                !(await tenantDirectory.GetEnabledModulesAsync(
                    tenantId.Value,
                    context.RequestAborted)).Contains(module.ModuleKey))
            {
                // A disabled module is deliberately indistinguishable from an
                // unknown route or tenant.
                context.Response.StatusCode = StatusCodes.Status404NotFound;
                return;
            }

            await next(context);
            return;
        }

        // No tenant segment (e.g. identity/session endpoints); tenant stays unresolved.
        await next(context);
    }

    private static Guid? GetUserId(ClaimsPrincipal user)
    {
        var value = user.FindFirstValue(ClaimTypes.NameIdentifier);
        return Guid.TryParse(value, out var id) ? id : null;
    }
}