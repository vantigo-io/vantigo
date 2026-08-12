using Asp.Versioning;

using Vantigo.Contracts.AspNetCore.Authorization;

namespace Vantigo.Customers.Endpoints;

public static class VersionedBusinessEndpointExtensions
{
    public static IEndpointRouteBuilder MapCustomersModule(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapGroup("/api/v{version:apiVersion}/customers")
            .HasApiVersion(new ApiVersion(1));

        api.MapCustomersEndpoints();
        api.MapContactsEndpoints();
        api.MapLookupEndpoints();

        return endpoints;
    }
}