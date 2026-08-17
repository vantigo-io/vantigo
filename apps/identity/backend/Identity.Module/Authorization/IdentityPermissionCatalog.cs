using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;

using Vantigo.Contracts.Authorization;

namespace Vantigo.Identity.Authorization;

public static class IdentityPermissionCatalogRegistration
{
    public static IServiceCollection AddPermissionCatalog(
        this IServiceCollection services,
        Action<PermissionCatalogBuilder>? configure = null)
    {
        if (services.Any(descriptor => descriptor.ServiceType == typeof(IPermissionCatalog)))
        {
            throw new InvalidOperationException(
                "The permission catalog has already been finalized. Register all contributors before calling AddPermissionCatalog again.");
        }

        if (configure is not null) services.AddSingleton<IPermissionCatalogContributor>(new DelegatePermissionCatalogContributor(configure));
        services.TryAddSingleton<IPermissionCatalog>(sp => PermissionCatalog.Create(
            sp.GetServices<IPermissionCatalogContributor>()));
        return services;
    }

    public static IServiceCollection AddPermissionCatalogContributors(
        this IServiceCollection services,
        IEnumerable<IPermissionCatalogContributor> contributors)
    {
        ArgumentNullException.ThrowIfNull(contributors);
        foreach (var contributor in contributors) services.AddSingleton(typeof(IPermissionCatalogContributor), contributor);
        return services;
    }

    public static IServiceCollection AddPermissionCatalogContributor<TContributor>(this IServiceCollection services)
        where TContributor : class, IPermissionCatalogContributor
    {
        services.AddSingleton<IPermissionCatalogContributor, TContributor>();
        return services;
    }
}

internal sealed class DelegatePermissionCatalogContributor(Action<PermissionCatalogBuilder> configure)
    : IPermissionCatalogContributor
{
    public void Contribute(PermissionCatalogBuilder catalog) => configure(catalog);
}