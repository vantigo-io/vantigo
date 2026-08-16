using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Registers the EF Core tenancy services.
/// </summary>
public static class TenancyEntityFrameworkServiceCollectionExtensions
{
    /// <summary>
    /// Registers singleton tenancy interceptors and the tenant counter service.
    /// An <see cref="Vantigo.Tenancy.Abstractions.ITenantContext"/> must also be
    /// registered; the standard registration is the singleton AmbientTenantContext.
    /// </summary>
    public static IServiceCollection AddVantigoTenancyEntityFramework(
        this IServiceCollection services)
    {
        ArgumentNullException.ThrowIfNull(services);

        services.TryAddSingleton<TenantStampingInterceptor>();
        services.TryAddSingleton<TenantConnectionInterceptor>();
        services.TryAddSingleton<ITenantCounterService, TenantCounterService>();
        return services;
    }
}