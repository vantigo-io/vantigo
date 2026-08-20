using Vantigo.Configuration;

namespace Vantigo.Communications.Endpoints;

internal static class ApiVersioningExtensions
{
    public static IServiceCollection AddCommunicationsModuleVersioning(this IServiceCollection services)
    {
        services.AddVantigoApiVersioning();
        return services;
    }
}