using Vantigo.Configuration;

namespace Vantigo.Products.Endpoints;

internal static class ProductApiServiceCollectionExtensions
{
    public static IServiceCollection AddProductApiVersioning(this IServiceCollection services)
    {
        services.AddVantigoApiVersioning();
        return services;
    }
}