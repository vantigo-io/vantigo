using Asp.Versioning;

using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Tenancy;

namespace Vantigo.Customers.Endpoints;

public static class VersionedBusinessEndpointExtensions
{
    public static IEndpointRouteBuilder MapCustomersModule(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapTenantGroup("/api/v{version:apiVersion}/customers")
            .HasApiVersion(new ApiVersion(1));

        api.MapCustomersEndpoints();
        api.MapContactsEndpoints();
        api.MapLookupEndpoints();

        return endpoints;
    }
}