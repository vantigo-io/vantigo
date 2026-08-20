using Vantigo.Configuration;

namespace Vantigo.Customers.Endpoints;

internal static class CustomerApiServiceCollectionExtensions
{
    internal static IServiceCollection AddCustomerApiVersioning(this IServiceCollection services)
    {
        services.AddVantigoApiVersioning();
        return services;
    }
}