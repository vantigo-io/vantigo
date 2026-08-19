using Vantigo.Configuration;

namespace Vantigo.Energy.Endpoints;

internal static class EnergyApiServiceCollectionExtensions
{
    internal static IServiceCollection AddEnergyApiVersioning(this IServiceCollection services)
    {
        services.AddVantigoApiVersioning();
        return services;
    }
}