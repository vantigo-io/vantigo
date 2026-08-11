using Asp.Versioning;

using Vantigo.Contracts.Identity;

namespace Vantigo.Customers.Endpoints;

public static class VersionedBusinessEndpointExtensions
{
    public static IEndpointRouteBuilder MapCustomersModule(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapGroup("/api/v{version:apiVersion}/customers")
            .HasApiVersion(new ApiVersion(1))
            .RequireAuthorization(AuthPolicies.Business);

        api.MapCustomersEndpoints();
        api.MapContactsEndpoints();
        api.MapLookupEndpoints();

        return endpoints;
    }
}