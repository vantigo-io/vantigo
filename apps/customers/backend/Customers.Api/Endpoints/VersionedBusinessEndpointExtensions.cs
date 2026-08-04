using Asp.Versioning;

using Vantigo.Customers.Api.Endpoints.Auth;

namespace Vantigo.Customers.Api.Endpoints;

internal static class VersionedBusinessEndpointExtensions
{
    internal static IEndpointRouteBuilder MapVersionedBusinessEndpoints(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapGroup("/api/v{version:apiVersion}")
            .HasApiVersion(new ApiVersion(1))
            .RequireAuthorization(AuthPolicies.Business);

        api.MapCustomersEndpoints();
        api.MapContactsEndpoints();
        api.MapLookupEndpoints();

        return endpoints;
    }
}