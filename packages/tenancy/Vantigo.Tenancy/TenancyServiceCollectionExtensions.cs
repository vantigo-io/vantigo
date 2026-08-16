using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Routing;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy;

public static class TenancyServiceCollectionExtensions
{
    /// <summary>
    /// Registers the ambient tenant context. Requires an <see cref="ITenantDirectory"/>
    /// implementation (provided by the identity module).
    /// </summary>
    public static IServiceCollection AddVantigoTenancy(this IServiceCollection services)
    {
        services.TryAddSingleton<ITenantContext, AmbientTenantContext>();
        return services;
    }

    /// <summary>Adds tenant resolution after authentication.</summary>
    public static IApplicationBuilder UseVantigoTenancy(this IApplicationBuilder app) =>
        app.UseMiddleware<TenantResolutionMiddleware>();

    /// <summary>
    /// Creates the route group business endpoints are mapped into. In multi-tenant
    /// mode the group is prefixed with the tenant slug segment; in single mode it
    /// is unprefixed. Canonical business module groups also receive the tenant
    /// entitlement convention.
    /// </summary>
    public static RouteGroupBuilder MapTenantGroup(this IEndpointRouteBuilder endpoints, string prefix)
    {
        var tenancy = endpoints.ServiceProvider.GetRequiredService<IOptions<TenancyOptions>>().Value;
        var routePrefix = tenancy.IsMultiTenant
            ? TenantRouteTemplates.AddTenantSegment(prefix)
            : prefix;
        var group = endpoints.MapGroup(routePrefix);
        if (TenantRouteTemplates.TryGetModuleKey(prefix, out var moduleKey))
            group.WithMetadata(new TenantModuleMetadata(moduleKey));

        return group;
    }
}